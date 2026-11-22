package glen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// McpUse is one tools.mcp entry: a tool by name, and whose connection its
// calls use. Glen writes a service use as the bare name.
type McpUse struct {
	Server string
	// As is user, agent or service. Empty is service.
	As string
}

func (m McpUse) MarshalJSON() ([]byte, error) {
	if m.As == "" || m.As == "service" {
		return json.Marshal(m.Server)
	}
	return json.Marshal(map[string]string{"server": m.Server, "as": m.As})
}

func (m *McpUse) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		m.Server, m.As = name, ""
		return nil
	}
	var t struct {
		Server string `json:"server"`
		As     string `json:"as"`
	}
	if err := strict(b, &t); err != nil {
		return err
	}
	m.Server, m.As = t.Server, t.As
	if m.As == "service" {
		m.As = ""
	}
	return nil
}

func normalizedUses(uses []McpUse) []McpUse {
	out := make([]McpUse, len(uses))
	for i, m := range uses {
		if m.As == "service" {
			m.As = ""
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
}

// ToolHost is a host Glen's egress proxy adds the token to, and how.
type ToolHost struct {
	Host string
	// BasicUser set: the token is the Basic auth password of this user.
	// Unset: the token is a Bearer token.
	BasicUser *string
}

func (h ToolHost) MarshalJSON() ([]byte, error) {
	var auth any = "bearer"
	if h.BasicUser != nil {
		auth = map[string]string{"basic": *h.BasicUser}
	}
	return json.Marshal(map[string]any{"host": h.Host, "auth": auth})
}

func (h *ToolHost) UnmarshalJSON(b []byte) error {
	var raw struct {
		Host string          `json:"host"`
		Auth json.RawMessage `json:"auth"`
	}
	if err := strict(b, &raw); err != nil {
		return err
	}
	h.Host, h.BasicUser = raw.Host, nil
	if len(raw.Auth) == 0 || string(raw.Auth) == `"bearer"` {
		return nil
	}
	var basic struct {
		Basic *string `json:"basic"`
	}
	if err := strict(raw.Auth, &basic); err != nil || basic.Basic == nil {
		return fmt.Errorf(`auth %s is neither "bearer" nor {"basic": <user>}`, raw.Auth)
	}
	h.BasicUser = basic.Basic
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
