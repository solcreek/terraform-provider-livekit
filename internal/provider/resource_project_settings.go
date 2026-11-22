// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var (
	_ resource.Resource                   = (*projectSettingsResource)(nil)
	_ resource.ResourceWithConfigure      = (*projectSettingsResource)(nil)
	_ resource.ResourceWithImportState    = (*projectSettingsResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*projectSettingsResource)(nil)
	_ resource.ResourceWithValidateConfig = (*projectSettingsResource)(nil)
)

type projectSettingsResource struct {
	data *providerData
}

func newProjectSettingsResource() resource.Resource { return &projectSettingsResource{} }

type projectSettingsModel struct {
	ID                               types.String `tfsdk:"id"`
	ProjectID                        types.String `tfsdk:"project_id"`
	Name                             types.String `tfsdk:"name"`
	EnableAnalytics                  types.Bool   `tfsdk:"enable_analytics"`
	EnableAutoCreate                 types.Bool   `tfsdk:"enable_auto_create"`
	EnableRemoteUnmute               types.Bool   `tfsdk:"enable_remote_unmute"`
	EnabledCodecs                    types.Set    `tfsdk:"enabled_codecs"`
	PinnedRegions                    types.Set    `tfsdk:"pinned_regions"`
	EnableUserDataRecording          types.Bool   `tfsdk:"enable_user_data_recording"`
	EnableUserDataTraining           types.Bool   `tfsdk:"enable_user_data_training"`
	RequireExplicitDispatch          types.Bool   `tfsdk:"require_explicit_dispatch"`
	EnablePIIRedaction               types.Bool   `tfsdk:"enable_pii_redaction"`
	PIIRedactionCategories           types.Set    `tfsdk:"pii_redaction_categories"`
	EnableInferenceRegionRestriction types.Bool   `tfsdk:"enable_inference_region_restriction"`
	DisableTokenEndpoint             types.Bool   `tfsdk:"disable_token_endpoint"`
	SFUAllowPause                    types.Bool   `tfsdk:"sfu_allow_pause"`
	EnableEgressBackupStorage        types.Bool   `tfsdk:"enable_egress_backup_storage"`
	EnableEgressAutoRetry            types.Bool   `tfsdk:"enable_egress_auto_retry"`
	Subdomain                        types.String `tfsdk:"subdomain"`
	URL                              types.String `tfsdk:"url"`
	WorkspaceID                      types.String `tfsdk:"workspace_id"`
	UserDataRegion                   types.String `tfsdk:"user_data_region"`
	CreatedAt                        types.String `tfsdk:"created_at"`
}

// fill copies every setting the API reports.
func (m *projectSettingsModel) fill(ctx context.Context, p *livekit.Project, diags *diag.Diagnostics) {
	m.ID = types.StringValue(p.ID)
	m.ProjectID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	m.EnableAnalytics = types.BoolValue(p.EnableAnalytics)
	m.EnableAutoCreate = types.BoolValue(p.EnableAutoCreate)
	m.EnableRemoteUnmute = types.BoolValue(p.EnableRemoteUnmute)
	m.EnabledCodecs = knownStringSet(ctx, p.EnabledCodecs, diags)
	m.PinnedRegions = knownStringSet(ctx, p.PinnedRegions, diags)
	m.EnableUserDataRecording = types.BoolValue(p.EnableUserDataRecording)
	m.EnableUserDataTraining = types.BoolValue(p.EnableUserDataTraining)
	m.RequireExplicitDispatch = types.BoolValue(p.RequireExplicitDispatch)
	m.EnablePIIRedaction = types.BoolValue(p.EnablePIIRedaction)
	m.PIIRedactionCategories = knownStringSet(ctx, mapStrings(p.PIIRedactionCategories, piiFromAPI), diags)
	m.EnableInferenceRegionRestriction = types.BoolValue(p.EnableInferenceRegionRestriction)
	m.DisableTokenEndpoint = types.BoolValue(p.Preferences.DisableTokenEndpoint)
	m.SFUAllowPause = types.BoolValue(p.Preferences.SFUAllowPause)
	m.EnableEgressBackupStorage = types.BoolValue(p.Preferences.EnableEgressBackupStorage)
	m.EnableEgressAutoRetry = types.BoolValue(p.Preferences.EnableEgressAutoRetry)
	m.Subdomain = types.StringValue(p.Subdomain)
	m.URL = types.StringValue(p.URL)
	m.WorkspaceID = types.StringValue(p.WorkspaceID)
	m.UserDataRegion = types.StringValue(p.UserDataRegion)
	m.CreatedAt = types.StringValue(p.CreatedAt)
}

// update builds the change from the configuration: only the arguments that
// are set, so settings the configuration leaves out are not touched.
func (m *projectSettingsModel) update(ctx context.Context, diags *diag.Diagnostics) livekit.ProjectUpdate {
	b := func(v types.Bool) *bool {
		if v.IsNull() || v.IsUnknown() {
			return nil
		}
		return ptr(v.ValueBool())
	}
	u := livekit.ProjectUpdate{
		EnableAnalytics:                  b(m.EnableAnalytics),
		EnableAutoCreate:                 b(m.EnableAutoCreate),
		EnableRemoteUnmute:               b(m.EnableRemoteUnmute),
		EnabledCodecs:                    setStrings(ctx, m.EnabledCodecs, diags),
		EnableUserDataRecording:          b(m.EnableUserDataRecording),
		EnableUserDataTraining:           b(m.EnableUserDataTraining),
		RequireExplicitDispatch:          b(m.RequireExplicitDispatch),
		EnablePIIRedaction:               b(m.EnablePIIRedaction),
		PIIRedactionCategories:           mapStrings(setStrings(ctx, m.PIIRedactionCategories, diags), piiToAPI),
		EnableInferenceRegionRestriction: b(m.EnableInferenceRegionRestriction),
		DisableTokenEndpoint:             b(m.DisableTokenEndpoint),
		SFUAllowPause:                    b(m.SFUAllowPause),
		EnableEgressBackupStorage:        b(m.EnableEgressBackupStorage),
		EnableEgressAutoRetry:            b(m.EnableEgressAutoRetry),
	}
	if !m.Name.IsNull() && !m.Name.IsUnknown() {
		u.Name = ptr(m.Name.ValueString())
	}
	if !m.PinnedRegions.IsNull() && !m.PinnedRegions.IsUnknown() {
		u.PinnedRegions = &livekit.PinnedRegions{Regions: setStrings(ctx, m.PinnedRegions, diags)}
		if u.PinnedRegions.Regions == nil {
			u.PinnedRegions.Regions = []string{}
		}
	}
	return u
}

func (r *projectSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_settings"
}

func (r *projectSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	setting := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: desc + " Left as it is when not set.",
			Optional:            true,
			Computed:            true,
			PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		}
	}
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: desc,
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
	}
	projectID := projectIDAttribute("settings")
	projectID.MarkdownDescription = "Project (`p_...`) whose settings to manage. Defaults to the provider's `project_id`."
	resp.Schema = schema.Schema{
		MarkdownDescription: "The settings of an existing LiveKit Cloud project. The project itself is not created " +
			"or deleted: creating this resource changes the settings that are set, and destroying it only stops " +
			"managing them. Settings left out of the configuration are not changed, and they are not watched " +
			"for drift. Changing them needs the admin role (`3`) in the project.",
		Attributes: map[string]schema.Attribute{
			"id":         computed("The project ID."),
			"project_id": projectID,
			"name": schema.StringAttribute{
				MarkdownDescription: "The project name. Left as it is when not set.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enable_analytics":     setting("Collect analytics for the project."),
			"enable_auto_create":   setting("Let the first participant to join a room that does not exist yet create it."),
			"enable_remote_unmute": setting("Let an admin unmute the tracks of another participant."),
			"enabled_codecs": schema.SetAttribute{
				MarkdownDescription: "Media codecs the project allows, by MIME type, such as `video/VP8` and " +
					"`audio/opus`. The API cannot clear the list, so it needs at least one. Left as it is when not set.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(codecPattern, "must be a MIME type such as `video/VP8`")),
				},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"pinned_regions": schema.SetAttribute{
				MarkdownDescription: "SFU regions to pin the project's traffic to, such as `us`. An empty set removes " +
					"the pins. Left as it is when not set. This is not where session data is kept; see `user_data_region`.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"enable_user_data_recording": setting("Record traces, transcripts and audio of agent sessions for agent observability. Data recorded earlier stays readable when it is turned off."),
			"enable_user_data_training":  setting("Let LiveKit use the project's observability data to improve models and services."),
			"require_explicit_dispatch":  setting("Reject an agent server that registers without an explicit agent name, so an accidental auto-dispatch server cannot connect."),
			"enable_pii_redaction":       setting("Redact PII from recorded session data. Turned on with no saved categories, it redacts LiveKit's default set; see `pii_redaction_categories`."),
			"pii_redaction_categories": schema.SetAttribute{
				MarkdownDescription: "PII categories to redact, by the short names the LiveKit CLI uses, such as `ssn`, " +
					"`credit-card` and `email-address`. The API cannot clear the list, so it needs at least one, and " +
					"turning redaction off keeps it. Left as it is when not set.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(piiCategories...)),
				},
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"enable_inference_region_restriction": setting("Restrict LiveKit Inference to models in the project's data region."),
			"disable_token_endpoint":              setting("Turn off the project's sandbox token endpoint."),
			"sfu_allow_pause":                     setting("Let the SFU pause video tracks when subscribers are congested."),
			"enable_egress_backup_storage":        setting("Keep an egress that fails to upload in LiveKit backup storage for 7 days."),
			"enable_egress_auto_retry":            setting("Retry a failed egress automatically."),
			"subdomain":                           computed("The project subdomain, which forms `url`."),
			"url":                                 computed("The project's LiveKit server URL (`wss://...`)."),
			"workspace_id":                        computed("The workspace that holds the project (`wo_...`)."),
			"user_data_region":                    computed("Where the project keeps session data. It is set when the project is created and cannot change."),
			"created_at":                          computed("When the project was created, as an RFC 3339 timestamp."),
		},
	}
}

func (r *projectSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectSettingsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

// ValidateConfig refuses categories without redaction being on somewhere:
// they would be saved but do nothing, which is rarely what is meant.
func (r *projectSettingsResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg projectSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !cfg.PIIRedactionCategories.IsNull() && !cfg.EnablePIIRedaction.IsUnknown() && !cfg.EnablePIIRedaction.ValueBool() {
		resp.Diagnostics.AddAttributeWarning(path.Root("pii_redaction_categories"), "PII redaction is not turned on",
			"`pii_redaction_categories` is set, but `enable_pii_redaction` is not `true` here, so the categories may not be redacted.")
	}
}

// apply sends the configured settings and stores what the API reports.
func (r *projectSettingsResource) apply(ctx context.Context, cfg, plan *projectSettingsModel, state *projectSettingsModel, diags *diag.Diagnostics, action string) {
	u := cfg.update(ctx, diags)
	if diags.HasError() {
		return
	}
	id := plan.ProjectID.ValueString()
	project, err := r.data.client.UpdateProject(ctx, id, u)
	if err != nil {
		diags.AddError("Unable to "+action+" project settings", err.Error())
		return
	}
	state.fill(ctx, project, diags)
}

func (r *projectSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var cfg, plan, state projectSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &cfg, &plan, &state, &resp.Diagnostics, "apply")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.data.client.GetProject(ctx, state.ProjectID.ValueString())
	if err != nil {
		if livekit.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read project settings", err.Error())
		return
	}
	state.fill(ctx, project, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var cfg, plan, state projectSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &cfg, &plan, &state, &resp.Diagnostics, "update")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete leaves the project and its settings as they are.
func (r *projectSettingsResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("Project settings left in place",
		"The project's settings were not changed; Terraform only stopped managing them.")
}

// ImportState takes the project ID.
func (r *projectSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !projectIDPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a project ID such as `p_abc123`, got \""+req.ID+"\".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
