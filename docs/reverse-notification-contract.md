# Native notification contract: static reconstruction

## Scope and evidence

This is an independent behavioral/schema reconstruction from the installed native CLI's Go runtime type metadata, Go function table and bounded ARM64 disassembly. It is **not recovered original source**, does not establish a source license, and does not claim vendor support. No executable patching, target execution, credential access, live subscription reads/writes, daemon activity, delivery or network request was performed.

The local ignored `local/native-contract.json` holds binary identity, exact field offsets/tags, selected function boundaries and resolved direct call edges. This document intentionally omits host paths, assembly listings and proprietary implementation dumps. The inspected artifact is the Darwin ARM64 native CLI; behavior on other builds/platforms is not established.

Confidence labels:

- **Confirmed metadata**: decoded runtime type/field metadata or named direct branch target.
- **Confirmed bounded flow**: explicit field access, comparison and branch in a selected function.
- **Inferred/documented**: consistent with call paths or existing reference documentation, but not fully proven by this static pass.

## Exact persisted field contract

The following tables were regenerated from runtime metadata, not copied from source. All tags shown are the exact `json` tag values. Empty cells mean no JSON tag. Types are Go field types, not JSON runtime values; a `string` containing JSON must not silently become an object.

### `SubscriptionState`

| Field | Go type | JSON tag |
|---|---|---|
| Active | bool | `active` |
| GuildID | string | `guild_id,omitempty` |
| MyTinyID | string | `my_tiny_id,omitempty` |
| CreatedAt | time.Time | `created_at` |

**Confirmed metadata.** Subscription enablement and delivery routes are separate records; `GuildID` existing in the type does not prove notifications are limited to one guild.

### `Route` and route resolution

| Type | Field | Go type | JSON tag |
|---|---|---|---|
| Route | Targets | []subscription.RouteTarget | `targets,omitempty` |
| Route | OpenClawBin | string | `openclaw_bin,omitempty` |
| RouteTarget | SessionKey | string | `session_key` |
| ResolvedTarget | SessionKey | string | none |
| ResolvedTarget | AgentID | string | none |
| ResolvedTarget | SessionID | string | none |
| ResolvedTarget | Channel | string | none |

**Confirmed metadata:** the persisted native target has **only `session_key`**, not `channel`, `to`, `account_id` or `topic_id`. `ResolvedTarget` is a separate in-memory representation. Do not implement the outdated documentation's `channel + to` file shape as though it matched this artifact.

**Confirmed calls:** `RouteTarget.Resolve` invokes `ParseAgentIDFromSessionKey` and `ResolveSessionIDFromSessionKey`; failure is returned through the error path. `LoadRoute` calls `ReadFile` and `json.Unmarshal`. `SaveRoute` calls `MkdirAll`, `json.MarshalIndent` and `atomicWriteFile`. Exact corruption handling, file permissions, rename/fsync semantics and interprocess locking need further verification.

The legacy session-ID resolver is a replaceable compatibility boundary, **not** a reason to fabricate session files or edit runtime state. The replacement needs an authorized owner-scoped host resolver preserving the full session key, account and topic context.

### `Baseline`

| Field | Go type | JSON tag |
|---|---|---|
| Sources | map[string]*subscription.SourceBaseline | `sources` |
| MyTinyID | string | `my_tiny_id,omitempty` |
| NextRef | int | `next_ref,omitempty` |
| DM | *subscription.DMBaseline | `dm,omitempty` |
| LegacyIDs | []string | `ids,omitempty` |

### `SourceBaseline`

| Field | Go type | JSON tag |
|---|---|---|
| Watermark | int64 | `watermark` |
| AtWatermark | []string | `at_watermark,omitempty` |
| RecentNotices | []subscription.RecentNotice | `recent_notices,omitempty` |
| OldIDs | []string | `ids,omitempty` |

**Confirmed metadata.** The precise stored JSON field is **`at_watermark`**, not the prose term `idsAtWatermark`. Legacy `ids` fields exist at both levels; their existence does not establish complete migration semantics. Source partition names `interact`, `system`, `dm` are documented and consistent with distinct native pollers.

### Private-message cursors

| Type | Field | Go type | JSON tag |
|---|---|---|---|
| DMBaseline | Channels | []subscription.DMChannelBaseline | `channels` |
| DMChannelBaseline | GuildID | string | `guild_id` |
| DMChannelBaseline | ChannelID | string | `channel_id` |
| DMChannelBaseline | LastSeq | string | `last_seq` |
| DMChannelBaseline | LastTime | string | `last_time,omitempty` |

**Confirmed metadata.** Sequence IDs are strings; converting them to platform-sized integers loses schema compatibility and may lose precision. Native `CompareDMBaseline` is distinct from the source timestamp watermark mechanism; preserve both.

### `RecentNotice`

| Field | Go type | JSON tag |
|---|---|---|
| Ref | int | `ref` |
| Source | string | `source` |
| FeedID | string | `feed_id,omitempty` |
| GuildID | string | `guild_id,omitempty` |
| GuildName | string | `guild_name,omitempty` |
| ChannelID | string | `channel_id,omitempty` |
| PosterNick | string | `poster_nick,omitempty` |
| PosterID | string | `poster_id,omitempty` |
| Type | string | `type,omitempty` |
| Summary | string | `summary,omitempty` |
| FeedCreateTime | string | `feed_create_time,omitempty` |
| FeedAuthorID | string | `feed_author_id,omitempty` |
| Timestamp | int64 | `ts,omitempty` |
| NoticeID | string | `notice_id,omitempty` |
| NoticeActionList | string | `notice_action_list,omitempty` |
| CommentID | string | `comment_id,omitempty` |
| CommentAuthorID | string | `comment_author_id,omitempty` |
| CommentCreateTime | string | `comment_create_time,omitempty` |
| ReplyID | string | `reply_id,omitempty` |
| FetchedAt | int64 | `fetched_at,omitempty` |
| Pushed | bool | `pushed,omitempty` |
| PushedAt | int64 | `pushed_at,omitempty` |

**Confirmed metadata.** `NoticeActionList` is a **string**. `Pushed` is a single bool, not a per-route acknowledgement map. An independent implementation must not claim exact-once per-route delivery from this schema.

## Watermark and recent-record behavior

### Exact predicate: `IsNewNotice`

**Confirmed bounded flow:** takes a baseline/source, timestamp and identity; creates a missing source record; compares against that source's watermark:

1. Timestamp greater than watermark: new.
2. Timestamp lower than watermark: not new.
3. Equal timestamp: new only when identity is absent from `AtWatermark`.

**Important:** this helper does **not** implement the first-poll `watermark == 0` historical-message suppression. The native poller/caller owns that policy. Reusing this helper alone would replay historical notices.

### Exact update: `AdvanceWatermark`

**Confirmed bounded flow:** initializes a missing source; greater timestamp replaces watermark and boundary-ID slice; lower timestamp does nothing; equal timestamp unions new identities into the existing boundary slice using a membership map. This retains same-second events instead of treating a second as a unique event.

### Reference and history handling

**Confirmed bounded flow:** `AppendRecentNotices` limits the resulting retained slice to **100 entries per source**. It increments shared `Baseline.NextRef`, wraps to **1** when the increment exceeds **999**, then assigns the record reference. Never treat `Ref` as a permanent globally unique event identifier.

The function also uses a four-string `dedupeKey` (`a`, `b`, `c`, `d`, no JSON tags). Exact key member mapping, record merge/ordering and whether repeated inputs update selected fields were **not** fully recovered. Implementing a guessed composite identity and calling it native-compatible would be unsupported.

**Confirmed bounded flow:** `MarkRecentNoticePushed` sets `Pushed = true` and `PushedAt` to current Unix seconds. It does not prove server acknowledgement or user receipt.

## Activation and confirmation call paths

### Initial command: `SubscribeNotices`

**Confirmed named direct calls:** platform availability (`HasOpenClawCLI`), session-key extraction (`OptionalStr`), `RouteTarget.Resolve`, host executable resolution, confirm-flag extraction (`GetBool`), daemon PID/liveness checks, `setupSubscription`, `WriteRouteVerify`, `WriteTestPassed`, and `subscribeConfirm`.

These calls establish that the initial flow is not just a JSON state toggle. Exact branch-sensitive side effects need fixture verification; the presence of `setupSubscription` does not mean every non-confirm invocation activates notifications.

### Confirmation: `subscribeConfirm`

**Confirmed bounded flow:** an empty session key fails; it resolves the route before state mutation; it clears a test-passed marker, examines existing daemon/liveness and routes, and may call `setupSubscription` or save a route into the existing set.

**Important safety distinction:** this pass recovered **no `ReadTestPassed` call or corresponding function**. The presence of `WriteTestPassed`/`ClearTestPassed` is not evidence of an enforced prior-delivery/receipt gate. Documentation requires the agent to wait for explicit user receipt before `--confirm`; a new implementation should enforce its own session-bound expiring test challenge/receipt and require explicit user confirmation, rather than assume the binary already provides it.

### Setup: `setupSubscription`

**Confirmed named direct calls:** old PID lookup, `StopDaemon`, `WaitForProcessExit`, daemon-error clearing, `NoticeTimestamp`, `NoticeIDFromRaw`, baseline save, current time, state save, route load/save, daemon-argument construction, fork, PID save and `WaitForDaemonAlive`.

**Inferred/documented:** setup establishes a source baseline and persists state/routes before starting a native daemon. Do not copy this into a test-only path that should not activate subscriptions. Exact rollback behavior after partial persistence/fork failure remains unproven.

### Later route verification

**Confirmed named direct calls:** `checkAndVerifyNewRoute` reads then clears a route-verification marker, resolves its target, resolves the host executable and invokes `safeDeliver`. This is distinct from adding a supported first-activation route API.

## Polling, delivery and retry contract

**Confirmed call graph:**

- Interaction: `pollOnce` → `NoticeTimestamp` / `NoticeIDFromRaw` → `IsNewNotice` → `CleanNotice` → `AdvanceWatermark` → `RecentNoticesFromCleaned` → `AppendRecentNotices`.
- System: `pollSystemNotices` → `SystemNoticeTimestamp` / identity extraction → `IsNewNotice` → `CleanSystemNotice` / guild-name resolution → watermark / recent-record helpers.
- DM: `pollDMNotices` → `ExtractDMList` → `CompareDMBaseline` → sequence parsing / sorting / cleaning → watermark / recent-record helpers; a base64 decoding call also exists. Full API and decoding semantics were not recovered.
- Push: `pushNotifications` contains a custom shell-template path using `FormatNewNotices` → `executeShellPush`, and a normal per-item path using `FormatNoticeCard` / `buildDeliverMessage` → `deliverOneNotification`.
- Retry: the normal path also calls `formatRecentNoticeForPush` → `buildDeliverMessage` → `deliverOneNotification`; success branches reach `MarkRecentNoticePushed`. Retained records therefore participate in retry independently of re-polling source events.
- Target delivery: `deliverOneNotification` reloads persisted routes, resolves target sessions and calls `safeDeliver`; `safeDeliver` calls `deliverToTarget`.

**Documented, not fully independently reconstructed here:** digest-gated interaction/system polling, every-loop DM polling, independent heartbeat, and saving baseline after polling/delivery. Cloud fixtures must verify each ordering before declaring parity.

**Not established:** all-target vs any-target success aggregation, stable target-specific retry state, retry delay/backoff, retry exhaustion, crash-window guarantees, exact event-identity extraction and full API pagination. The shell-template path must not be assumed to preserve recent-record marking/reference/retry behavior merely because it can emit formatted text. No shell-based messaging transport should be enabled as a substitute for an approved host delivery capability.

## Concrete independent native Go implementation plan

1. **Reconstruct schema only:** define owned Go types with the confirmed tags above. Decode fixture state, route and baseline records, including optional/legacy fields and unknown-field tolerance; do not read the user's live records during implementation.
2. **Separate pure policies:** implement/test `IsNewNotice`, watermark advancement and initial-baseline suppression as different functions. Keep timestamp plus boundary-ID sets and separate DM string cursors. Use explicit deterministic source identities; until key mapping is recovered, label them a new compatibility policy rather than pretending binary parity.
3. **Build explicit host boundaries:** define interfaces for source fetching, owner-scoped route resolution, delivery with receipts, clock, persistence and daemon control. Persist the full authorized session key. Preserve topic/account context within a host-owned authorized resolver rather than synthesizing a legacy session index.
4. **Implement safe two-step enablement:** test-only delivery must not activate the subscription; bind an expiring challenge to owner and route; require receipt plus explicit user confirmation; then add only that route. Failure must preserve existing routes. Registration is independent of global `active` status.
5. **Durable delivery ledger:** a timestamp watermark tracks ingestion, not success. Retain pending records before advancing persisted ingestion; acknowledge only confirmed delivery. Prefer a versioned per-route ledger, with careful migration from the recovered aggregate `Pushed` bool. Do not silently assign old aggregate success to newly added routes. Refuse unauthenticated/fabricated receipts.
6. **Persistence and lifecycle:** write atomically with restrictive permissions, crash recovery and per-owner/process locking. Stop/start/rollback and token-change cleanup need explicit tests. Do not hand-edit the existing live state or start a daemon before authorization and acceptance gates.
7. **Manual cloud QA:** fixtures for historical suppression, equal-second identities, older timestamps, missing source, DM sequence precision, 999→1 ref rollover, 100-record retention, partial route failures, topic isolation, invalid owner, test-only no activation, failed receipt, restart/crash windows and credentials changing. Add baseline-before/after behavior evidence without publishing host identity, private state or credentials.
8. **Acceptance still requires an authorized real path:** fixture tests demonstrate code behavior, not real host delivery. Only after approved integration and explicit permission may a test notification, user receipt, confirmed exact route and one full native notification lifecycle establish operational success.

The original source is not necessary to begin this independent implementation. What remains unknown should be isolated as replaceable compatibility policies and validated, rather than used as a generic reason to stop engineering or an excuse to patch the opaque executable.
