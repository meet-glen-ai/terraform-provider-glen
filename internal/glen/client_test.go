package glen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestWritesSayTerraformManagesThem(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Method+" "+r.URL.Path] = r.Header.Get("Glen-Managed-By")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version": 1, "spec": {}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "key")
	ctx := context.Background()
	if _, err := c.PutAgent(ctx, AgentSpec{Agent: "triage"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutKnowledgeBase(ctx, KnowledgeBaseSpec{Name: "secops"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutTool(ctx, ToolSpec{Name: "tickets"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAgent(ctx, "triage"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PUT /v1/agents/triage": "terraform", "PUT /v1/knowledge-bases/secops": "terraform", "PUT /v1/mcp-servers/tickets": "terraform", "GET /v1/agents/triage": ""}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s carried Glen-Managed-By %q, want %q", k, seen[k], v)
		}
	}
}
