# Native three-source fetch contract (CLI 1.0.10)

## Scope and evidence

Independent behavioral reconstruction, **not original source recovery or a licensing claim**. Evidence: installed Darwin ARM64 binary identity matching `local/native-contract.json`, Go function/type metadata, bounded disassembly, and offline `schema` / `--help` output. No API call, credentials, account/state read, daemon, delivery, executable patch or authentication bypass was used. Offline help/schema executes the installed CLI but does not execute business commands. Other platform/version parity is unproven.

Labels: **confirmed** = explicit selected instruction/data/type evidence; **inferred** = recommended owned representation or integration design, not a native declaration. Local address/evidence details are in ignored `local/native-fetch-contract.json`. These are actual MCP `tools/call` names, **not** automatically available OpenClaw tool IDs.

## Result: exact fetch identities and construction

All five selected calls use the same native `mcp.Caller.Call` interface method, not `CallRaw`. The interface table's first method resolves to `httpCaller.Call`; its second to `CallRaw`. `Call` recursively transforms argument keys using `toCamelKeysInner`, then invokes `callMCPInternal`; `CallRaw` goes directly to that internal function. Therefore distinguish native pre-wrapper map keys from wire keys.

| Source / stage | Exact MCP tool name | Native map before wrapper | Wire argument object |
|---|---|---|---|
| Interaction | `get_interact_notice` | `page_num: int(50)`, `guild_id: string("0")` or selected guild string | `{"pageNum":50,"guildId":"0"}` (or selected guild) |
| System | `get_notice_list` | `limit: int(5)` | `{"limit":5}` |
| DM channel list | `query_normal_dm_list` | empty map | `{}` |
| DM changed-channel messages | `query_guild_channel_msg` | `guildChannelParam: map`, `directMessageFlag: int(1)` | see below |
| Digest gate | `query_user_guild_digest` | empty map | `{}` |

**Confirmed:** interaction guild ID defaults to the string `"0"`; a nonempty non-`"0"` subscription guild replaces it. System has no guild argument in this poller. The numbers 50, 5 and 1 are boxed Go `int` constants (not strings, bools or page counts).

Exact DM message request shape (all four nested fields are strings):

```json
{
  "guildChannelParam": {
    "guildId": "<diff.GuildID>",
    "channelId": "<diff.ChannelID>",
    "beginSeq": "<computed string>",
    "endSeq": "<diff.NewSeq>"
  },
  "directMessageFlag": 1
}
```

**Confirmed begin sequence:** `"1"` when `diff.IsNew` or `diff.OldSeq` is empty; otherwise parse `OldSeq` as signed decimal 64-bit, add 1 and format decimal. If parsing fails, the branch retains `OldSeq` unchanged. Do not substitute JS floating-point arithmetic or infer an unbounded server-side pagination API. Native itself uses signed 64-bit parsing; a replacement can validate more strictly but must label changed overflow/error behavior.

### There are no native typed API request structs here

The examined fetchers build `map[string]interface {}` objects and type-assert dynamic response maps/slices. **No named API request/response struct or native JSON field tags for these five tools were established.** Inventing `QueryNormalDMListRequest` as a recovered vendor type would be wrong. The exact keys above are map literals, not recovered struct tags. The following are useful **owned reconstruction types**, not original types:

```go
type GuildChannelParam struct {
    GuildID   string `json:"guildId"`
    ChannelID string `json:"channelId"`
    BeginSeq  string `json:"beginSeq"`
    EndSeq    string `json:"endSeq"`
}
type DMMessageArgs struct {
    GuildChannelParam GuildChannelParam `json:"guildChannelParam"`
    DirectMessageFlag int               `json:"directMessageFlag"`
}
```

Use wire-shaped structs only at a boundary explicitly expecting wire keys; do not camelize tool names. This is a protocol description, not permission to create a new credential/network client.

## Response envelope and source keys

**Confirmed wrapper:** `callMCPInternal` builds a JSON-RPC request with `jsonrpc`, `id`, `method`, `params.name`, `params.arguments`; it decodes `result`, checks `isError`, `_meta.AdditionalFields.retCode` and `errMsg`, and classifies failures. It has existing native endpoint/auth/cookie behavior. A new source must not bypass those checks or export its token. The pollers unwrap `structuredContent` only when it is a map; otherwise they use the returned map directly.

| Stage | Exact keys and dynamic shapes observed |
|---|---|
| Interaction | payload `notices`: `[]interface {}`; each accepted element a `map[string]interface {}`. Timestamp helper checks `psvFeed.createTime`, `psvFeed.create_time`, then top-level `createTime` / `create_time` fallback paths. Full interaction identity/cleaning is not reconstructed here. |
| System | payload `noticeList`: `[]interface {}`; accepted items maps. Timestamp helper reads `createTs`, then `create_time`; identity reads `noticeId`, fallback `notice_id`. Cleaner accesses `title`, `subTitle`, `comment`, `createTs`, `actionList`, `noticeType`, `jumpButtons`; button paths access `type`, `paramH5`, `param`, `summary`. This is not a complete server schema. |
| DM list | payload `rptMyDirectMessageInfo`: `[]interface {}`. Item guild keys `guildId` / `guild_id`; channel keys `channelId` / `channel_id`. **Exact misspelling:** `lastestMsgSeq` (not `latestMsgSeq`), a map containing `uint64Seq` and `uint64Time`, extracted as strings. Entries missing usable guild/channel IDs are skipped. |
| DM messages | payload `guildChannelMsg`: map, then `msgs`: `[]interface {}` of maps. Comparator reads `msgSeq` as a decimal string and sorts ascending using signed 64-bit parse. Poller reads string `text`, string `msgTime`, sender `fromTinyId` / `from_tiny_id`; skips empty text and messages from `SubscriptionState.MyTinyID` when both IDs are available/equal. Metadata path: `extInfo.directMessageMembers`, with sender nick lookup and `nickName` base64 decode. Decode-failure/fallback exact text semantics are not completely recovered. |
| Digest | payload `datas`: `[]interface {}`; each accepted map has string `businessType` and string `latestTimeStampMs`; top-level string `nextCallTimeStamp`. |

### Confirmed native DM diff type

`subscription.DMChannelDiff` (72-byte Go struct) has **no JSON tags**:

| Native field | Type | Offset |
|---|---|---|
| GuildID | string | 0 |
| ChannelID | string | 16 |
| OldSeq | string | 32 |
| NewSeq | string | 48 |
| IsNew | bool | 64 |

Existing persisted `DMBaseline` / `DMChannelBaseline` tags remain as documented in [native state contract](reverse-notification-contract.md). `CompareDMBaseline` builds its lookup keyed by guild ID in this artifact and compares `LastSeq` strings for equality; it does **not** compare numerically for greater-than or use the timestamp as its change test. Channel ID is retained in the returned diff. This guild-only key is a native limitation, not a recommended universal DM identity policy.

## Pagination, cold start and baseline caveats

### Interaction/system

**Confirmed:** one fetch call site per poll function. Interaction requests first batch of 50 without `attach_info`; system requests 5 without a continuation parameter. There is no second page fetch loop in these selected native pollers. Do not claim exhaustive ingestion under bursts or lossless catch-up just because watermarks are present.

Each poller reads its starting source watermark. At zero, it scans the returned batch to establish the maximum timestamp and IDs at that maximum instead of emitting the historical batch. At nonzero, it uses `IsNewNotice`, cleans accepted notices, then advances the source watermark and appends recent records. Equal-second identity handling is specified by the existing state contract. A cold first result containing only zero/unparseable timestamps may leave a zero baseline; do not invent a durable initialized flag.

### DM

**Confirmed:** if `Baseline.DM == nil`, the poller saves the extracted channel snapshot and returns no notifications, with no message-detail fetch. Later `CompareDMBaseline` emits diffs for previously unseen guild keys or changed sequence strings. New entries use `IsNew=true`; existing entries carry old and new sequences. There is one message call site executed per changed-channel diff; the request specifies a sequence interval, not a page token. The function eventually replaces the DM snapshot with the extracted current list. A message-fetch failure takes a continue-to-next-diff path, so **do not promise retry-safe preservation of the old per-channel cursor on detail failure**. Full branch/error recovery needs fixtures before parity claims.

Messages with valid positive `msgTime` additionally pass a source timestamp-watermark/boundary identity check. The observed identity construction combines sender, a separator and text; exact complete timestamp-edge dedupe behavior is not a recovered server `msgSeq` identity. Do not conflate source watermark, DM channel sequence cursor and delivery acknowledgement.

### Digest

**Confirmed metadata:** `manage.digestCategory` has underlying Go `int`. Initialization maps:

- `BUSINESS_TYPE_MEMBER_INTERACTION_NOTIFY` → category 0.
- `BUSINESS_TYPE_VISTOR_INTERACTION_NOTIFY` → category 0 (**exact vendor spelling `VISTOR`**).
- `BUSINESS_TYPE_MANAGE` → category 1.
- `BUSINESS_TYPE_GUILD_ASSISTANT` → category 1.

`digestHasUpdate` ignores unmapped types, retains nonempty string timestamps by business type, compares timestamp strings, and compares with prior digest state. It parses `nextCallTimeStamp` as signed decimal seconds, constructs a Unix-time target and uses `time.Until`, clamping nonpositive/invalid delay to zero. It is **not** a millisecond timestamp merely because a neighboring field is named `latestTimeStampMs`. Complete outer daemon scheduling/fallback order was outside this pass; use category 0→interaction / 1→system as inferred semantic labels until the caller branch is verified.

## What can actually be invoked while keeping credentials in the CLI?

### Available public interaction fetch

Offline schema confirms:

```text
tencent-channel-cli feed get-notices --page-num 50 --json
# Optional --guild-id <authorized guild>
# Next page: --attach-info <previous returned attach_info>
```

Default page size in public schema is 20; explicitly choose 50 for the native first-batch size. `feed.GetNotices` calls **the same** `get_interact_notice` tool through `Caller.Call`, adds `attach_info`, and cleans `notices` before returning them. Response continuation is extracted from `attachInfo` / `attach_info` and exposed as `attach_info`. Public cleaned output is **not proven equivalent to the daemon's raw notice input**; adapter fixtures must account for lost/renamed identity fields. Passing pagination flags does not imply the native daemon paginates.

These example commands were **not executed**. Normal CLI authentication, sentinel, trust and controlled-egress policy remain mandatory. The authorization for static reconstruction does not authorize live fetching.

### No advertised independent public system/DM/digest fetch

Offline schema searches for exact MCP names and `digest` return empty. Searches for system notifications return only the write command `deal-notice`; DM text search returns only the write command `push-group-dm-msg`. Direct schema lookup of plausible `manage.get-notice-list` and `manage.query-normal-dm-list` returns command-not-found. Root help advertises no generic MCP/raw-call command. These are bounded **observed public surface results**, not proof that no alternate build or undocumented registration can exist.

`manage check-notices` / schema alias `manage.check-new-notices` **does** contain all three pollers, but it is not an isolated stateless fetch API: bounded call edges show `LoadState`, `GuardDaemon`, baseline load, all three pollers, baseline save. `GuardDaemon` is reached before the subsequent state check. Its schema risk label `read` does **not** make it side-effect free. Do not run it as an independent replacement source or probe it with dry-run assumptions: it can touch native state/lifecycle. `get-recent-notices` is local retained history, not a fresh server fetch.

## Recommended next implementation (bounded and actionable)

1. Implement owned pure request/response adapters and three-source fixture parsers using the exact five tool names and keys above; use an injected `NativeCall(tool, args)` capability boundary, with all system/DM/digest capability checks failing closed when absent. This is useful runnable parsing/cursor logic, not a fabricated live integration.
2. The only presently established independent live read adapter is **public interaction `feed get-notices --json`**, via a controlled existing-CLI process that retains all existing auth/sentinel/policy checks. Prepare offline golden output fixtures first; do not advertise three-source readiness from that adapter.
3. For full three-source operational use **without token export**, the feasible existing path is the native `check-notices` entrypoint only in an explicitly authorized native lifecycle/state integration. It requires separate approval to let native `GuardDaemon` and native baseline persistence run and cannot satisfy a daemon-free/stateless source contract. It is not authorized by this investigation.
4. If the replacement must own its baseline and remain daemon-free, require a vendor-supported/public native CLI read surface for `get_notice_list`, `query_normal_dm_list`, `query_guild_channel_msg`, `query_user_guild_digest` (or a vendor-owned credential-contained bridge). The recovered names/objects are a concrete implementation specification for that bridge, **not permission to export a token or bypass the sentinel with custom HTTP/MCP networking**. Do not patch the opaque executable to manufacture that capability.

This closes the focused static investigation: exact tool identities, arguments, wrapper mode, response keys, cold-start and cursor risks are recovered sufficiently to implement fixture-backed source logic. The remaining boundary is a specific missing credential-contained public invocation capability for four tools, not missing original source and not an indefinite request for more disassembly.
