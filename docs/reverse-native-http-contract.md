# Native authenticated HTTP contract — CLI 1.0.10

## Result and scope

**A standalone owned HTTP caller is technically feasible. It does not require a vendor bridge or token export.** The installed Darwin ARM64 artifact contains sufficient credential-location and request-construction information to implement a caller that reads an already authorized credential into process memory at normal runtime. This corrects the engineering limitation stated in [the earlier fetch report](reverse-fetch-contract.md): a missing public CLI subcommand is not a missing wire capability.

This is independent behavioral reconstruction, not recovered original source or a licensing assertion. Evidence is bounded Go metadata, selected disassembly and literal/constant references in the installed 1.0.10 artifact. No actual credential file, account state, keychain item, business CLI command, network API or messaging endpoint was read/invoked. No binary was patched. No login, persistence or daemon was executed. Exact local addresses and artifact identity are recorded in ignored `local/native-http-contract.json`; public documentation omits device identity and local absolute paths.

**Confirmed** below means static instruction/data evidence. **Recommended** means owned implementation policy, not a claim of vendor behavior. Runtime server acceptance, active credential availability and network reachability remain untested.

## Exact request

Default notification-service endpoint:

```text
POST https://graph.qq.com/mcp_gateway/open_platform_agent_mcp/mcp
Content-Type: application/json
Authorization: Bearer <credential held only in caller memory>
X-Forwarded-Method: POST
```

The authorization prefix is exactly `Bearer ` (capital B, one trailing ASCII space); the resolved credential is concatenated directly. It is not a cookie token, Basic auth, OAuth client secret, OpenClaw RPC credential or a new authorization scope.

The only additional application header constructed in `callMCPInternal` is optional `Cookie`, described below. Go's HTTP implementation supplies ordinary transport headers; native does not explicitly set `Accept`, `MCP-Protocol-Version`, `Mcp-Session-Id`, an application `User-Agent` or an extra request-ID header in this function. Logging request IDs are not evidence of a wire header.

Exact body (native uses numeric constant ID **1**, not its logging request ID):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "get_notice_list",
    "arguments": {"limit": 5}
  }
}
```

All five tool names and argument objects are documented in [the fetch contract](reverse-fetch-contract.md). Native `Caller.Call` recursively camelizes argument-map keys before reaching this constructor; `CallRaw` skips that conversion. Tool names remain unchanged. A replacement with already wire-shaped owned structs must not camelize them a second time.

### No initial MCP handshake in this native path

`callMCPInternal` constructs one JSON body and performs one `http.Client.do` call. Neither `initialize`, `notifications/initialized`, a session negotiation request nor an SSE reader occurs on this path. Responses are read with `io.ReadAll` and decoded as ordinary JSON. The five notification fetchers reach this path directly through `httpCaller.Call`.

This proves the selected native path has no initial MCP handshake requirement; it does not establish every general-purpose MCP server's protocol behavior. Do not add speculative session IDs or require a generic MCP SDK initialization sequence to reproduce this caller.

## Environment variable names and routing cookie

Only names, never actual environment values, were inspected:

| Name | Confirmed role |
|---|---|
| `QQ_AI_CONNECT_MCP_URL` | `ResolveServerURL` reads and trims this endpoint override; otherwise resolves current service/domain context through the service registry. |
| `QQ_AI_CONNECT_MCP_ENV` | Optional routing environment used to construct a cookie. |
| `QQ_AI_CONNECT_DOTENV` | Optional trimmed override for the dotenv credential-file path. |
| `QQ_AI_CONNECT_TOKEN` | Exact key read **from the selected dotenv file**, and used for dotenv persistence fallback. It is **not read using `os.Getenv` in `token.Resolve`**. |
| `TENCENT_CHANNEL_MCPORTER_SERVICE` | Optional trimmed MCPorter service override; default `tencent-channel-mcp`. Only relevant to the last credential fallback. |

**Exact cookie:** trim the `QQ_AI_CONNECT_MCP_ENV` string. If empty, omit the Cookie header entirely. If nonempty:

```text
Cookie: qq_env_front=test; qq_env_back=<trimmed environment string>
```

`qq_env_front` is the compile-time constant `test`, **not the variable's value repeated twice**. No credential is inserted into this cookie. The observed function uses `fmt.Sprintf` directly without cookie escaping. **Recommended:** production replacement omits this optional routing cookie unless explicitly configured/approved; reject CR/LF, separators and invalid cookie bytes rather than reproducing unsafe formatting. Do not set an environment value merely to make requests work.

**Recommended controlled routing:** pin the above HTTPS host/path for the initial implementation. If endpoint overrides are supported, validate them against an approved host/path allowlist before loading the credential; reject plaintext, userinfo, fragments and unexpected ports/redirects. Knowing an override name is not authorization to redirect a credential.

## Existing credential resolution and normal login persistence

### Read order (`token.Resolve`)

1. Native keychain lookup, service **`qq-cli`**, account **`token`**. If lookup has no error and returns a nonempty string, use it.
2. Read key **`QQ_AI_CONNECT_TOKEN`** from the selected dotenv path. If nonempty, use it.
3. MCPorter fallback (`TokenFromMCPorter`), which runs a bounded child process for `mcporter config get <service>` and parses its captured Authorization value. This is configuration output, not a request to the MCP business endpoint. A case-insensitive `bearer ` prefix is stripped before returning a bare token.
4. Missing-token error; do not invent a token, scrape unrelated stores, or broaden the lookup.

The installed keychain implementation uses `github.com/zalando/go-keyring`'s macOS provider. A replacement can use the same library or the platform's equivalent generic-password API with the exact service/account; it need not print a `security ... -w` result. This investigation did not make any keychain query or verify access rights.

**Recommended initial replacement:** implement only keychain + explicit/native dotenv fallback, and fail closed when neither works. MCPorter compatibility is optional; do not run arbitrary subprocesses implicitly just because the original has this fallback. If added, capture output privately, avoid logging it, set an explicit deadline and select only the configured service. Never export the credential to a new environment, CLI flag, JSON report or file.

### Dotenv path selection

- If `QQ_AI_CONNECT_DOTENV` is nonempty after trimming: native expands a leading `~` using the home directory, then tries `filepath.Abs`; on absolute-path conversion error it retains the expanded path.
- Otherwise select the home-relative `.qqcli/.env`.
- Only if that default path is absent (`IsNotExist`) does native test the legacy home-relative `.openclaw/.env`; use the legacy path only when its `Stat` succeeds. Other default-path errors do not trigger legacy selection.

These are generic native storage conventions, not the actual user's paths or evidence that files exist. A replacement should not source/execute a dotenv file. Read only the selected key in memory.

### Dotenv value parsing

`ReadDotenvKey` reads the file, splits lines, trims each line, skips empty lines and lines beginning `#`, optionally removes the exact prefix `export ` and trims again. It matches the requested key followed by `=` or by ` =`, trims the value, removes paired single/double outer quotes, performs limited double-quoted unescaping (`\\` then `\"`), and trims the result. First matching assignment returns, including an empty value. It does not evaluate shell expansion or run commands. A general shell `source` or full dotenv interpolator would be a behavioral/security change.

### Successful normal login

Static call edges show `runPollTokenJSON` calls `auth.PollDeviceToken`, then `token.PersistToken`. `PersistToken` first invokes the keyring provider's Set with service `qq-cli`, account `token` and the newly authorized token. If Set succeeds, it reports keychain success; it does **not** also write the token into dotenv. If Set fails, it calls `GetWriteDotenvPath` and writes key `QQ_AI_CONNECT_TOKEN` into the home-relative `.qqcli/.env`. This write-path helper does **not** use the dotenv read override. Therefore a successful login need not create any dotenv file, and inspecting dotenv alone can falsely report missing credentials.

The login command subsequently performs subscription cleanup and a connectivity probe. A replacement read caller must not rerun login, call this persistence routine, migrate files or trigger native subscription state changes merely to obtain a credential.

## HTTP lifecycle, timeout and retries

- Confirmed static `http.Client` initializer: **30 seconds** (`30,000,000,000` nanoseconds) in its Timeout field; Transport/CheckRedirect/Jar are initially nil. Thus it uses Go's default transport semantics absent later modification. Static initialization alone does not prove the host's current transport settings.
- One application-level HTTP dispatch per `callMCPInternal`; no application retry/backoff loop or `time.Sleep` call on this path. Go transport behavior is distinct from an application retry policy.
- `NewRequestWithContext` uses a background context on this native path; the shared client provides the timeout.
- Response body is closed by deferred wrapper. Native `io.ReadAll` is not bounded in the recovered path.
- Native accepts **HTTP 200 exactly**, not every 2xx status, before JSON decoding; other status codes go through `classifyHTTPError`.
- There is no native cookie jar initialized in this client. Optional routing cookie is explicitly constructed each call.

**Recommended owned client:** clone an approved default transport, preserve `http.ProxyFromEnvironment`, use normal HTTPS certificate/hostname verification and approved system roots, keep the 30-second client timeout plus caller cancellation, bound response bytes, and reject redirects rather than forwarding authorization to another location. Do not set `InsecureSkipVerify`, direct-dial around a controlled proxy, replace DNS for policy evasion, or add a bearer token to proxy authorization. A proxy/egress failure is a concrete blocker, not a reason to disable it. Do not recreate any previously proposed sentinel bypass; required trust/policy checks must pass before dispatch. This is a Tencent service HTTP caller, **not a substitute route for OpenClaw messaging/RPC tools**.

## Response/error contract

Typical envelope shape (illustrative placeholders, not a captured server response):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "isError": false,
    "_meta": {"AdditionalFields": {"retCode": 0, "errMsg": ""}},
    "structuredContent": {"noticeList": []}
  }
}
```

Confirmed native order:

1. Require HTTP 200, read the response and unmarshal a JSON map. Reject read/decode failures.
2. Check top-level `error`; a non-null JSON-RPC error produces an error. When map-shaped, native accesses `message` and `code` and calls `classifyMCPError`.
3. Require map-shaped `result`; otherwise return an abnormal-server-format error.
4. If `result.isError` is boolean true, call `parseMCPBusinessError`; do not treat HTTP 200 or a payload map as success. That parser examines metadata `retCode`/`errMsg` and `content` entries (including `type`/`text`) for a meaningful error. Exact precedence and every wording fallback are not fully specified here.
5. If `result._meta` and its `AdditionalFields` are map-shaped, inspect `retCode`. Missing/null retCode is accepted on this non-isError branch. A decoded JSON **numeric zero** is accepted. Other non-null types/values enter the error branch, using `toIntOrZero` and `errMsg`; native does not simply parse the string `"0"` as success. `toIntOrZero` supports several runtime numeric/string representations, but native JSON decoding normally produces float64 values.
6. Return the complete result map. Notification fetchers then use map-shaped `structuredContent` if present; otherwise use the result map directly. Keep `_meta` and error information until validation is complete; never discard them by unwrapping first.

**Recommended:** enforce non-null RPC error, boolean isError, numeric-zero/nonzero metadata semantics with typed errors; fail closed on malformed metadata rather than silently coercing it to success. Preserve unknown safe payload fields. Sanitize error messages and never include request authorization, arbitrary response bodies or dotenv text in logs. Correlating response id/version is useful stricter validation; native equivalence of such validation is not asserted.

The native error-classification initializer includes auth return codes `8001`, `8003`, `8011`, `100007`, `401`. The low-level caller does not automatically reauthenticate. Rate-limit retry policies in skill prose or outer command wrappers must not be confused with a low-level automatic retry. For an owned notification poller, bounded backoff belongs in the explicit scheduler; authentication failures stop and request normal re-login, never escalate scopes or loop token discovery.

## Implementation boundary and verification plan

Implement owned interfaces `CredentialSource.Resolve(ctx) -> private credential` and `Call(ctx, allowedTool, wireArgs) -> validated result`. Resolve the credential only after endpoint and trust/egress preflight passes. Keep the bare credential in process memory; attach the exact Authorization prefix inside the request constructor, then send through the approved proxy-aware verified-TLS client. Permit only the five recovered read tools initially. No native daemon or native baseline storage is needed for this HTTP layer; the replacement owns its separate cursor state.

Pure offline tests should cover exact JSON/body/header construction using a fake RoundTripper, cookie omission/construction, strict host/path rejection, no cross-host redirects, numeric zero versus string-zero/null/missing retCode, JSON-RPC and isError failures, structuredContent fallback, deadline and response-size limits. Credential-provider fixtures must use synthetic tokens and temporary synthetic dotenv files only. Never record live credentials, real notification payloads, raw executable bytes or disassembly in fixtures.

This pass produced documentation and a machine-readable local contract only. It did not implement or run a live caller, and it does not certify live authentication/server behavior. The engineering auth/HTTP specification is no longer blocked on a vendor bridge; remaining verification is owned implementation + offline fixtures, followed by separately authorized normal-runtime controlled-egress validation.
