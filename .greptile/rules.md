# MuxShift review context

This repository is a Go-first multi-source media project with distinct Core,
MyGo desktop, Web, and browser-extension boundaries.

- Identify reproducible correctness, security, concurrency, resource-lifecycle,
  timebase, buffering, and cross-platform defects.
- Keep Core independent of client UI frameworks and browser runtimes.
- Do not assume a third-party browser session grants the Web app access to
  cookies, credentials, or arbitrary media sources.
- Verify authorization boundaries around media acquisition and local control.
- Make suggestions against implemented behavior; avoid speculative abstractions
  and style-only comments.
- Cite concrete files and execution paths. Treat review comments as advisory;
  the project's CI and human judgment determine readiness.
