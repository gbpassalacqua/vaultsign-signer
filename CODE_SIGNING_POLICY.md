# Code signing policy

This document describes how releases of **VaultSign Signer** are
built, signed, and approved. It exists to satisfy the disclosure
requirements of the SignPath Foundation OSS code-signing program and
to give downstream users a clear picture of who signs what, when, and
how to verify.

## Project

- **Project name:** VaultSign Signer
- **Repository:** https://github.com/gbpassalacqua/vaultsign-signer
- **License:** [MIT](./LICENSE)
- **Components signed:**
  - `vaultsign-host.exe` — Windows native messaging host (Go)
  - Future installer (`vaultsign-signer-setup.exe`) — once the
    `/install` page is published; will register the host manifest and
    HKCU key on the end-user's machine.

The Chrome extension itself is signed by the Chrome Web Store and is
out of scope for this policy.

## Privacy

This program will not transfer any information to other networked
systems unless specifically requested by the user or the person
installing or operating it.

The host writes a stderr log to `%TEMP%\vaultsign-host.log` on each
launch (action received + payload size, no certificate material, no
hash content). The log is local-only and never leaves the user's
machine. To disable, delete the file after each session or build the
host with `-tags nolog` (Go build constraint).

The extension communicates with `vaultsign.app` (and, in development,
`localhost:8080`). When a third-party site uses the extension via
`externally_connectable`, requests from that origin reach the
background service worker; the worker forwards them to the host
without inspecting payload contents. No analytics, no remote logging,
no telemetry.

## Roles

The project is currently maintained by a single individual. Until a
second long-term maintainer joins, all three roles below are filled by
the same person:

- **Author** — commits source code and PRs. Trusted to modify the
  codebase without external review for routine changes.
  - *Giuliano Passalacqua* — `gbpassalacqua` on GitHub
- **Reviewer** — examines diffs between releases for unintended
  behavior, supply-chain changes, or anything that would invalidate
  the binary's match to the source tree.
  - *Giuliano Passalacqua*
- **Approver** — authorizes each signing request after the
  build pipeline produces an artifact, and only for artifacts that
  match a tagged release commit.
  - *Giuliano Passalacqua*

Multi-factor authentication is enforced on the GitHub account and on
the SignPath account (when active). When a second maintainer joins,
this section will be updated to reflect the separation of reviewer
and approver roles.

## Release process

1. **Source change** — commits land on `main`. Each commit is signed
   off by the author and pushed via HTTPS with a personal access
   token (`gho_…`) protected by 2FA.

2. **Tag** — when a release is ready, a SemVer tag (`v0.X.Y`) is
   pushed to GitHub. The tag points at a specific commit on `main`;
   the tag itself is annotated (`git tag -a`).

3. **Build** — GitHub Actions
   ([`.github/workflows/build-sign-release.yml`](./.github/workflows/build-sign-release.yml))
   runs on `windows-latest`, checks out the tagged commit, runs
   `go build` to produce `vaultsign-host.exe`. The build environment
   is recorded (Go version, runner image, commit SHA) and attached
   to the release as a build provenance file.

4. **Sign** — the binary is signed in the same workflow step. The
   signing credential is either:
   - A SignPath.io managed pipeline (cert issued by SignPath
     Foundation), or
   - Microsoft Trusted Signing (Azure-managed cert)
   depending on which is currently active. The credential's secret
   material never appears in plain text in the workflow logs.

5. **Approve** — for SignPath, the approver reviews the build summary
   (commit SHA, file hashes, version metadata) in the SignPath
   dashboard and clicks Approve. The signed artifact is downloaded
   back to the workflow.

6. **Publish** — the signed artifact is attached to the GitHub
   release.

Every signed binary corresponds to exactly one tagged commit and one
approved SignPath request. Reproducing the build is a matter of
checking out the tagged commit, running `go build` with the same Go
version, and comparing the unsigned binary's hash to the build
provenance entry.

## Third-party dependencies

The host depends on:

- `golang.org/x/image` — image decoding for the icon-generation
  tool only; not linked into the runtime host binary.
- `golang.org/x/sys` — Windows syscalls (`CryptoAPI` wrappers).

Both are part of the Go standard extension repository and follow the
Go security policy. No other runtime dependencies.

The extension has zero JavaScript dependencies (no npm packages).

## Attribution

When this project is signed by SignPath Foundation, downstream pages
(release notes, install page) will display:

> Free code signing on Windows provided by [SignPath.io](https://about.signpath.io/),
> certificate by [SignPath Foundation](https://signpath.org/).

This attribution is required by the SignPath OSS terms and is added to
each release once active.

## Reporting concerns

If you spot a signed release that doesn't appear to match the source
tree, an unexpected signing certificate, or anything that suggests the
signing pipeline was compromised, open a public issue at
https://github.com/gbpassalacqua/vaultsign-signer/issues or contact
the maintainer directly at `giuliano@rocket99ventures.com`.
