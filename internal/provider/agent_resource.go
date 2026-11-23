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

type agentResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure      = &agentResource{}
	_ resource.ResourceWithImportState    = &agentResource{}
	_ resource.ResourceWithModifyPlan     = &agentResource{}
	_ resource.ResourceWithValidateConfig = &agentResource{}
)

func newAgentResource() resource.Resource { return &agentResource{} }

func (r *agentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *agentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	strs := func(desc string) schema.ListAttribute {
		return schema.ListAttribute{Description: desc, ElementType: types.StringType, Optional: true}
	}
	count := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Description: desc, Optional: true}
	}
	resp.Schema = schema.Schema{
		Description: "A Glen agent. Every apply that changes it is a new version in Glen.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "The agent's name, kebab-case. Changing it replaces the agent.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"instructions": schema.StringAttribute{
				Description: "The instructions text, usually file(\"...\").",
				Required:    true,
			},
			"model": schema.StringAttribute{Description: "The model the agent runs on.", Required: true},
			"harness": schema.StringAttribute{
				Description: "The harness that runs each turn, such as claude-code. Unset, Glen picks it, and the attribute holds Glen's choice.",
				Optional:    true,
				Computed:    true,
			},
			"knowledge_base": schema.StringAttribute{
				Description: "The glen_knowledge_base this agent reads, usually glen_knowledge_base.<name>.name. Glen refuses the apply if no such knowledge base exists.",
				Optional:    true,
			},
			"version": schema.Int64Attribute{
				Description: "The agent's version in Glen after the last apply or refresh.",
				Computed:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"trigger": schema.ListNestedBlock{
				Description: "What starts a session. Each block sets exactly one of schedule, feed and webhook.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"schedule": schema.StringAttribute{Description: "A five-field cron expression, UTC.", Optional: true},
						"feed":     schema.StringAttribute{Description: "A feed this Glen has, by name; the console's agent form lists them.", Optional: true},
						"webhook":  schema.StringAttribute{Description: "<source>:<event>, such as github:pull_request.", Optional: true},
					},
				},
			},
			"tools": schema.SingleNestedBlock{
				Description: "What the agent may use in its sandbox.",
				Attributes: map[string]schema.Attribute{
					"mcp": schema.ListNestedAttribute{
						Description: "The tools the agent uses, by name: a glen_tool, usually glen_tool.<name>.name, or an MCP server this deployment has.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"server": schema.StringAttribute{Description: "The tool's name.", Required: true},
								"as": schema.StringAttribute{
									Description: "Whose connection the tool's calls use: user, agent or service. Unset is service. A tool people sign in to takes user or agent.",
									Optional:    true,
								},
							},
						},
					},
				},
			},
			"sandbox": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{
					"image": schema.StringAttribute{Description: "Container image override.", Optional: true},
					"egress": strs("The hosts the sandbox may connect to through Glen's egress proxy, besides Glen: a hostname such as api.github.com, " +
						"or *. and a domain such as *.githubusercontent.com. Unset, it reaches Glen only."),
					"web_search": schema.BoolAttribute{
						Description: "The model provider's server-side web search. It runs at the provider, past the egress list, with a query the agent writes. Off when unset.",
						Optional:    true,
					},
				},
			},
			"budget": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{
					"tokens_per_session": count("Token limit per session."),
					"sessions_per_day":   count("Session limit per day."),
					"max_turns":          count("Turn limit per session."),
				},
			},
		},
	}
}

func (r *agentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *agentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var triggers types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("trigger"), &triggers)...)
	for i, t := range triggers.Elements() {
		obj, ok := t.(types.Object)
		if !ok {
			continue
		}
		set, known := 0, true
		for _, v := range obj.Attributes() {
			known = known && !v.IsUnknown()
			if !v.IsNull() {
				set++
			}
		}
		if known && set != 1 {
			resp.Diagnostics.AddAttributeError(path.Root("trigger").AtListIndex(i), "Invalid trigger",
				fmt.Sprintf("A trigger sets exactly one of schedule, feed and webhook; this one sets %d.", set))
		}
	}
}

func (r *agentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil || !req.Config.Raw.IsFullyKnown() {
		return
	}
	var m agentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ValidateAgent(ctx, r.withToolsGlenHas(ctx, m.spec())); err != nil {
		resp.Diagnostics.AddError("Glen refused agent "+m.Name.ValueString(), glenMessage(err))
	}
}

// withToolsGlenHas leaves out of spec's tools.mcp each tool Glen has no
// record of, so a plan that creates a glen_tool and the agent that uses it
// passes. Glen checks those at apply, after Terraform has made the tool.
func (r *agentResource) withToolsGlenHas(ctx context.Context, spec glen.AgentSpec) glen.AgentSpec {
	has := []glen.McpUse{}
	for _, u := range spec.Tools.MCP {
		if _, err := r.client.GetTool(ctx, u.Server); !validToolName(u.Server) || !glen.IsNotFound(err) {
			has = append(has, u)
		}
	}
	spec.Tools.MCP = has
	return spec
}

func (r *agentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m agentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	name := m.Name.ValueString()
	if _, err := r.client.GetAgent(ctx, name); err == nil {
		resp.Diagnostics.AddError("Agent already exists",
			fmt.Sprintf("Glen already has an agent named %q. Import it with `terraform import <address> %s` to manage it here.", name, name))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading agent "+name, glenMessage(err))
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *agentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m agentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *agentResource) put(ctx context.Context, m agentModel, state *tfsdk.State, diags *diag.Diagnostics) {
	a, err := r.client.PutAgent(ctx, m.spec())
	if err != nil {
		diags.AddError("Writing agent "+m.Name.ValueString(), glenMessage(err))
		return
	}
	m.Version = types.Int64Value(a.Version)
	m.Harness = types.StringValue(a.Spec.Harness)
	diags.Append(state.Set(ctx, m)...)
}

func (r *agentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m agentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	a, err := r.client.GetAgent(ctx, m.Name.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading agent "+m.Name.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m.refreshed(a))...)
}

func (r *agentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteAgent(ctx, name.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting agent "+name.ValueString(), glenMessage(err))
	}
}

func (r *agentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
