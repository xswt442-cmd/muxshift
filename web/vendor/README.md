# Vendored hls.js

| field | value |
| --- | --- |
| package | `hls.js` |
| version | `1.7.4` |
| build | `dist/hls.light.min.js` (UMD light build) |
| source | `https://cdn.jsdelivr.net/npm/hls.js@1.7.4/dist/hls.light.min.js` |
| license source | `https://cdn.jsdelivr.net/npm/hls.js@1.7.4/LICENSE` |
| size | 388933 bytes (`hls.light.min.js`), 1338 bytes (`hls.LICENSE`) |
| SHA-256 | `482cccb3a224554f64d8c2cf0d335dba4a3819bdb726afbfddfe15de4c277d82` |
| license | Apache-2.0, kept beside the file as `hls.LICENSE` |

The build is referenced from `../index.html` with a plain `<script>` tag, so the
repository needs no package manifest, bundler, or front-end toolchain. To check
what is on disk:

```sh
sha256sum web/vendor/hls.light.min.js
```

The version is pinned: an update replaces this table with the new version, its
exact URL, and its hash in the same commit.
