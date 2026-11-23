// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var (
	_ resource.ResourceWithConfigure   = (*phoneNumberResource)(nil)
	_ resource.ResourceWithImportState = (*phoneNumberResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*phoneNumberResource)(nil)
)

type phoneNumberResource struct {
	data *providerData
}

func newPhoneNumberResource() resource.Resource { return &phoneNumberResource{} }

type phoneNumberModel struct {
	ID                 types.String `tfsdk:"id"`
	ProjectID          types.String `tfsdk:"project_id"`
	PhoneNumber        types.String `tfsdk:"phone_number"`
	Name               types.String `tfsdk:"name"`
	CountryCode        types.String `tfsdk:"country_code"`
	AreaCode           types.String `tfsdk:"area_code"`
	NumberType         types.String `tfsdk:"number_type"`
	Locality           types.String `tfsdk:"locality"`
	Region             types.String `tfsdk:"region"`
	Status             types.String `tfsdk:"status"`
	DispatchRuleIDs    types.Set    `tfsdk:"dispatch_rule_ids"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
}

// shortEnum turns PHONE_NUMBER_TYPE_LOCAL into "local".
func shortEnum(v, prefix string) string {
	return strings.ToLower(strings.TrimPrefix(v, prefix))
}

func (m *phoneNumberModel) fill(ctx context.Context, n *livekit.PhoneNumber, diags *diag.Diagnostics) {
	m.ID = types.StringValue(n.ID)
	m.PhoneNumber = types.StringValue(n.E164)
	m.Name = types.StringValue(n.Name)
	m.CountryCode = types.StringValue(n.CountryCode)
	m.AreaCode = types.StringValue(n.AreaCode)
	m.NumberType = types.StringValue(shortEnum(n.NumberType, "PHONE_NUMBER_TYPE_"))
	m.Locality = types.StringValue(n.Locality)
	m.Region = types.StringValue(n.Region)
	m.Status = types.StringValue(shortEnum(n.Status, "PHONE_NUMBER_STATUS_"))
	m.DispatchRuleIDs = knownStringSet(ctx, n.SIPDispatchRuleIDs, diags)
}

func (r *phoneNumberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_phone_number"
}

func (r *phoneNumberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A phone number **bought** for a project from LiveKit. **Warning:** creating this " +
			"resource buys the number, which costs money; destroying it releases the number, which the project cannot get " +
			"back. Find numbers for sale with `data.livekit_available_phone_numbers`. To route the number's " +
			"calls, add its `id` to a `livekit_sip_dispatch_rule`'s `trunk_ids`.",
		Attributes: map[string]schema.Attribute{
			"id":         computed("Phone number ID (`PN_...`)."),
			"project_id": projectIDAttribute("phone number"),
			"phone_number": schema.StringAttribute{
				MarkdownDescription: "The number to buy, in E.164 format such as `+14155550100`, from " +
					"`data.livekit_available_phone_numbers`. Changing it buys another number and releases this one.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(e164Pattern, "must be a phone number in E.164 format, such as +14155550100"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "A label for the number. Defaults to `\"\"`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"country_code": computed("The number's country."),
			"area_code":    computed("The number's area code."),
			"number_type":  computed("`local`, `mobile`, `toll_free` or `unknown`."),
			"locality":     computed("The number's city or locality."),
			"region":       computed("The number's state or region."),
			"status":       computed("`active`, `pending` or `offline`."),
			"dispatch_rule_ids": schema.SetAttribute{
				MarkdownDescription: "Dispatch rules (`SDR_...`) that list this number.",
				ElementType:         types.StringType,
				Computed:            true,
				PlanModifiers:       []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"deletion_protection": deletionProtectionAttribute("a released number cannot be bought back, and callers " +
				"who know it reach nothing."),
		},
	}
}

func (r *phoneNumberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *phoneNumberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

func (r *phoneNumberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan phoneNumberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := plan.ProjectID.ValueString()
	n, err := r.data.client.PurchasePhoneNumber(ctx, projectID, plan.PhoneNumber.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to buy phone number", err.Error())
		return
	}
	name := plan.Name.ValueString()
	plan.fill(ctx, n, &resp.Diagnostics)
	// Bought: record it before anything else can fail.
	plan.Name = types.StringValue(n.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() || name == n.Name {
		return
	}
	n, err = r.data.client.RenamePhoneNumber(ctx, projectID, n.ID, name)
	if err != nil {
		resp.Diagnostics.AddError("Unable to name the new phone number", err.Error())
		return
	}
	plan.fill(ctx, n, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *phoneNumberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state phoneNumberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n, err := r.data.client.GetPhoneNumber(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if livekit.IsNotFound(err) || (err == nil && n.Status == livekit.PhoneNumberReleased) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read phone number", err.Error())
		return
	}
	state.fill(ctx, n, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *phoneNumberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state phoneNumberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.Equal(state.Name) {
		n, err := r.data.client.RenamePhoneNumber(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), plan.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Unable to rename phone number", err.Error())
			return
		}
		plan.fill(ctx, n, &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *phoneNumberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state phoneNumberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if deletionProtected(state.DeletionProtection, "phone number", &resp.Diagnostics) {
		return
	}
	err := r.data.client.ReleasePhoneNumber(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !livekit.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to release phone number", err.Error())
	}
}

// ImportState adopts a number the project owns by "<project_id>/<id>".
func (r *phoneNumberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importProjectChild(ctx, req, resp, "id")
}
