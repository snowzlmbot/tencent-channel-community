package notices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type nativeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nativeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeFixture(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const nativeOK = `{"jsonrpc":"2.0","id":1,"result":{"isError":false,"_meta":{"AdditionalFields":{"retCode":0}},"structuredContent":{"noticeList":[]}}}`

func TestNativeHTTPExactRequest(t *testing.T) {
	t.Setenv("QQ_AI_CONNECT_MCP_ENV", "")
	t.Setenv("QQ_AI_CONNECT_MCP_URL", "https://unapproved.invalid/ignored")
	calls, resolves := 0, 0
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { resolves++; return "synthetic-only-token", nil }, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != nativeMCPEndpoint || r.Method != "POST" {
			t.Fatal("unexpected destination or method")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-only-token" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Forwarded-Method") != "POST" {
			t.Fatal("incorrect native headers")
		}
		for _, h := range []string{"Cookie", "Proxy-Authorization", "Mcp-Session-Id", "MCP-Protocol-Version", "X-Request-Id"} {
			if r.Header.Get(h) != "" {
				t.Errorf("unexpected header %s", h)
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal("request body read failed")
		}
		if strings.Contains(string(body), "synthetic-only-token") {
			t.Fatal("credential leaked into request body")
		}
		var envelope map[string]any
		if json.Unmarshal(body, &envelope) != nil || envelope["jsonrpc"] != "2.0" || envelope["id"] != float64(1) || envelope["method"] != "tools/call" {
			t.Fatal("incorrect JSON-RPC envelope")
		}
		params, ok := envelope["params"].(map[string]any)
		if !ok || params["name"] != "get_notice_list" {
			t.Fatal("incorrect tool")
		}
		args, ok := params["arguments"].(map[string]any)
		if !ok || len(args) != 1 || args["limit"] != float64(5) {
			t.Fatal("incorrect wire arguments")
		}
		return nativeFixture(200, nativeOK), nil
	}))
	result, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5})
	if err != nil || result["structuredContent"] == nil || result["_meta"] == nil || calls != 1 || resolves != 1 {
		t.Fatal("native result must preserve complete validated result")
	}
	if strings.Contains(fmt.Sprintf("%+v", caller), "synthetic-only-token") {
		t.Fatal("caller retained printable credential")
	}
}

func TestNativeHTTPTransportPolicy(t *testing.T) {
	caller := NewNativeHTTPCaller(nil)
	if caller.client.Timeout != 30*time.Second || caller.client.Jar != nil || caller.client.CheckRedirect == nil {
		t.Fatal("unsafe client policy")
	}
	transport, ok := caller.client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil || transport.TLSClientConfig != nil {
		t.Fatal("proxy-aware verified TLS required")
	}
	if caller.client.CheckRedirect(&http.Request{}, nil) != ErrInvalid {
		t.Fatal("redirect accepted")
	}
	for _, endpoint := range []string{"http://graph.qq.com/mcp_gateway/open_platform_agent_mcp/mcp", nativeMCPEndpoint + "?a=1", nativeMCPEndpoint + "#x", "https://graph.qq.com:443/mcp_gateway/open_platform_agent_mcp/mcp", "https://user@graph.qq.com/mcp_gateway/open_platform_agent_mcp/mcp", "https://evil.invalid/mcp", nativeMCPEndpoint + "/"} {
		if approvedNativeEndpoint(endpoint) {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	if !approvedNativeEndpoint(nativeMCPEndpoint) {
		t.Fatal("fixed endpoint rejected")
	}
}

func TestNativeHTTPCookie(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"", "", true}, {"   ", "", true}, {" staging-1 ", "qq_env_front=test; qq_env_back=staging-1", true},
		{"test; injected=x", "", false}, {"x\r\nHeader: value", "", false}, {"x\n", "", false}, {"a,b", "", false}, {"a b", "", false}, {"a=b", "", false}, {"\"test\"", "", false}, {"a\\b", "", false}, {"测试", "", false},
	} {
		got, ok := nativeRoutingCookie(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("routing-cookie validation mismatch for synthetic input %q", tc.raw)
		}
	}
	t.Setenv("QQ_AI_CONNECT_MCP_ENV", " dev ")
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "qq_env_front=test; qq_env_back=dev" {
			t.Fatal("wrong routing cookie")
		}
		return nativeFixture(200, nativeOK), nil
	}))
	if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); err != nil {
		t.Fatal("valid routing failed")
	}
}

func TestNativeHTTPToolsAndArguments(t *testing.T) {
	goodDM := func() map[string]any {
		return map[string]any{"directMessageFlag": 1, "guildChannelParam": map[string]any{"guildId": "g", "channelId": "c", "beginSeq": "1", "endSeq": "9"}}
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_interact_notice", map[string]any{"pageNum": 50, "guildId": "0"}},
		{"get_notice_list", map[string]any{"limit": 5}},
		{"query_normal_dm_list", map[string]any{}}, {"query_user_guild_digest", map[string]any{}}, {"query_guild_channel_msg", goodDM()},
	} {
		args, ok := nativeReadArgs(tc.tool, tc.args)
		if !ok || args == nil {
			t.Errorf("documented read rejected: %s", tc.tool)
		}
	}
	badDM := goodDM()
	badDM["guildChannelParam"].(map[string]any)["beginSeq"] = "10"
	extraDM := goodDM()
	extraDM["guildChannelParam"].(map[string]any)["extra"] = "no"
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"push_group_dm_msg", map[string]any{}}, {"deal_notice", map[string]any{}}, {"initialize", map[string]any{}}, {"", map[string]any{}},
		{"get_notice_list", nil}, {"get_notice_list", map[string]any{"limit": 6}}, {"get_notice_list", map[string]any{"limit": 0}}, {"get_notice_list", map[string]any{"limit": "5"}}, {"get_notice_list", map[string]any{"limit": float64(5)}}, {"get_notice_list", map[string]any{"limit": 5, "extra": true}},
		{"get_interact_notice", map[string]any{"pageNum": 51, "guildId": "0"}}, {"get_interact_notice", map[string]any{"pageNum": 50, "guildId": ""}}, {"get_interact_notice", map[string]any{"page_num": 50, "guild_id": "0"}},
		{"query_normal_dm_list", map[string]any{"extra": 1}}, {"query_user_guild_digest", map[string]any{"extra": 1}},
		{"query_guild_channel_msg", badDM}, {"query_guild_channel_msg", extraDM},
	} {
		called := false
		caller := newNativeHTTPCaller(func(context.Context) (string, error) { called = true; return "dummy", nil }, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid args dispatched")
			return nil, ErrInvalid
		}))
		if _, err := caller.Call(context.Background(), tc.tool, tc.args); !errors.Is(err, ErrInvalid) || called {
			t.Errorf("invalid read resolved credentials: %s", tc.tool)
		}
	}
	t.Setenv("QQ_AI_CONNECT_MCP_ENV", "bad;cookie")
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { t.Fatal("invalid cookie resolved credential"); return "", nil }, nil)
	caller.client.Transport = nativeRoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("bad cookie dispatched"); return nil, ErrInvalid })
	if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid cookie accepted")
	}
}

func TestNativeHTTPResultValidation(t *testing.T) {
	success := []string{
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"error":null,"result":{"structuredContent":{"noticeList":[]}}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"AdditionalFields":{"retCode":null}}}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"AdditionalFields":{}}}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"AdditionalFields":{"retCode":-0.0e99}}}}`,
		nativeOK,
	}
	for i, body := range success {
		if _, err := decodeNativeResult([]byte(body)); err != nil {
			t.Errorf("valid response %d rejected", i)
		}
	}
	invalid := []string{
		`null`, `[]`, `{}`, `{"jsonrpc":"2.0","id":1,"result":null}`,
		`{"jsonrpc":"2.0","id":"1","result":{}}`, `{"jsonrpc":"2.0","id":2,"result":{}}`,
		`{"jsonrpc":"1.0","id":1,"result":{}}`, `{"jsonrpc":"2.0","id":1,"error":{},"result":{}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"isError":true}}`, `{"jsonrpc":"2.0","id":1,"result":{"isError":"false"}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"isError":null}}`, `{"jsonrpc":"2.0","id":1,"result":{"_meta":null}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"_meta":{"AdditionalFields":[]}}}`, nativeOK + ` {}`,
	}
	for _, code := range []string{`"0"`, `1`, `-1`, `1e-9999`, `false`, `{}`, `[]`} {
		invalid = append(invalid, `{"jsonrpc":"2.0","id":1,"result":{"_meta":{"AdditionalFields":{"retCode":`+code+`}}}}`)
	}
	for i, body := range invalid {
		if _, err := decodeNativeResult([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid response %d accepted", i)
		}
	}
}

type nativeCountingBody struct {
	remaining, read int
	closed          bool
}

func (b *nativeCountingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > b.remaining {
		n = b.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = ' '
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (b *nativeCountingBody) Close() error { b.closed = true; return nil }

func TestNativeHTTPLimitsRedirectsAndSafeErrors(t *testing.T) {
	t.Setenv("QQ_AI_CONNECT_MCP_ENV", "")
	for _, status := range []int{201, 204, 301, 302, 307, 308, 400, 401, 429, 500} {
		calls := 0
		caller := newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			r := nativeFixture(status, "private server body")
			r.Header.Set("Location", "https://unapproved.invalid/redirect")
			return r, nil
		}))
		if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) || calls != 1 {
			t.Errorf("status %d accepted or retried/redirected", status)
		}
	}
	body := &nativeCountingBody{remaining: maxNativeResponse + 100}
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	}))
	if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) || body.read != maxNativeResponse+1 || !body.closed {
		t.Fatal("response was not bounded and closed")
	}
	for _, provider := range []CredentialProvider{
		func(context.Context) (string, error) { return "", errors.New("synthetic secret provider error") },
		func(context.Context) (string, error) { return "bad\r\ntoken", nil },
		func(context.Context) (string, error) { return "", nil },
	} {
		caller := newNativeHTTPCaller(provider, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid credential dispatched")
			return nil, ErrInvalid
		}))
		if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) {
			t.Fatal("unsanitized credential error")
		}
	}
	caller = newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private transport detail") }))
	if _, err := caller.Call(context.Background(), "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) {
		t.Fatal("transport error was not sanitized")
	}
}

func TestNativeHTTPCancellationAndFetcherUnwrap(t *testing.T) {
	t.Setenv("QQ_AI_CONNECT_MCP_ENV", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	caller := newNativeHTTPCaller(func(context.Context) (string, error) { t.Fatal("canceled request resolved credential"); return "", nil }, nil)
	if _, err := caller.Call(ctx, "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) {
		t.Fatal("canceled context accepted")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	caller = newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	if _, err := caller.Call(ctx, "get_notice_list", map[string]any{"limit": 5}); !errors.Is(err, ErrInvalid) {
		t.Fatal("transport cancellation not honored")
	}
	for _, result := range []string{`{"structuredContent":{"noticeList":[]}}`, `{"noticeList":[]}`} {
		caller = newNativeHTTPCaller(func(context.Context) (string, error) { return "dummy", nil }, nativeRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nativeFixture(200, `{"jsonrpc":"2.0","id":1,"result":`+result+`}`), nil
		}))
		list, err := (Fetcher{Caller: caller}).ReadSystem(context.Background())
		if err != nil || list == nil || len(list) != 0 {
			t.Fatal("fetcher must unwrap exactly once or use direct result")
		}
	}
}
