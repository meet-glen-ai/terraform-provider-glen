package glen

import (
	"context"
	"net/http"
)

// AutoRefill is the org's auto refill, in cents: when the balance falls
// below BelowCents, Glen pays AmountCents with the saved card, at most
// CapCents in a calendar month (UTC).
type AutoRefill struct {
	On          bool    `json:"on"`
	BelowCents  int64   `json:"below_cents"`
	AmountCents int64   `json:"amount_cents"`
	CapCents    int64   `json:"cap_cents"`
	ChangedBy   *string `json:"changed_by,omitempty"`
	ChangedAt   *string `json:"changed_at,omitempty"`
}

// Billing is what GET /v1/billing answers that the provider reads.
type Billing struct {
	AutoRefill AutoRefill `json:"auto_refill"`
}

func (c *Client) GetBilling(ctx context.Context) (Billing, error) {
	var b Billing
	err := c.do(ctx, http.MethodGet, "/v1/billing", nil, &b)
	return b, err
}

// PutAutoRefill sets the org's auto refill. Glen refuses amounts outside
// its limits, in words for an admin.
func (c *Client) PutAutoRefill(ctx context.Context, a AutoRefill) error {
	body := map[string]any{"on": a.On, "below_cents": a.BelowCents, "amount_cents": a.AmountCents, "cap_cents": a.CapCents}
	return c.do(ctx, http.MethodPut, "/v1/billing/auto-refill", body, nil)
}
