package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// approverConfig is the role security held by members, and an agent
// naming it as its approvers.
func approverConfig(members ...string) string {
	quoted := make([]string, len(members))
	for i, m := range members {
		quoted[i] = fmt.Sprintf("%q", m)
	}
	return fmt.Sprintf(`
resource "glen_role" "security" {
  name        = "security"
  description = "Allows the triage agent's actions."
  members     = [%s]
}

resource "glen_agent" "triage" {
  name         = "triage"
  instructions = "Triage the alert.\n"
  model        = "claude-opus-5-5"
  approvers    = [glen_role.security.name]
}
`, strings.Join(quoted, ", "))
}

func (b backend) glenHasRole(name, description string, members ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetRole(context.Background(), name)
		if err != nil {
			return err
		}
		if got.Description != description || !slices.Equal(got.Members, members) && len(got.Members)+len(members) > 0 {
			return fmt.Errorf("glen has role %s as %+v, want %q held by %v", name, got, description, members)
		}
		return nil
	}
}

func (b backend) glenAgentApprovers(agent string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetAgent(context.Background(), agent)
		if err != nil {
			return err
		}
		if !slices.Equal(got.Spec.Approvers, want) {
			return fmt.Errorf("glen has %s's approvers as %v, want %v", agent, got.Spec.Approvers, want)
		}
		return nil
	}
}

func (b backend) roleGone(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if _, err := b.api.GetRole(context.Background(), name); !glen.IsNotFound(err) {
			return fmt.Errorf("role %s: want 404, got %v", name, err)
		}
		return nil
	}
}

func TestRoleHeldByMembersNamedByAnAgent(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ana@tf.example", "admin")
		b.seedMember(t, "ben@tf.example", "member")
		b.seedMember(t, "cy@tf.example", "member")
		b.test(t, resource.TestCase{
			CheckDestroy: b.roleGone("security"),
			Steps: []resource.TestStep{
				{
					Config: b.config(approverConfig("ben@tf.example")),
					Check: resource.ComposeTestCheckFunc(
						b.glenHasRole("security", "Allows the triage agent's actions.", "ben@tf.example"),
						b.glenAgentApprovers("triage", "security"),
					),
				},
				{
					Config: b.config(approverConfig("cy@tf.example", "ben@tf.example")),
					Check:  b.glenHasRole("security", "Allows the triage agent's actions.", "ben@tf.example", "cy@tf.example"),
				},
				{
					Config: b.config(approverConfig()),
					Check:  b.glenHasRole("security", "Allows the triage agent's actions."),
				},
				{
					ResourceName:                         "glen_role.security",
					ImportState:                          true,
					ImportStateId:                        "security",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					Config:      b.config(approverConfig("dee@tf.example")),
					ExpectError: regexp.MustCompile(`dee@tf.example\s+is\s+not\s+a\s+member\s+of\s+this\s+org`),
				},
			},
		})
	})
}

func TestRoleAnAgentNamesStays(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ana@tf.example", "admin")
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{Config: b.config(approverConfig())},
				{
					// The agent outlives the apply, so the role it names does too.
					Config: b.config(`
removed {
  from = glen_agent.triage
  lifecycle {
    destroy = false
  }
}
`),
					ExpectError: regexp.MustCompile(`role\s+security\s+approves\s+for\s+triage`),
				},
				{
					Config: b.config(`
removed {
  from = glen_agent.triage
  lifecycle {
    destroy = false
  }
}

removed {
  from = glen_role.security
  lifecycle {
    destroy = false
  }
}
`),
					Check: b.glenHasRole("security", "Allows the triage agent's actions."),
				},
			},
		})
	})
}

func TestAgentApproversMustBeRoles(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{Steps: []resource.TestStep{{
			Config: b.config(`
resource "glen_agent" "triage" {
  name         = "triage"
  instructions = "Triage the alert.\n"
  model        = "claude-opus-5-5"
  approvers    = ["security"]
}
`),
			ExpectError: regexp.MustCompile(`approver\s+roles\s+the\s+org\s+does\s+not\s+have:\s+security`),
		}}})
	})
}

func TestRoleConfigIsCheckedAtPlan(t *testing.T) {
	t.Parallel()
	b := newBackend(startFake(t, "key"), "key", false)
	for _, c := range []struct{ name, want string }{
		{"admin", `is\s+built\s+in`},
		{"Security", `is\s+not\s+a\s+role\s+name`},
		{"on--call", `is\s+not\s+a\s+role\s+name`},
	} {
		b.test(t, resource.TestCase{Steps: []resource.TestStep{{
			Config:      b.config(fmt.Sprintf("resource \"glen_role\" \"x\" {\n  name = %q\n}\n", c.name)),
			ExpectError: regexp.MustCompile(c.want),
		}}})
	}
}
