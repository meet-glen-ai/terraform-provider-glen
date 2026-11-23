package provider

import (
	"context"
	"errors"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type glenProvider struct {
	version string
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &glenProvider{version: version} }
}

type providerModel struct {
	URL    types.String `tfsdk:"url"`
	APIKey types.String `tfsdk:"api_key"`
}

func (p *glenProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "glen"
	resp.Version = p.version
}

func (p *glenProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages Glen agents, knowledge bases, tools and members through Glen's admin API.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Description: "Glen's admin listener, such as https://glen.example.com. Defaults to GLEN_URL.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "A Glen admin's personal API token, made in the console's Settings, under API tokens. Defaults to GLEN_API_KEY.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

func (p *glenProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.URL.IsUnknown() || m.APIKey.IsUnknown() {
		return
	}
	url := setting(m.URL, "GLEN_URL", path.Root("url"), &resp.Diagnostics)
	key := setting(m.APIKey, "GLEN_API_KEY", path.Root("api_key"), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.ResourceData = glen.NewClient(url, key)
}

func setting(v types.String, env string, at path.Path, diags *diag.Diagnostics) string {
	s := v.ValueString()
	if v.IsNull() {
		s = os.Getenv(env)
	}
	if s == "" {
		diags.AddAttributeError(at, "Missing Glen setting", "Set "+at.String()+" in the provider block or "+env+" in the environment.")
	}
	return s
}

func (p *glenProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newAgentResource, newKnowledgeBaseResource, newToolResource, newMemberResource, newModelKeyResource, newAutoRefillResource}
}

func (p *glenProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func clientFrom(data any, diags *diag.Diagnostics) *glen.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*glen.Client)
	if !ok {
		diags.AddError("Provider bug", "the glen provider handed a resource something other than its client")
	}
	return c
}

func requireClient(c *glen.Client, diags *diag.Diagnostics) bool {
	if c == nil {
		diags.AddError("Glen provider not configured", "The provider's url or api_key is not known yet, so it cannot call Glen.")
		return false
	}
	return true
}

func glenMessage(err error) string {
	var e *glen.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}
