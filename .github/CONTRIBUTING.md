# Contributing

## Branches

Pull requests target `dev`. Maintainers fast-forward `main` before a release. Merging, tagging, and publishing are maintainer actions. CI runs on pull requests.

## Pull request descriptions

A description states the change, the work completed, and the checks actually run, in a Simplified Chinese section and a matching English section. Deferred work belongs in an issue.

## Documentation

`README.md` and `README.en.md` change together. The paired documents under `docs/development/` change together, and `CHANGELOG.md` keeps its `简体中文` and `English` sections in step. Tracked files contain no personal paths, credentials, private host details, or one-off logs.

## Verification

Run `go test ./...` at the repository root and again inside `desktop/`. `docs/development/VERIFICATION.md` records what each check covers. Run client-specific checks only after the corresponding clients and scripts exist, and state which checks were not run.

## Version history

Release notes derive from the section for the tagged version in `CHANGELOG.md`. `docs/development/RELEASE.md` records the maintainer steps.

## Security reports

Security findings follow [SECURITY.md](SECURITY.md).
