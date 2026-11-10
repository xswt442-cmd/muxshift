# Separation harness

`separate.py` splits one recording into a vocal stem and a non-vocal stem with a
UVR MDX-Net ONNX export. It is an experiment, not part of the desktop or web
playback path: nothing in `desktop/`, `web/`, or `extension/` calls it yet.

The model weights are **not** in this repository. Published UVR weights carry no
confirmed redistribution license (the upstream README covers the code only, the
weight repository has no license file, and the license question in
`ultimatevocalremovergui` issue 2185 was never answered), so keep any model in
the Git-ignored `testplace/models/` and pass its path explicitly.

## Requirements

- Python 3.12 with `numpy` and one ONNX Runtime build: `onnxruntime-directml`
  for a DirectML GPU or plain `onnxruntime` for CPU only.
- `ffmpeg` and `ffprobe` on `PATH`; they decode any local file or http(s)
  address the desktop can also open.

## Running

```
python tools/separate/separate.py ^
  --model testplace/models/Kim_Vocal_2.onnx ^
  --input testplace/media/p11/sample-mixed.wav ^
  --out-dir testplace/p11-stems
```

The last line of standard output is one JSON object:

```
{"provider":"DmlExecutionProvider","fell_back":false,"seconds":12.0,"chunks":4,
 "decode_s":0.209,"load_s":0.842,"first_chunk_s":0.529,"steady_chunk_s":0.236,
 "inference_s":1.74,"realtime_factor":6.9,"latency_s":5.92,
 "vocal":"...\\vocal.wav","non_vocal":"...\\non_vocal.wav"}
```

`latency_s` is the algorithmic delay: the model sees 5.92 s of audio per call, so
no output for time T exists before the input reaches T + 5.92 s. `--overlap`
(default 0.25) trades overlap for step size; it does not shorten that delay.

`compare.py` prints levels and correlations for the two stems, and optionally
against the recordings that were mixed together:

```
python tools/separate/compare.py ^
  --mix testplace/media/p11/sample-mixed-44k.wav ^
  --vocal testplace/p11-stems/vocal.wav ^
  --non-vocal testplace/p11-stems/non_vocal.wav ^
  --expect-vocal testplace/media/p11/commentary-44k.wav ^
  --expect-non-vocal testplace/media/p11/crowd-44k.wav
```

## Measured on this machine

RTX 4060 Laptop (8 GiB), driver 621.01, Python 3.12.4, onnxruntime-directml
1.24.4, ffmpeg 9.0.2, 44.1 kHz stereo, `Kim_Vocal_2.onnx` (66,759,214 bytes):

| input | provider | chunks | first chunk | steady chunk | inference | realtime |
| --- | --- | --- | --- | --- | --- | --- |
| 12 s | DirectML | 4 | 0.53 s | 0.24 s | 1.74 s | 6.9x |
| 60 s | DirectML | 15 | 0.51 s | 0.26 s | 7.72 s | 7.8x |
| 30 s | CPU | 8 | 5.33 s | 5.75 s | 50.2 s | 0.6x |

CPU inference is slower than real time, so a live pipeline needs a GPU; DirectML
held 6.2 GiB of the 8 GiB card while running. See
`testplace/evidence/P11/evidence.md` for the full record.
