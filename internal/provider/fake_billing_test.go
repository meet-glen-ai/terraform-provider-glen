package provider

import (
	"io"
	"net/http"

	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// defaultAutoRefill is every org's until an admin changes it, as in Glen:
// $20 below $5, at most $200 a month.
var defaultAutoRefill = glen.AutoRefill{On: true, BelowCents: 500, AmountCents: 2000, CapCents: 20000}

func (f *fakeGlen) billingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/billing", f.getBilling)
	mux.HandleFunc("PUT /v1/billing/auto-refill", f.putAutoRefill)
}

func (f *fakeGlen) getBilling(w http.ResponseWriter, _ *http.Request) {
	answer(w, map[string]any{"payments_ready": false, "auto_refill": f.autoRefill, "card_saved": false, "refill_waits": nil, "refill_failure": nil, "payments": []any{}})
}

// putAutoRefill keeps Glen's limits and words for them.
func (f *fakeGlen) putAutoRefill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On          bool  `json:"on"`
		BelowCents  int64 `json:"below_cents"`
		AmountCents int64 `json:"amount_cents"`
		CapCents    int64 `json:"cap_cents"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := strictJSON(raw, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	within := func(v, lo, hi int64) bool { return lo <= v && v <= hi }
	switch {
	case !within(body.BelowCents, 100, 100_000):
		fail(w, http.StatusBadRequest, "the threshold is $1 to $1000")
		return
	case !within(body.AmountCents, 2000, 100_000):
		fail(w, http.StatusBadRequest, "a refill is $20 to $1000")
		return
	case !within(body.CapCents, 2000, 1_000_000):
		fail(w, http.StatusBadRequest, "the monthly cap is $20 to $10000")
		return
	case body.CapCents < body.AmountCents:
		fail(w, http.StatusBadRequest, "the monthly cap is at least one refill")
		return
	}
	by, at := "terraform-admin@example.com", "2026-11-23T12:00:00Z"
	f.autoRefill = glen.AutoRefill{On: body.On, BelowCents: body.BelowCents, AmountCents: body.AmountCents, CapCents: body.CapCents, ChangedBy: &by, ChangedAt: &at}
	w.WriteHeader(http.StatusNoContent)
}
