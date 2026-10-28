# Native notification repair: source-level gate

The requested target is the native CLI notification engine, not the independent interaction snapshot adapter. Do not activate the adapter as a substitute for this request.

## Verified blockers

- Current npm native CLI: 1.0.10; module `qqcli`, build gitHead `078c474fde6a9b26139acf5075bde15180b71939`.
- The official Skill repository has documentation, not the native Go implementation.
- npm metadata does not identify a source repository or license. The local package has no Go source, and no local qqcli module source was found.
- Official Skill repository has no releases at investigation time; the open compatibility issue has no maintainer response.
- The existing CLI route test fails on a retired legacy session JSON lookup. Altering that file is not a source-level fix.

## Required patch surface once source is available

1. Replace direct legacy session JSON discovery with a supported, owner-scoped conversation route resolver. Keep conversation/topic/account context intact, reject unknown or unauthorized sessions, and avoid reading transcripts.
2. Replace shell-based platform delivery (where present) with a supported authorized host integration. Source availability alone does not grant messaging authority; require an approved delivery capability and receipts.
3. Keep native interaction/system/direct-message pollers, source-specific baselines, same-second dedupe, retries and reference mapping. Do not substitute first-page snapshot hashes for native event identity.
4. Require test delivery and explicit user receipt confirmation before activating any route. Treat a successful CLI envelope with `status=test_failed` as failure.
5. Preserve existing credential storage, controlled egress and TLS verification. Do not export secrets, rewrite subscriptions by hand, inject a fake executable, downgrade session storage or hot-patch opaque binaries.

## Validation gate

Use a manually dispatched cloud workflow to test owner/session routing, topic isolation, authentication failure, receipt failure, two-step activation, duplicate/retry semantics and all supported notice sources. Credential-free fixtures do not establish real delivery. Final acceptance additionally needs an authenticated native test message, user confirmation, subscription status showing the exact authorized route, and a real notification lifecycle.

## Revised approach: authorized binary analysis and independent reconstruction

The owner has now explicitly requested analysis of the installed native CLI and secondary development, rather than waiting for original source. Missing source is no longer the only engineering path. Recover Go metadata and selected control/type contracts from the installed binary, then author a separately buildable compatibility implementation.

Recovered names, layouts and control flow are **not original source code**. Decompiled pseudocode can omit types, semantics and source-level constructs. Do not claim a lossless restoration or infer native binary redistribution rights from the Skill README badge. Original binaries, bulk disassembly and embedded absolute developer paths stay local; public code is independently authored, with provenance and verification limits documented.

The original installed CLI and credentials remain unchanged until reconstructed behavior, supported host integration and real native delivery have passed their gates. The independent native core does not count as an integrated CLI or enabled subscription.

## Native binary audit findings

Read-only help/schema inspection and static Go function/call-path analysis identified a real `notify-daemon --push-command` extension. Its shell-template branch formats native notices and executes a shell command; it is not a socket/callback transport. This does **not** solve initial activation: `notices-on` still resolves the legacy session index, the daemon requires active subscription state, and the worker validates an existing session-key route. `OPENCLAW_STATE_DIR` changes lookup roots, not the route representation.

The normal native worker contains interaction, system and direct-message pollers. Preserve those rather than substitute the independent snapshot adapter. Static evidence also shows the custom shell-template branch differs from the normal per-item delivery path; do not assume identical retry, pushed-record or reference semantics without tests.

Likely source-level patch points, based on compiled function/source metadata:

- `internal/subscription/state.go`: `RouteTarget.Resolve`, `ResolveSessionIDFromSessionKey`.
- `commands/pd/manage/subscription.go`: activation/confirmation, `hasPushRoute`, `pushNotifications`, normal delivery and template transport handling.
- `internal/subscription/daemon.go` / `daemon_unix.go`: persistence and inheritance across worker restart and guard recovery.

These are static investigation findings, not recovered buildable source or proof of a running fix. No binary patching, subscription-state editing, daemon startup or real message delivery was performed during this audit. There is still no supported first-activation route injection flag identified.

## Work streams

- `scripts/recover_go_metadata.py`: dependency-free recovery of selected Go function metadata from the installed Mach-O; synthetic parser tests in cloud.
- `docs/reverse-notification-contract.md`: selected behavioral/type findings, with confirmed versus inferred facts.
- `native/`: independently authored Go route/test-confirmation and source-watermark core; no guessed Tencent endpoint or shell delivery transport.
- `.github/workflows/native-qa.yml`: manual cloud parser tests, native race tests and static checks; no binary or account credentials uploaded.

Until the host transport and real three-source notification integration are verified, this remains development work, not completed native repair.

## Fetch and durable-state reconstruction

See [recovered fetch contract](reverse-fetch-contract.md) for five exact tool names/arguments, three-source response shapes and native cursor caveats. `native/notices/schema.go` models recovered DTOs; `store.go` persists a separate versioned compatibility state with per-route delivery records. These components do not write original CLI subscription files. Authenticated caller and host-delivery integrations remain separate gates; no all-source live readiness is claimed.

## Authenticated runtime implementation path

[The native HTTP contract](reverse-native-http-contract.md) recovers the direct read-only Tencent service request format. The independent caller will keep credentials private in memory, pin the endpoint and five read tools, retain controlled proxy/TLS verification, and reject redirects. This supersedes the prior vendor-bridge-only interpretation. No native API request or account data is included in public fixtures.

## Read-only development command

The standalone `native/cmd/tencent-channel-native` command supports `probe`: it performs bounded interaction, system and DM-list reads with the independent caller, prints only counts/stage status, and does not write a baseline, send messages or enable subscriptions. It currently supports an existing private dotenv login only, not keychain-only credentials.

Cloud QA tests and cross-compiles the exact source. The separate `Native development package` workflow requires an explicit boolean approval and reruns checks before packaging the application. Its artifact is a deployment application, not a source of local test evidence; no test logs or credential data are downloaded as substitutes for cloud results. Production replacement and notification activation remain unperformed.

## First isolated runtime probe

The cloud-built development application was checksum-verified and deployed only under an ignored local directory. Its first read-only run failed at the interaction stage; no source data or credential was printed and no subscription was activated. Sanitized stage diagnostics are being added to distinguish protocol-shape, transport and credential failures before further runtime checks. The separately reconstructed engine is not yet connected to active delivery.

## Native command / authorized host bridge

The independent native command now includes `route-test`, `record-test`, `enable --confirm`, `once`, `ack`, `status` and `off`. It owns a separate private state file. Route inputs must come from the actual trusted conversation context, not guessed IDs. The command never sends platform messages itself.

`route-test` persists a ten-minute challenge before emitting its generic test request. The authorized host sends the request through its available messaging tool, then records the actual receipt with `record-test`. Only explicit user confirmation permits `enable`. Each `once` persists the three-source engine's generic outbox before output; after confirmed messaging-tool delivery, `ack` records the exact item/route receipt. Receipts are trusted-host inputs, not cryptographically verified by this standalone process.

This is an independently rebuilt native engine and command, not an in-place patch of the original vendor binary. Generic update notices do not yet reproduce rich cards, notification reference actions or precise native cleaning parity. No active subscription or background host supervisor has been enabled during development.

## Observed empty-response correction

Value-free native diagnostics and a bounded authenticated schema-key precheck confirmed that the empty interaction response contains an opaque string `attachInfo` and `isFinish=true` without `notices`; empty system/DM responses contain an empty `structuredContent` map. The parser now handles these exact protobuf-style empty cases after HTTP/RPC validation. Present null/wrong-type lists and unknown envelopes remain rejected. This is real empty-account compatibility evidence, not proof of populated event delivery.
