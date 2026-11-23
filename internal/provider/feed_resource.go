package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type feedModel struct {
	Name     types.String `tfsdk:"name"`
	Format   types.String `tfsdk:"format"`
	URL      types.String `tfsdk:"url"`
	PollSecs types.Int64  `tfsdk:"poll_secs"`
	Baseline types.Bool   `tfsdk:"baseline"`
	Version  types.Int64  `tfsdk:"version"`
}

func (m feedModel) spec() glen.FeedSpec {
	return glen.FeedSpec{
		Name:     m.Name.ValueString(),
		Format:   m.Format.ValueString(),
		URL:      m.URL.ValueString(),
		PollSecs: m.PollSecs.ValueInt64(),
		Baseline: m.Baseline.ValueBool(),
	}
}

func feedModelOf(f glen.Feed) feedModel {
	return feedModel{
		Name:     types.StringValue(f.Spec.Name),
		Format:   types.StringValue(f.Spec.Format),
		URL:      types.StringValue(f.Spec.URL),
		PollSecs: types.Int64Value(f.Spec.PollSecs),
		Baseline: types.BoolValue(f.Spec.Baseline),
		Version:  types.Int64Value(f.Version),
	}
}

type feedResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure   = &feedResource{}
	_ resource.ResourceWithImportState = &feedResource{}
)

func newFeedResource() resource.Resource { return &feedResource{} }

func (r *feedResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_feed"
}

func (r *feedResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A feed the org's agents start on. Glen polls it and sends each new entry to every agent whose trigger names it. Every apply that changes it is a new version in Glen.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description:   "The feed's name, kebab-case, which an agent's trigger names. Changing it replaces the feed.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"format": schema.StringAttribute{
				Description: "What the document is: rss (an entry per item), osv (a JSON array of OSV records) or cisa-kev (CISA's known-exploited list).",
				Required:    true,
			},
			"url": schema.StringAttribute{
				Description: "Where Glen fetches the feed: https on a public address. Glen follows no redirect, so give the address the feed answers at.",
				Required:    true,
			},
			"poll_secs": schema.Int64Attribute{
				Description: "Seconds between polls, 60 to 86400. Defaults to 3600.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(3600),
			},
			"baseline": schema.BoolAttribute{
				Description: "Mark what the feed already lists as seen at its first poll, so an old list does not start a session per entry. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"version": schema.Int64Attribute{
				Description: "The feed's version in Glen after the last apply or refresh.",
				Computed:    true,
			},
		},
	}
}

func (r *feedResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *feedResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m feedModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	name := m.Name.ValueString()
	if _, err := r.client.GetFeed(ctx, name); err == nil {
		resp.Diagnostics.AddError("Feed already exists",
			fmt.Sprintf("Glen already has a feed named %q. Import it with `terraform import <address> %s` to manage it here.", name, name))
		return
	} else if !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Reading feed "+name, glenMessage(err))
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *feedResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m feedModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	r.put(ctx, m, &resp.State, &resp.Diagnostics)
}

func (r *feedResource) put(ctx context.Context, m feedModel, state *tfsdk.State, diags *diag.Diagnostics) {
	f, err := r.client.PutFeed(ctx, m.spec())
	if err != nil {
		diags.AddError("Writing feed "+m.Name.ValueString(), glenMessage(err))
		return
	}
	m.Version = types.Int64Value(f.Version)
	diags.Append(state.Set(ctx, m)...)
}

func (r *feedResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	f, err := r.client.GetFeed(ctx, name.ValueString())
	if glen.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading feed "+name.ValueString(), glenMessage(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, feedModelOf(f))...)
}

func (r *feedResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var name types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() || !requireClient(r.client, &resp.Diagnostics) {
		return
	}
	if err := r.client.DeleteFeed(ctx, name.ValueString()); err != nil && !glen.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting feed "+name.ValueString(), glenMessage(err))
	}
}

func (r *feedResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
