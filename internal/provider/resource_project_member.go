// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var (
	_ resource.ResourceWithConfigure   = (*projectMemberResource)(nil)
	_ resource.ResourceWithImportState = (*projectMemberResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*projectMemberResource)(nil)
)

// roles are the member roles by name; the API numbers them 1 to 3.
var roles = []string{"read", "write", "admin"}

func roleName(n int) string {
	if n >= 1 && n <= len(roles) {
		return roles[n-1]
	}
	return fmt.Sprintf("unknown-%d", n)
}

func roleNumber(name string) int {
	for i, r := range roles {
		if r == name {
			return i + 1
		}
	}
	return 0
}

type projectMemberResource struct {
	data *providerData
}

func newProjectMemberResource() resource.Resource { return &projectMemberResource{} }

type projectMemberModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	UserID    types.String `tfsdk:"user_id"`
	Role      types.String `tfsdk:"role"`
	Email     types.String `tfsdk:"email"`
}

func (m *projectMemberModel) fill(mem *livekit.Member) {
	m.ID = types.StringValue(m.ProjectID.ValueString() + "/" + mem.UserID)
	m.UserID = types.StringValue(mem.UserID)
	m.Role = types.StringValue(roleName(mem.Role))
	m.Email = types.StringValue(mem.Email)
}

func (r *projectMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_member"
}

func (r *projectMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A member of a **private** project (`is_private` on `data.livekit_project`) and their " +
			"role. The user must already be a member of the project's workspace; `data.livekit_workspace_members` " +
			"lists them. A project that is not private takes its members from its workspace, where a change would " +
			"reach every project in it, so this resource refuses one. It also refuses to remove the user the " +
			"session belongs to, and a member who owns API keys in the project: removing them would delete their " +
			"keys. An existing member is adopted only through import.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<project_id>/<user_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": projectIDAttribute("membership"),
			"user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user. Changing it replaces the membership.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				MarkdownDescription: "The member's role: `read`, `write` or `admin`. A plan that allows only admins " +
					"refuses the others.",
				Required:   true,
				Validators: []validator.String{stringvalidator.OneOf(roles...)},
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "The user's email address.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *projectMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectMemberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

// requirePrivate refuses a project that takes its members from its
// workspace.
func (r *projectMemberResource) requirePrivate(ctx context.Context, projectID, action string, diags *diag.Diagnostics) bool {
	p, err := r.data.client.GetProject(ctx, projectID)
	if err != nil {
		diags.AddError("Unable to read project", err.Error())
		return false
	}
	if !p.IsPrivate {
		diags.AddError("Project is not private",
			fmt.Sprintf("Project %s takes its members from its workspace, so %s a member there would change the "+
				"user's role on the whole workspace and every project in it. livekit_project_member only manages "+
				"members of private projects.", projectID, action))
		return false
	}
	return true
}

func (r *projectMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, userID := plan.ProjectID.ValueString(), plan.UserID.ValueString()
	if !r.requirePrivate(ctx, projectID, "adding", &resp.Diagnostics) {
		return
	}
	// Adding a user who is a member already silently changes their role, so
	// look first: an existing membership comes in only through import.
	switch existing, err := r.data.client.GetMember(ctx, projectID, userID); {
	case err == nil:
		resp.Diagnostics.AddError("Already a member",
			fmt.Sprintf("%s is already a member of project %s (role %s). Import the membership instead: "+
				"terraform import <address> %s/%s", existing.Email, projectID, roleName(existing.Role), projectID, userID))
		return
	case !livekit.IsNotFound(err):
		resp.Diagnostics.AddError("Unable to read member", err.Error())
		return
	}
	if err := r.data.client.AddWorkspaceMember(ctx, projectID, userID, roleNumber(plan.Role.ValueString())); err != nil {
		resp.Diagnostics.AddError("Unable to add member", err.Error())
		return
	}
	mem, err := r.data.client.GetMember(ctx, projectID, userID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read the new member", err.Error())
		return
	}
	plan.fill(mem)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mem, err := r.data.client.GetMember(ctx, state.ProjectID.ValueString(), state.UserID.ValueString())
	if err != nil {
		if livekit.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read member", err.Error())
		return
	}
	state.fill(mem)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := plan.ProjectID.ValueString()
	if !r.requirePrivate(ctx, projectID, "changing", &resp.Diagnostics) {
		return
	}
	mem, err := r.data.client.UpdateMemberRole(ctx, projectID, plan.UserID.ValueString(), roleNumber(plan.Role.ValueString()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to change the member's role", err.Error())
		return
	}
	plan.fill(mem)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, userID := state.ProjectID.ValueString(), state.UserID.ValueString()
	me, err := r.data.client.CurrentUser(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read the signed-in user", err.Error())
		return
	}
	if me.ID == userID {
		resp.Diagnostics.AddError("Refusing to remove yourself",
			"This membership belongs to the user whose session Terraform runs as; removing it would lock that user "+
				"out of the project. Remove it from the state with `terraform state rm`, or from the dashboard.")
		return
	}
	if !r.requirePrivate(ctx, projectID, "removing", &resp.Diagnostics) {
		return
	}
	err = r.data.client.RemoveMember(ctx, projectID, userID)
	switch {
	case err == nil || livekit.IsNotFound(err):
	case livekit.StatusCode(err) == 400:
		resp.Diagnostics.AddError("Unable to remove member",
			err.Error()+"\n\nThe member owns API keys in the project, which removing them would delete. Delete or "+
				"hand over those keys first; the provider never deletes keys as a side effect.")
	default:
		resp.Diagnostics.AddError("Unable to remove member", err.Error())
	}
}

// ImportState adopts a membership by "<project_id>/<user_id>".
func (r *projectMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importProjectChild(ctx, req, resp, "user_id")
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	}
}
