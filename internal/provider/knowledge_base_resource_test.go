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

const secopsAddr = "glen_knowledge_base.secops"

const secopsDescription = "What the security operations agents know about our hosts, alerts and people."

func secopsConfig(guidance string) string {
	return fmt.Sprintf(`
resource "glen_knowledge_base" "secops" {
  name        = "secops"
  description = %q
  guidance    = %q

  curator {
    model          = "claude-opus-5-5"
    tokens_per_day = 2000000
  }
}
`, secopsDescription, guidance)
}

func secopsSpec(guidance string) glen.KnowledgeBaseSpec {
	return glen.KnowledgeBaseSpec{
		Name:        "secops",
		Description: secopsDescription,
		Guidance:    &guidance,
		Curator:     glen.Curator{Model: "claude-opus-5-5", TokensPerDay: 2000000},
	}
}

const triageOnSecops = `
resource "glen_agent" "triage" {
  name         = "alert-triage"
  instructions = "# Alert triage\n"
  model        = "claude-opus-5-5"
  knowledge_base = glen_knowledge_base.secops.name
}
`

func triageOnKnowledgeBase(knowledge_base string) glen.AgentSpec {
	return glen.AgentSpec{
		Agent: "alert-triage", Instructions: "# Alert triage\n", Harness: "claude-code", Model: "claude-opus-5-5", KnowledgeBase: &knowledge_base,
	}
}

func (b backend) glenHasKnowledgeBase(want glen.KnowledgeBaseSpec, version int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetKnowledgeBase(context.Background(), want.Name)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got.Spec, want) {
			return fmt.Errorf("glen has\n%+v\nwant\n%+v", got.Spec, want)
		}
		if got.Version != version {
			return fmt.Errorf("glen has knowledge base version %d, want %d", got.Version, version)
		}
		return nil
	}
}

func (b backend) knowledgeBaseAndTriageGone(*terraform.State) error {
	if _, err := b.api.GetKnowledgeBase(context.Background(), "secops"); !glen.IsNotFound(err) {
		return fmt.Errorf("knowledge base secops: want 404, got %v", err)
	}
	return b.agentGone("alert-triage")(nil)
}

func TestKnowledgeBaseAndItsAgentCreateUpdateImportDestroy(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.knowledgeBaseAndTriageGone,
			Steps: []resource.TestStep{
				{
					Config: b.config(secopsConfig("One page per host group.\n") + triageOnSecops),
					Check: resource.ComposeTestCheckFunc(
						b.glenHasKnowledgeBase(secopsSpec("One page per host group.\n"), 1),
						b.glenHas(triageOnKnowledgeBase("secops"), 1),
					),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(secopsAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
						statecheck.ExpectKnownValue(triageAddr, tfjsonpath.New("knowledge_base"), knownvalue.StringExact("secops")),
					},
				},
				{
					Config: b.config(secopsConfig("One page per host group.\nWrite for the on-call engineer at 3am.\n") + triageOnSecops),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(secopsAddr, plancheck.ResourceActionUpdate),
							plancheck.ExpectResourceAction(triageAddr, plancheck.ResourceActionNoop),
						},
					},
					Check: b.glenHasKnowledgeBase(secopsSpec("One page per host group.\nWrite for the on-call engineer at 3am.\n"), 2),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(secopsAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
					},
				},
				{
					ResourceName:                         secopsAddr,
					ImportState:                          true,
					ImportStateId:                        "secops",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
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

func TestKnowledgeBaseDriftShowsInPlan(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(secopsConfig("One page per host group.\n"))
		b.test(t, resource.TestCase{
			CheckDestroy: b.knowledgeBaseAndTriageGone,
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig: func() {
						if _, err := b.api.PutKnowledgeBase(context.Background(), secopsSpec("Someone else's guidance.\n")); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(secopsAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasKnowledgeBase(secopsSpec("One page per host group.\n"), 3),
				},
				{
					PreConfig: func() {
						if err := b.api.DeleteKnowledgeBase(context.Background(), "secops"); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(secopsAddr, plancheck.ResourceActionCreate)},
					},
					Check: b.glenHasKnowledgeBase(secopsSpec("One page per host group.\n"), 5),
				},
			},
		})
	})
}

func TestKnowledgeBaseWithoutGuidanceImportsAsNull(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				PreConfig: func() {
					spec := secopsSpec("")
					spec.Guidance = nil
					if _, err := b.api.PutKnowledgeBase(context.Background(), spec); err != nil {
						t.Fatal(err)
					}
				},
				Config: b.config(fmt.Sprintf(`
resource "glen_knowledge_base" "secops" {
  name        = "secops"
  description = %q
  curator {
    model          = "claude-opus-5-5"
    tokens_per_day = 2000000
  }
}
`, secopsDescription)),
				ResourceName:       secopsAddr,
				ImportState:        true,
				ImportStateId:      "secops",
				ImportStatePersist: true,
			}, {
				Config: b.config(fmt.Sprintf(`
resource "glen_knowledge_base" "secops" {
  name        = "secops"
  description = %q
  curator {
    model          = "claude-opus-5-5"
    tokens_per_day = 2000000
  }
}
`, secopsDescription)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			}},
		})
	})
}

func TestKnowledgeBaseGlenRefusesAtPlan(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_knowledge_base" "broke" {
  name        = "broke"
  description = "x"
  curator {
    model          = "claude-opus-5-5"
    tokens_per_day = 0
  }
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`curator.tokens_per_day must be at least 1`),
			}},
		})
	})
}

func TestKnowledgeBaseNeedsACuratorBlock(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_knowledge_base" "bare" {
  name        = "bare"
  description = "x"
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`needs a curator block`),
			}},
		})
	})
}

func TestAgentNamingAMissingKnowledgeBasePlansThenFailsAtApply(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(`
resource "glen_agent" "lost" {
  name         = "lost-agent"
  instructions = "x"
  model        = "claude-opus-5-5"
  knowledge_base = "nowhere"
}
`)
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("lost-agent"),
			Steps: []resource.TestStep{
				{Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
				{Config: config, ExpectError: regexp.MustCompile(`agent\s+lost-agent\s+names\s+knowledge\s+base\s+nowhere,\s+which\s+does\s+not\s+exist`)},
			},
		})
	})
}

func TestAgentKnowledgeBaseMustBeAKnowledgeBaseNameAtPlan(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			Steps: []resource.TestStep{{
				Config: b.config(`
resource "glen_agent" "odd" {
  name         = "odd-knowledge-base"
  instructions = "x"
  model        = "claude-opus-5-5"
  knowledge_base = "Sec Ops"
}
`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`knowledge\s+base\s+"Sec\s+Ops"\s+must\s+be\s+a\s+kebab-case\s+knowledge\s+base\s+name`),
			}},
		})
	})
}

func TestKnowledgeBaseDeleteWhileAnAgentNamesItShowsGlensMessage(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.knowledgeBaseAndTriageGone,
			Steps: []resource.TestStep{
				{Config: b.config(secopsConfig("x"))},
				{
					PreConfig: func() {
						if _, err := b.api.PutAgent(context.Background(), triageOnKnowledgeBase("secops")); err != nil {
							t.Fatal(err)
						}
					},
					Config:      b.config(""),
					ExpectError: regexp.MustCompile(`knowledge\s+base\s+secops\s+is\s+the\s+knowledge\s+base\s+of\s+alert-triage;\s+change\s+or\s+delete\s+those\s+agents\s+first`),
				},
				{
					PreConfig: func() {
						if err := b.api.DeleteAgent(context.Background(), "alert-triage"); err != nil {
							t.Fatal(err)
						}
					},
					Config: b.config(""),
				},
			},
		})
	})
}
