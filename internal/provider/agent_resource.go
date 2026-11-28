package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
			"allowed_message_senders": schema.ListAttribute{
				Description: "The roles whose members may message this agent, usually glen_role.<name>.name: start it, reply to it, answer its questions, allow or deny its held tool calls and stop it. Its admins always may, and member names everyone in the org. Unset, every member may message it, only admins allow its held calls, and admins are told of its asks; set, only the roles' members and admins may message it, and the roles' members are told. Glen refuses the apply if the org has no such role.",
				ElementType: types.StringType,
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
								"policies": schema.MapAttribute{
									Description: "The tool's functions this agent holds back, by function name: approve (a call waits for a person's approval in its session) or never (Glen refuses it). " +
										"Each function gets the stricter of this and the org's policy, which admins set on the tool's page in the console, so an agent makes a function stricter, never looser. " +
										"Glen refuses it while a CLI tool of the same service the agent has sends every request with its credential.",
									ElementType: types.StringType,
									Optional:    true,
									Validators:  []validator.Map{valuesOneOf(glen.ToolPolicies)},
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
			"notify": schema.SingleNestedBlock{
				Description: "Who Glen tells when one of the agent's sessions finishes, fails or needs someone, and the Slack channel its asks and held calls are posted to. Unset, Glen emails no one of them and posts nothing.",
				Blocks: map[string]schema.Block{
					"email": schema.SingleNestedBlock{
						Description: "Email to the org's members.",
						Attributes: map[string]schema.Attribute{
							"to": schema.ListAttribute{
								Description: "Required in an email block. The org members Glen emails, by their sign-in address, usually glen_member.<name>.email. Glen refuses the apply if one is not a member of the org, or if on names an event and to names no one.",
								ElementType: types.StringType,
								Optional:    true,
							},
							"on": schema.ListAttribute{
								Description: "What Glen emails them on, each at most once: finished (a session settled done, with no ask open), failed (a session failed, not stopped by a budget) and needs_you (it asked, Glen held one of its calls, or a budget stopped it). Unset or empty, Glen emails them on none.",
								ElementType: types.StringType,
								Optional:    true,
								Validators:  []validator.List{eachOnceOf(glen.NotifyOn)},
							},
						},
					},
					"slack": schema.SingleNestedBlock{
						Description: "A Slack channel each of the agent's asks and held calls is posted to, in the workspace the org added Glen's Slack app to. A reply in the post's thread is the session's next message, and an approver allows or denies a held call with its buttons.",
						Attributes: map[string]schema.Attribute{
							"channel": schema.StringAttribute{
								Description: "Required in a slack block. The channel's id, such as C0123ABCD (a private channel's starts with G), from the channel's details in Slack. Glen's app must be in the channel.",
								Optional:    true,
							},
						},
					},
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
	// An absent single nested block holds its attributes null, so the
	// schema cannot mark to required; the email block requires it here.
	var email types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("notify").AtName("email"), &email)...)
	if !email.IsNull() && !email.IsUnknown() {
		if to, ok := email.Attributes()["to"]; ok && to.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("notify").AtName("email").AtName("to"), "Missing notify.email.to",
				"An email block lists the org members Glen emails in to.")
		}
	}
	var slack types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("notify").AtName("slack"), &slack)...)
	if !slack.IsNull() && !slack.IsUnknown() {
		if channel, ok := slack.Attributes()["channel"]; ok && channel.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("notify").AtName("slack").AtName("channel"), "Missing notify.slack.channel",
				"A slack block names the channel's id in channel.")
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

// valuesOneOf checks that each value of a map of strings is one of
// allowed. Unknown values are left to Glen at apply.
func valuesOneOf(allowed []string) validator.Map {
	return valuesOneOfValidator{allowed}
}

type valuesOneOfValidator struct{ allowed []string }

func (v valuesOneOfValidator) Description(context.Context) string {
	return "each value is one of " + strings.Join(v.allowed, ", ")
}

func (v valuesOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v valuesOneOfValidator) ValidateMap(_ context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for k, e := range req.ConfigValue.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			continue
		}
		if !slices.Contains(v.allowed, s.ValueString()) {
			resp.Diagnostics.AddAttributeError(req.Path.AtMapKey(k), "Invalid value",
				fmt.Sprintf("%s is %q, which is not one of %s: an agent makes a function stricter than the org's policy, never looser.", k, s.ValueString(), strings.Join(v.allowed, ", ")))
		}
	}
}

// eachOnceOf checks that a list of strings names only allowed values, each
// at most once. Unknown items are left to Glen at apply.
func eachOnceOf(allowed []string) validator.List {
	return eachOnceOfValidator{allowed}
}

type eachOnceOfValidator struct{ allowed []string }

func (v eachOnceOfValidator) Description(context.Context) string {
	return "each item is one of " + strings.Join(v.allowed, ", ") + ", named at most once"
}

func (v eachOnceOfValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v eachOnceOfValidator) ValidateList(_ context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var seen []string
	for i, e := range req.ConfigValue.Elements() {
		s, ok := e.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			continue
		}
		at := req.Path.AtListIndex(i)
		switch item := s.ValueString(); {
		case !slices.Contains(v.allowed, item):
			resp.Diagnostics.AddAttributeError(at, "Invalid value",
				fmt.Sprintf("%q is not one of %s.", item, strings.Join(v.allowed, ", ")))
		case slices.Contains(seen, item):
			resp.Diagnostics.AddAttributeError(at, "Duplicate value", fmt.Sprintf("%s is named twice.", item))
		default:
			seen = append(seen, item)
		}
	}
}
