package glen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ManagedBy is the Glen-Managed-By header every write carries, so Glen
// records that Terraform manages what it writes and the console warns
// before an edit the next apply would undo.
const ManagedBy = "terraform"

type Client struct {
	base string
	key  string
	http *http.Client
}

func NewClient(baseURL, key string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		key:  key,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("glen answered %d: %s", e.Status, e.Message)
}

func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

func (c *Client) ValidateAgent(ctx context.Context, spec AgentSpec) error {
	return c.do(ctx, http.MethodPost, "/v1/validate/agent", spec.Normalized(), nil)
}

func (c *Client) GetAgent(ctx context.Context, name string) (Agent, error) {
	var a Agent
	err := c.do(ctx, http.MethodGet, "/v1/agents/"+url.PathEscape(name), nil, &a)
	return a, err
}

func (c *Client) PutAgent(ctx context.Context, spec AgentSpec) (Agent, error) {
	var a Agent
	err := c.do(ctx, http.MethodPut, "/v1/agents/"+url.PathEscape(spec.Agent), spec.Normalized(), &a)
	return a, err
}

func (c *Client) DeleteAgent(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/agents/"+url.PathEscape(name), nil, nil)
}

func (c *Client) ValidateKnowledgeBase(ctx context.Context, spec KnowledgeBaseSpec) error {
	return c.do(ctx, http.MethodPost, "/v1/validate/knowledge_base", spec, nil)
}

func (c *Client) GetKnowledgeBase(ctx context.Context, name string) (KnowledgeBase, error) {
	var w KnowledgeBase
	err := c.do(ctx, http.MethodGet, "/v1/knowledge-bases/"+url.PathEscape(name), nil, &w)
	return w, err
}

func (c *Client) PutKnowledgeBase(ctx context.Context, spec KnowledgeBaseSpec) (KnowledgeBase, error) {
	var w KnowledgeBase
	err := c.do(ctx, http.MethodPut, "/v1/knowledge-bases/"+url.PathEscape(spec.Name), spec, &w)
	return w, err
}

func (c *Client) DeleteKnowledgeBase(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/knowledge-bases/"+url.PathEscape(name), nil, nil)
}

func (c *Client) GetTool(ctx context.Context, name string) (Tool, error) {
	var t Tool
	err := c.do(ctx, http.MethodGet, "/v1/mcp-servers/"+url.PathEscape(name), nil, &t)
	return t, err
}

func (c *Client) PutTool(ctx context.Context, spec ToolSpec) (Tool, error) {
	var t Tool
	err := c.do(ctx, http.MethodPut, "/v1/mcp-servers/"+url.PathEscape(spec.Name), spec.Normalized(), &t)
	return t, err
}

func (c *Client) DeleteTool(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/mcp-servers/"+url.PathEscape(name), nil, nil)
}

// SetToolCredential makes value the credential of tool name, whose record
// names its kind. Glen keeps it in the org's vault and never answers it.
func (c *Client) SetToolCredential(ctx context.Context, name, value string) error {
	return c.do(ctx, http.MethodPut, "/v1/mcp-servers/"+url.PathEscape(name)+"/credential", map[string]string{"value": value}, nil)
}

// RemoveToolCredential removes the tool's credential's value; nothing to
// remove is no error.
func (c *Client) RemoveToolCredential(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/mcp-servers/"+url.PathEscape(name)+"/credential", nil, nil)
}

func (c *Client) GetToolCredential(ctx context.Context, name string) (CredentialShown, error) {
	var s CredentialShown
	err := c.do(ctx, http.MethodGet, "/v1/mcp-servers/"+url.PathEscape(name)+"/credential", nil, &s)
	return s, err
}

// ModelKey is one provider's key as Glen shows it: never the key.
type ModelKey struct {
	Set   bool    `json:"set"`
	In    *string `json:"in"`
	SetBy *string `json:"set_by"`
	SetAt *string `json:"set_at"`
	Last4 *string `json:"last4"`
}

func (c *Client) ModelKeys(ctx context.Context) (map[string]ModelKey, error) {
	var keys map[string]ModelKey
	err := c.do(ctx, http.MethodGet, "/v1/model-keys", nil, &keys)
	return keys, err
}

// PutModelKey makes key the org's key of provider, anthropic or openai.
// Glen checks it with the provider first.
func (c *Client) PutModelKey(ctx context.Context, provider, key string) error {
	return c.do(ctx, http.MethodPut, "/v1/model-keys/"+url.PathEscape(provider), map[string]string{"key": key}, nil)
}

func (c *Client) DeleteModelKey(ctx context.Context, provider string) error {
	return c.do(ctx, http.MethodDelete, "/v1/model-keys/"+url.PathEscape(provider), nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPut {
		req.Header.Set("Glen-Managed-By", ManagedBy)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(raw))
		}
		return &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: reading Glen's answer: %w", method, path, err)
	}
	return nil
}
