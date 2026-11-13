# Release process (maintainers)

MuxShift ships one product: a Windows amd64 ZIP. The version, the changelog and
the published assets describe the same commit; if they disagree, nothing is
published.

| Item | Value |
| --- | --- |
| Version source | `VERSION` at the repository root (`x.y.z`) and the matching section of `CHANGELOG.md` |
| Tag | annotated `vX.Y.Z` |
| Channel | GitHub Releases |
| Assets | `muxshift-<version>-windows-amd64.zip`, carrying `VERSION.txt`, `THIRD-PARTY.md` and the checksum |

## Steps

1. **Prepare the version in the feature or fix PR, not in a version-only PR.**
   The same commit updates `VERSION`, adds the bilingual `CHANGELOG.md` entry,
   and — when the version changes — regenerates and commits
   `desktop\rsrc_windows_amd64.syso` via `scripts\winres.ps1`. The commit is
   eligible for release only after it reaches `main` through review.
2. **Tag a green `main` head.** Run `release.yml` (manual dispatch, exact
   version). It checks that `VERSION` matches the input, that `CHANGELOG.md`
   has that version in both languages with no placeholders, that the tag does
   not exist, that the commit is the current `main` head, and that the `main`
   push CI for that commit passed. Any mismatch fails closed: no automatic
   version bump, no forced tag, no skipped check.
3. **Build and verify the tagged commit.** The workflow checks out the tag and
   runs `scripts\verify-release.ps1`: two clones in different directories must
   build identical bytes (`-trimpath`, cgo off); the binary must name the tag's
   commit, carry no path from the build machine, and carry the icon and version
   block that match `VERSION`.
4. **Generate the release body** with `scripts\release-notes.ps1 -Tag vX.Y.Z`.
   The title is only `MuxShift vX.Y.Z`; the body has `### 简体中文` then
   `### English`, each with the install, runtime and checksum facts inside it.
5. **Create the release as a draft, check it, then publish.** The draft holds
   only the assets this version produced, and the SHA-256 must match the file.
   Published tags are not moved and published binaries are not replaced; a
   wrong description is corrected in the body only.

## Changelog rules

- `CHANGELOG.md` records completed changes that matter to users, not
  development history, plans or a roadmap.
- `[Unreleased]` accumulates entries; a release moves them into
  `[X.Y.Z] - YYYY-MM-DD` with identical meaning in Chinese and English.
- Categories are used only when they have items: **新增 / 改进 / 修复 / 安全 /
  兼容性 / 移除** and **Added / Improved / Fixed / Security / Compatibility /
  Removed**. The extractor rejects empty categories, template comments and
  `TODO`/`TBD` placeholders.
- Notes describe observable behaviour: "position settled" never becomes "frame
  presented", and an experimental capability is never described as a product
  feature.

## Local build and verification

```powershell
scripts\winres.ps1          # only when the version changes; commit the .syso
scripts\release.ps1         # writes dist\muxshift-<version>-windows-amd64
scripts\verify-release.ps1  # fresh clones, two directories, resources, paths
```

`release.ps1` refuses a dirty tree (unless `-AllowDirty`, which labels the build
`+dirty`) and refuses a version block that disagrees with `VERSION`.

## Failure and rollback

- **Build or verification failed:** do not publish; fix and cut a new version
  with a new tag.
- **Missing or wrong assets:** delete the draft release and the tag, fix, and
  run the process again. A published release is not rewritten.
- **Wrong description:** edit the release body only, and say it was corrected.
- **User rollback:** install the previous published version; configuration
  lives in the user's directory and is not deleted by rolling back, but an older
  build is not promised to read configuration a newer one wrote — back it up
  first.

## Security

- No personal absolute paths, LAN addresses, tokens or credentials in the
  repository or the release notes.
- Build output (`dist/`) is not committed; `VERSION.txt` carries only the
  version, commit, build time, SHA-256 and `-version` output.
- Release logs keep only the non-sensitive evidence needed to judge the release
  state.
