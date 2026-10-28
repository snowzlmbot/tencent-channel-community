# SQLite notification repair

This fork tracks a user-requested repair for [upstream issue #5](https://github.com/tencent-connect/tencent-channel-community/issues/5).

## Verified baseline

- Skill 1.1.5; native CLI 1.0.10; OpenClaw 2026.10.10-beta.4.
- CLI authentication, endpoint reachability and business probes pass.
- `manage notices-on --session-key "<actual-current-session-key>" --json` fails with `data.status=test_failed` because its route resolver looks for legacy `sessions.json`.
- The current OpenClaw runtime owns session rows in SQLite. Legacy JSON is migration input, not a live route index.
- `subscribe-notices` is an alias; `check-new-notices` requires subscription, so neither solves route discovery.

## Repair requirements

1. Discover only the requesting conversation through supported OpenClaw session interfaces, not raw transcripts or database edits.
2. Keep delivery through approved messaging tools; do not use a shell push template or direct unauthenticated RPC as a delivery workaround.
3. Preserve test delivery, explicit user receipt confirmation and final activation as separate stages.
4. Never generate a fake legacy session index, silently activate a subscription or request a fresh login for a route error.
5. Keep credential handling in the existing CLI. No token extraction or private state may enter this repository.
6. Run implementation tests via a manually triggered GitHub Actions workflow once an executable repair exists; mock success is not live notification delivery proof.

## Current implementation boundary

This repository contains the Skill and references, not the native `qqcli` implementation. The installed npm package supplies platform binaries and has no repository metadata pointing to CLI source. A fork of this documentation alone cannot patch the native resolver or subscription engine.

## Independent interaction-query adapter

`scripts/interaction_watch.py` is an independent change detector, not a replacement native binary. A real read-only `feed get-notices` call succeeded without native subscription; the current account returned an empty result (`data.has_more=false`). Populated item schemas and live new-event delivery have not yet been validated.

The adapter compares canonical response snapshots, not guessed event IDs. It reports **query result changes**, which may include deletions or reorderings; it does not promise exactly-once new-event semantics or exhaustive pagination. Only the first page (20 items) is observed. It cannot cover system messages, join requests or direct messages.

```bash
python3 scripts/interaction_watch.py --state /private/local/directory/interaction-state.json
# Only after the approved messaging tool confirms delivery:
python3 scripts/interaction_watch.py --state /private/local/directory/interaction-state.json --ack <delivery_id>
```

The first successful read creates a silent baseline. Later changes create a pending generic notice. Until acknowledged, polls retry that pending notice without moving the baseline. Only a matching acknowledgement commits it; a crash after delivery but before acknowledgement can cause duplicate generic notices (at-least-once, not exactly-once).

No script sends a message, creates a timer, calls a messaging CLI or stores notification content. Use approved OpenClaw tools for delivery, and confirm the intended interval and destination before creating an automation. Keep all state outside the tracked tree, with no credentials in fixtures.

### Verification gates

Run the manual **Interaction adapter QA** workflow on the exact branch commit. Unit fixtures are synthetic; CI does not prove live Tencent delivery. Live activation additionally requires a test message through the approved messaging tool, explicit user receipt confirmation, and a separately agreed schedule. The production Skill, native binary, and existing native subscription files remain unchanged.

## Licensing

The upstream README advertises MIT via a badge, but no root LICENSE file was found in the inspected tree. Do not infer that an unavailable native CLI implementation is covered by that badge, relicense it, or redistribute a modified binary without establishing its source and license.
