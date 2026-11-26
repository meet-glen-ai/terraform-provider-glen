package glen

import (
	"context"
	"net/http"
	"net/url"
)

// Role is one of the org's roles, as GET /v1/roles/{name} answers: a
// named group of members that grants nothing by itself. An agent's
// allowed_message_senders name the roles whose members may message it.
// Members are by email, sorted. admin and member are built in, and
// WorkOS decides who holds them.
type Role struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	BuiltIn     bool     `json:"built_in"`
	Members     []string `json:"members"`
}

// RoleSpec is the body of PUT /v1/roles/{name}. Members nil leaves who
// holds the role as it is; otherwise exactly they hold it.
type RoleSpec struct {
	Description string    `json:"description"`
	Members     *[]string `json:"members,omitempty"`
}

func (c *Client) GetRole(ctx context.Context, name string) (Role, error) {
	var r Role
	err := c.do(ctx, http.MethodGet, "/v1/roles/"+url.PathEscape(name), nil, &r)
	return r, err
}

// PutRole creates the role or describes it again, and with members set
// makes exactly those members hold it.
func (c *Client) PutRole(ctx context.Context, name string, spec RoleSpec) (Role, error) {
	var r Role
	err := c.do(ctx, http.MethodPut, "/v1/roles/"+url.PathEscape(name), spec, &r)
	return r, err
}

// DeleteRole deletes the role. Glen refuses (409) while an agent names
// it in its allowed_message_senders.
func (c *Client) DeleteRole(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/roles/"+url.PathEscape(name), nil, nil)
}
