package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

const pagerAddr = "glen_agent.pager"

func notifyConfig(notify string) string {
	return fmt.Sprintf(`
resource "glen_agent" "pager" {
  name         = "pager"
  instructions = "Page on real alerts.\n"
  model        = "claude-opus-5-5"
%s}
`, notify)
}

func pagerSpec(notify *glen.Notify) glen.AgentSpec {
	return glen.AgentSpec{Agent: "pager", Instructions: "Page on real alerts.\n", Harness: "claude-code", Model: "claude-opus-5-5", Notify: notify}
}

func emailNotify(to []string, on ...string) *glen.Notify {
	return &glen.Notify{Email: &glen.EmailNotify{To: to, On: on}}
}

func TestAgentNotifyEmail(t *testing.T) {
	t.Parallel()
	ann := `
  notify {
    email {
      to = ["ann@tf.example"]
      on = ["finished", "needs_you"]
    }
  }
`
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ann@tf.example", "member")
		b.seedMember(t, "ben@tf.example", "member")
		annOn := emailNotify([]string{"ann@tf.example"}, "finished", "needs_you")
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("pager"),
			Steps: []resource.TestStep{
				{
					Config: b.config(notifyConfig(ann)),
					Check:  b.glenHas(pagerSpec(annOn), 1),
				},
				{
					ResourceName:                         pagerAddr,
					ImportState:                          true,
					ImportStateId:                        "pager",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					// Glen keeps each address as the member's own, so one
					// written in another case is the same agent.
					Config: b.config(notifyConfig(`
  notify {
    email {
      to = ["Ann@TF.example"]
      on = ["finished", "needs_you"]
    }
  }
`)),
					Check: b.glenHas(pagerSpec(annOn), 1),
				},
				{
					Config: b.config(notifyConfig(`
  notify {
    email {
      to = ["Ann@TF.example"]
      on = ["finished", "needs_you"]
    }
  }
`)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
				},
				{
					// A list to email on nothing is kept, and Glen emails no one.
					Config: b.config(notifyConfig(`
  notify {
    email {
      to = ["ann@tf.example", "ben@tf.example"]
    }
  }
`)),
					Check: b.glenHas(pagerSpec(emailNotify([]string{"ann@tf.example", "ben@tf.example"})), 2),
				},
				{
					Config: b.config(notifyConfig("\n  notify {}\n")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(pagerAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHas(pagerSpec(nil), 3),
				},
				{
					// Taking out the empty block changes the configuration,
					// not the agent.
					Config: b.config(notifyConfig("")),
					Check:  b.glenHas(pagerSpec(nil), 3),
				},
			},
		})
	})
}

func TestAgentNotifyOnTakesTheThreeEventsOnce(t *testing.T) {
	t.Parallel()
	email := func(body string) string {
		return notifyConfig("\n  notify {\n    email {\n" + body + "    }\n  }\n")
	}
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{
					Config:      b.config(email("      to = [\"ann@tf.example\"]\n      on = [\"done\"]\n")),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`"done"\s+is\s+not\s+one\s+of\s+finished,\s+failed,\s+needs_you`),
				},
				{
					Config:      b.config(email("      to = [\"ann@tf.example\"]\n      on = [\"failed\", \"failed\"]\n")),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`failed\s+is\s+named\s+twice`),
				},
				{
					Config:      b.config(email("      on = [\"failed\"]\n")),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(`Missing\s+notify.email.to`),
				},
			},
		})
	})
}

func TestAgentNotifyGlenRefusesAtApply(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ann@tf.example", "member")
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{
					// An API key is no one, so it cannot default to to itself.
					Config: b.config(notifyConfig(`
  notify {
    email {
      to = []
      on = ["failed"]
    }
  }
`)),
					ExpectError: regexp.MustCompile(`notify.email.to\s+names\s+no\s+one`),
				},
				{
					Config: b.config(notifyConfig(`
  notify {
    email {
      to = ["ann@tf.example", "cy@tf.example"]
      on = ["failed"]
    }
  }
`)),
					ExpectError: regexp.MustCompile(`not\s+members\s+of\s+the\s+org:\s+cy@tf.example`),
				},
			},
		})
	})
}
