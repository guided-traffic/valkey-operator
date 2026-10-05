# Security policy

## Reporting a vulnerability

Report privately. Please do **not** open a public issue for a finding that lets someone read
a Secret, escalate RBAC, or destroy data.

- Report through **GitHub private vulnerability reporting** on
  <https://github.com/guided-traffic/valkey-operator> (Security → Report a vulnerability):
  <https://github.com/guided-traffic/valkey-operator/security/advisories/new>.
- Or report to the maintainer organisation, <https://github.com/guided-traffic>.

> **Gap, stated plainly:** this repository publishes no contact address and no response time
> or disclosure window — stated rather than invented. Whether private vulnerability reporting
> is switched on for this repository is not verified here. If the form is not offered to you,
> the maintainer organisation is the remaining route.

Include the operator version (`app.kubernetes.io/version` on the operator pod), the chart
version, and whether TLS and auth were enabled. Leave out the cluster password, TLS private
keys and any other Secret content.

## What is already known

The security design of the operator, including the gaps it does not close, is documented
under [docs/security/](docs/security/). Anything written there is known — a report that adds
a working exploit, a wider consequence, or a case the analysis missed is still valuable. An
open gap there carries an `H-<n>` identifier in its heading; naming it in a report saves a
round trip. Anything that is **not** written there is what we most want to hear about.

## Supported versions

Releases are cut from `main` only: semantic-release is configured with `main` as its one
release branch ([.releaserc.json](.releaserc.json)), and the repository has no maintenance
branch (remote branches checked 2026-09-27). A fix therefore lands on `main` and ships in the
next release; there is no backport to an earlier release.
