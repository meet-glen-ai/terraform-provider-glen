package glen

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The JSON is what Glen's admin API writes and reads for a tool and a
// tools.mcp entry.
func TestToolsAreWrittenAsGlenReadsThem(t *testing.T) {
	user := "x-access-token"
	provider := "github-oauth"
	spec := ToolSpec{
		Name:    "github",
		CLI:     &ToolCLI{Hosts: []ToolHost{{Host: "api.github.com"}, {Host: "github.com", BasicUser: &user}}},
		Connect: &ToolConnect{Provider: &provider},
	}
	got, err := json.Marshal(spec.Normalized())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"github","connect":{"provider":"github-oauth","scopes":[]},"cli":{"hosts":[{"auth":"bearer","host":"api.github.com"},{"auth":{"basic":"x-access-token"},"host":"github.com"}]}}`
	if string(got) != want {
		t.Errorf("wrote\n%s\nwant\n%s", got, want)
	}
	var back ToolSpec
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(spec) {
		t.Errorf("read back %+v", back)
	}
	if err := json.Unmarshal([]byte(`{"host":"a.example","auth":"token"}`), &ToolHost{}); err == nil {
		t.Error("an auth Glen does not have read as bearer")
	}

	uses := []McpUse{{Server: "tickets"}, {Server: "notion", As: "user"}, {Server: "jira", As: "service"}}
	got, _ = json.Marshal(uses)
	if want := `["tickets",{"as":"user","server":"notion"},"jira"]`; string(got) != want {
		t.Errorf("wrote %s, want %s", got, want)
	}
	var read []McpUse
	if err := json.Unmarshal(got, &read); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, normalizedUses(uses)) {
		t.Errorf("read back %+v", read)
	}
}
