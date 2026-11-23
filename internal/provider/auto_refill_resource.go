package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/glen"
)

type autoRefillModel struct {
	Enabled     types.Bool   `tfsdk:"enabled"`
	BelowCents  types.Int64  `tfsdk:"below_cents"`
	AmountCents types.Int64  `tfsdk:"amount_cents"`
	CapCents    types.Int64  `tfsdk:"cap_cents"`
	ChangedBy   types.String `tfsdk:"changed_by"`
	ChangedAt   types.String `tfsdk:"changed_at"`
}

func autoRefillModelOf(a glen.AutoRefill) autoRefillModel {
	return autoRefillModel{
		Enabled:     types.BoolValue(a.On),
		BelowCents:  types.Int64Value(a.BelowCents),
		AmountCents: types.Int64Value(a.AmountCents),
		CapCents:    types.Int64Value(a.CapCents),
		ChangedBy:   types.StringPointerValue(a.ChangedBy),
		ChangedAt:   types.StringPointerValue(a.ChangedAt),
	}
}

type autoRefillResource struct {
	client *glen.Client
}

var (
	_ resource.ResourceWithConfigure   = &autoRefillResource{}
	_ resource.ResourceWithImportState = &autoRefillResource{}
)

func newAutoRefillResource() resource.Resource { return &autoRefillResource{} }

func (r *autoRefillResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_auto_refill"
}

func (r *autoRefillResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The org's auto refill, as the console's Billing page sets it: when credit falls below below_cents, Glen pays amount_cents with the card saved at the last top-up, at most cap_cents in a calendar month (UTC). An org has one, so declare at most one. The provider's token must be an admin's. Destroying it leaves the org's auto refill as it is.",
		Attributes: map[string]schema.Attribute{
			"enabled": schema.BoolAttribute{
				Description: "Whether Glen refills the org's credit.",
				Required:    true,
			},
			"below_cents": schema.Int64Attribute{
				Description: "Refill when the balance falls below this, in cents: 100 to 100000. Defaults to 500.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(500),
			},
			"amount_cents": schema.Int64Attribute{
				Description: "What one refill pays before tax, in cents: 2000 to 100000. Defaults to 2000.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(2000),
			},
			"cap_cents": schema.Int64Attribute{
				Description: "The most refills pay before tax in a calendar month (UTC), in cents: at least one refill, up to 1000000. Defaults to 20000.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(20000),
			},
			"changed_by": schema.StringAttribute{
				Description: "Who last changed the org's auto refill, after the last apply or refresh.",
				Computed:    true,
			},
			"changed_at": schema.StringAttribute{
				Description: "When the org's auto refill was last changed, after the last apply or refresh.",
				Computed:    true,
			},
		},
	}
}

func (r *autoRefillResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// read sets state to the org's auto refill as Glen answers it.
func (r *autoRefillResource) read(ctx context.Context, state *tfsdk.State, diags *diag.Diagnostics) {
	b, err := r.client.GetBilling(ctx)
	if err != nil {
		diags.AddError("Reading the org's auto refill", glenMessage(err))
		return
	}
	diags.Append(state.Set(ctx, autoRefillModelOf(b.AutoRefill))...)
}

func (r *autoRefillResource) write(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics) {
	var m autoRefillModel
	diags.Append(plan.Get(ctx, &m)...)
	if diags.HasError() || !requireClient(r.client, diags) {
		return
	}
	a := glen.AutoRefill{On: m.Enabled.ValueBool(), BelowCents: m.BelowCents.ValueInt64(), AmountCents: m.AmountCents.ValueInt64(), CapCents: m.CapCents.ValueInt64()}
	if err := r.client.PutAutoRefill(ctx, a); err != nil {
		diags.AddError("Glen refused the auto refill", glenMessage(err))
		return
	}
	r.read(ctx, state, diags)
}

func (r *autoRefillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *autoRefillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.write(ctx, req.Plan, &resp.State, &resp.Diagnostics)
}

func (r *autoRefillResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	if requireClient(r.client, &resp.Diagnostics) {
		r.read(ctx, &resp.State, &resp.Diagnostics)
	}
}

// Delete leaves the org's auto refill as it is: turning it off or back to
// Glen's defaults would change what the org pays, which only an apply that
// says so should do.
func (r *autoRefillResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState takes any id, since an org has one auto refill.
func (r *autoRefillResource) ImportState(ctx context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if requireClient(r.client, &resp.Diagnostics) {
		r.read(ctx, &resp.State, &resp.Diagnostics)
	}
}
