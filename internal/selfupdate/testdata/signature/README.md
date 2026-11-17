# Public checksum signature fixture

`checksums.txt` and `checksums.txt.sigstore.json` are the published, public
v0.16.1 release artifacts from matcra587/jira-cli. The bundle contains public
certificate and transparency evidence, with no private key or credentials.

`trusted_root.json` was fetched on 2026-11-17 through sigstore-go v1.4.0's
TUF verifier, anchored in that module's embedded production TUF root. It is a
fixed offline test fixture, not the application's runtime trust configuration.
Production verification refreshes authenticated trust metadata in memory.

These fixtures deliberately target a fixed historical release. New releases do
not require updating them. Tests check the exact manifest, workflow/tag identity,
certificate timestamps and transparency evidence, and reject a changed manifest
or a different release tag. Archive-extraction fixtures use an explicit verifier
stub; their success does not establish signature validity.
