# Native runner credential policy

## Resolution and privacy

On macOS, the owned runner first reads the official CLI generic-password item: service `qq-cli`, account `token`. It runs only the fixed `/usr/bin/security` executable with fixed lookup arguments, captures stdout into a bounded private memory buffer, discards stderr and provides no stdin. A three-second context deadline and bounded process wait prevent an access-control prompt or locked Keychain from indefinitely blocking the runner; it never unlocks the Keychain, changes access controls or retries interactively. A denied, unavailable, empty, malformed or timed-out lookup falls back to the selected private dotenv file, matching the recovered native resolution order. Parent cancellation fails closed without fallback.

A valid Keychain result wins even when `QQ_AI_CONNECT_DOTENV` points at an older or missing file. Linux retains the existing dotenv-only behavior and never launches a Keychain command. Dotenv selection and strict owner/0600/symlink/hardlink checks are unchanged. Neither platform reads the token environment variable or searches MCPorter/legacy stores.

Only one trailing newline from the system helper is removed. Empty, whitespace/control/non-ASCII and oversized values are rejected. Credentials are never printed, persisted, copied to dotenv, passed as command arguments or included in error messages. Capture bytes are cleared after use; Go strings and HTTP headers necessarily hold the credential in runtime memory, so this is not a claim of guaranteed whole-process memory erasure. All failures expose only existing sanitized categories. No token refresh, login, subscription mutation or baseline migration occurs.

## Verification and deployment

All credential tests inject synthetic command/provider functions or use private temporary fixtures. They do not call `DefaultCredential` against a real Keychain, run the system credential helper or read a real login file. Lightweight credential-only precheck:

```sh
cd native
go test -count=1 ./notices -run 'Test(Keychain|NativeCredential|NativeDotenv|DefaultCredential)'
```

Full race/static checks and cross-build belong to manually dispatched `native-qa.yml`. Development application packaging belongs to `native-package.yml`, requiring its explicit `approve_development_package=true` input. Do not remove or bypass that gate. Verify the source commit and both successful cloud runs before downloading the deployment application and its SHA-256 manifest to an ignored, new version directory. Do not download test artifacts as deployment evidence or build locally.

Use only the verified application `probe-structure` command for the authorized live read: it checks interaction, system and DM-list envelopes, outputs fixed field/type/count metadata and does not reveal notice text or identifiers. Check each source's `accepted` result; the command's zero exit status alone is not success. This read neither writes a baseline nor activates, sends, acknowledges or schedules anything. Activation and scheduler payload changes require their separate authorized workflow. Keep the previous executable/state untouched for rollback.

Before synchronization, verify repository visibility matches the authorized destination. A public/private mismatch requires owner clarification; do not change repository visibility or publish based on a mistaken private-repository assumption.
