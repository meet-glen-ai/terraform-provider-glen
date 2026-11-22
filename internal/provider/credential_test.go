package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// nothingHolds checks that no attribute of any resource in state holds
// any of values: a write-only value never reaches state.
func nothingHolds(values ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for addr, r := range s.RootModule().Resources {
			for k, v := range r.Primary.Attributes {
				for _, value := range values {
					if strings.Contains(v, value) {
						return fmt.Errorf("%s.%s holds a write-only value", addr, k)
					}
				}
			}
		}
		return nil
	}
}

// credentialIs checks Glen shows tool's credential set, ending in last4.
func (b backend) credentialIs(tool, last4 string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := b.api.GetToolCredential(context.Background(), tool)
		if err != nil {
			return err
		}
		if !got.Set || got.Last4 == nil || *got.Last4 != last4 {
			return fmt.Errorf("glen shows %s's credential %+v, want set ending %s", tool, got, last4)
		}
		return nil
	}
}

// secretsOf is what the backend's vault holds whose name ends with
// suffix: the fake's own values, or the real Glen's fake Secrets Manager.
func (b backend) secretsOf(suffix string) map[string]string {
	if !b.real {
		v, _ := fakes.Load(b.url)
		f := v.(*fakeGlen)
		f.mu.Lock()
		defer f.mu.Unlock()
		out := map[string]string{}
		for name, value := range f.credentials {
			if strings.HasSuffix("tool/"+name+"/credential", suffix) {
				out[name] = value
			}
		}
		for p, value := range f.modelKeys {
			if strings.HasSuffix("model-key/"+p, suffix) {
				out[p] = value
			}
		}
		return out
	}
	v, _ := vaults.Load(b.url)
	return v.(*fakeVault).holding(suffix)
}

func datadogConfig(value string, version int) string {
	return fmt.Sprintf(`
resource "glen_tool" "datadog" {
  name = "datadog"
  url  = "https://mcp.datadoghq.com/mcp"
  credential {
    kind   = "api-key"
    header = "DD-API-KEY"
  }
  credential_wo         = %q
  credential_wo_version = %d
}
`, value, version)
}

func TestToolCredentialIsWrittenToGlenAndNeverToState(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		const first, second = "dd-first-secret-0001", "dd-second-secret-0002"
		datadog := glen.ToolSpec{Name: "datadog", URL: str("https://mcp.datadoghq.com/mcp"), Credential: &glen.ToolCredential{Kind: "api-key", Header: str("DD-API-KEY")}}
		b.test(t, resource.TestCase{
			CheckDestroy: resource.ComposeTestCheckFunc(b.toolsGone("datadog"), func(*terraform.State) error {
				if held := b.secretsOf("tool/datadog/credential"); len(held) > 0 {
					return fmt.Errorf("the vault still holds the tool's credential: %v", len(held))
				}
				return nil
			}),
			Steps: []resource.TestStep{
				{
					Config: b.config(datadogConfig(first, 1)),
					Check:  resource.ComposeTestCheckFunc(b.glenHasTool(datadog, 1), b.credentialIs("datadog", "0001"), nothingHolds(first), resource.TestCheckNoResourceAttr("glen_tool.datadog", "credential_wo")),
				},
				{
					// A new value at the same version is not compared, so not written.
					Config:           b.config(datadogConfig(second, 1)),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
					Check:            b.credentialIs("datadog", "0001"),
				},
				{
					Config: b.config(datadogConfig(second, 2)),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("glen_tool.datadog", plancheck.ResourceActionUpdate)},
					},
					Check: resource.ComposeTestCheckFunc(b.glenHasTool(datadog, 1), b.credentialIs("datadog", "0002"), nothingHolds(first, second)),
				},
				{
					// Without the block, the record names no credential, and Glen drops the value.
					Config: b.config(`
resource "glen_tool" "datadog" {
  name = "datadog"
  url  = "https://mcp.datadoghq.com/mcp"
}
`),
					Check: resource.ComposeTestCheckFunc(b.glenHasTool(glen.ToolSpec{Name: "datadog", URL: str("https://mcp.datadoghq.com/mcp")}, 2), func(*terraform.State) error {
						if held := b.secretsOf("tool/datadog/credential"); len(held) > 0 {
							return fmt.Errorf("the vault still holds a credential the record does not name")
						}
						return nil
					}),
				},
			},
		})
	})
}

func datadogAt(url, value string, version int) string {
	return strings.Replace(datadogConfig(value, version), "https://mcp.datadoghq.com/mcp", url, 1)
}

func TestToolCredentialMovesOnlyWithANewValue(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		const first, second = "dd-first-secret-0001", "dd-second-secret-0002"
		const there = "https://mcp.datadoghq.eu/mcp"
		b.test(t, resource.TestCase{
			CheckDestroy: b.toolsGone("datadog"),
			Steps: []resource.TestStep{
				{Config: b.config(datadogAt("https://mcp.datadoghq.com/mcp", first, 1)), Check: b.credentialIs("datadog", "0001")},
				{
					// Glen sends a value only where it was set for.
					Config:      b.config(datadogAt(there, first, 1)),
					ExpectError: saying("remove the credential, change the tool, then set the credential again"),
				},
				{
					// With a new value, the provider removes the old one, moves the tool and sets it.
					Config: b.config(datadogAt(there, second, 2)),
					Check: resource.ComposeTestCheckFunc(
						b.glenHasTool(glen.ToolSpec{Name: "datadog", URL: str(there), Credential: &glen.ToolCredential{Kind: "api-key", Header: str("DD-API-KEY")}}, 2),
						b.credentialIs("datadog", "0002"), nothingHolds(first, second)),
				},
			},
		})
	})
}

func TestToolCredentialIsRefusedWhereGlenWouldRefuseIt(t *testing.T) {
	b := newBackend(startFake(t, "k"), "k", false)
	for _, c := range []struct{ config, msg string }{
		{`credential_wo = "dd-secret-0001"`, "add a credential block naming its kind"},
		{"credential {\n  kind = \"oauth\"\n}", `credential.kind "oauth" is not one Glen has`},
		{"credential {\n  kind      = \"api-key\"\n  client_id = \"glen\"\n}", "client_id is a service credential's"},
		{"credential {\n  kind   = \"service\"\n  header = \"DD-API-KEY\"\n}", "leave header out"},
		{"credential {\n  kind = \"service\"\n}", "A service credential needs token_url"},
	} {
		b.test(t, resource.TestCase{Steps: []resource.TestStep{{
			Config:      b.config(fmt.Sprintf("resource \"glen_tool\" \"x\" {\n  name = \"x\"\n  url  = \"https://x.example/mcp\"\n  %s\n}\n", c.config)),
			PlanOnly:    true,
			ExpectError: saying(c.msg),
		}}})
	}
}

func modelKeyConfig(key string, version int) string {
	return fmt.Sprintf(`
resource "glen_model_key" "anthropic" {
  model_provider = "anthropic"
  key_wo         = %q
  key_wo_version = %d
}
`, key, version)
}

func TestModelKeyIsWrittenToGlenRotatedByVersionAndRemovedOnDestroy(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		const first, second = "sk-ant-tf-first-0001", "sk-ant-tf-second-0002"
		keySet := func(last4 string) resource.TestCheckFunc {
			return func(*terraform.State) error {
				keys, err := b.api.ModelKeys(context.Background())
				if err != nil {
					return err
				}
				if k := keys["anthropic"]; !k.Set || k.Last4 == nil || *k.Last4 != last4 {
					return fmt.Errorf("glen shows the anthropic key %+v, want set ending %s", k, last4)
				}
				return nil
			}
		}
		b.test(t, resource.TestCase{
			CheckDestroy: func(*terraform.State) error {
				keys, err := b.api.ModelKeys(context.Background())
				if err != nil {
					return err
				}
				if keys["anthropic"].Set {
					return fmt.Errorf("the anthropic key is still set")
				}
				return nil
			},
			Steps: []resource.TestStep{
				{
					Config: b.config(modelKeyConfig(first, 1)),
					Check: resource.ComposeTestCheckFunc(keySet("0001"), nothingHolds(first),
						resource.TestCheckResourceAttr("glen_model_key.anthropic", "last4", "0001"), resource.TestCheckNoResourceAttr("glen_model_key.anthropic", "key_wo")),
				},
				{
					Config:           b.config(modelKeyConfig(second, 1)),
					ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				},
				{
					Config: b.config(modelKeyConfig(second, 2)),
					Check:  resource.ComposeTestCheckFunc(keySet("0002"), nothingHolds(first, second), resource.TestCheckResourceAttr("glen_model_key.anthropic", "last4", "0002")),
				},
			},
		})
	})
}

func TestModelKeyNamesAProviderGlenHas(t *testing.T) {
	b := newBackend(startFake(t, "k"), "k", false)
	b.test(t, resource.TestCase{Steps: []resource.TestStep{{
		Config:      b.config("resource \"glen_model_key\" \"x\" {\n  model_provider = \"gemini\"\n  key_wo = \"k-0000-0000\"\n}\n"),
		PlanOnly:    true,
		ExpectError: saying(`provider "gemini" is not one Glen has a key for`),
	}}})
}
