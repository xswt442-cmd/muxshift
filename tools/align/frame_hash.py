"""Estimate a video offset from coarse luminance fingerprints of sampled frames.

Frames are sampled with ffmpeg at a fixed rate, reduced to a 16x16 grey
thumbnail and normalised. Two streams of the same event keep a high correlation
between their thumbnails even after a re-encode, which is the cheap, no-model
alternative to audio matching for silent or differently commented streams.
"""

import argparse
import json
import subprocess
import sys

import numpy as np

SIZE = 16


def thumbnails(path, rate, start, seconds):
    command = [
        "ffmpeg", "-hide_banner", "-loglevel", "error",
        "-ss", f"{start}", "-t", f"{seconds}", "-i", path,
        "-vf", f"fps={rate},scale={SIZE}:{SIZE}:flags=area,format=gray",
        "-f", "rawvideo", "-pix_fmt", "gray", "-",
    ]
    result = subprocess.run(command, capture_output=True, check=False)
    if result.returncode != 0:
        raise SystemExit(f"ffmpeg failed: {result.stderr.decode(errors='replace').strip()[:300]}")
    frame = SIZE * SIZE
    raw = np.frombuffer(result.stdout, dtype=np.uint8)
    count = len(raw) // frame
    if count == 0:
        return np.zeros((0, frame))
    frames = raw[: count * frame].reshape(count, frame).astype(np.float64)
    frames = frames - frames.mean(axis=1, keepdims=True)
    scale = np.sqrt((frames * frames).sum(axis=1, keepdims=True))
    scale[scale == 0] = 1.0
    return frames / scale


def mean_correlation(a, b, lag):
    if lag >= 0:
        left, right = a[: len(a) - lag], b[lag:]
    else:
        left, right = a[-lag:], b[: len(b) + lag]
    count = min(len(left), len(right))
    if count < 8:
        return None, 0
    return float(np.mean(np.sum(left[:count] * right[:count], axis=1))), count


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--a", required=True)
    parser.add_argument("--b", required=True)
    parser.add_argument("--fps", type=float, default=1.0)
    parser.add_argument("--max-lag", type=float, default=60.0)
    parser.add_argument("--a-start", type=float, default=0.0)
    parser.add_argument("--b-start", type=float, default=0.0)
    parser.add_argument("--a-seconds", type=float, default=180.0)
    parser.add_argument("--b-seconds", type=float, default=180.0)
    parser.add_argument("--min-correlation", type=float, default=0.98)
    parser.add_argument("--min-sharpness", type=float, default=0.25)
    args = parser.parse_args()

    a = thumbnails(args.a, args.fps, args.a_start, args.a_seconds)
    b = thumbnails(args.b, args.fps, args.b_start, args.b_seconds)
    max_lag = int(args.max_lag * args.fps)
    if len(a) < 8 or len(b) < 8:
        print(json.dumps({"accepted": False, "reason": "not enough frames were sampled"}))
        return 1

    scored = []
    for lag in range(-max_lag, max_lag + 1):
        correlation, count = mean_correlation(a, b, lag)
        if correlation is not None:
            scored.append((correlation, lag, count))
    scored.sort(reverse=True)
    best = scored[0]
    runner = next((item[0] for item in scored[1:] if abs(item[1] - best[1]) > 2), -1.0)
    values = np.array([item[0] for item in scored])
    background = float(np.median(values))
    sharpness = best[0] - background
    accepted = best[0] >= args.min_correlation and sharpness >= args.min_sharpness
    reason = "the matching lag stands out from the background"
    if best[0] < args.min_correlation:
        reason = f"best correlation {best[0]:.3f} is below {args.min_correlation:.2f}"
    elif sharpness < args.min_sharpness:
        reason = f"the best lag is only {sharpness:.3f} above the median lag, so the match is ambiguous"
    print(
        json.dumps(
            {
                "a": args.a,
                "b": args.b,
                "offset_s": round(best[1] / args.fps, 3),
                "correlation": round(best[0], 4),
                "runner_up": round(runner, 4),
                "background": round(background, 4),
                "sharpness": round(sharpness, 4),
                "frames": best[2],
                "accepted": accepted,
                "reason": reason,
            }
        )
    )
    return 0 if accepted else 1


if __name__ == "__main__":
    sys.exit(main())
