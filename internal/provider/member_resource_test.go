package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

func memberConfig(name, email, role string) string {
	return fmt.Sprintf(`
resource "glen_member" %q {
  email = %q
  role  = %q
}
`, name, email, role)
}

func (b backend) glenHasMember(email, role, state string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetMember(context.Background(), email)
		if err != nil {
			return err
		}
		if got.Role == nil || *got.Role != role || got.State != state {
			return fmt.Errorf("glen has %s as %+v, want role %s, state %s", email, got, role, state)
		}
		return nil
	}
}

func (b backend) memberGone(email string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if _, err := b.api.GetMember(context.Background(), email); !glen.IsNotFound(err) {
			return fmt.Errorf("member %s: want 404, got %v", email, err)
		}
		return nil
	}
}

func TestMemberImportRoleChangeDestroy(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ana@tf.example", "admin")
		b.seedMember(t, "ben@tf.example", "member")
		b.test(t, resource.TestCase{
			CheckDestroy: resource.ComposeTestCheckFunc(b.memberGone("ben@tf.example"), b.glenHasMember("ana@tf.example", "admin", "active")),
			Steps: []resource.TestStep{
				{
					Config:             b.config(memberConfig("ben", "ben@tf.example", "member")),
					ResourceName:       "glen_member.ben",
					ImportState:        true,
					ImportStateId:      "ben@tf.example",
					ImportStatePersist: true,
				},
				{
					Config: b.config(memberConfig("ben", "ben@tf.example", "admin")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("glen_member.ben", plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasMember("ben@tf.example", "admin", "active"),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("glen_member.ben", tfjsonpath.New("state"), knownvalue.StringExact("active")),
					},
				},
				{
					ResourceName:                         "glen_member.ben",
					ImportState:                          true,
					ImportStateId:                        "ben@tf.example",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "email",
				},
			},
		})
	})
}

func TestMemberTheLastAdminStays(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ana@tf.example", "admin")
		b.test(t, resource.TestCase{
			CheckDestroy: b.glenHasMember("ana@tf.example", "admin", "active"),
			Steps: []resource.TestStep{
				{
					Config:             b.config(memberConfig("ana", "ana@tf.example", "admin")),
					ResourceName:       "glen_member.ana",
					ImportState:        true,
					ImportStateId:      "ana@tf.example",
					ImportStatePersist: true,
				},
				{
					Config:      b.config(memberConfig("ana", "ana@tf.example", "member")),
					ExpectError: regexp.MustCompile(`last admin`),
				},
				{
					// Left in Glen: destroying the last admin is refused too.
					Config: b.config(`
removed {
  from = glen_member.ana
  lifecycle {
    destroy = false
  }
}
`),
					Check: b.glenHasMember("ana@tf.example", "admin", "active"),
				},
			},
		})
	})
}

func TestMemberNewIsInvited(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.seedMember(t, "ana@tf.example", "admin")
		if b.real {
			// The test Glen has no WorkOS, so it invites nobody.
			b.test(t, resource.TestCase{Steps: []resource.TestStep{{
				Config:      b.config(memberConfig("cy", "cy@tf.example", "member")),
				ExpectError: regexp.MustCompile(`no WorkOS`),
			}}})
			return
		}
		b.test(t, resource.TestCase{
			CheckDestroy: b.memberGone("cy@tf.example"),
			Steps: []resource.TestStep{{
				Config: b.config(memberConfig("cy", "cy@tf.example", "member")),
				Check:  b.glenHasMember("cy@tf.example", "member", "invited"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("glen_member.cy", tfjsonpath.New("state"), knownvalue.StringExact("invited")),
				},
			}},
		})
	})
}

func TestMemberConfigIsCheckedAtPlan(t *testing.T) {
	t.Parallel()
	b := newBackend(startFake(t, "key"), "key", false)
	for _, c := range []struct{ config, want string }{
		{memberConfig("x", "Ana@tf.example", "admin"), `Glen keeps emails lowercase`},
		{memberConfig("x", "ana@tf.example", "owner"), `is not a role`},
	} {
		b.test(t, resource.TestCase{Steps: []resource.TestStep{{Config: b.config(c.config), ExpectError: regexp.MustCompile(c.want)}}})
	}
}
