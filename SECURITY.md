# Security Policy

> **TODO before merging:** the contact email below is now real
> (team@lightchain.ai). The PGP fingerprints and
> `.well-known/security.txt` are still placeholders — that file
> can't simply be edited, since a valid `security.txt` must be
> signed by the key it names. LightChain's security contact needs
> to generate and sign a replacement with their own key before
> the encryption piece of this policy is accurate.

## Supported Versions

This is a fork of [Prysm](https://github.com/OffchainLabs/prysm) maintained
by LightChain for the LightChain network. Security fixes are tracked against
this repository's `main` branch; see the [Changelog](./CHANGELOG.md) for
release history. Upstream Prysm's release cadence and support window do not
apply here — LightChain-specific patches (e.g. the inactivity-ejection
logic in `beacon-chain/core/epoch/`) are only fixed in this repo.

## Reporting a Vulnerability

Please email **team@lightchain.ai** with details of the vulnerability. See
our signed [security.txt](./.well-known/security.txt) for the preferred
encryption key once it has been regenerated with LightChain's own PGP key.

**Please do not file a public GitHub issue** mentioning the vulnerability,
as doing so could increase the likelihood of it being exploited before a
fix has been created, released, and adopted across the validator set.

If you believe you have found a consensus-safety issue (anything that could
cause validators to disagree on the canonical chain, or that could halt or
fork the network — including in the LightChain-specific inactivity-ejection
or fixed-supply patches), please treat it with the same urgency as an
upstream Ethereum consensus bug and flag it as such in your report.
