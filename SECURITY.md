# Security Policy

> **TODO before merging:** the contact address, PGP fingerprints, and
> `.well-known/security.txt` below are placeholders. `.well-known/security.txt`
> in this repo is still Offchain Labs' own PGP-signed file (signed with their
> key) — it can't simply be edited, since a valid `security.txt` must be
> signed by the key it names. LightChain's security contact needs to
> generate and sign a replacement with their own key before this policy is
> accurate. Until that lands, a report about a LightChain-specific bug
> (like the 2026-08-11 consensus incident) has no correct place to go.

## Supported Versions

This is a fork of [Prysm](https://github.com/OffchainLabs/prysm) maintained
by LightChain for the LightChain network. Security fixes are tracked against
this repository's `main` branch; see the [Changelog](./CHANGELOG.md) for
release history. Upstream Prysm's release cadence and support window do not
apply here — LightChain-specific patches (e.g. the inactivity-ejection
logic in `beacon-chain/core/epoch/`) are only fixed in this repo.

## Reporting a Vulnerability

Please email **TODO: security@lightchain.example** (replace with the real
LightChain security contact) with details of the vulnerability. See our
signed [security.txt](./.well-known/security.txt) for the preferred
encryption key once it has been regenerated with LightChain's own PGP key.

**Please do not file a public GitHub issue** mentioning the vulnerability,
as doing so could increase the likelihood of it being exploited before a
fix has been created, released, and adopted across the validator set.

If you believe you have found a consensus-safety issue (anything that could
cause validators to disagree on the canonical chain, or that could halt or
fork the network — including in the LightChain-specific inactivity-ejection
or fixed-supply patches), please treat it with the same urgency as an
upstream Ethereum consensus bug and flag it as such in your report.
