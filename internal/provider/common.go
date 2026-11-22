// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// projectIDPattern is the shape of a project ID.
var projectIDPattern = regexp.MustCompile(`^p_[A-Za-z0-9]+$`)

// codecPattern is the shape of a codec MIME type. The API answers an unknown
// codec with a 500 (observed 2026-11-22), so the shape is checked here and
// the name is left to the API.
var codecPattern = regexp.MustCompile(`^(audio|video)/[A-Za-z0-9.+-]+$`)

// httpURLPattern is a loose shape check for an http(s) URL. The API also
// requires a public address.
var httpURLPattern = regexp.MustCompile(`^https?://[^\s/]+\S*$`)

const piiCategoryPrefix = "PII_REDACTION_CATEGORY_"

// piiCategories are the PII redaction categories by their short names, as
// the LiveKit CLI spells them (`credit-card`), from the OpenAPI enum
// cloud_protocol.PIIRedactionCategory (2026-11-22).
var piiCategories = []string{
	"account-number", "age", "bank-account", "date", "dob", "driver-license", "education-level",
	"email-address", "government-id", "healthcare-number", "ip-address", "license-plate", "location",
	"location-address", "location-city", "location-coordinate", "location-country", "location-state",
	"location-zip", "name", "name-given", "name-family", "numerical-pii", "occupation", "origin",
	"passport-number", "password", "phone-number", "routing-number", "ssn", "swift-code", "url",
	"username", "vehicle-id", "credit-card", "credit-card-expiration", "cvv", "language", "organization",
	"political-affiliation", "religion",
}

// piiToAPI turns `credit-card` into PII_REDACTION_CATEGORY_CREDIT_CARD.
func piiToAPI(short string) string {
	return piiCategoryPrefix + strings.ToUpper(strings.ReplaceAll(short, "-", "_"))
}

// piiFromAPI turns PII_REDACTION_CATEGORY_CREDIT_CARD into `credit-card`.
func piiFromAPI(full string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(full, piiCategoryPrefix)), "_", "-")
}

// mapStrings applies f to each element.
func mapStrings(in []string, f func(string) string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

// stringOrNull maps an empty string onto Terraform's null.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// stringSet converts strings to a set value. With nothing to hold it
// returns null, unless prior was a known (then empty) set: a configured
// `[]` and an omitted argument both read back as an empty list.
func stringSet(ctx context.Context, vals []string, prior types.Set, diags *diag.Diagnostics) types.Set {
	if len(vals) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.SetNull(types.StringType)
	}
	if vals == nil {
		vals = []string{}
	}
	out, d := types.SetValueFrom(ctx, types.StringType, vals)
	diags.Append(d...)
	return out
}

// knownStringSet converts strings to a set value that is never null.
func knownStringSet(ctx context.Context, vals []string, diags *diag.Diagnostics) types.Set {
	if vals == nil {
		vals = []string{}
	}
	out, d := types.SetValueFrom(ctx, types.StringType, vals)
	diags.Append(d...)
	return out
}

// setStrings reads a set of strings; null and unknown give nil.
func setStrings(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	return out
}

// projectIDAttribute is the `project_id` of a resource: the provider's
// default project unless set. Changing it replaces the resource; see
// planProjectID.
func projectIDAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project (`p_...`) the " + what + " belongs to. Defaults to the provider's " +
			"`project_id` when the " + what + " is created; a later change of that default does not move it. " +
			"Changing this argument replaces the " + what + ".",
		Optional: true,
		Computed: true,
	}
}

// planProjectID fills a resource's planned `project_id` from the provider's
// default when the configuration leaves it out, and replaces the resource
// when the project changes. Attribute plan modifiers cannot see the
// provider's configuration, so this runs in ModifyPlan.
func planProjectID(ctx context.Context, data *providerData, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroy
	}
	var configured, state types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project_id"), &configured)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("project_id"), &state)...)
	}
	if resp.Diagnostics.HasError() || configured.IsUnknown() {
		if !state.IsNull() {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("project_id"))
		}
		return
	}
	planned := configured.ValueString()
	if configured.IsNull() {
		switch {
		case !state.IsNull():
			// An existing object stays in its project: a changed provider
			// default must not replace (and so delete) it.
			planned = state.ValueString()
		case data != nil && data.projectID != "":
			planned = data.projectID
		case data == nil:
			return // the provider is not configured yet
		default:
			resp.Diagnostics.AddAttributeError(path.Root("project_id"), "Missing project",
				"Set `project_id` here or in the provider configuration (or LIVEKIT_PROJECT_ID).")
			return
		}
	}
	if !projectIDPattern.MatchString(planned) {
		resp.Diagnostics.AddAttributeError(path.Root("project_id"), "Invalid project ID",
			fmt.Sprintf("Expected a project ID like `p_abc123`, got %q.", planned))
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("project_id"), planned)...)
	if !state.IsNull() && state.ValueString() != planned {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("project_id"))
	}
}

// dataSourceProjectID resolves the project of a data source.
func dataSourceProjectID(configured types.String, data *providerData, diags *diag.Diagnostics) string {
	id := configured.ValueString()
	if id == "" && data != nil {
		id = data.projectID
	}
	if id == "" {
		diags.AddAttributeError(path.Root("project_id"), "Missing project",
			"Set `project_id` here or in the provider configuration (or LIVEKIT_PROJECT_ID).")
	}
	return id
}

// deletionProtectionAttribute guards an object whose loss cannot be undone
// by creating it again.
func deletionProtectionAttribute(consequence string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Refuse to delete or replace this object. Defaults to `true`, because " + consequence +
			" Apply `false` before destroying it.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(true),
	}
}

// deletionProtected adds an error and returns true when the object may not
// be deleted.
func deletionProtected(protection types.Bool, what string, diags *diag.Diagnostics) bool {
	// Null after an import, before any apply: stay on the safe side.
	if protection.IsNull() || protection.ValueBool() {
		diags.AddError(
			"Deletion protection is enabled",
			fmt.Sprintf("This %s has `deletion_protection = true`, so it is not deleted. Set `deletion_protection = false`, "+
				"apply, and then destroy or replace it.", what),
		)
		return true
	}
	return false
}

// importProjectChild parses a "<project_id>/<id>" import ID.
func importProjectChild(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, idAttr string) {
	project, id, ok := strings.Cut(req.ID, "/")
	if !ok || !projectIDPattern.MatchString(project) || id == "" || strings.Contains(id, "/") {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected `<project_id>/<%s>`, such as `p_abc123/%s`, got %q.", idAttr, "XYZ", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(idAttr), id)...)
}

func ptr[T any](v T) *T { return &v }
