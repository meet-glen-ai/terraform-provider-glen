package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type modelKeyModel struct {
	Provider types.String `tfsdk:"model_provider"`
	// The key: write-only, so never in plan or state.
	KeyWO        types.String `tfsdk:"key_wo"`
	KeyWOVersion types.Int64  `tfsdk:"key_wo_version"`
	SetBy        types.String `tfsdk:"set_by"`
	Last4        types.String `tfsdk:"last4"`
}

type modelKeyResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &modelKeyResource{}
	_ resource.ResourceWithImportState    = &modelKeyResource{}
	_ resource.ResourceWithValidateConfig = &modelKeyResource{}
)

func newModelKeyResource() resource.Resource { return &modelKeyResource{} }

func (r *modelKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model_key"
}

func (r *modelKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The org's key of a model provider, which its agents' model calls use, as the console's Settings sets it. Glen checks the key with the provider, keeps it in the org's vault and never answers it; destroying the resource removes it.",
		Attributes: map[string]schema.Attribute{
			"model_provider": schema.StringAttribute{
				Description:   "anthropic or openai. Changing it replaces the key.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"key_wo": schema.StringAttribute{
				Description: "The key. Write-only: Terraform sends it to Glen and keeps it in neither plan nor state. It is written at create and whenever key_wo_version changes. Needs Terraform 1.11 or later.",
				Required:    true,
				Sensitive:   true,
				WriteOnly:   true,
			},
			"key_wo_version": schema.Int64Attribute{
				Description: "Change it to write key_wo again, such as to rotate the key. Terraform compares it, never the key.",
				Optional:    true,
			},
			"set_by": schema.StringAttribute{
				Description: "Who set the key Glen holds, after the last apply or refresh.",
				Computed:    true,
			},
			"last4": schema.StringAttribute{
				Description: "The last four characters of the key Glen holds, after the last apply or refresh: other ones mean the key was set elsewhere, such as in the console.",
				Computed:    true,
			},
		},
	}
}

func (r *modelKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *modelKeyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var p types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("model_provider"), &p)...)
	if p.IsNull() || p.IsUnknown() {
		return
	}
	if v := p.ValueString(); v != "anthropic" && v != "openai" {
		resp.Diagnostics.AddAttributeError(path.Root("model_provider"), "Invalid provider", fmt.Sprintf("provider %q is not one Glen has a key for; it is anthropic or openai.", v))
	}
}

// shown reads what Glen shows of the key into m; false when none is set
// in the org's vault.
func (r *modelKeyResource) shown(ctx context.Context, m *modelKeyModel, diags *diag.Diagnostics) bool {
	keys, err := r.client.ModelKeys(ctx)
	if err != nil {
		diags.AddError("Reading the org's model keys", glenMessage(err))
		return false
	}
	k, ok := keys[m.Provider.ValueString()]
	if !ok || !k.Set || k.In == nil || *k.In != "vault" {
		return false
	}
	m.SetBy, m.Last4 = types.StringPointerValue(k.SetBy), types.StringPointerValue(k.Last4)
	return true
}

func (r *modelKeyResource) write(ctx context.Context, m modelKeyModel, config tfsdk.Config, state *tfsdk.State, diags *diag.Diagnostics) {
	var key types.String
	diags.Append(config.GetAttribute(ctx, path.Root("key_wo"), &key)...)
	if diags.HasError() || !requireClient(r.client, diags) {
		return
	}
	provider := m.Provider.ValueString()
	if err := r.client.PutModelKey(ctx, provider, key.ValueString()); err != nil {
		diags.AddError("Glen refused the "+provider+" key", glenMessage(err))
		return
	}
	if !r.shown(ctx, &m, diags) {
		diags.AddError("Reading the "+provider+" key", "Glen took the key and then showed none set in the org's vault.")
		return
	}
	m.KeyWO = types.StringNull()
	diags.Append(state.Set(ctx, m)...)
}

func (r *modelKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m modelKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, m, req.Config, &resp.State, &resp.Diagnostics)
}

func (r *modelKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m modelKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, m, req.Config, &resp.State, &resp.Diagnostics)
}

func (r *modelKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m modelKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if !r.shown(ctx, &m, &resp.Diagnostics) {
		if !resp.Diagnostics.HasError() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *modelKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var p types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("model_provider"), &p)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteModelKey(ctx, p.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Glen refused to remove the "+p.ValueString()+" key", glenMessage(err))
	}
}

func (r *modelKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("model_provider"), req, resp)
}
