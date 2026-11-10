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
 "inference_s":1.74,"realtime_factor":6.9,"chunk_window_s":5.92,
 "vocal":"...\\vocal.wav","non_vocal":"...\\non_vocal.wav"}
```

`chunk_window_s` is the chunk window, not a measured end-to-end delay: the model sees
5.92 s of audio per call. What a listener would actually wait for also includes
overlap-add scheduling, encoding, buffering and player startup, and none of that
is measured here. `--overlap` (default 0.25) trades overlap for step size; it
does not change the window length.

The two stems are derived from one recording, not two independent live sources.
Feeding them back as separate sources is a way to hear the result today; a real
integration has to keep the source, the sample rate, the original timeline and
the processing delay together, and any cross-source offset has to come from the
content timestamps rather than from a constant.

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
| 12 s synthetic | DirectML | 4 | 0.53 s | 0.24 s | 1.74 s | 6.9x |
| 60 s synthetic | DirectML | 15 | 0.51 s | 0.26 s | 7.72 s | 7.8x |
| 30 s synthetic | CPU | 8 | 5.33 s | 5.75 s | 50.2 s | 0.6x |
| 20 s real | DirectML | 6 | 0.80 s | 0.26 s | 3.49 s | 5.7x |

CPU inference is slower than real time, so a live pipeline needs a GPU; DirectML
held 6.2 GiB of the 8 GiB card while running. See
`testplace/evidence/P11/evidence.md` for the full record.

## Real-recording check

The synthetic sample only proves the plumbing. A second check mixes two real,
public-domain recordings: a LibriVox reading
(`escape_benson_bn_64kb.mp3` from the LibriVox Nonfiction Collection Vol. 001,
public domain per the item's Creative Commons public-domain dedication) and
applause recorded by a Wikimedia Commons contributor
(`Slow_starting_applause.ogg`, public domain). Both are kept in the ignored
`testplace/media/p11-real/`, mixed at 0.8 applause level for 20 s, and
separated with the same command as above.

| check | synthetic | real |
| --- | --- | --- |
| vocal vs its source | +0.992 | +0.929 |
| non-vocal vs its source | +0.834 | +0.939 |
| vocal + non-vocal - mix (rms) | 3e-5 | 2e-5 |

That is still not commentary inside a stadium broadcast: the reading is quiet
studio speech and the applause is a clean field recording, so crowd noise
masking a shouting commentator is not covered.
