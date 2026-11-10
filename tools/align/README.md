# Alignment probe

`match.py` estimates the time offset between two recordings of the same event
from their content alone: it compares log-energy envelopes, so level, codec and
mixing differences survive. It never uses network arrival time, player position
or wall clock, because those are not event time.

It is a probe, not a product feature. Nothing in `desktop/`, `web/` or `core/`
calls it, and an accepted offset is not applied to playback: the caller has to
decide what to do with it, and `desktop/sync.go` keeps the manual control.

## Running

```
python tools/align/match.py --a testplace/media/p12/ref.wav --b testplace/media/p12/shift-15.wav --max-lag 60
```

The last line of standard output is one JSON object:

```
{"offset_s":-15.0,"peak":0.9241,"runner_up":0.0732,"confidence":0.921,
 "accepted":true,"reason":"a single candidate stands out","max_lag_s":60.0,
 "hop_s":0.01,"elapsed_s":0.109}
```

`offset_s` is positive when `b` runs ahead of `a` (its content is later on the
shared timeline). A candidate is accepted only when the peak is at least
`--min-peak` (default 0.25), the runner-up is at least `--min-confidence` below
it (default 0.5 of the peak) and the peak is not at the edge of the search
window. Otherwise the exit status is 1, the JSON says `accepted:false`, and the
reason names which test failed: no shared content, an ambiguous peak, or a
candidate outside the window.

## Fixtures

Both sides are mono 16 kHz WAV, built from one public-domain LibriVox reading
(`escape_benson_bn_64kb.mp3`, LibriVox Nonfiction Collection Vol. 001, public
domain per the item's public-domain dedication). The files live in the ignored
`testplace/media/p12/`.

```
ffmpeg -ss 0 -t 300 -i speech.mp3 -ar 16000 -ac 1 base.wav
ffmpeg -ss 0 -t 180 -i base.wav -ar 16000 -ac 1 ref.wav
ffmpeg -ss 15 -t 180 -i base.wav -ar 16000 -ac 1 shift-15.wav
ffmpeg -i shift-15.wav -af atempo=1.05 -ar 16000 -ac 1 slowmo.wav
ffmpeg -i ref.wav -af "volume=0.5,adelay=3000|3000" -ar 16000 -ac 1 gap-ref.wav
ffmpeg -i loop-seg.wav -i loop-seg.wav -filter_complex "[0:a][1:a]concat=n=2:v=0:a=1" speech-loop.wav
```

## Measured on this machine

180 s pairs, one Core i7 laptop, `--max-lag 60`:

| case | expected | estimate | peak | runner-up | confidence | decision | elapsed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| same speech, 2 s apart | -2 s | -2.000 | 0.984 | 0.077 | 0.922 | accepted | 0.112 s |
| same speech, 15 s apart | -15 s | -15.000 | 0.924 | 0.073 | 0.921 | accepted | 0.109 s |
| same speech, 45 s apart | -45 s | -45.000 | 0.747 | 0.073 | 0.902 | accepted | 0.075 s |
| 3 s silence inserted first | +3 s | +3.000 | 0.996 | 0.084 | 0.916 | accepted | 0.088 s |
| same speech, different mix | 0 s | 0.000 | 0.759 | 0.115 | 0.849 | accepted | 0.012 s |
| another recording | none | -12.54 | 0.147 | 0.138 | 0.056 | refused: no shared content | 0.047 s |
| repeated segment | ambiguous | -60.0 (edge) | 0.564 | 0.543 | 0.037 | refused: ambiguous peak | 0.099 s |
| same speech at 1.05x speed | none | -49.04 | 0.075 | 0.075 | 0.010 | refused: no shared content | 0.079 s |

The estimate resolution is the 10 ms envelope hop, and every accepted case lands
exactly on the prepared offset. Refusals are the interesting half: a different
recording, a looped segment and a rate change all stay below the thresholds
instead of producing a confident wrong answer.

## Limits

- Envelope matching needs shared audio. A muted stream, a different commentary
  track, music-only versus commentary-only, or a strong level difference beyond
  the log envelope's tolerance can all defeat it.
- Slow motion and time-stretched playback are not handled: the correlation
  collapses rather than reporting a rate.
- The search window is a fixed `--max-lag`; candidates further away are invisible
  and the run says so.

## Video fingerprint

`frame_hash.py` samples frames with ffmpeg at `--fps`, reduces each to a 16x16
grey thumbnail, normalises it, and correlates thumbnail sequences at every lag.
It accepts a lag when the correlation is at least `--min-correlation` (0.98) and
at least `--min-sharpness` (0.25) above the median correlation of all lags, so a
peak that is not separated from the background is refused instead of guessed.

```
python tools/align/frame_hash.py --a testplace/media/p12/vid-d.mp4 --b testplace/media/p12/vid-e-shift15.mp4 --fps 2 --max-lag 40
```

Measured on synthetic 320x180 test patterns (2 fps, 75 s, `--max-lag 40`):

| case | expected | estimate | correlation | background | sharpness | decision | wall time |
| --- | --- | --- | --- | --- | --- | --- | --- |
| clip against itself | 0 s | 0.000 | 1.000 | 0.901 | 0.099 | refused: peak not separated | 1.53 s |
| re-encoded copy, 15 s later | -15 s | -15.000 | 0.9996 | 0.900 | 0.099 | refused: peak not separated | — |
| copy with a different crop | -15 s | -39.0 | 0.778 | 0.738 | 0.040 | refused: below 0.98 | — |
| another clip | none | -34.0 | -0.066 | -0.069 | 0.003 | refused: below 0.98 | — |

The estimator finds the prepared offset exactly on the re-encoded copy, but the
acceptance rule refuses it: the synthetic pattern keeps its layout, so almost
every lag correlates with the whole clip and a correct peak cannot be separated
from the background. That is the intended behaviour of the confidence rule, and
it means this footage cannot show whether the technique works on real video.
Downloading public-domain match footage failed: the Wikimedia copy of the 1903
Princeton-Yale game returned HTTP 429 (Too Many Requests) twice, and the
archive.org football items had no playable derivative file listed. The video
path is therefore implemented but unvalidated (blocked), and nothing claims it
works on real broadcasts.
