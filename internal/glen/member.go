package glen

import (
	"context"
	"net/http"
	"net/url"
)

// Member is a person of the org, or one invited to it, as GET
// /v1/members/{email} answers: State is "active" or "invited". Role is
// nil for a WorkOS membership whose roles grant no Glen role.
type Member struct {
	Email string  `json:"email"`
	Role  *string `json:"role"`
	State string  `json:"state"`
}

func (c *Client) GetMember(ctx context.Context, email string) (Member, error) {
	var m Member
	err := c.do(ctx, http.MethodGet, "/v1/members/"+url.PathEscape(email), nil, &m)
	return m, err
}

// PutMember makes the person hold role: a member's role is set, and
// anyone else is invited with it through the org's WorkOS organization.
func (c *Client) PutMember(ctx context.Context, email, role string) (Member, error) {
	var m Member
	err := c.do(ctx, http.MethodPut, "/v1/members/"+url.PathEscape(email), map[string]string{"role": role}, &m)
	return m, err
}

// DeleteMember ends the person's membership, with their console logins
// and API tokens, or revokes their invitation.
func (c *Client) DeleteMember(ctx context.Context, email string) error {
	return c.do(ctx, http.MethodDelete, "/v1/members/"+url.PathEscape(email), nil, nil)
}
