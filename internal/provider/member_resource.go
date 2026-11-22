package provider

import (
	"context"
	"fmt"
	"strings"

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

type memberModel struct {
	Email types.String `tfsdk:"email"`
	Role  types.String `tfsdk:"role"`
	State types.String `tfsdk:"state"`
}

func memberModelOf(m glen.Member) memberModel {
	return memberModel{Email: types.StringValue(m.Email), Role: types.StringPointerValue(m.Role), State: types.StringValue(m.State)}
}

type memberResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &memberResource{}
	_ resource.ResourceWithImportState    = &memberResource{}
	_ resource.ResourceWithValidateConfig = &memberResource{}
)

func newMemberResource() resource.Resource { return &memberResource{} }

func (r *memberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (r *memberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A person of the Glen org the provider's token belongs to, with their role. A new one is invited by email through the org's WorkOS organization; destroying one ends the membership, with the person's console logins and API tokens, or revokes the invitation. Glen refuses to demote or remove the org's last admin.",
		Attributes: map[string]schema.Attribute{
			"email": schema.StringAttribute{
				Description:   "The person's email, lowercase. Changing it replaces the member.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Description: "admin or member. An admin manages API tokens, agents, knowledge bases, tools and settings; a member runs agents and reads.",
				Required:    true,
			},
			"state": schema.StringAttribute{
				Description: "active once the person has joined, invited while their invitation is pending.",
				Computed:    true,
			},
		},
	}
}

func (r *memberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *memberResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m memberModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if e := m.Email; !e.IsUnknown() && !e.IsNull() && e.ValueString() != strings.ToLower(e.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("email"), "Email is not lowercase", fmt.Sprintf("Glen keeps emails lowercase; write %q.", strings.ToLower(e.ValueString())))
	}
	if role := m.Role; !role.IsUnknown() && !role.IsNull() && role.ValueString() != "admin" && role.ValueString() != "member" {
		resp.Diagnostics.AddAttributeError(path.Root("role"), "Unknown role", fmt.Sprintf("%q is not a role; a role is admin or member.", role.ValueString()))
	}
}

func (r *memberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	email := m.Email.ValueString()
	if found, err := r.client.GetMember(ctx, email); err == nil {
		resp.Diagnostics.AddError("Member already exists",
			fmt.Sprintf("%s is already %s in the org. Import it with `terraform import <address> %s` to manage it here.", email, found.State, email))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading member "+email, glenMessage(err))
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *memberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m memberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *memberResource) put(ctx context.Context, m memberModel, state *tfsdk.State, diags *diag.Diagnostics) {
	got, err := r.client.PutMember(ctx, m.Email.ValueString(), m.Role.ValueString())
	if err != nil {
		diags.AddError("Writing member "+m.Email.ValueString(), glenMessage(err))
		return
	}
	diags.Append(state.Set(ctx, memberModelOf(got))...)
}

func (r *memberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var email types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("email"), &email)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	got, err := r.client.GetMember(ctx, email.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading member "+email.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, memberModelOf(got))...)
}

func (r *memberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var email types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("email"), &email)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteMember(ctx, email.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Removing member "+email.ValueString(), glenMessage(err))
	}
}

func (r *memberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("email"), req, resp)
}
