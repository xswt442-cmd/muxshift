"""Separate one recording into a vocal stem and a non-vocal stem.

The model file is not part of this repository: pass a UVR MDX-Net export with
`--model`. The pipeline mirrors the reference implementation in
ultimatevocalremovergui (`separate.py`, `lib_v5/tfc_tdf_v3.py`) so the numbers
here describe that model, not a hand-rolled approximation:

* 44.1 kHz stereo, 6144-point Hann window, hop 1024, 3072 kept bins
* chunks of `hop * (dim_t - 1)` samples with a Hann overlap-add
* the model's own output is the vocal spectrogram; the non-vocal stem is the
  mixture minus that stem.

Decoding goes through ffmpeg so the same command works for a local file and for
an HLS or HTTP address. Timing is printed as one JSON line so a caller can parse
it instead of scraping prose.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
import wave

import numpy as np
import onnxruntime as ort

DIM_F = 3072
DIM_T = 256
N_FFT = DIM_F * 2
HOP = 1024
TRIM = N_FFT // 2
CHUNK = HOP * (DIM_T - 1)
GEN = CHUNK - 2 * TRIM
DEFAULT_OVERLAP = 0.25


def decode(source, rate, seconds):
    ffmpeg = shutil.which("ffmpeg")
    if not ffmpeg:
        raise SystemExit("ffmpeg is not on PATH")
    probe = shutil.which("ffprobe")
    channels = 2
    if probe:
        query = [probe, "-v", "error", "-select_streams", "a:0",
                 "-show_entries", "stream=channels", "-of", "csv=p=0", source]
        found = subprocess.run(query, capture_output=True, text=True, check=False)
        value = found.stdout.strip().splitlines()
        if value and value[0].isdigit():
            channels = min(max(int(value[0]), 1), 2)
    command = [ffmpeg, "-hide_banner", "-loglevel", "error", "-i", source,
               "-vn", "-ac", str(channels), "-ar", str(rate), "-f", "f32le", "-"]
    if seconds:
        command[-1:-1] = ["-t", str(seconds)]
    result = subprocess.run(command, capture_output=True, check=False)
    if result.returncode != 0:
        raise SystemExit(f"ffmpeg could not decode {source}: {result.stderr.decode('utf-8', 'replace').strip()}")
    data = np.frombuffer(result.stdout, dtype="<f4")
    audio = data.reshape(-1, channels).T.copy()
    if channels == 1:
        # ffmpeg's mono to stereo upmix applies a -3 dB pan law, so duplicate the
        # channel here instead of letting the decoder scale it.
        audio = np.repeat(audio, 2, axis=0)
    return audio


def write_wav(path, rate, samples):
    pcm = (np.clip(samples, -1.0, 1.0).T * 32767.0).astype("<i2")
    with wave.open(path, "wb") as handle:
        handle.setnchannels(2)
        handle.setsampwidth(2)
        handle.setframerate(rate)
        handle.writeframes(pcm.tobytes())


def window():
    return 0.5 - 0.5 * np.cos(2.0 * np.pi * np.arange(N_FFT) / N_FFT)


def stft(chunk):
    frames = 1 + (chunk.shape[-1]) // HOP
    output = np.zeros((2, DIM_F, frames), dtype=np.complex64)
    win = window()
    padded = np.pad(chunk, ((0, 0), (TRIM, TRIM)))
    for index in range(frames):
        start = index * HOP
        block = padded[:, start:start + N_FFT] * win[None, :]
        spectrum = np.fft.rfft(block, axis=1)[:, :DIM_F]
        output[:, :, index] = spectrum
    return output


def istft(spec):
    bins = spec.shape[1]
    frames = spec.shape[2]
    win = window()
    total = HOP * (frames - 1) + N_FFT
    output = np.zeros((2, total), dtype=np.float64)
    weight = np.zeros(total, dtype=np.float64)
    for index in range(frames):
        full = np.zeros((2, bins + 1), dtype=np.complex64)
        full[:, :bins] = spec[:, :, index]
        block = np.fft.irfft(full, n=N_FFT, axis=1) * win[None, :]
        start = index * HOP
        output[:, start:start + N_FFT] += block
        weight[start:start + N_FFT] += win ** 2
    weight[weight < 1e-8] = 1.0
    return (output / weight)[:, TRIM:TRIM + HOP * (frames - 1) + 1]


def run_model(session, spec):
    batch = np.zeros((1, 4, DIM_F, DIM_T), dtype=np.float32)
    batch[0, 0] = spec[0].real
    batch[0, 1] = spec[0].imag
    batch[0, 2] = spec[1].real
    batch[0, 3] = spec[1].imag
    result = session.run(None, {"input": batch})[0]
    out = np.zeros((2, DIM_F, DIM_T), dtype=np.complex64)
    out[0] = result[0, 0] + 1j * result[0, 1]
    out[1] = result[0, 2] + 1j * result[0, 3]
    return out


def separate(session, mix, overlap):
    step = int((1.0 - overlap) * CHUNK) if overlap else CHUNK
    pad = GEN + TRIM - (mix.shape[-1] % GEN)
    mixture = np.concatenate((np.zeros((2, TRIM), dtype=np.float32), mix,
                              np.zeros((2, pad), dtype=np.float32)), axis=1)
    result = np.zeros((1, 2, mixture.shape[-1]), dtype=np.float32)
    divider = np.zeros((1, 2, mixture.shape[-1]), dtype=np.float32)
    chunk_times = []
    chunks = 0
    for start in range(0, mixture.shape[-1], step):
        end = min(start + CHUNK, mixture.shape[-1])
        actual = end - start
        part = mixture[:, start:end]
        if actual < CHUNK:
            part = np.concatenate((part, np.zeros((2, CHUNK - actual), dtype=np.float32)), axis=1)
        spec = stft(part)
        begin = time.perf_counter()
        stem = run_model(session, spec)
        chunk_times.append(time.perf_counter() - begin)
        wave_out = istft(stem)
        if overlap:
            window_out = np.hanning(even(actual))[None, :]
            wave_out = wave_out[:, :actual] * window_out
            divider[0, :, start:end] += window_out
        else:
            divider[0, :, start:end] += 1
        result[0, :, start:end] += wave_out[:, :actual]
        chunks += 1
    stem = (result / np.maximum(divider, 1e-8))[0][:, TRIM:-TRIM]
    stem = stem[:, :mix.shape[-1]]
    return stem, chunk_times, chunks


def even(value):
    return value if value % 2 == 0 else value + 1


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True, help="UVR MDX-Net ONNX export")
    parser.add_argument("--input", required=True, help="local file or http(s) address ffmpeg can read")
    parser.add_argument("--out-dir", default=".")
    parser.add_argument("--provider", default="DmlExecutionProvider")
    parser.add_argument("--overlap", type=float, default=DEFAULT_OVERLAP)
    parser.add_argument("--seconds", type=float, default=0.0)
    parser.add_argument("--rate", type=int, default=44100)
    args = parser.parse_args()

    started = time.perf_counter()
    if not os.path.exists(args.model):
        raise SystemExit(f"model file {args.model} is missing; this repository does not ship weights")
    mix = decode(args.input, args.rate, args.seconds)
    decoded = time.perf_counter()
    providers = [args.provider]
    fell_back = False
    try:
        session = ort.InferenceSession(args.model, providers=providers)
    except Exception as error:
        if args.provider == "CPUExecutionProvider":
            raise
        print(f"{args.provider} is not usable here ({error}); falling back to the CPU", file=sys.stderr)
        providers = ["CPUExecutionProvider"]
        fell_back = True
        session = ort.InferenceSession(args.model, providers=providers)
    loaded = time.perf_counter()
    vocal, chunk_times, chunks = separate(session, mix, args.overlap)
    finished = time.perf_counter()

    non_vocal = mix - vocal
    os.makedirs(args.out_dir, exist_ok=True)
    vocal_path = f"{args.out_dir}\\vocal.wav"
    non_vocal_path = f"{args.out_dir}\\non_vocal.wav"
    write_wav(vocal_path, args.rate, vocal)
    write_wav(non_vocal_path, args.rate, non_vocal)
    seconds = mix.shape[-1] / args.rate
    report = {
        "provider": session.get_providers()[0],
        "fell_back": fell_back,
        "seconds": round(seconds, 3),
        "chunks": chunks,
        "decode_s": round(decoded - started, 3),
        "load_s": round(loaded - decoded, 3),
        "first_chunk_s": round(chunk_times[0], 3) if chunk_times else None,
        "steady_chunk_s": round(float(np.mean(chunk_times[1:])), 3) if len(chunk_times) > 1 else None,
        "inference_s": round(finished - loaded, 3),
        "realtime_factor": round(seconds / max(finished - loaded, 1e-6), 2),
        "latency_s": round(CHUNK / args.rate, 2),
        "vocal": vocal_path,
        "non_vocal": non_vocal_path,
    }
    print(json.dumps(report))


if __name__ == "__main__":
    sys.exit(main())
