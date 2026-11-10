"""Print levels and correlations for separated stems.

A pass-through result shows up as a vocal stem that correlates with the
mixture. When the two sources that were mixed together are available, the stem
should track the one it claims to hold and not the other.
"""

import argparse
import wave

import numpy as np


def read(path):
    with wave.open(path, "rb") as handle:
        rate = handle.getframerate()
        channels = handle.getnchannels()
        raw = handle.readframes(handle.getnframes())
    data = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0
    if channels > 1:
        data = data.reshape(-1, channels).mean(axis=1)
    return data, rate


def rms(signal):
    return float(np.sqrt(np.mean(np.square(signal)))) if len(signal) else 0.0


def correlated(a, b):
    length = min(len(a), len(b))
    return float(np.corrcoef(a[:length], b[:length])[0, 1])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--mix", required=True)
    parser.add_argument("--vocal", required=True)
    parser.add_argument("--non-vocal", required=True, dest="non_vocal")
    parser.add_argument("--expect-vocal", help="recording the vocal stem should follow")
    parser.add_argument("--expect-non-vocal", dest="expect_non_vocal",
                        help="recording the non-vocal stem should follow")
    args = parser.parse_args()

    mix, rate = read(args.mix)
    vocal, _ = read(args.vocal)
    non_vocal, _ = read(args.non_vocal)
    print(f"rate {rate} mix {len(mix) / rate:.2f}s")
    for name, signal in (("mix", mix), ("vocal", vocal), ("non-vocal", non_vocal)):
        print(f"{name:10s} rms {rms(signal):.4f}")
    print(f"vocal vs mix      {correlated(vocal, mix):+.3f}")
    print(f"non-vocal vs mix  {correlated(non_vocal, mix):+.3f}")
    print(f"vocal vs non-vocal {correlated(vocal, non_vocal):+.3f}")
    reconstruction = rms((vocal + non_vocal) - mix[:len(vocal)])
    print(f"vocal + non-vocal - mix rms {reconstruction:.5f}")
    if args.expect_vocal:
        reference, _ = read(args.expect_vocal)
        print(f"vocal vs expected     {correlated(vocal, reference):+.3f}")
    if args.expect_non_vocal:
        reference, _ = read(args.expect_non_vocal)
        print(f"non-vocal vs expected {correlated(non_vocal, reference):+.3f}")


if __name__ == "__main__":
    main()
