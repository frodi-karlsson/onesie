# Security

## Reporting a vulnerability

Please report a vulnerability privately through GitHub's
[security advisories](https://github.com/frodi-karlsson/onesie/security/advisories/new),
not in a public issue. Expect a first reply within a week.

## Scope

onesie handles API keys. These are in scope:

- a key reaching output, a log, a request to a host other than its provider's, or a file other than profiles.json
- profiles.json or credentials.json written with group or world access
- a release archive that does not match its checksum or provenance
- the install script or setup-onesie fetching anything other than a verified release

Only the latest release is supported.
