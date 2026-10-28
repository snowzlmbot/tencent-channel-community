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
