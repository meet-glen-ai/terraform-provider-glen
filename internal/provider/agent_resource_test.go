package provider

import (
	"context"
	"fmt"
	"reflect"
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

const triageAddr = "glen_agent.triage"

func triageConfig(model, instructions string) string {
	return fmt.Sprintf(`
resource "glen_agent" "triage" {
  name         = "alert-triage"
  instructions = %q
  model        = %q

  trigger {
    schedule = "0 */6 * * *"
  }
  trigger {
    feed = "cisa-kev"
  }
  trigger {
    webhook = "github:pull_request"
  }

  tools {
    env = ["GITHUB_TOKEN"]
  }

  sandbox {
    image = "glen-sandbox:1"
  }

  budget {
    tokens_per_session = 100000
    sessions_per_day   = 200
    max_turns          = 30
  }
}
`, instructions, model)
}

func ptr[T any](v T) *T { return &v }

func triageSpec(model, instructions string) glen.AgentSpec {
	return glen.AgentSpec{
		Agent:        "alert-triage",
		Instructions: instructions,
		Harness:      "claude-code",
		Model:        model,
		Trigger: []glen.Trigger{
			{Schedule: ptr("0 */6 * * *")},
			{Feed: ptr("cisa-kev")},
			{Webhook: ptr("github:pull_request")},
		},
		Tools:   glen.Tools{Env: []string{"GITHUB_TOKEN"}},
		Sandbox: glen.Sandbox{Image: ptr("glen-sandbox:1")},
		Budget:  glen.Budget{TokensPerSession: ptr(int64(100000)), SessionsPerDay: ptr(int64(200)), MaxTurns: ptr(int64(30))},
	}
}

func (b backend) glenHas(want glen.AgentSpec, version int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetAgent(context.Background(), want.Agent)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got.Spec, want.Normalized()) {
			return fmt.Errorf("glen has\n%+v\nwant\n%+v", got.Spec, want.Normalized())
		}
		if got.Version != version {
			return fmt.Errorf("glen has version %d, want %d", got.Version, version)
		}
		return nil
	}
}

func (b backend) agentGone(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if _, err := b.api.GetAgent(context.Background(), name); !glen.IsNotFound(err) {
			return fmt.Errorf("agent %s: want 404, got %v", name, err)
		}
		return nil
	}
}

func TestAgentCreateUpdateImportDelete(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("alert-triage"),
			Steps: []resource.TestStep{
				{
					Config: b.config(triageConfig("claude-opus-5-5", "# Alert triage\n")),
					Check:  b.glenHas(triageSpec("claude-opus-5-5", "# Alert triage\n"), 1),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(triageAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					},
				},
				{
					Config: b.config(triageConfig("claude-sonnet-5", "# Alert triage\nPage only on real alerts.\n")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(triageAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHas(triageSpec("claude-sonnet-5", "# Alert triage\nPage only on real alerts.\n"), 2),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(triageAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
					},
				},
				{
					ResourceName:                         triageAddr,
					ImportState:                          true,
					ImportStateId:                        "alert-triage",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
			},
		})
	})
}

func TestAgentLeftOutSettingsSendGlensDefaults(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "min" {
  name         = "minimal"
  instructions = "Do the thing.\n"
  model        = "claude-opus-5-5"
}
`),
				Check: b.glenHas(glen.AgentSpec{
					Agent: "minimal", Instructions: "Do the thing.\n", Harness: "claude-code", Model: "claude-opus-5-5",
				}, 1),
			}, {
				ResourceName:                         "glen_agent.min",
				ImportState:                          true,
				ImportStateId:                        "minimal",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			}},
		})
	})
}

func TestAgentEmptyListsMatchLeftOutLists(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "empty" {
  name         = "empty-lists"
  instructions = "Do the thing.\n"
  model        = "claude-opus-5-5"
  tools {
    env = []
  }
}
`),
				Check: b.glenHas(glen.AgentSpec{
					Agent: "empty-lists", Instructions: "Do the thing.\n", Harness: "claude-code", Model: "claude-opus-5-5",
				}, 1),
			}},
		})
	})
}

func TestAgentDriftShowsInPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(triageConfig("claude-opus-5-5", "# Alert triage\n"))
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig: func() {
						if _, err := b.api.PutAgent(context.Background(), triageSpec("someone-elses-model", "# Alert triage\n")); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(triageAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHas(triageSpec("claude-opus-5-5", "# Alert triage\n"), 3),
				},
				{
					PreConfig: func() {
						if err := b.api.DeleteAgent(context.Background(), "alert-triage"); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(triageAddr, plancheck.ResourceActionCreate)},
					},
					Check: b.glenHas(triageSpec("claude-opus-5-5", "# Alert triage\n"), 5),
				},
			},
		})
	})
}

func TestAgentGlenRefusesAtPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("bad-cron"),
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "bad" {
  name         = "bad-cron"
  instructions = "x"
  model        = "claude-opus-5-5"
  trigger {
    schedule = "every six hours"
  }
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`schedule "every six hours" must have five cron\s+fields`),
			}},
		})
	})
}

func TestAgentTriggerSetsExactlyOneKind(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "two" {
  name         = "two-kinds"
  instructions = "x"
  model        = "claude-opus-5-5"
  trigger {
    feed    = "cisa-kev"
    webhook = "github:pull_request"
  }
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`sets exactly one of schedule, feed and webhook; this one sets\s+2`),
			}},
		})
	})
}

func TestAgentCreateRefusesAnExistingAgent(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				PreConfig: func() {
					if _, err := b.api.PutAgent(context.Background(), triageSpec("claude-opus-5-5", "# Alert triage\n")); err != nil {
						t.Fatal(err)
					}
				},
				Config:      b.config(triageConfig("claude-opus-5-5", "# Alert triage\n")),
				ExpectError: regexp.MustCompile(`already has an agent named "alert-triage"`),
			}},
		})
	})
}

func TestAgentImportOfAnAgentMadeElsewhere(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				PreConfig: func() {
					if _, err := b.api.PutAgent(context.Background(), triageSpec("claude-opus-5-5", "# Alert triage\n")); err != nil {
						t.Fatal(err)
					}
				},
				Config:             b.config(triageConfig("claude-opus-5-5", "# Alert triage\n")),
				ResourceName:       triageAddr,
				ImportState:        true,
				ImportStateId:      "alert-triage",
				ImportStatePersist: true,
				ImportStateCheck: func(s []*terraform.InstanceState) error {
					if len(s) != 1 || s[0].Attributes["model"] != "claude-opus-5-5" || s[0].Attributes["trigger.#"] != "3" ||
						s[0].Attributes["budget.sessions_per_day"] != "200" || s[0].Attributes["version"] != "1" {
						return fmt.Errorf("imported %+v", s)
					}
					return nil
				},
			}, {
				Config: b.config(triageConfig("claude-opus-5-5", "# Alert triage\n")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			}},
		})
	})
}

func TestAgentToolsEnvOutsideGlensAllowlistFailsAtPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("env-outside"),
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "env" {
  name         = "env-outside"
  instructions = "x"
  model        = "claude-opus-5-5"
  tools {
    env = ["GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY"]
  }
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`tools.env\s+names\s+AWS_SECRET_ACCESS_KEY,\s+which\s+this\s+deployment\s+does\s+not\s+allow`),
			}},
		})
	})
}
