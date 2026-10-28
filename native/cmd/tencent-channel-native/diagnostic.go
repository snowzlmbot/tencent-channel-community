package main

import (
	"context"
	"encoding/json"
	"strings"
	"tencent-channel-community/native/notices"
)

// diagnosticShape emits only fixed known field names and type/count metadata.
// It never emits string values, arbitrary dynamic keys, IDs or notice contents.
func diagnosticShape(v any, depth int) any {
	if depth > 5 {
		return "depth_limit"
	}
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		known := map[string]bool{"structuredContent": true, "content": true, "text": true, "data": true, "notices": true, "noticeList": true, "rptMyDirectMessageInfo": true, "guildChannelMsg": true, "msgs": true, "_meta": true, "AdditionalFields": true, "retCode": true, "isError": true, "has_more": true, "hasMore": true}
		other := 0
		for k, value := range x {
			if known[k] {
				out[k] = diagnosticShape(value, depth+1)
			} else {
				other++
			}
			if k == "text" {
				if text, ok := value.(string); ok && len(text) <= 2<<20 {
					var decoded any
					dec := json.NewDecoder(strings.NewReader(text))
					dec.UseNumber()
					if dec.Decode(&decoded) == nil {
						out["parsed_text_shape"] = diagnosticShape(decoded, depth+1)
					}
				}
			}
		}
		out["unknown_field_count"] = other
		return out
	case []any:
		out := map[string]any{"type": "array", "count": len(x)}
		if len(x) > 0 {
			out["first_item_shape"] = diagnosticShape(x[0], depth+1)
		}
		return out
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number:
		return "number"
	case nil:
		return "null"
	default:
		return "other"
	}
}
func probeStructure(ctx context.Context, caller notices.NativeCaller) map[string]any {
	out := map[string]any{}
	for _, item := range []struct {
		name, tool string
		args       map[string]any
	}{
		{"interaction", "get_interact_notice", map[string]any{"pageNum": 50, "guildId": "0"}},
		{"system", "get_notice_list", map[string]any{"limit": 5}},
		{"dm", "query_normal_dm_list", map[string]any{}},
	} {
		result, err := caller.Call(ctx, item.tool, item.args)
		if err != nil {
			out[item.name] = map[string]any{"accepted": false, "category": notices.NativeFailureStage(err)}
			continue
		}
		out[item.name] = map[string]any{"accepted": true, "shape": diagnosticShape(result, 0)}
	}
	return out
}
