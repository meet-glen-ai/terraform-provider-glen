package provider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// testdata/spec-fields.json is every field path of the specs the admin
// API takes, as Glen's own tests write them from its spec types: `.`
// between fields, `[]` for a list's items, `*` for a map's keys. Glen's
// CI runs this test with GLEN_SPEC_FIELDS naming the file of the Glen it
// tests, so a field added to a spec fails there until the provider
// carries it.
//
// A spec path is carried when its resource's schema has the same path
// with the `[]` taken out, or the path renamed names an attribute.
var specFieldRenames = map[string]map[string]string{
	"agent": {
		"agent":                  "name",
		"tools.mcp[].policies.*": "tools.mcp.policies",
	},
	"tool": {
		"cli.hosts[].auth.basic":       "cli.hosts.user",
		"cli.hosts[].auth.aws.service": "cli.hosts.aws_service",
		"cli.hosts[].auth.aws.region":  "cli.hosts.aws_region",
		"cli.env.*":                    "cli.env",
	},
}

// specFieldsNotTerraforms are the spec fields no resource carries, each
// with why: a person's own act, or a value Glen computes. None yet.
var specFieldsNotTerraforms = map[string]map[string]string{}

var specFieldResources = map[string]func() resource.Resource{
	"agent":          newAgentResource,
	"feed":           newFeedResource,
	"knowledge_base": newKnowledgeBaseResource,
	"tool":           newToolResource,
}

func TestEverySpecFieldIsCarried(t *testing.T) {
	t.Parallel()
	file := "testdata/spec-fields.json"
	if env := os.Getenv("GLEN_SPEC_FIELDS"); env != "" {
		file = env
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string][]string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for kind := range fields {
		if specFieldResources[kind] == nil {
			t.Errorf("%s has the %s spec, which no resource here manages", file, kind)
		}
	}
	for kind, newResource := range specFieldResources {
		paths := fields[kind]
		if len(paths) == 0 {
			t.Errorf("%s has no %s fields", file, kind)
			continue
		}
		attrs := schemaPaths(t, newResource())
		seen := map[string]bool{}
		for _, p := range paths {
			seen[p] = true
			if why, ok := specFieldsNotTerraforms[kind][p]; ok {
				t.Logf("%s %s is not Terraform's: %s", kind, p, why)
				continue
			}
			attr, renamed := specFieldRenames[kind][p]
			if !renamed {
				attr = strings.ReplaceAll(p, "[]", "")
			}
			if !attrs[attr] {
				t.Errorf("the %s spec has %s, which the provider's schema does not carry (as %s). Add it to the resource, or to specFieldsNotTerraforms with why.", kind, p, attr)
			}
		}
		for _, list := range []map[string]map[string]string{specFieldRenames, specFieldsNotTerraforms} {
			for p := range list[kind] {
				if !seen[p] {
					t.Errorf("%s %s is listed here but is not a field of the spec; take it out", kind, p)
				}
			}
		}
	}
}

// schemaPaths is every attribute and block of r's schema, nested ones
// as dotted paths.
func schemaPaths(t *testing.T, r resource.Resource) map[string]bool {
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	out := map[string]bool{}
	addSchema("", resp.Schema.Attributes, resp.Schema.Blocks, out)
	return out
}

func addSchema(prefix string, attrs map[string]schema.Attribute, blocks map[string]schema.Block, out map[string]bool) {
	for name, a := range attrs {
		p := prefix + name
		out[p] = true
		switch a := a.(type) {
		case schema.ListNestedAttribute:
			addSchema(p+".", a.NestedObject.Attributes, nil, out)
		case schema.SingleNestedAttribute:
			addSchema(p+".", a.Attributes, nil, out)
		case schema.SetNestedAttribute:
			addSchema(p+".", a.NestedObject.Attributes, nil, out)
		case schema.MapNestedAttribute:
			addSchema(p+".", a.NestedObject.Attributes, nil, out)
		}
	}
	for name, b := range blocks {
		p := prefix + name
		out[p] = true
		switch b := b.(type) {
		case schema.SingleNestedBlock:
			addSchema(p+".", b.Attributes, b.Blocks, out)
		case schema.ListNestedBlock:
			addSchema(p+".", b.NestedObject.Attributes, b.NestedObject.Blocks, out)
		case schema.SetNestedBlock:
			addSchema(p+".", b.NestedObject.Attributes, b.NestedObject.Blocks, out)
		}
	}
}
