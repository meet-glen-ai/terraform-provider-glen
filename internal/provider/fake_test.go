package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type fakeGlen struct {
	mu              sync.Mutex
	adminKey        string
	agents          map[string]*fakeAgent
	knowledge_bases map[string]*fakeKnowledgeBase
	tools           map[string]*fakeTool
	members         map[string]*fakeMember
	// Each tool's credential value, and each provider's model key, as set.
	credentials map[string]string
	modelKeys   map[string]string
}

type fakeKnowledgeBase struct {
	version int64
	spec    glen.KnowledgeBaseSpec
	deleted bool
}

type fakeAgent struct {
	version int64
	spec    glen.AgentSpec
	deleted bool
}

func startFake(t *testing.T, adminKey string) string {
	f := &fakeGlen{adminKey: adminKey, agents: map[string]*fakeAgent{}, knowledge_bases: map[string]*fakeKnowledgeBase{}, tools: map[string]*fakeTool{}, members: map[string]*fakeMember{}, credentials: map[string]string{}, modelKeys: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/validate/agent", f.validate)
	mux.HandleFunc("GET /v1/agents/{name}", f.getAgent)
	mux.HandleFunc("PUT /v1/agents/{name}", f.putAgent)
	mux.HandleFunc("DELETE /v1/agents/{name}", f.deleteAgent)
	mux.HandleFunc("POST /v1/validate/knowledge_base", f.validateKnowledgeBase)
	mux.HandleFunc("GET /v1/knowledge-bases/{name}", f.getKnowledgeBase)
	mux.HandleFunc("PUT /v1/knowledge-bases/{name}", f.putKnowledgeBase)
	mux.HandleFunc("DELETE /v1/knowledge-bases/{name}", f.deleteKnowledgeBase)
	mux.HandleFunc("GET /v1/mcp-servers/{name}", f.getTool)
	mux.HandleFunc("PUT /v1/mcp-servers/{name}", f.putTool)
	mux.HandleFunc("DELETE /v1/mcp-servers/{name}", f.deleteTool)
	mux.HandleFunc("GET /v1/mcp-servers/{name}/credential", f.getCredential)
	mux.HandleFunc("PUT /v1/mcp-servers/{name}/credential", f.putCredential)
	mux.HandleFunc("DELETE /v1/mcp-servers/{name}/credential", f.deleteCredential)
	mux.HandleFunc("GET /v1/model-keys", f.getModelKeys)
	mux.HandleFunc("PUT /v1/model-keys/{provider}", f.putModelKey)
	mux.HandleFunc("DELETE /v1/model-keys/{provider}", f.deleteModelKey)
	f.memberRoutes(mux)
	srv := httptest.NewServer(f.auth(mux))
	fakes.Store(srv.URL, f)
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeGlen) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		defer f.mu.Unlock()
		if key == f.adminKey {
			next.ServeHTTP(w, r)
			return
		}
		fail(w, http.StatusUnauthorized, "unknown, expired or revoked token")
	})
}

func (f *fakeGlen) validate(w http.ResponseWriter, r *http.Request) {
	spec, ok := readSpec(w, r)
	if !ok {
		return
	}
	if msg := f.agentRules(spec); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	answer(w, map[string]bool{"ok": true})
}

func (f *fakeGlen) getAgent(w http.ResponseWriter, r *http.Request) {
	a := f.agents[r.PathValue("name")]
	if a == nil || a.deleted {
		fail(w, http.StatusNotFound, "no such agent")
		return
	}
	answer(w, glen.Agent{Version: a.version, Spec: a.spec})
}

func (f *fakeGlen) putAgent(w http.ResponseWriter, r *http.Request) {
	spec, ok := readSpec(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if spec.Agent != name {
		fail(w, http.StatusBadRequest, fmt.Sprintf("spec.agent %q must equal %q", spec.Agent, name))
		return
	}
	if msg := f.agentRules(spec); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	if knowledge_base := spec.KnowledgeBase; knowledge_base != nil && !f.liveKnowledgeBase(*knowledge_base) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("invalid agent spec: agent %s names knowledge base %s, which does not exist", spec.Agent, *knowledge_base))
		return
	}
	if spec.Harness == "" {
		spec.Harness = harnessFor(spec.Model)
	}
	spec = spec.Normalized()
	a := f.agents[name]
	if a == nil {
		a = &fakeAgent{}
		f.agents[name] = a
	}
	if a.deleted || a.version == 0 || !a.spec.Equal(spec) {
		a.version++
		a.spec, a.deleted = spec, false
	}
	answer(w, glen.Agent{Version: a.version, Spec: a.spec})
}

func (f *fakeGlen) deleteAgent(w http.ResponseWriter, r *http.Request) {
	a := f.agents[r.PathValue("name")]
	if a == nil || a.deleted {
		fail(w, http.StatusNotFound, "no such agent")
		return
	}
	a.version++
	a.deleted = true
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGlen) liveKnowledgeBase(name string) bool {
	wk := f.knowledge_bases[name]
	return wk != nil && !wk.deleted
}

func (f *fakeGlen) validateKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	if _, ok := readKnowledgeBase(w, r); ok {
		answer(w, map[string]bool{"ok": true})
	}
}

func (f *fakeGlen) getKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !f.liveKnowledgeBase(name) {
		fail(w, http.StatusNotFound, "no knowledge base named "+name)
		return
	}
	wk := f.knowledge_bases[name]
	answer(w, glen.KnowledgeBase{Version: wk.version, Spec: wk.spec})
}

func (f *fakeGlen) putKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	spec, ok := readKnowledgeBase(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if spec.Name != name {
		fail(w, http.StatusBadRequest, fmt.Sprintf("the body names knowledge base %q, the path %q", spec.Name, name))
		return
	}
	wk := f.knowledge_bases[name]
	if wk == nil {
		wk = &fakeKnowledgeBase{}
		f.knowledge_bases[name] = wk
	}
	if wk.deleted || wk.version == 0 || !reflect.DeepEqual(wk.spec, spec) {
		wk.version++
		wk.spec, wk.deleted = spec, false
	}
	answer(w, glen.KnowledgeBase{Version: wk.version, Spec: wk.spec})
}

func (f *fakeGlen) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !f.liveKnowledgeBase(name) {
		fail(w, http.StatusNotFound, "no knowledge base named "+name)
		return
	}
	var naming []string
	for agent, a := range f.agents {
		if !a.deleted && a.spec.KnowledgeBase != nil && *a.spec.KnowledgeBase == name {
			naming = append(naming, agent)
		}
	}
	if len(naming) > 0 {
		slices.Sort(naming)
		fail(w, http.StatusConflict, fmt.Sprintf("knowledge base %s is the knowledge base of %s; change or delete those agents first", name, strings.Join(naming, ", ")))
		return
	}
	f.knowledge_bases[name].version++
	f.knowledge_bases[name].deleted = true
	w.WriteHeader(http.StatusNoContent)
}

func readKnowledgeBase(w http.ResponseWriter, r *http.Request) (glen.KnowledgeBaseSpec, bool) {
	var body struct {
		Name        *string       `json:"name"`
		Description *string       `json:"description"`
		Guidance    *string       `json:"guidance"`
		Curator     *glen.Curator `json:"curator"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid knowledge base spec: "+err.Error())
		return glen.KnowledgeBaseSpec{}, false
	}
	if body.Name == nil || body.Description == nil || body.Curator == nil {
		fail(w, http.StatusBadRequest, "invalid knowledge base spec: missing field")
		return glen.KnowledgeBaseSpec{}, false
	}
	spec := glen.KnowledgeBaseSpec{Name: *body.Name, Description: *body.Description, Guidance: body.Guidance, Curator: *body.Curator}
	msg := ""
	switch {
	case !kebab(spec.Name):
		msg = fmt.Sprintf("knowledge base name %q must be kebab-case", spec.Name)
	case strings.TrimSpace(spec.Description) == "":
		msg = "description must be set"
	case strings.TrimSpace(spec.Curator.Model) == "":
		msg = "curator.model must be set"
	case spec.Curator.TokensPerDay < 1:
		msg = "curator.tokens_per_day must be at least 1"
	}
	if msg != "" {
		fail(w, http.StatusBadRequest, "invalid knowledge base spec: "+msg)
		return spec, false
	}
	return spec, true
}

func kebab(name string) bool {
	ok := name != ""
	for _, c := range name {
		ok = ok && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
	}
	return ok
}

func readSpec(w http.ResponseWriter, r *http.Request) (glen.AgentSpec, bool) {
	var spec glen.AgentSpec
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return spec, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return spec, false
	}
	var lists struct {
		Trigger json.RawMessage `json:"trigger"`
		Tools   struct {
			Env json.RawMessage `json:"env"`
			MCP json.RawMessage `json:"mcp"`
		} `json:"tools"`
		Sandbox struct {
			Egress json.RawMessage `json:"egress"`
		} `json:"sandbox"`
	}
	_ = json.Unmarshal(body, &lists)
	for _, l := range []json.RawMessage{lists.Trigger, lists.Tools.Env, lists.Tools.MCP, lists.Sandbox.Egress} {
		if string(l) == "null" {
			fail(w, http.StatusBadRequest, "invalid type: null, expected a sequence")
			return spec, false
		}
	}
	return spec, true
}

func glenAgentRules(s glen.AgentSpec) string {
	if !kebab(s.Agent) {
		return fmt.Sprintf("agent name %q must be kebab-case", s.Agent)
	}
	if s.KnowledgeBase != nil && !kebab(*s.KnowledgeBase) {
		return fmt.Sprintf("knowledge base %q must be a kebab-case knowledge base name", *s.KnowledgeBase)
	}
	if strings.TrimSpace(s.Model) == "" {
		return "model must be set"
	}
	if runs := harnessFor(s.Model); s.Harness != "" && s.Harness != runs {
		return fmt.Sprintf("model %s runs on %s; set harness to %s or leave it out", s.Model, runs, runs)
	}
	for _, h := range s.Sandbox.Egress {
		if why := egressProblem(h); why != "" {
			return fmt.Sprintf("sandbox.egress %q %s", h, why)
		}
	}
	for _, t := range s.Trigger {
		n := 0
		for _, set := range []bool{t.Schedule != nil, t.Feed != nil, t.Webhook != nil} {
			if set {
				n++
			}
		}
		if n != 1 {
			return "a trigger has exactly one of schedule, feed, webhook"
		}
		if t.Schedule != nil && len(strings.Fields(*t.Schedule)) != 5 {
			return fmt.Sprintf("schedule %q must have five cron fields", *t.Schedule)
		}
		if t.Webhook != nil && !strings.Contains(*t.Webhook, ":") {
			return fmt.Sprintf("webhook %q must be <source>:<event>", *t.Webhook)
		}
	}
	for _, v := range s.Tools.Env {
		if !slices.Contains(toolsEnv, v) {
			return fmt.Sprintf("tools.env names %s, which this deployment does not allow", v)
		}
	}
	return ""
}

// harnessFor is the harness Glen gives a spec that leaves harness out.
// The fake answers as a Glen where the harness follows the model, as
// Glen decides: Codex for OpenAI's models,
// Claude Code for the rest. A Glen with one harness gives claude-code to
// every model, and the provider takes either answer.
func harnessFor(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	openAI := len(m) > 1 && m[0] == 'o' && m[1] >= '0' && m[1] <= '9'
	for _, p := range []string{"gpt-", "codex-", "chatgpt-"} {
		openAI = openAI || strings.HasPrefix(m, p)
	}
	if openAI {
		return "codex"
	}
	return "claude-code"
}

// egressProblem is why Glen would refuse h in sandbox.egress, or "".
func egressProblem(h string) string {
	name, wild := strings.CutPrefix(h, "*.")
	labels := strings.Split(name, ".")
	if wild && len(labels) < 2 {
		return "must name a domain of at least two labels after *."
	}
	return hostProblem(name)
}

func answer(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
