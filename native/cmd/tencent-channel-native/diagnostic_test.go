package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticDoesNotExposeValuesOrDynamicKeys(t *testing.T) {
	secret := "synthetic-private-value"
	payload := map[string]any{
		"content":             []any{map[string]any{"text": `{"notices":[{"unrecognized-private-key":"` + secret + `"}]}`}},
		"dynamic-private-key": secret,
		"structuredContent":   map[string]any{"noticeList": []any{}},
	}
	b, err := json.Marshal(diagnosticShape(payload, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{secret, "dynamic-private-key", "unrecognized-private-key"} {
		if strings.Contains(string(b), marker) {
			t.Fatal("diagnostic leaked private marker")
		}
	}
	if !strings.Contains(string(b), "parsed_text_shape") {
		t.Fatal("embedded JSON shape not inspected")
	}
}
