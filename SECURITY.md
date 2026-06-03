# Security Policy

The MOCO maintainers take the security of MOCO seriously. We appreciate your
efforts to responsibly disclose your findings, and will make every effort to
acknowledge your contributions.

## Supported Versions

Security fixes are provided only for the latest released version of MOCO.
We strongly recommend always running the most recent release.

For the list of releases, see the [Releases page][releases].

Note that MOCO depends on specific versions of MySQL and Kubernetes. Only the
combinations listed in the [README](README.md#supported-software) are tested and
supported.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, please report them privately through GitHub's
[private vulnerability reporting][gh-advisory] feature, which is the only channel
we accept vulnerability reports through:

1. Go to the [Security tab][security-tab] of this repository.
2. Click **Report a vulnerability**.
3. Fill in the advisory form with as much detail as possible.

Please do not publicly disclose the vulnerability, in whole or in part, without
the maintainers' prior consent. We ask that you give us a reasonable amount of
time to investigate and release a fix, and coordinate the timing of any public
disclosure with us.

### What to include

To help us triage and fix the issue quickly, please include as much of the
following as you can:

- A description of the vulnerability and its potential impact.
- The affected version(s) of MOCO (and, if relevant, MySQL / Kubernetes
  versions).
- Steps to reproduce, or a proof-of-concept.
- Any known workarounds or mitigations.

### Reports based solely on scanner output

We do not accept reports that are based solely on the output of automated
security scanners (for example, vulnerabilities reported against MOCO's
dependencies). Such tools frequently produce false positives, and a dependency
advisory does not necessarily mean MOCO is affected. Before reporting, please
confirm that the issue is actually exploitable in the context of MOCO and
include an explanation or proof-of-concept of how it can be exploited.

[releases]: https://github.com/cybozu-go/moco/releases
[gh-advisory]: https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability
[security-tab]: https://github.com/cybozu-go/moco/security
