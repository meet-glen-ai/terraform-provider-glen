package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

const webAddr = "glen_agent.web"

func sandboxConfig(sandbox string) string {
	return fmt.Sprintf(`
resource "glen_agent" "web" {
  name         = "web-reader"
  instructions = "Read the advisories.\n"
  model        = "claude-opus-5-5"
%s}
`, sandbox)
}

func webSpec(sandbox glen.Sandbox) glen.AgentSpec {
	return glen.AgentSpec{Agent: "web-reader", Instructions: "Read the advisories.\n", Harness: "claude-code", Model: "claude-opus-5-5", Sandbox: sandbox}
}

func TestAgentSandboxEgressAndWebSearch(t *testing.T) {
	open := `
  sandbox {
    egress     = ["api.github.com", "*.githubusercontent.com"]
    web_search = true
  }
`
	opened := glen.Sandbox{Egress: []string{"api.github.com", "*.githubusercontent.com"}, WebSearch: true}
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("web-reader"),
			Steps: []resource.TestStep{
				{
					Config: b.config(sandboxConfig(open)),
					Check:  b.glenHas(webSpec(opened), 1),
				},
				{
					ResourceName:                         webAddr,
					ImportState:                          true,
					ImportStateId:                        "web-reader",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					// Someone narrows the list and turns search off in the console.
					PreConfig: func() {
						if _, err := b.api.PutAgent(context.Background(), webSpec(glen.Sandbox{Egress: []string{"api.github.com"}})); err != nil {
							t.Fatal(err)
						}
					},
					Config: b.config(sandboxConfig(open)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(webAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHas(webSpec(opened), 3),
				},
				{
					Config: b.config(sandboxConfig("")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(webAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHas(webSpec(glen.Sandbox{}), 4),
				},
			},
		})
	})
}

func TestAgentEgressGlenRefusesAtPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config:      b.config(sandboxConfig("\n  sandbox {\n    egress = [\"169.254.169.254\"]\n  }\n")),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`sandbox.egress\s+"169.254.169.254"`),
			}},
		})
	})
}

// glenPicksHarness checks that the state holds the harness Glen gave the
// agent, whichever Glen it is.
func (b backend) glenPicksHarness(addr, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		a, err := b.api.GetAgent(context.Background(), name)
		if err != nil {
			return err
		}
		if a.Spec.Harness == "" {
			return fmt.Errorf("glen gave %s no harness", name)
		}
		return resource.TestCheckResourceAttr(addr, "harness", a.Spec.Harness)(s)
	}
}

func coderConfig(model, harness string) string {
	h := ""
	if harness != "" {
		h = fmt.Sprintf("  harness      = %q\n", harness)
	}
	return fmt.Sprintf(`
resource "glen_agent" "coder" {
  name         = "coder"
  instructions = "Fix the bug.\n"
  model        = %q
%s}
`, model, h)
}

func TestAgentHarnessLeftOutIsGlensChoice(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{Config: b.config(coderConfig("gpt-6-luna", "")), Check: b.glenPicksHarness("glen_agent.coder", "coder")},
				{
					Config: b.config(coderConfig("gpt-6-luna", "")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					},
				},
				{
					Config: b.config(coderConfig("claude-opus-5-5", "")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction("glen_agent.coder", plancheck.ResourceActionUpdate),
							plancheck.ExpectUnknownValue("glen_agent.coder", tfjsonpath.New("harness")),
						},
					},
					Check: resource.ComposeTestCheckFunc(
						b.glenPicksHarness("glen_agent.coder", "coder"),
						resource.TestCheckResourceAttr("glen_agent.coder", "harness", "claude-code"),
					),
				},
				{
					ResourceName:                         "glen_agent.coder",
					ImportState:                          true,
					ImportStateId:                        "coder",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
			},
		})
	})
}

// The fake answers as a Glen whose harness follows the model, the rule
// Glen takes on.
func TestAgentOnAnOpenAIModelRunsOnCodexWhereGlenSaysSo(t *testing.T) {
	key := randomKey(t)
	b := newBackend(startFake(t, key), key, false)
	b.test(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: b.config(coderConfig("gpt-6-luna", "")),
				Check:  resource.TestCheckResourceAttr("glen_agent.coder", "harness", "codex"),
			},
			{
				Config: b.config(coderConfig("gpt-6-luna", "codex")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config:      b.config(coderConfig("gpt-6-luna", "claude-code")),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`model gpt-6-luna runs on codex`),
			},
		},
	})
}
