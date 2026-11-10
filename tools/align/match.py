"""Estimate the time offset between two recordings from their content.

The estimate comes only from the audio envelope of both files: no network
arrival time, player position or wall clock is used. The result is a proposal,
not an automatic change of playback state; a caller decides what to do with an
accepted offset.
"""

import argparse
import json
import sys
import time
import wave

import numpy as np

RATE = 16000
HOP = 160  # 10 ms


def read_mono(path, rate=RATE):
    with wave.open(path, "rb") as handle:
        channels = handle.getnchannels()
        width = handle.getsampwidth()
        source = handle.getframerate()
        frames = handle.readframes(handle.getnframes())
    if width != 2:
        raise SystemExit(f"{path}: only 16-bit PCM is supported, got {width * 8} bit")
    data = np.frombuffer(frames, dtype="<i2").astype(np.float64) / 32768.0
    if channels > 1:
        data = data.reshape(-1, channels).mean(axis=1)
    if source != rate:
        count = int(len(data) * rate / source)
        data = np.interp(np.arange(count) * source / rate, np.arange(len(data)), data)
    return data


def envelope(samples, hop=HOP):
    """Log energy per hop, which survives level and codec differences."""
    frames = len(samples) // hop
    if frames == 0:
        return np.zeros(1)
    trimmed = samples[: frames * hop].reshape(frames, hop)
    energy = np.sqrt(np.mean(trimmed * trimmed, axis=1) + 1e-12)
    return np.log1p(energy * 20.0)


def normalise(values):
    values = values - values.mean()
    scale = values.std()
    return values / scale if scale > 0 else values


def correlate(a, b, max_lag):
    """Return (lag, peak, runner_up); positive lag means b is later than a."""
    a = normalise(a)
    b = normalise(b)
    size = 1 << int(np.ceil(np.log2(len(a) + len(b))))
    spectrum = np.fft.rfft(a, size) * np.conj(np.fft.rfft(b, size))
    correlation = np.fft.irfft(spectrum, size)
    correlation = np.concatenate((correlation[-max_lag:], correlation[: max_lag + 1]))
    lags = np.arange(-max_lag, max_lag + 1)
    scores = correlation / min(len(a), len(b))
    order = np.argsort(scores)[::-1]
    peak = int(order[0])
    runner = 0.0
    for index in order[1:]:
        if abs(lags[index] - lags[peak]) > 50:  # skip the shoulder of the same peak
            runner = float(scores[index])
            break
    return int(lags[peak]), float(scores[peak]), runner


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--a", required=True)
    parser.add_argument("--b", required=True)
    parser.add_argument("--max-lag", type=float, default=90.0)
    parser.add_argument("--min-peak", type=float, default=0.25)
    parser.add_argument("--min-confidence", type=float, default=0.5)
    parser.add_argument("--decimate", type=int, default=1)
    parser.add_argument("--hop", type=int, default=HOP)
    args = parser.parse_args()

    started = time.perf_counter()
    a = envelope(read_mono(args.a), args.hop)
    b = envelope(read_mono(args.b), args.hop)
    if args.decimate > 1:
        keep = (len(a) // args.decimate) * args.decimate
        a = a[:keep].reshape(-1, args.decimate).mean(axis=1)
        keep = (len(b) // args.decimate) * args.decimate
        b = b[:keep].reshape(-1, args.decimate).mean(axis=1)
    max_lag = max(1, int(args.max_lag * RATE / args.hop / args.decimate))
    lag, peak, runner = correlate(a, b, max_lag)
    seconds = -lag * args.hop / RATE * args.decimate
    confidence = 0.0 if peak <= 0 else max(0.0, 1.0 - runner / peak)

    if len(a) < 2 or len(b) < 2:
        accepted, reason = False, "one side has less than 20 ms of audio"
    elif peak < args.min_peak:
        accepted, reason = False, f"peak {peak:.3f} is below {args.min_peak:.2f}: no shared content"
    elif confidence < args.min_confidence:
        accepted, reason = False, (
            f"confidence {confidence:.3f} is below {args.min_confidence:.2f}: "
            f"the second best candidate ({runner:.3f}) is close to the best ({peak:.3f})"
        )
    elif abs(lag) >= max_lag:
        accepted, reason = False, f"the best candidate is at the edge of the {args.max_lag:g} s window"
    else:
        accepted, reason = True, "a single candidate stands out"

    print(
        json.dumps(
            {
                "a": args.a,
                "b": args.b,
                "offset_s": round(seconds, 3),
                "peak": round(peak, 4),
                "runner_up": round(runner, 4),
                "confidence": round(confidence, 3),
                "accepted": accepted,
                "reason": reason,
                "max_lag_s": args.max_lag,
                "hop_s": args.hop / RATE * args.decimate,
                "elapsed_s": round(time.perf_counter() - started, 3),
            }
        )
    )
    if not accepted:
        print(reason, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
