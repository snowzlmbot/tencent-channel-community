package notices

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const nativeMCPEndpoint = "https://graph.qq.com/mcp_gateway/open_platform_agent_mcp/mcp"
const maxNativeResponse = 2 << 20

// CredentialProvider resolves an already authorized credential at call time.
// Implementations must not log, export, persist, or include it in errors.
type CredentialProvider func(context.Context) (string, error)

// NativeHTTPCaller exposes only the five bounded notification reads. The
// endpoint, verified TLS, proxy behavior and redirect policy are not configurable
// through its public API. It never retains a resolved token in a struct field.
type NativeHTTPCaller struct {
	credential CredentialProvider
	client     *http.Client
}

var _ NativeCaller = (*NativeHTTPCaller)(nil)

// NewNativeHTTPCaller does not resolve a credential or perform network I/O.
// A nil provider fails closed on Call; pass DefaultCredential explicitly to use
// the native dotenv location. No endpoint environment override is honored.
func NewNativeHTTPCaller(provider CredentialProvider) *NativeHTTPCaller {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return newNativeHTTPCaller(provider, transport)
}

// The transport seam is package-private, for offline synthetic fixtures only.
func newNativeHTTPCaller(provider CredentialProvider, transport http.RoundTripper) *NativeHTTPCaller {
	return &NativeHTTPCaller{credential: provider, client: &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return ErrInvalid },
	}}
}

func approvedNativeEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && endpoint == nativeMCPEndpoint && u.Scheme == "https" &&
		u.Host == "graph.qq.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		u.Path == "/mcp_gateway/open_platform_agent_mcp/mcp" && u.RawPath == ""
}

func validNativeToken(token string) bool {
	if token == "" || len(token) > 64<<10 {
		return false
	}
	for i := 0; i < len(token); i++ {
		// Bearer credentials are opaque printable ASCII, never whitespace/control.
		if token[i] < 0x21 || token[i] > 0x7e {
			return false
		}
	}
	return true
}

func nativeRoutingCookie(raw string) (string, bool) {
	// Reject controls BEFORE trimming, so CR/LF cannot disappear into a valid value.
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] > 0x7e {
			return "", false
		}
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", true
	}
	if len(value) > 128 {
		return "", false
	}
	for i := 0; i < len(value); i++ {
		// A deliberately narrow routing label, not arbitrary cookie syntax.
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return "", false
		}
	}
	return "qq_env_front=test; qq_env_back=" + value, true
}

func nativeIdentifier(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok && len(s) <= 256 && valid(s)
}
func boundedNativeInt(value any, max int) bool {
	n, ok := value.(int)
	return ok && n > 0 && n <= max
}

// Freeze a fresh, known-type argument object before credential resolution. No
// arbitrary Marshaler or extra fields can run or cross this capability boundary.
func nativeReadArgs(tool string, args map[string]any) (map[string]any, bool) {
	if args == nil {
		return nil, false
	}
	switch tool {
	case "get_interact_notice":
		guild, ok := nativeIdentifier(args["guildId"])
		if len(args) != 2 || !ok || !boundedNativeInt(args["pageNum"], 50) {
			return nil, false
		}
		return map[string]any{"guildId": guild, "pageNum": args["pageNum"].(int)}, true
	case "get_notice_list":
		if len(args) != 1 || !boundedNativeInt(args["limit"], 5) {
			return nil, false
		}
		return map[string]any{"limit": args["limit"].(int)}, true
	case "query_normal_dm_list", "query_user_guild_digest":
		return map[string]any{}, len(args) == 0
	case "query_guild_channel_msg":
		flag, ok := args["directMessageFlag"].(int)
		nested, mapOK := args["guildChannelParam"].(map[string]any)
		if len(args) != 2 || !ok || flag != 1 || !mapOK || len(nested) != 4 {
			return nil, false
		}
		guild, gok := nativeIdentifier(nested["guildId"])
		channel, cok := nativeIdentifier(nested["channelId"])
		begin, bok := nested["beginSeq"].(string)
		end, eok := nested["endSeq"].(string)
		if !gok || !cok || !bok || !eok || len(begin) > 256 || len(end) > 256 {
			return nil, false
		}
		b, bok := decimal(begin)
		e, eok := decimal(end)
		if !bok || !eok || b.Cmp(e) > 0 {
			return nil, false
		}
		return map[string]any{"directMessageFlag": 1, "guildChannelParam": map[string]any{
			"guildId": guild, "channelId": channel, "beginSeq": begin, "endSeq": end,
		}}, true
	}
	return nil, false
}

func (c *NativeHTTPCaller) Call(ctx context.Context, tool string, args map[string]any) (map[string]any, error) {
	if c == nil || ctx == nil || ctx.Err() != nil || c.credential == nil || c.client == nil || c.client.Transport == nil ||
		!approvedNativeEndpoint(nativeMCPEndpoint) {
		return nil, ErrInvalid
	}
	safeArgs, ok := nativeReadArgs(tool, args)
	if !ok {
		return nil, ErrInvalid
	}
	cookie, ok := nativeRoutingCookie(os.Getenv("QQ_AI_CONNECT_MCP_ENV"))
	if !ok {
		return nil, ErrInvalid
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": safeArgs}})
	if err != nil {
		return nil, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, nativeMCPEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	token, err := c.credential(ctx)
	if err != nil || !validNativeToken(token) || ctx.Err() != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Method", "POST")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, ErrInvalid
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxNativeResponse+1))
	if err != nil || len(data) > maxNativeResponse || ctx.Err() != nil {
		return nil, ErrInvalid
	}
	return decodeNativeResult(data)
}

// JSON numbers retain their lexical form: underflow (e.g. 1e-999) must not
// accidentally turn a nonzero retCode into a successful float64 zero.
func numericNativeZero(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	mantissa := string(number)
	if exponent := strings.IndexAny(mantissa, "eE"); exponent >= 0 {
		mantissa = mantissa[:exponent]
	}
	if mantissa == "" {
		return false
	}
	for _, c := range mantissa {
		if c != '0' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func decodeNativeResult(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if decoder.Decode(&root) != nil || root == nil {
		return nil, ErrInvalid
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrInvalid
	}
	if root["jsonrpc"] != "2.0" || root["id"] != json.Number("1") || root["error"] != nil {
		return nil, ErrInvalid
	}
	result, ok := root["result"].(map[string]any)
	if !ok || result == nil {
		return nil, ErrInvalid
	}
	if value, exists := result["isError"]; exists {
		isError, ok := value.(bool)
		if !ok || isError {
			return nil, ErrInvalid
		}
	}
	if value, exists := result["_meta"]; exists {
		meta, ok := value.(map[string]any)
		if !ok || meta == nil {
			return nil, ErrInvalid
		}
		if value, exists := meta["AdditionalFields"]; exists {
			fields, ok := value.(map[string]any)
			if !ok || fields == nil {
				return nil, ErrInvalid
			}
			if code := fields["retCode"]; code != nil && !numericNativeZero(code) {
				return nil, ErrInvalid
			}
		}
	}
	// Fetcher owns the one-time structuredContent unwrap; metadata stays intact.
	return result, nil
}
