# Security policy

## Supported versions

MuxShift is an early prototype. Security fixes ship on the current main line. Older snapshots receive no fixes.

## Reporting

Open an issue that only asks for a preferred security contact. Keep exploit details, reproduction steps for an unfixed weakness, credentials, and private host information out of that issue; provide them through the contact the maintainer names in reply.

A report names the affected version, the affected component, the observed behavior, and the steps that reproduce it.

## Scope

HLS metadata parsing, media URLs, cookies, request headers, browser messages, local file paths, the bounded replay buffer, and the boundaries between the shared Go Core and the desktop, web, and extension clients belong to this repository. Weaknesses in another project's code belong to that project's reporting channel.

## Handling

Maintainers answer the contact request and assess each finding against the current main line. Report details stay out of public issues until a fix is published.
