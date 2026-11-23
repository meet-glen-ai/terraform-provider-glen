package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

func (b backend) glenHasAutoRefill(want glen.AutoRefill) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetBilling(context.Background())
		if err != nil {
			return err
		}
		a := got.AutoRefill
		if a.On != want.On || a.BelowCents != want.BelowCents || a.AmountCents != want.AmountCents || a.CapCents != want.CapCents {
			return fmt.Errorf("glen has auto refill %+v, want %+v", a, want)
		}
		return nil
	}
}

func TestAutoRefillIsSetChangedAndLeftOnDestroy(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		off := glen.AutoRefill{On: false, BelowCents: 1000, AmountCents: 5000, CapCents: 10000}
		b.test(t, resource.TestCase{
			// Destroying it changes nothing in Glen.
			CheckDestroy: b.glenHasAutoRefill(off),
			Steps: []resource.TestStep{
				{
					Config: b.config(`
resource "glen_auto_refill" "org" {
  enabled = true
}
`),
					Check: resource.ComposeTestCheckFunc(
						b.glenHasAutoRefill(defaultAutoRefill),
						resource.TestCheckResourceAttr("glen_auto_refill.org", "below_cents", "500"),
						resource.TestCheckResourceAttrSet("glen_auto_refill.org", "changed_by"),
					),
				},
				{
					Config: b.config(`
resource "glen_auto_refill" "org" {
  enabled      = false
  below_cents  = 1000
  amount_cents = 5000
  cap_cents    = 10000
}
`),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("glen_auto_refill.org", plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasAutoRefill(off),
				},
			},
		})
	})
}

func TestAutoRefillImportedAndChangedElsewhereShowsADiff(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		set := func(a glen.AutoRefill) func() {
			return func() {
				if err := b.api.PutAutoRefill(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			}
		}
		config := b.config(`
resource "glen_auto_refill" "org" {
  enabled     = true
  below_cents = 800
}
`)
		mine := glen.AutoRefill{On: true, BelowCents: 800, AmountCents: 2000, CapCents: 20000}
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{
					PreConfig:          set(mine),
					Config:             config,
					ResourceName:       "glen_auto_refill.org",
					ImportState:        true,
					ImportStateId:      "org",
					ImportStatePersist: true,
				},
				{
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
				},
				{
					// The console turned it off; the next apply turns it back on.
					PreConfig: set(glen.AutoRefill{On: false, BelowCents: 800, AmountCents: 2000, CapCents: 20000}),
					Config:    config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("glen_auto_refill.org", plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasAutoRefill(mine),
				},
			},
		})
	})
}

func TestAutoRefillOutsideGlensLimitsIsRefusedInItsWords(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{Steps: []resource.TestStep{{
			Config: b.config(`
resource "glen_auto_refill" "org" {
  enabled      = true
  amount_cents = 5000
  cap_cents    = 2000
}
`),
			ExpectError: regexp.MustCompile(`the monthly cap is at least one refill`),
		}}})
	})
}
