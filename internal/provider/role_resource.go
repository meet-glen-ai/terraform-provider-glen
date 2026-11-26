package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

// roleName is the form Glen takes a role's name in: kebab-case, at most
// 40 characters.
var roleName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type roleModel struct {
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	// Members is nil when Terraform leaves who holds the role to the console.
	Members []string `tfsdk:"members"`
}

// roleModelOf is the role as Glen holds it, with members kept unset when
// the configuration leaves them unset.
func roleModelOf(r glen.Role, manageMembers bool) roleModel {
	m := roleModel{Name: types.StringValue(r.Name), Description: types.StringValue(r.Description)}
	if manageMembers {
		m.Members = r.Members
		if m.Members == nil {
			m.Members = []string{}
		}
	}
	return m
}

type roleResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &roleResource{}
	_ resource.ResourceWithImportState    = &roleResource{}
	_ resource.ResourceWithValidateConfig = &roleResource{}
)

func newRoleResource() resource.Resource { return &roleResource{} }

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "One of the org's own roles: a named group of members that grants nothing by itself. An agent's approvers name the roles whose members may allow its held tool calls, and are told of its asks. Every change is kept in Glen's history of roles. Glen refuses to destroy a role an agent names.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "The role's name, kebab-case, at most 40 characters, and neither admin nor member, which are built in. Changing it replaces the role.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Description: "What the role is for, up to 300 characters.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"members": schema.SetAttribute{
				Description: "The emails of the members who hold the role, exactly. Each must already be a member of the org: an invitation holds no role until it is accepted. Unset, Terraform leaves who holds the role to the console.",
				ElementType: types.StringType,
				Optional:    true,
			},
		},
	}
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *roleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m roleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if n := m.Name; !n.IsUnknown() && !n.IsNull() {
		name := n.ValueString()
		switch {
		case name == "admin" || name == "member":
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Built-in role", name+" is built in, and WorkOS decides who holds it; manage it with glen_member.")
		case len(name) > 40 || !roleName.MatchString(name):
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Not a role name", fmt.Sprintf("%q is not a role name: lowercase letters, digits and single dashes, at most 40 characters.", name))
		}
	}
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	name := m.Name.ValueString()
	if _, err := r.client.GetRole(ctx, name); err == nil {
		resp.Diagnostics.AddError("Role already exists",
			fmt.Sprintf("The org already has the role %s. Import it with `terraform import <address> %s` to manage it here.", name, name))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading role "+name, glenMessage(err))
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m roleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *roleResource) put(ctx context.Context, m roleModel, state *tfsdk.State, diags *diag.Diagnostics) {
	spec := glen.RoleSpec{Description: m.Description.ValueString()}
	if m.Members != nil {
		members := slices.Clone(m.Members)
		spec.Members = &members
	}
	got, err := r.client.PutRole(ctx, m.Name.ValueString(), spec)
	if err != nil {
		diags.AddError("Writing role "+m.Name.ValueString(), glenMessage(err))
		return
	}
	diags.Append(state.Set(ctx, roleModelOf(got, m.Members != nil))...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m roleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	got, err := r.client.GetRole(ctx, m.Name.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading role "+m.Name.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, roleModelOf(got, m.Members != nil))...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteRole(ctx, name.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting role "+name.ValueString(), glenMessage(err))
	}
}

// ImportState takes a role by name, with its members, so a configuration
// that lists them imports with no change.
func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	got, err := r.client.GetRole(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Reading role "+req.ID, glenMessage(err))
		return
	}
	if got.BuiltIn {
		resp.Diagnostics.AddError("Built-in role", req.ID+" is built in, and WorkOS decides who holds it; manage it with glen_member.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, roleModelOf(got, true))...)
}
