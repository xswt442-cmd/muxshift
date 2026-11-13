# Cutting a release

A release is one ZIP built from one clean commit: `muxshift-<version>-windows-amd64`.
Nothing is published from a working tree with changes; the binary and the
directory both say which commit they came from, and the same commit built twice
has to give the same bytes.

## What goes in it

```
LICENSE
README.md            (the Chinese README, which is the one the app is built around)
README.en.md
RUNNING.txt         (what to type, what the machine needs, what is not shipped)
THIRD-PARTY.md      (generated from the modules the binary actually links)
VERSION.txt         (version, commit, build time, sha256 of the binary)
muxshift.exe
```

## Before building

1. `dev` is green: both CI jobs (`go` and `desktop`) pass on the commit.
2. Locally: `go vet ./...`, `go test ./...`, and in `desktop/`
   `go vet ./...`, `go test ./...`.
3. The working tree is clean (`git status --short` prints nothing). If it is
   not, `release.ps1` refuses to build unless `-AllowDirty` marks the build.

## Version

`VERSION` at the repository root is the single source: `scripts\release.ps1`,
`scripts\verify-release.ps1` and `scripts\winres.ps1` read it when no
`-Version` is passed. It holds `x.y.z` only; the build adds nothing, the commit
is what distinguishes two builds of the same version.

When the version changes, the version block baked into the executable has to
change too, and it lives in a committed object file:

```powershell
# after editing VERSION
scripts\winres.ps1            # rewrites desktop\rsrc_windows_amd64.syso
git add VERSION desktop\rsrc_windows_amd64.syso
git commit -m "release: 0.0.1"
```

`winres.ps1` needs the network once (`go run ...@latest`); a plain build does
not, because the `.syso` is committed.

## Build and verify

```powershell
scripts\release.ps1
scripts\verify-release.ps1 -Ref HEAD
```

`release.ps1` writes `dist\muxshift-0.0.1-windows-amd64\` and refuses to finish
if the binary does not name its commit, if the version block disagrees with
`VERSION`, or if `thirdparty.ps1` fails.

`verify-release.ps1` is the stronger check: it clones the repository, builds the
release in the clone, asks the binary what it is, confirms it carries no path
from the machine that built it, reads the PE resources (icon, icon group,
version block), and builds the same commit twice to prove the bytes match.

## Tag and publish

Only after both checks pass on the exact commit that will be tagged:

```powershell
git tag -a v0.0.1 -m "MuxShift 0.0.1"
git push origin v0.0.1
```

Then publish a GitHub release for that tag with:

- the ZIP from `dist\`;
- `VERSION.txt`, or the same facts in the release body (commit, sha256);
- notes that say what this version does **and** what it does not do — take the
  capability table from `README.md`'s limits section, do not invent a feature
  list;
- a pointer to `docs/development/VERIFICATION.md` for what was actually checked.

The ZIP is uploaded as built. Do not rebuild after uploading: a second build
with a different toolchain would not match `VERSION.txt`.

## Never

- Publish a `+dirty` build.
- Hand-edit `THIRD-PARTY.md`; it is generated from what the binary links.
- Tag a commit whose CI is red, or tag first and fix afterwards.
- Move or delete a published tag. If a release is wrong, cut a new version.
