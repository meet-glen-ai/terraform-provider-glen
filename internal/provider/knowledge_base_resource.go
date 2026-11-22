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

type knowledgeBaseModel struct {
	Name        types.String  `tfsdk:"name"`
	Description types.String  `tfsdk:"description"`
	Guidance    types.String  `tfsdk:"guidance"`
	Curator     *curatorModel `tfsdk:"curator"`
	Version     types.Int64   `tfsdk:"version"`
}

type curatorModel struct {
	Model        types.String `tfsdk:"model"`
	TokensPerDay types.Int64  `tfsdk:"tokens_per_day"`
}

func (m knowledgeBaseModel) spec() glen.KnowledgeBaseSpec {
	s := glen.KnowledgeBaseSpec{
		Name:        m.Name.ValueString(),
		Description: m.Description.ValueString(),
		Guidance:    m.Guidance.ValueStringPointer(),
	}
	if m.Curator != nil {
		s.Curator = glen.Curator{Model: m.Curator.Model.ValueString(), TokensPerDay: m.Curator.TokensPerDay.ValueInt64()}
	}
	return s
}

func knowledgeBaseModelOf(w glen.KnowledgeBase) knowledgeBaseModel {
	return knowledgeBaseModel{
		Name:        types.StringValue(w.Spec.Name),
		Description: types.StringValue(w.Spec.Description),
		Guidance:    types.StringPointerValue(w.Spec.Guidance),
		Curator: &curatorModel{
			Model:        types.StringValue(w.Spec.Curator.Model),
			TokensPerDay: types.Int64Value(w.Spec.Curator.TokensPerDay),
		},
		Version: types.Int64Value(w.Version),
	}
}

type knowledgeBaseResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &knowledgeBaseResource{}
	_ resource.ResourceWithImportState    = &knowledgeBaseResource{}
	_ resource.ResourceWithModifyPlan     = &knowledgeBaseResource{}
	_ resource.ResourceWithValidateConfig = &knowledgeBaseResource{}
)

func newKnowledgeBaseResource() resource.Resource { return &knowledgeBaseResource{} }

func (r *knowledgeBaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_knowledge_base"
}

func (r *knowledgeBaseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Glen knowledge base: the files the agents that name it read, written by its curator. Every apply that changes it is a new version in Glen.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "The knowledge base's name, kebab-case. Changing it replaces the knowledge base.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				Description: "What the knowledge base is for. The console shows it and the curator reads it.",
				Required:    true,
			},
			"guidance": schema.StringAttribute{
				Description: "The team's direction to the curator, usually file(\"...\").",
				Optional:    true,
			},
			"version": schema.Int64Attribute{
				Description: "The knowledge base's version in Glen after the last apply or refresh.",
				Computed:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"curator": schema.SingleNestedBlock{
				Description: "The curator Glen runs for this knowledge base. Required.",
				Attributes: map[string]schema.Attribute{
					"model":          schema.StringAttribute{Description: "The model the curator runs on.", Required: true},
					"tokens_per_day": schema.Int64Attribute{Description: "The curator's daily token budget, at least 1. Cache reads count at a tenth of their tokens.", Required: true},
				},
			},
		},
	}
}

func (r *knowledgeBaseResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *knowledgeBaseResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var curator types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("curator"), &curator)...)
	if curator.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("curator"), "Missing curator block",
			"A glen_knowledge_base needs a curator block that sets model and tokens_per_day.")
	}
}

func (r *knowledgeBaseResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil || !req.Config.Raw.IsFullyKnown() {
		return
	}
	var m knowledgeBaseModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ValidateKnowledgeBase(ctx, m.spec()); err != nil {
		resp.Diagnostics.AddError("Glen refused knowledge base "+m.Name.ValueString(), glenMessage(err))
	}
}

func (r *knowledgeBaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m knowledgeBaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	name := m.Name.ValueString()
	if _, err := r.client.GetKnowledgeBase(ctx, name); err == nil {
		resp.Diagnostics.AddError("KnowledgeBase already exists",
			fmt.Sprintf("Glen already has a knowledge base named %q. Import it with `terraform import <address> %s` to manage it here.", name, name))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading knowledge base "+name, glenMessage(err))
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *knowledgeBaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m knowledgeBaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *knowledgeBaseResource) put(ctx context.Context, m knowledgeBaseModel, state *tfsdk.State, diags *diag.Diagnostics) {
	w, err := r.client.PutKnowledgeBase(ctx, m.spec())
	if err != nil {
		diags.AddError("Writing knowledge base "+m.Name.ValueString(), glenMessage(err))
		return
	}
	m.Version = types.Int64Value(w.Version)
	diags.Append(state.Set(ctx, m)...)
}

func (r *knowledgeBaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	w, err := r.client.GetKnowledgeBase(ctx, name.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading knowledge base "+name.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, knowledgeBaseModelOf(w))...)
}

func (r *knowledgeBaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteKnowledgeBase(ctx, name.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting knowledge base "+name.ValueString(), glenMessage(err))
	}
}

func (r *knowledgeBaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
