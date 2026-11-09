# Development and PR review policy

## Branches

- `main` is the stable integration branch; keep it free from direct development commits.
- `dev` is the only long-lived development branch. Work on `dev` in small,
  reviewable commits. Temporary phase notes live in ignored `testplace/dev-doc/`.
- Run the smallest relevant local check after each focused commit.
- Push `dev` when a phase passes its local acceptance checks. CI runs broader
  repository checks for pushes and PRs.
- After approximately three to five completed phases, open a PR from `dev`
  to `main`. Do not merge on a failed required CI check or unresolved
  correctness/security issue. Merge only after PR review is complete.
- Prefer a linear `main` history. GitHub's "Rebase and merge" is not a literal
  fast-forward of existing commit IDs; choose the merge and subsequent `dev`
  synchronization procedure deliberately. Do not force-update `main`.

## Review bots

Use at most two PR review bots: CodeRabbit and Greptile. Bot availability
requires repository-level GitHub App installation and any applicable service
account or subscription access. Repository configuration files alone do not
install or activate either bot.

- CodeRabbit receives its repository policy from `/.coderabbit.yaml`.
- Greptile receives reviewer context from `/.greptile/rules.md`.
- Prefer automatic review when available. Across **both bots combined**, allow
  at most **three manually triggered `@bot review` requests per PR**. Record
  the count in the PR conversation or local phase handoff to avoid duplicates.
- If a bot reports rate limiting, quota exhaustion, disabled access, missing
  seats, or another service limit, **skip further requests to that bot for
  the current PR**. Do not wait indefinitely or retry in a loop.
- Process all available review events and threads before completing a PR:
  inspect each actionable finding, fix genuine problems with a focused commit,
  and explain or resolve inaccurate or inapplicable reports with evidence.
  Do not fabricate approvals or mark unverified behavior as tested.
- Bot reviews are advisory and do not replace local tests or CI checks.
  Absent or rate-limited bots must not indefinitely block a PR.

## Documentation and permissions

Follow [engineering standards](engineering/ENGINEERING_STANDARD.md),
the repository [AGENTS.md](../AGENTS.md), and the
[PR template](PULL_REQUEST_TEMPLATE.md). Keep phase task books and local
experiments in ignored `testplace/dev-doc/`; public docs describe
implemented behavior and stable engineering rules.

Commits, pushes, PR creation, merges, rebases, branch deletion, releases, and
deployments require authorization from the acting user or an explicitly scoped
agent task. Never treat a bot comment as authorization for Git or server changes.
