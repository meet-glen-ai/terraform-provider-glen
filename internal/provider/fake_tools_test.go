package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// fakeTool is an org's tool as the fake keeps it. The fake answers as a
// Glen with [identity] and a console address, so connect is taken, and
// it registers no client anywhere.
type fakeTool struct {
	version int64
	spec    glen.ToolSpec
	deleted bool
}

const toolSample = `{"url":"https://tickets.example/mcp"}`

// agentRules is what Glen's /v1/validate/agent and PUT refuse of spec:
// the spec itself, then its tools.mcp against the org's tools. The fake
// has no deployment MCP servers.
func (f *fakeGlen) agentRules(s glen.AgentSpec) string {
	if msg := glenAgentRules(s); msg != "" {
		return msg
	}
	for i, m := range s.Tools.MCP {
		if !validToolName(m.Server) || slices.ContainsFunc(s.Tools.MCP[:i], func(o glen.McpUse) bool { return o.Server == m.Server }) {
			return fmt.Sprintf("tools.mcp %q must be a kebab-case server name, other than glen, named once", m.Server)
		}
	}
	for _, m := range s.Tools.MCP {
		t := f.tools[m.Server]
		as := m.As
		if as == "" {
			as = "service"
		}
		switch {
		case t == nil || t.deleted:
			return fmt.Sprintf("tools.mcp names %s, which is not a tool of this org; add it on the console's Tools page", m.Server)
		case as != "service" && t.spec.Connect == nil:
			return fmt.Sprintf("tools.mcp uses %s as %s, which needs sign-in set on the org's MCP server %s; set it on the tool's page in the console's Tools", m.Server, as, m.Server)
		case as == "service" && t.spec.Connect != nil:
			return fmt.Sprintf(`tools.mcp uses %s as service, but people sign in to %s; use {"server": "%s", "as": "user"} or "agent"`, m.Server, m.Server, m.Server)
		}
	}
	return ""
}

func (f *fakeGlen) liveTool(name string) *fakeTool {
	if t := f.tools[name]; t != nil && !t.deleted {
		return t
	}
	return nil
}

func (f *fakeGlen) getTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t := f.liveTool(name)
	if t == nil {
		fail(w, http.StatusNotFound, "no MCP server named "+name)
		return
	}
	answer(w, glen.Tool{Version: t.version, Spec: t.spec})
}

func (f *fakeGlen) putTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var spec glen.ToolSpec
	if err := strictJSON(body, &spec); err != nil {
		fail(w, http.StatusBadRequest, "invalid MCP server spec: "+err.Error())
		return
	}
	if spec.Name == "" {
		spec.Name = name
	}
	problems := toolProblems(spec)
	if spec.Name != name {
		problems = append([]string{fmt.Sprintf("the body names name %q, the path %q; leave name out or make them match", spec.Name, name)}, problems...)
	}
	if len(problems) > 0 {
		fail(w, http.StatusBadRequest, fmt.Sprintf("invalid MCP server spec: %s. A minimal one, its name taken from the path: %s", strings.Join(problems, "; "), toolSample))
		return
	}
	spec = spec.Normalized()
	t := f.tools[name]
	if t == nil {
		t = &fakeTool{}
		f.tools[name] = t
	}
	if before := t.spec; !t.deleted && t.version > 0 && before.Connect != nil && spec.Connect != nil && (!eqPtr(before.URL, spec.URL) || !eqPtr(before.Connect.Provider, spec.Connect.Provider)) {
		fail(w, http.StatusBadRequest, fmt.Sprintf("invalid MCP server spec: people sign in to %s, so its address and provider cannot change under their connections; delete the tool and add it again. A minimal one, its name taken from the path: %s", name, toolSample))
		return
	}
	if _, set := f.credentials[name]; set && !t.deleted && t.version > 0 {
		if field := t.spec.CredentialMoves(spec); field != "" {
			fail(w, http.StatusBadRequest, fmt.Sprintf("invalid MCP server spec: tool %s's credential is set, and it goes only where it was set for; to change %s, remove the credential, change the tool, then set the credential again", name, field))
			return
		}
	}
	if spec.Credential == nil {
		// A record that names no credential has no value.
		delete(f.credentials, name)
	}
	if t.deleted || t.version == 0 || !t.spec.Equal(spec) {
		t.version++
		t.spec, t.deleted = spec, false
	}
	answer(w, glen.Tool{Version: t.version, Spec: t.spec})
}

func (f *fakeGlen) deleteTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t := f.liveTool(name)
	if t == nil {
		fail(w, http.StatusNotFound, "no MCP server named "+name)
		return
	}
	var users []string
	for agent, a := range f.agents {
		if !a.deleted && slices.ContainsFunc(a.spec.Tools.MCP, func(m glen.McpUse) bool { return m.Server == name }) {
			users = append(users, agent)
		}
	}
	if len(users) > 0 {
		slices.Sort(users)
		fail(w, http.StatusConflict, fmt.Sprintf("MCP server %s is used by %s; change or delete those agents first", name, strings.Join(users, ", ")))
		return
	}
	t.version++
	t.deleted = true
	delete(f.credentials, name)
	w.WriteHeader(http.StatusNoContent)
}

// lastFour is what Glen shows of a value: its last four characters, of
// one of 20 or more.
func lastFour(v string) *string {
	if len(v) < 20 {
		return nil
	}
	l := v[len(v)-4:]
	return &l
}

// credentialTool is the live tool name whose record names a credential,
// or nil after answering why not.
func (f *fakeGlen) credentialTool(w http.ResponseWriter, name string) *fakeTool {
	t := f.liveTool(name)
	if t == nil {
		fail(w, http.StatusNotFound, "no MCP server named "+name)
		return nil
	}
	if t.spec.Credential == nil {
		fail(w, http.StatusConflict, fmt.Sprintf("tool %s names no credential: add one to its record, with its kind, first", name))
		return nil
	}
	return t
}

func (f *fakeGlen) getCredential(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t := f.credentialTool(w, name)
	if t == nil {
		return
	}
	shown := glen.CredentialShown{Settable: true, Kind: t.spec.Credential.Kind}
	if v, ok := f.credentials[name]; ok {
		by := "terraform-admin@example.com"
		shown.Set, shown.SetBy, shown.Last4 = true, &by, lastFour(v)
	}
	answer(w, shown)
}

func (f *fakeGlen) putCredential(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Value string `json:"value"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := strictJSON(raw, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.credentialTool(w, name) == nil {
		return
	}
	if v := strings.TrimSpace(body.Value); len(v) < 8 || len(v) > 4096 || strings.ContainsFunc(v, func(c rune) bool { return c <= ' ' || c > '~' }) {
		fail(w, http.StatusBadRequest, "a tool's credential is 8 to 4096 printable characters with no spaces")
		return
	}
	f.credentials[name] = strings.TrimSpace(body.Value)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGlen) deleteCredential(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if f.credentialTool(w, name) == nil {
		return
	}
	delete(f.credentials, name)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGlen) getModelKeys(w http.ResponseWriter, _ *http.Request) {
	keys := map[string]glen.ModelKey{}
	for _, p := range []string{"anthropic", "openai"} {
		k := glen.ModelKey{}
		if v, ok := f.modelKeys[p]; ok {
			in, by, last4 := "vault", "terraform-admin@example.com", v[len(v)-4:]
			k = glen.ModelKey{Set: true, In: &in, SetBy: &by, Last4: &last4}
		}
		keys[p] = k
	}
	answer(w, keys)
}

func (f *fakeGlen) putModelKey(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	var body struct {
		Key string `json:"key"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := strictJSON(raw, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if p != "anthropic" && p != "openai" {
		fail(w, http.StatusNotFound, fmt.Sprintf("no model provider %q; it is anthropic or openai", p))
		return
	}
	if len(strings.TrimSpace(body.Key)) < 8 {
		fail(w, http.StatusBadRequest, "that is not an "+p+" API key")
		return
	}
	f.modelKeys[p] = strings.TrimSpace(body.Key)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeGlen) deleteModelKey(w http.ResponseWriter, r *http.Request) {
	delete(f.modelKeys, r.PathValue("provider"))
	w.WriteHeader(http.StatusNoContent)
}

// toolProblems is what Glen's admin API refuses of a tool's spec.
func toolProblems(s glen.ToolSpec) []string {
	var out []string
	if !validToolName(s.Name) {
		out = append(out, fmt.Sprintf("MCP server name %q must be kebab-case and not glen", s.Name))
	}
	if c := s.CLI; c != nil {
		if s.URL != nil || s.Sample != nil {
			out = append(out, "a tool is an MCP server at a url, a sample, or a CLI's service, not two of them")
		}
		if len(c.Hosts) == 0 {
			out = append(out, "cli.hosts is empty: the hosts Glen adds the credential to, such as api.github.com")
		}
		for _, h := range c.Hosts {
			if why := hostProblem(h.Host); why != "" {
				out = append(out, fmt.Sprintf("cli.hosts %q %s", h.Host, why))
			}
			if u := h.BasicUser; u != nil && (*u == "" || strings.Contains(*u, ":")) {
				out = append(out, fmt.Sprintf("cli.hosts %q: a basic user is not empty and has no colon", h.Host))
			}
		}
		for name, value := range c.Env {
			reserved := slices.Contains([]string{"PATH", "HOME", "HTTPS_PROXY", "SSL_CERT_FILE"}, name) || strings.HasPrefix(name, "GLEN_")
			if name == "" || strings.ToUpper(name) != name || value == "" {
				out = append(out, fmt.Sprintf("cli.env %q must be a variable name in capitals with a stand-in value that is not empty", name))
			} else if reserved {
				out = append(out, fmt.Sprintf("cli.env %q is a variable Glen or the sandbox sets; a stand-in is for a client's own token variable, such as GH_TOKEN", name))
			}
		}
		switch {
		case s.Connect == nil:
			out = append(out, "connect is missing: the sign-in whose token Glen adds for the CLI")
		case s.Connect.Provider == nil:
			out = append(out, "a CLI's service has no sign-in Glen can set up itself: name an Identity OAuth2 provider in connect.provider")
		}
	} else {
		switch {
		case s.URL != nil && s.Sample != nil:
			out = append(out, "an MCP server has a url or is a sample, not both")
		case s.URL == nil && s.Sample == nil:
			out = append(out, "url is missing: where the MCP server answers, such as https://tickets.example/mcp")
		case s.URL != nil && !strings.HasPrefix(*s.URL, "http://") && !strings.HasPrefix(*s.URL, "https://"):
			out = append(out, fmt.Sprintf("url %q must be an http or https URL", *s.URL))
		case s.Sample != nil && *s.Sample != "meetings":
			out = append(out, fmt.Sprintf("sample %q is not one Glen has; it has meetings", *s.Sample))
		}
	}
	if c := s.Credential; c != nil {
		switch {
		case s.URL == nil:
			out = append(out, "a credential goes with a url: a sample needs none, and a cli tool uses its connection's token")
		case c.Kind == "api-key" && (c.TokenURL != nil || c.ClientID != nil || len(c.Scopes) > 0):
			out = append(out, "an api-key credential has no token_url, client_id or scopes; those are a service credential's")
		case c.Kind == "service" && c.Header != nil:
			out = append(out, "a service credential's token is sent as Authorization: Bearer; leave header out")
		case c.Kind == "service" && (c.TokenURL == nil || c.ClientID == nil):
			out = append(out, "a service credential needs token_url and client_id")
		case c.Kind != "api-key" && c.Kind != "service":
			out = append(out, fmt.Sprintf("credential.kind %q is not api-key or service", c.Kind))
		}
	}
	if c := s.Connect; c != nil {
		if s.Sample != nil {
			out = append(out, "a sample needs no sign-in; leave connect out")
		}
		if p := c.Provider; p != nil && strings.TrimSpace(*p) == "" {
			out = append(out, "connect.provider is empty: name an Identity OAuth2 provider, or leave it out for Glen to make one")
		} else if p != nil && strings.HasPrefix(*p, "glen-") {
			out = append(out, fmt.Sprintf(`connect.provider %q begins with "glen-", which only the providers Glen makes do; name a provider made outside Glen, or leave it out for Glen to make one`, *p))
		}
		if slices.ContainsFunc(c.Scopes, func(s string) bool { return s == "" || strings.ContainsAny(s, " \t\n") }) {
			out = append(out, "connect.scopes holds an empty scope or one with a space")
		}
	}
	return out
}

func eqPtr(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

func strictJSON(b []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
