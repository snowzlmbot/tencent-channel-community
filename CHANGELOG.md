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
