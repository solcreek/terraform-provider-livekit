// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
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
	e164 := plan.PhoneNumber.ValueString()
	name := plan.Name.ValueString()
	// Only the purchase's success is used: its response and the rename's
	// are not complete (observed 2026-11-23), so the number is always read
	// back with a GET.
	if _, err := r.data.client.PurchasePhoneNumber(ctx, projectID, e164); err != nil {
		resp.Diagnostics.AddError("Unable to buy phone number", err.Error())
		return
	}
	// Bought: record it at once, by its E.164 form, so that nothing bought
	// is lost if a later step fails; Read finds a number without an ID.
	plan.ID = types.StringValue("")
	for _, v := range []*types.String{&plan.CountryCode, &plan.AreaCode, &plan.NumberType, &plan.Locality, &plan.Region, &plan.Status} {
		*v = types.StringValue("")
	}
	plan.Name = types.StringValue("")
	plan.DispatchRuleIDs = knownStringSet(ctx, nil, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	found, err := r.data.client.FindPhoneNumber(ctx, projectID, e164)
	if err != nil {
		resp.Diagnostics.AddError("Bought the phone number, but cannot find it",
			fmt.Sprintf("%s was bought, but the project's list of numbers does not show it: %v. Check the "+
				"dashboard; a later refresh looks for it again.", e164, err))
		return
	}
	plan.fill(ctx, found, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() || name == found.Name {
		return
	}
	r.rename(ctx, &plan, name, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// rename names the number and reads it back.
func (r *phoneNumberResource) rename(ctx context.Context, m *phoneNumberModel, name string, diags *diag.Diagnostics) {
	projectID, id := m.ProjectID.ValueString(), m.ID.ValueString()
	if err := r.data.client.RenamePhoneNumber(ctx, projectID, id, name); err != nil {
		diags.AddError("Unable to name the phone number", err.Error())
		return
	}
	n, err := r.data.client.GetPhoneNumber(ctx, projectID, id)
	if err != nil {
		diags.AddError("Unable to read the phone number", err.Error())
		return
	}
	m.fill(ctx, n, diags)
}

func (r *phoneNumberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state phoneNumberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var n *livekit.PhoneNumber
	var err error
	if state.ID.ValueString() == "" {
		// Bought, but its ID was not found yet.
		n, err = r.data.client.FindPhoneNumber(ctx, state.ProjectID.ValueString(), state.PhoneNumber.ValueString())
	} else {
		n, err = r.data.client.GetPhoneNumber(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	}
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
		r.rename(ctx, &plan, plan.Name.ValueString(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
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
	if state.ID.ValueString() == "" {
		resp.Diagnostics.AddError("Phone number ID unknown",
			"The number's ID was never found, so it cannot be released from here. Release "+
				state.PhoneNumber.ValueString()+" in the dashboard.")
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
