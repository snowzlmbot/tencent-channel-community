# 更新日志

## 0.1.0 — fork interaction adapter (2026-10-28)

- Add an independent interaction-query change detector using the supported read-only `feed get-notices` CLI command.
- Establish a silent initial baseline; deliver only generic update indicators, never historical notification content.
- Keep local state private and separate from native CLI subscriptions. Delivery acknowledgement is explicit and must follow confirmed messaging-tool delivery.
- Add manually triggered, credential-free GitHub Actions contract tests.
- This is not a patched native CLI. System notifications, join requests and direct messages are outside this adapter's scope. No automatic subscription or scheduler installation is performed.

- Cloud QA exposed a symlink-test fixture collision with a deliberately persistent lock; isolate the test cases without weakening the runtime rejection behavior.

## Native repair investigation

- Clarify that the requested full native engine repair is blocked on the unavailable native CLI source/build interface; the snapshot adapter is not activated as a substitute.
- Document native routing, authorized host delivery, full-source semantics and acceptance requirements in `docs/native-repair.md`.

- Audit confirms native shell-template extension exists but does not remove legacy route/active-subscription gates; document precise compiled source patch points without claiming a working native patch.

## Native reconstruction development

- Owner authorized local binary analysis and independently authored native compatibility development.
- Preserve the installed binary and existing login; distinguish recovered metadata/behavior from original source.
- Add manual cloud validation for the native core and synthetic metadata recovery fixtures; do not upload original binaries or private state.

- Bind native test receipt confirmation to a ten-minute expiry, including revalidation after route resolution; add deterministic expiry fixture.

- Recover five exact native notification MCP tool contracts and preserve native caveats separately from independently authored improvements.
- Add recovered state DTOs plus versioned dedicated-state store with strict decoding, atomic writes, private files and nonblocking lifetime lock; no legacy-state migration or production activation.

- Reject state and lock files not owned by the effective runtime user, in addition to symlink, hardlink and permission checks.

- Add five-source-stage native fetch protocol parsers and precise DM sequence handling; recover private-at-runtime authenticated Tencent HTTP contract, replacing the earlier bridge-only engineering limitation.

- Add an independently authored, fixed-endpoint authenticated native read client with private dotenv runtime credential loading, five-tool allowlist, verified TLS/proxy transport, no redirects, and bounded sanitized responses.
- Add a bounded standalone `tencent-channel-native probe` command that reports only source counts and never activates subscriptions; cloud cross-compilation and explicitly approved development-application packaging are separate from test evidence and production installation.

- First isolated live probe failed at the interaction stage; add closed, sanitized credential/transport/HTTP/envelope/shape error categories rather than printing raw request, credential or response details.
- Add the reconstructed three-source atomic engine with separate durable pending ledger and explicit delivery acknowledgment; native installation and activation remain unchanged.

- Isolated live diagnostics confirmed authenticated service acceptance but interaction response-shape mismatch. Add fixed-key/type-only diagnostics to inspect envelopes without disclosing notification bodies, values or dynamic identifiers.

- Integrate independent native route-test, actual-host-receipt recording, explicit activation, durable three-source poll/outbox, exact receipt acknowledgments, status and off commands. Message delivery stays in approved host tools; no shell messaging or legacy session index is introduced.
- Add a synthetic assertion that envelope diagnostics never expose values or arbitrary dynamic identifiers.

- Fix real authenticated empty responses: interaction uses omitted notices plus string attachInfo/isFinish=true; system and DM use empty structuredContent maps. Keep malformed/null and unrecognized envelopes rejected.

- Align old missing-list fixtures with the authenticated empty-map observation while retaining an unknown-envelope rejection fixture.

- Runtime pagination metadata confirms a nonempty opaque cursor may accompany an empty finished interaction list; validate its type, not its value, without logging it.

## Live development verification checkpoint

- Exact source e154b8f passed manual cloud race/static checks and application build, then real authenticated interaction/system/DM-list reads on macOS (all empty for this account).
- Native route test delivered through an approved host tool and actual receipt persisted as TESTED; no activation, background supervisor or real new-event proof yet.

## Confirmed activation

- After explicit user receipt confirmation, native route activation advanced from TESTED to ACTIVE.
- Add a bounded host-tool supervisor template and synthetic receipt/route/privacy contracts. It polls the native three-source engine, sends only fixed generic messages through the approved tool, and acknowledges only confirmed exact-topic receipts. Quiet checks do not run an LLM.
- First scheduler-forced run succeeded and initialized all three baselines silently; no actual new-event delivery has yet been observed.

- Correct the supervisor tool-budget fixture to distinguish nine exec calls from eight message calls (17 total), using distinct synthetic outbox IDs. No runtime behavior changed.
