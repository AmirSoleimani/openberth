package install

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestConfigJSONIncludesFlatURLs ensures the install-time config.json
// template carries the FlatURLs flag through to the running server.
// Without this, the server would always read FlatURLs=false on boot
// even after `berth-server install --flat-urls`.
func TestConfigJSONIncludesFlatURLs(t *testing.T) {
	cases := []struct {
		flat bool
		want string
	}{
		{flat: true, want: `"flatUrls": true`},
		{flat: false, want: `"flatUrls": false`},
	}
	for _, tc := range cases {
		// Mirror what writeConfig does (steps.go:121).
		content := fmt.Sprintf(configJSONTemplate, "acme.example.com", 72, 10, false, tc.flat)
		if !strings.Contains(content, tc.want) {
			t.Errorf("flat=%v: config.json missing %q, got:\n%s", tc.flat, tc.want, content)
		}
		// Sanity: the rendered template must be valid JSON.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Errorf("flat=%v: rendered config.json is not valid JSON: %v\n%s", tc.flat, err, content)
		}
		if got := parsed["flatUrls"]; got != tc.flat {
			t.Errorf("flat=%v: parsed flatUrls = %v, want %v", tc.flat, got, tc.flat)
		}
	}
}

// TestConfigJSONInsecureIncludesFlatURLs — same for the --insecure
// template (separate string in templates.go).
func TestConfigJSONInsecureIncludesFlatURLs(t *testing.T) {
	content := fmt.Sprintf(configJSONInsecureTemplate, "local.dev", 72, 10, false, true)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("insecure config.json invalid: %v\n%s", err, content)
	}
	if got := parsed["flatUrls"]; got != true {
		t.Errorf("insecure config.json flatUrls = %v, want true", got)
	}
}

// TestConfigJSONCloudflareIncludesFlatURLs — same for the --cloudflare
// template.
func TestConfigJSONCloudflareIncludesFlatURLs(t *testing.T) {
	content := fmt.Sprintf(configJSONCloudflareTemplate, "edge.example.com", 72, 10, false, true)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("cloudflare config.json invalid: %v\n%s", err, content)
	}
	if got := parsed["flatUrls"]; got != true {
		t.Errorf("cloudflare config.json flatUrls = %v, want true", got)
	}
}
