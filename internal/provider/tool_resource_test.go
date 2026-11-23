package provider

import (
	"context"
	"encoding/json"
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

const ticketsAddr = "glen_tool.tickets"

func ticketsConfig(url string) string {
	return fmt.Sprintf(`
resource "glen_tool" "tickets" {
  name = "tickets"
  url  = %q
}
`, url)
}

func str(s string) *string { return &s }

// saying matches Glen's message however Terraform wraps it.
func saying(msg string) *regexp.Regexp {
	return regexp.MustCompile(strings.Join(strings.Fields(regexp.QuoteMeta(msg)), `\s+`))
}

// needsSignIn skips the glen subtest: the glen serve the tests start has
// no [identity], so Glen refuses connect there.
func needsSignIn(t *testing.T, b backend) {
	if b.real {
		t.Skip("connect needs [identity], which the test's glen serve does not have")
	}
}

func (b backend) glenHasTool(want glen.ToolSpec, version int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetTool(context.Background(), want.Name)
		if err != nil {
			return err
		}
		if !got.Spec.Equal(want) {
			g, w := got.Spec.Normalized(), want.Normalized()
			return fmt.Errorf("glen has\n%s\nwant\n%s", jsonOf(g), jsonOf(w))
		}
		if got.Version != version {
			return fmt.Errorf("glen has tool version %d, want %d", got.Version, version)
		}
		return nil
	}
}

func (b backend) toolsGone(names ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, name := range names {
			if _, err := b.api.GetTool(context.Background(), name); !glen.IsNotFound(err) {
				return fmt.Errorf("tool %s: want 404, got %v", name, err)
			}
		}
		return nil
	}
}

func jsonOf(s glen.ToolSpec) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestToolURLCreateUpdateImportDestroy(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("tickets"),
			Steps: []resource.TestStep{
				{
					Config: b.config(ticketsConfig("https://tickets.example/mcp")),
					Check:  b.glenHasTool(glen.ToolSpec{Name: "tickets", URL: str("https://tickets.example/mcp")}, 1),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(ticketsAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					},
				},
				{
					Config:           b.config(ticketsConfig("https://tickets.example/mcp")),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				},
				{
					Config: b.config(ticketsConfig("https://tickets.example/v2/mcp")),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(ticketsAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasTool(glen.ToolSpec{Name: "tickets", URL: str("https://tickets.example/v2/mcp")}, 2),
				},
				{
					ResourceName:                         ticketsAddr,
					ImportState:                          true,
					ImportStateId:                        "tickets",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
			},
		})
	})
}

func TestToolSampleSwitchesToURL(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("meetings"),
			Steps: []resource.TestStep{
				{
					Config: b.config(`
resource "glen_tool" "meetings" {
  name   = "meetings"
  sample = "meetings"
}
`),
					Check: b.glenHasTool(glen.ToolSpec{Name: "meetings", Sample: str("meetings")}, 1),
				},
				{
					ResourceName:                         "glen_tool.meetings",
					ImportState:                          true,
					ImportStateId:                        "meetings",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					Config: b.config(`
resource "glen_tool" "meetings" {
  name = "meetings"
  url  = "https://meetings.example/mcp"
}
`),
					Check: b.glenHasTool(glen.ToolSpec{Name: "meetings", URL: str("https://meetings.example/mcp")}, 2),
				},
			},
		})
	})
}

func TestToolDriftShowsInPlan(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(ticketsConfig("https://tickets.example/mcp"))
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("tickets"),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig: func() {
						if _, err := b.api.PutTool(context.Background(), glen.ToolSpec{Name: "tickets", URL: str("https://elsewhere.example/mcp")}); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(ticketsAddr, plancheck.ResourceActionUpdate)},
					},
					Check: b.glenHasTool(glen.ToolSpec{Name: "tickets", URL: str("https://tickets.example/mcp")}, 3),
				},
				{
					PreConfig: func() {
						if err := b.api.DeleteTool(context.Background(), "tickets"); err != nil {
							t.Fatal(err)
						}
					},
					Config: config,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(ticketsAddr, plancheck.ResourceActionCreate)},
					},
					Check: b.glenHasTool(glen.ToolSpec{Name: "tickets", URL: str("https://tickets.example/mcp")}, 5),
				},
			},
		})
	})
}

const githubTool = `
resource "glen_tool" "github" {
  name = "github"

  cli {
    hosts = [
      { host = "api.github.com" },
      { host = "github.com", auth = "basic", user = "x-access-token" },
    ]
    allow = ["*.githubusercontent.com"]
    env   = { GH_TOKEN = "glen-proxy-injected" }
  }

  connect {
    provider = "github-oauth"
    scopes   = ["repo", "read:org"]
  }
}
`

func githubSpec() glen.ToolSpec {
	return glen.ToolSpec{
		Name: "github",
		CLI: &glen.ToolCLI{
			Hosts: []glen.ToolHost{{Host: "api.github.com"}, {Host: "github.com", BasicUser: str("x-access-token")}},
			Allow: []string{"*.githubusercontent.com"},
			Env:   map[string]string{"GH_TOKEN": "glen-proxy-injected"},
		},
		Connect: &glen.ToolConnect{Provider: str("github-oauth"), Scopes: []string{"repo", "read:org"}},
	}
}

func TestToolCLIRoundTrip(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		needsSignIn(t, b)
		bearerOnly := strings.Replace(githubTool, `{ host = "github.com", auth = "basic", user = "x-access-token" },`, `{ host = "github.com", auth = "bearer" },`, 1)
		bearer := githubSpec()
		bearer.CLI.Hosts[1].BasicUser = nil
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("github"),
			Steps: []resource.TestStep{
				{
					Config: b.config(githubTool),
					Check:  b.glenHasTool(githubSpec(), 1),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("glen_tool.github", tfjsonpath.New("cli").AtMapKey("hosts").AtSliceIndex(1).AtMapKey("user"), knownvalue.StringExact("x-access-token")),
					},
				},
				{
					Config:           b.config(githubTool),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				},
				{
					ResourceName:                         "glen_tool.github",
					ImportState:                          true,
					ImportStateId:                        "github",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					Config: b.config(bearerOnly),
					Check:  b.glenHasTool(bearer, 2),
				},
				{
					Config:           b.config(bearerOnly),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				},
			},
		})
	})
}

func TestToolSignInGlenSetsUpHasNoDiff(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		needsSignIn(t, b)
		notion := func(url string) string {
			return b.config(fmt.Sprintf(`
resource "glen_tool" "notion" {
  name = "notion"
  url  = %q
  connect {}
}
`, url))
		}
		config := notion("https://mcp.notion.example/mcp")
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("notion"),
			Steps: []resource.TestStep{
				{
					Config: config,
					Check:  b.glenHasTool(glen.ToolSpec{Name: "notion", URL: str("https://mcp.notion.example/mcp"), Connect: &glen.ToolConnect{}}, 1),
				},
				{
					Config:           config,
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				},
				{
					ResourceName:                         "glen_tool.notion",
					ImportState:                          true,
					ImportStateId:                        "notion",
					ImportStateVerify:                    true,
					ImportStateVerifyIdentifierAttribute: "name",
				},
				{
					Config:      notion("https://mcp.notion.example/v2/mcp"),
					ExpectError: saying("people sign in to notion, so its address and provider cannot change under their connections; delete the tool and add it again"),
				},
			},
		})
	})
}

const ticketsAgent = `
resource "glen_agent" "triage" {
  name         = "alert-triage"
  instructions = "# Alert triage\n"
  model        = "claude-opus-5-5"

  tools {
    mcp = [{ server = glen_tool.tickets.name }]
  }
}
`

func triageWithTools(uses ...glen.McpUse) glen.AgentSpec {
	return glen.AgentSpec{Agent: "alert-triage", Instructions: "# Alert triage\n", Harness: "claude-code", Model: "claude-opus-5-5", Tools: glen.Tools{MCP: uses}}
}

func TestAgentUsesAToolMadeInTheSameApply(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(ticketsConfig("https://tickets.example/mcp") + ticketsAgent)
		b.test(t, resource.TestCase{
			CheckDestroy: resource.ComposeTestCheckFunc(b.agentGone("alert-triage"), b.toolsGone("tickets")),
			Steps: []resource.TestStep{
				{
					Config: config,
					Check: resource.ComposeTestCheckFunc(
						b.glenHasTool(glen.ToolSpec{Name: "tickets", URL: str("https://tickets.example/mcp")}, 1),
						b.glenHas(triageWithTools(glen.McpUse{Server: "tickets"}), 1),
					),
				},
				{
					Config:           config,
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
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

func TestAgentUsesAToolAsUser(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		needsSignIn(t, b)
		config := b.config(githubTool + `
resource "glen_agent" "triage" {
  name         = "alert-triage"
  instructions = "# Alert triage\n"
  model        = "claude-opus-5-5"

  tools {
    mcp = [{ server = glen_tool.github.name, as = "user" }]
  }
}
`)
		want := triageWithTools(glen.McpUse{Server: "github", As: "user"})
		b.test(t, resource.TestCase{
			CheckDestroy: resource.ComposeTestCheckFunc(b.agentGone("alert-triage"), b.toolsGone("github")),
			Steps: []resource.TestStep{
				{Config: config, Check: b.glenHas(want, 1)},
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

func TestAgentUsingASignInToolAsServiceFailsAtPlan(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		needsSignIn(t, b)
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("github"),
			Steps: []resource.TestStep{
				{Config: b.config(githubTool)},
				{
					Config: b.config(githubTool + `
resource "glen_agent" "triage" {
  name         = "alert-triage"
  instructions = "x"
  model        = "claude-opus-5-5"
  tools {
    mcp = [{ server = glen_tool.github.name }]
  }
}
`),
					PlanOnly:    true,
					ExpectError: saying("tools.mcp uses github as service, but people sign in to github"),
				},
			},
		})
	})
}

func TestAgentNamingAMissingToolFailsAtApply(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		config := b.config(`
resource "glen_agent" "lost" {
  name         = "lost-agent"
  instructions = "x"
  model        = "claude-opus-5-5"
  tools {
    mcp = [{ server = "nowhere" }]
  }
}
`)
		b.test(t, resource.TestCase{
			CheckDestroy: b.agentGone("lost-agent"),
			Steps: []resource.TestStep{
				{Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
				{Config: config, ExpectError: saying("tools.mcp names nowhere, which is not a tool of this org")},
			},
		})
	})
}

func TestToolDeleteWhileAnAgentUsesItShowsGlensMessage(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: resource.ComposeTestCheckFunc(b.agentGone("alert-triage"), b.toolsGone("tickets")),
			Steps: []resource.TestStep{
				{Config: b.config(ticketsConfig("https://tickets.example/mcp"))},
				{
					PreConfig: func() {
						if _, err := b.api.PutAgent(context.Background(), triageWithTools(glen.McpUse{Server: "tickets"})); err != nil {
							t.Fatal(err)
						}
					},
					Config:      b.config(""),
					ExpectError: saying("MCP server tickets is used by alert-triage; change or delete those agents first"),
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

func TestToolBadSpecShowsGlensMessageAtApply(t *testing.T) {
	t.Parallel()
	eachBackend(t, func(t *testing.T, b backend) {
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("tickets"),
			Steps: []resource.TestStep{{
				Config:      b.config(ticketsConfig("ftp://tickets.example/mcp")),
				ExpectError: regexp.MustCompile(saying(`Glen refused tool tickets`).String() + `[\s\S]*` + saying(`url "ftp://tickets.example/mcp" must be an http or https URL`).String()),
			}},
		})
	})
}

func TestToolShapeIsCheckedAtPlan(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ body, says string }{
		"url and sample": {`url = "https://a.example/mcp"
  sample = "meetings"`, "sets exactly one of url, sample and a cli block; this one sets 2"},
		"none":                {``, "sets exactly one of url, sample and a cli block; this one sets 0"},
		"glen":                {`url = "https://a.example/mcp"`, ""},
		"sample with connect": {"sample = \"meetings\"\n  connect {}", "A sample needs no sign-in"},
		"cli without connect": {`cli {
    hosts = [{ host = "api.github.com" }]
  }`, "A cli tool needs connect with a provider"},
		"cli without provider": {`cli {
    hosts = [{ host = "api.github.com" }]
  }
  connect {}`, "name an Identity OAuth2 provider in connect.provider"},
		"wildcard host": {`cli {
    hosts = [{ host = "*.github.com" }]
  }
  connect { provider = "p" }`, `cli.hosts "*.github.com" must be one hostname`},
		"basic without user": {`cli {
    hosts = [{ host = "github.com", auth = "basic" }]
  }
  connect { provider = "p" }`, `auth = "basic" needs a user`},
		"unknown auth": {`cli {
    hosts = [{ host = "github.com", auth = "token" }]
  }
  connect { provider = "p" }`, `auth "token" is not one Glen has`},
		"no hosts": {`cli {}
  connect { provider = "p" }`, "cli.hosts is empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tool, says := "odd", c.says
			if name == "glen" {
				tool, says = "glen", `Tool name "glen" must be kebab-case and not glen.`
			}
			b := newBackend(startFake(t, "key"), "key", false)
			b.test(t, resource.TestCase{
				Steps: []resource.TestStep{{
					Config:      b.config(fmt.Sprintf("resource \"glen_tool\" \"odd\" {\n  name = %q\n  %s\n}\n", tool, c.body)),
					PlanOnly:    true,
					ExpectError: saying(says),
				}},
			})
		})
	}
}
