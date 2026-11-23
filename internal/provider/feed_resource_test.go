package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

const zeroWireAddr = "glen_feed.zero_wire"

// zeroWireConfig leaves poll_secs and baseline out when extra is empty,
// so Glen's defaults apply.
func zeroWireConfig(extra string) string {
	return fmt.Sprintf(`
resource "glen_feed" "zero_wire" {
  name   = "zero-wire"
  format = "rss"
  url    = "https://radar.example/zero-wire/feed"
%s
}
`, extra)
}

const intelOnZeroWire = `
resource "glen_agent" "intel" {
  name         = "intel"
  instructions = "Triage the briefing.\n"
  model        = "claude-opus-5-5"

  trigger {
    feed = glen_feed.zero_wire.name
  }
}
`

func (b backend) glenHasFeed(want glen.FeedSpec, version int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetFeed(context.Background(), want.Name)
		if err != nil {
			return err
		}
		if got.Spec != want || got.Version != version {
			return fmt.Errorf("glen has %+v at version %d, want %+v at %d", got.Spec, got.Version, want, version)
		}
		return nil
	}
}

// feedInUse checks Glen refuses to delete a feed a live agent starts on.
func (b backend) feedInUse(name, agent string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		err := b.api.DeleteFeed(context.Background(), name)
		if err == nil || !strings.Contains(err.Error(), "starts "+agent) {
			return fmt.Errorf("deleting feed %s with agent %s on it: want a refusal naming the agent, got %v", name, agent, err)
		}
		return nil
	}
}

func (b backend) feedAndIntelGone(*terraform.State) error {
	if _, err := b.api.GetFeed(context.Background(), "zero-wire"); !glen.IsNotFound(err) {
		return fmt.Errorf("feed zero-wire: want 404, got %v", err)
	}
	return b.agentGone("intel")(nil)
}

func TestFeedAndItsAgentCreateUpdateImportDestroy(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		spec := glen.FeedSpec{Name: "zero-wire", Format: "rss", URL: "https://radar.example/zero-wire/feed", PollSecs: 3600, Baseline: true}
		later := spec
		later.PollSecs, later.Baseline = 900, false
		b.test(t, resource.TestCase{
			CheckDestroy: b.feedAndIntelGone,
			Steps: []resource.TestStep{
				{
					Config: b.config(zeroWireConfig("") + intelOnZeroWire),
					Check: resource.ComposeTestCheckFunc(
						b.glenHasFeed(spec, 1),
						b.feedInUse("zero-wire", "intel"),
					),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(zeroWireAddr, tfjsonpath.New("poll_secs"), knownvalue.Int64Exact(3600)),
						statecheck.ExpectKnownValue(zeroWireAddr, tfjsonpath.New("baseline"), knownvalue.Bool(true)),
						statecheck.ExpectKnownValue(zeroWireAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					},
				},
				{
					Config: b.config(zeroWireConfig("  poll_secs = 900\n  baseline  = false") + intelOnZeroWire),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(zeroWireAddr, plancheck.ResourceActionUpdate),
							plancheck.ExpectResourceAction("glen_agent.intel", plancheck.ResourceActionNoop),
						},
					},
					Check: b.glenHasFeed(later, 2),
				},
				{
					ResourceName:                         zeroWireAddr,
					ImportState:                          true,
					ImportStateId:                        "zero-wire",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
			},
		})
	})
}

func TestAFeedGlenRefusesFailsTheApplyInGlensWords(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{
					Config: b.config(`
resource "glen_feed" "inside" {
  name   = "inside"
  format = "rss"
  url    = "http://radar.example/feed"
}
`),
					ExpectError: regexp.MustCompile(`must be an https URL`),
				},
			},
		})
	})
}
