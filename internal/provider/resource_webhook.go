// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var (
	_ resource.Resource                = (*webhookResource)(nil)
	_ resource.ResourceWithConfigure   = (*webhookResource)(nil)
	_ resource.ResourceWithImportState = (*webhookResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*webhookResource)(nil)
)

type webhookResource struct {
	data *providerData
}

func newWebhookResource() resource.Resource { return &webhookResource{} }

type webhookModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	Name          types.String `tfsdk:"name"`
	URL           types.String `tfsdk:"url"`
	SigningKey    types.String `tfsdk:"signing_key"`
	IncludeEvents types.Set    `tfsdk:"include_events"`
	ExcludeEvents types.Set    `tfsdk:"exclude_events"`
}

// fill copies what the API reports. An empty event list reads back as null
// unless the configuration had `[]`.
func (m *webhookModel) fill(ctx context.Context, w *livekit.Webhook, diags *diag.Diagnostics) {
	m.ID = types.StringValue(w.ID)
	m.Name = types.StringValue(w.Name)
	m.URL = types.StringValue(w.URL)
	m.SigningKey = types.StringValue(w.SigningKey)
	m.IncludeEvents = stringSet(ctx, w.FilterParams.IncludeEvents, m.IncludeEvents, diags)
	m.ExcludeEvents = stringSet(ctx, w.FilterParams.ExcludeEvents, m.ExcludeEvents, diags)
}

// filter is the whole event filter of the plan; the API replaces the filter
// on every update that sends one.
func (m *webhookModel) filter(ctx context.Context, diags *diag.Diagnostics) livekit.FilterParams {
	f := livekit.FilterParams{
		IncludeEvents: setStrings(ctx, m.IncludeEvents, diags),
		ExcludeEvents: setStrings(ctx, m.ExcludeEvents, diags),
	}
	if f.IncludeEvents == nil {
		f.IncludeEvents = []string{}
	}
	if f.ExcludeEvents == nil {
		f.ExcludeEvents = []string{}
	}
	return f
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	eventSet := func(desc string) schema.SetAttribute {
		return schema.SetAttribute{
			MarkdownDescription: desc + " Names come from `data.livekit_webhook_events`; the API refuses others.",
			ElementType:         types.StringType,
			Optional:            true,
			Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A webhook: a URL a LiveKit Cloud project sends its room, participant, track, egress " +
			"and ingress events to, signed with the secret of one of the project's API keys. Every change is made " +
			"in place. The API accepts several webhooks with the same URL.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Webhook ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": projectIDAttribute("webhook"),
			"name": schema.StringAttribute{
				MarkdownDescription: "A label for the webhook. Defaults to `\"\"`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "HTTP or HTTPS URL the events are POSTed to. The API refuses a URL that does " +
					"not resolve to a public address.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(httpURLPattern, "must be an http:// or https:// URL"),
				},
			},
			"signing_key": schema.StringAttribute{
				MarkdownDescription: "ID (`API...`) of the project's API key whose secret signs the deliveries; " +
					"a key ID, not a secret. When not set, the API picks the project's oldest key when the webhook " +
					"is created, and removing the argument later keeps the key. The webhook keeps the ID even when " +
					"that key is deleted, and then cannot be verified: delete keys with care.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"include_events": eventSet("Deliver only these events. With neither `include_events` nor " +
				"`exclude_events`, the webhook gets every event."),
			"exclude_events": eventSet("Deliver every event but these."),
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *webhookResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	create := livekit.WebhookCreate{
		Name:         plan.Name.ValueString(),
		URL:          plan.URL.ValueString(),
		SigningKey:   plan.SigningKey.ValueString(), // "" when unknown
		FilterParams: plan.filter(ctx, &resp.Diagnostics),
	}
	if resp.Diagnostics.HasError() {
		return
	}
	webhook, err := r.data.client.CreateWebhook(ctx, plan.ProjectID.ValueString(), create)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create webhook", err.Error())
		return
	}
	plan.fill(ctx, webhook, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	webhook, err := r.data.client.GetWebhook(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil {
		if livekit.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read webhook", err.Error())
		return
	}
	state.fill(ctx, webhook, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filter := plan.filter(ctx, &resp.Diagnostics)
	update := livekit.WebhookUpdate{
		Name:         ptr(plan.Name.ValueString()),
		URL:          ptr(plan.URL.ValueString()),
		FilterParams: &filter,
	}
	if !plan.SigningKey.IsNull() && !plan.SigningKey.IsUnknown() {
		update.SigningKey = ptr(plan.SigningKey.ValueString())
	}
	webhook, err := r.data.client.UpdateWebhook(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), update)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update webhook", err.Error())
		return
	}
	plan.fill(ctx, webhook, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.data.client.DeleteWebhook(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !livekit.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete webhook", err.Error())
	}
}

// ImportState adopts an existing webhook by "<project_id>/<id>".
func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importProjectChild(ctx, req, resp, "id")
}
