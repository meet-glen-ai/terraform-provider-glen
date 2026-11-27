package glen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// McpUse is one tools.mcp entry: a tool by name, whose connection its
// calls use, and the functions it holds back for this agent. Glen writes
// a service use with no policies as the bare name.
type McpUse struct {
	Server string
	// As is user, agent or service. Empty is service.
	As string
	// Policies are the tool's functions this agent's calls to which wait
	// for a person's approval (approve) or never run (never), by name. Each
	// is the stricter of it and the org's policy for the function.
	Policies map[string]string
}

// ToolPolicies is every policy an agent's tools.mcp entry may set: an
// agent makes a function stricter than the org's policy, never looser.
var ToolPolicies = []string{"approve", "never"}

func (m McpUse) MarshalJSON() ([]byte, error) {
	service := m.As == "" || m.As == "service"
	if service && len(m.Policies) == 0 {
		return json.Marshal(m.Server)
	}
	t := map[string]any{"server": m.Server}
	if !service {
		t["as"] = m.As
	}
	if len(m.Policies) > 0 {
		t["policies"] = m.Policies
	}
	return json.Marshal(t)
}

func (m *McpUse) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		m.Server, m.As = name, ""
		return nil
	}
	var t struct {
		Server   string            `json:"server"`
		As       string            `json:"as"`
		Policies map[string]string `json:"policies"`
	}
	if err := strict(b, &t); err != nil {
		return err
	}
	m.Server, m.As, m.Policies = t.Server, t.As, t.Policies
	if m.As == "service" {
		m.As = ""
	}
	if len(m.Policies) == 0 {
		m.Policies = nil
	}
	return nil
}

func normalizedUses(uses []McpUse) []McpUse {
	out := make([]McpUse, len(uses))
	for i, m := range uses {
		if m.As == "service" {
			m.As = ""
		}
		if len(m.Policies) == 0 {
			m.Policies = nil
		}
		out[i] = m
	}
	return out
}

// ToolSpec is an org's tool, the record behind /v1/mcp-servers/{name}:
// an MCP server at a url, a sample Glen answers itself, or a service the
// agent's CLIs call.
type ToolSpec struct {
	Name       string          `json:"name"`
	URL        *string         `json:"url,omitempty"`
	Sample     *string         `json:"sample,omitempty"`
	Connect    *ToolConnect    `json:"connect,omitempty"`
	CLI        *ToolCLI        `json:"cli,omitempty"`
	Credential *ToolCredential `json:"credential,omitempty"`
}

// ToolCredential is the kind of the tool's own credential, which Glen
// adds to an agent's calls as the service: an API key in header, or an
// OAuth2 client traded at token_url. Its value is never in the record.
type ToolCredential struct {
	Kind     string   `json:"kind"`
	Header   *string  `json:"header,omitempty"`
	TokenURL *string  `json:"token_url,omitempty"`
	ClientID *string  `json:"client_id,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// CredentialMoves names the field of to that sends s's credential
// somewhere else: the url, or the credential's kind, header, token_url or
// client_id. Glen refuses such a change while a value is set; "" when
// none differs, or either names no credential.
func (s ToolSpec) CredentialMoves(to ToolSpec) string {
	a, b := s.Credential, to.Credential
	if a == nil || b == nil {
		return ""
	}
	lower := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.ToLower(*p)
	}
	switch {
	case !eqString(s.URL, to.URL):
		return "url"
	case a.Kind != b.Kind:
		return "credential.kind"
	case lower(a.Header) != lower(b.Header):
		return "credential.header"
	case !eqString(a.TokenURL, b.TokenURL):
		return "credential.token_url"
	case !eqString(a.ClientID, b.ClientID):
		return "credential.client_id"
	}
	return ""
}

func eqString(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// CredentialShown is a tool's credential as Glen shows it: whether it is
// set, by whom, when and, of a value of 20 characters or more, its last
// four. Never the value.
type CredentialShown struct {
	Set      bool    `json:"set"`
	SetBy    *string `json:"set_by"`
	SetAt    *string `json:"set_at"`
	Last4    *string `json:"last4"`
	Settable bool    `json:"settable"`
	Managed  *string `json:"managed"`
	Kind     string  `json:"kind"`
}

// ToolConnect is how people and agents sign in to a tool.
type ToolConnect struct {
	Provider *string  `json:"provider,omitempty"`
	Scopes   []string `json:"scopes"`
}

// ToolCLI is the hosts of a service an agent's CLIs call.
type ToolCLI struct {
	Hosts []ToolHost        `json:"hosts"`
	Allow []string          `json:"allow,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
	// AWSRole is the IAM role whose session signs the AWS hosts' requests.
	AWSRole *string `json:"aws_role,omitempty"`
}

// ToolHost is a host Glen's egress proxy adds the token to, and how.
type ToolHost struct {
	Host string
	// BasicUser set: the token is the Basic auth password of this user.
	// Unset: the token is a Bearer token.
	BasicUser *string
	// Requests set: the only requests Glen's proxy sends on this host
	// with the token.
	Requests []ToolRequest
	// Others is what Glen does with the rest: "refuse" (unset) or
	// "anonymous", sent with no token.
	Others *string
	// AWS set: Glen signs the host's requests with SigV4 for this service
	// and region, with a session of the tool's AWS role.
	AWS *ToolAWS
}

// ToolAWS is what an AWS host's requests are signed for.
type ToolAWS struct {
	Service string `json:"service"`
	Region  string `json:"region"`
}

// ToolRequest is a request a tool's host may be sent: a method and a
// path pattern, where * is within one segment and a final ** the rest.
type ToolRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func (h ToolHost) MarshalJSON() ([]byte, error) {
	var auth any = "bearer"
	if h.BasicUser != nil {
		auth = map[string]string{"basic": *h.BasicUser}
	}
	if h.AWS != nil {
		auth = map[string]ToolAWS{"aws": *h.AWS}
	}
	out := map[string]any{"host": h.Host, "auth": auth}
	if len(h.Requests) > 0 {
		out["requests"] = h.Requests
	}
	if h.Others != nil && *h.Others != "refuse" {
		out["others"] = *h.Others
	}
	return json.Marshal(out)
}

func (h *ToolHost) UnmarshalJSON(b []byte) error {
	var raw struct {
		Host     string          `json:"host"`
		Auth     json.RawMessage `json:"auth"`
		Requests []ToolRequest   `json:"requests"`
		Others   *string         `json:"others"`
	}
	if err := strict(b, &raw); err != nil {
		return err
	}
	h.Host, h.BasicUser, h.Requests, h.Others, h.AWS = raw.Host, nil, raw.Requests, raw.Others, nil
	if h.Others != nil && *h.Others == "refuse" {
		h.Others = nil
	}
	if len(raw.Auth) == 0 || string(raw.Auth) == `"bearer"` {
		return nil
	}
	var signed struct {
		Basic *string  `json:"basic"`
		AWS   *ToolAWS `json:"aws"`
	}
	if err := strict(raw.Auth, &signed); err != nil || (signed.Basic == nil) == (signed.AWS == nil) {
		return fmt.Errorf(`auth %s is not "bearer", {"basic": <user>} or {"aws": {"service": ..., "region": ...}}`, raw.Auth)
	}
	h.BasicUser, h.AWS = signed.Basic, signed.AWS
	return nil
}

type Tool struct {
	Version int64    `json:"version"`
	Spec    ToolSpec `json:"spec"`
}

// Normalized is the spec as Glen reads it back: no scopes is an empty
// list, and an empty allow or env is left out.
func (s ToolSpec) Normalized() ToolSpec {
	if s.Connect != nil {
		c := *s.Connect
		c.Scopes = orEmpty(c.Scopes)
		s.Connect = &c
	}
	if s.CLI != nil {
		c := *s.CLI
		c.Hosts = orEmpty(c.Hosts)
		for i := range c.Hosts {
			if len(c.Hosts[i].Requests) == 0 {
				c.Hosts[i].Requests = nil
			}
		}
		if len(c.Allow) == 0 {
			c.Allow = nil
		}
		if len(c.Env) == 0 {
			c.Env = nil
		}
		s.CLI = &c
	}
	if s.Credential != nil {
		c := *s.Credential
		if len(c.Scopes) == 0 {
			c.Scopes = nil
		}
		s.Credential = &c
	}
	return s
}

func (s ToolSpec) Equal(o ToolSpec) bool {
	return reflect.DeepEqual(s.Normalized(), o.Normalized())
}

func strict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
