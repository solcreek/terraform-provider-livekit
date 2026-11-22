// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var (
	_ resource.Resource                = (*apiKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*apiKeyResource)(nil)
	_ resource.ResourceWithImportState = (*apiKeyResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*apiKeyResource)(nil)
)

type apiKeyResource struct {
	data *providerData
}

func newAPIKeyResource() resource.Resource { return &apiKeyResource{} }

type apiKeyModel struct {
	ID                 types.String `tfsdk:"id"`
	ProjectID          types.String `tfsdk:"project_id"`
	Description        types.String `tfsdk:"description"`
	ServiceAccount     types.Bool   `tfsdk:"service_account"`
	Secret             types.String `tfsdk:"secret"`
	OwnerEmail         types.String `tfsdk:"owner_email"`
	CreatedAt          types.String `tfsdk:"created_at"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
}

// fill copies what the key list reports. The secret is only in the create
// response, so the state's value stays.
func (m *apiKeyModel) fill(k *livekit.APIKey) {
	m.ID = types.StringValue(k.APIKey)
	m.Description = types.StringValue(k.Description)
	m.ServiceAccount = types.BoolValue(k.OwnerID == "")
	m.OwnerEmail = stringOrNull(k.OwnerEmail)
	m.CreatedAt = types.StringValue(k.CreatedAt)
}

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An API key of a LiveKit Cloud project, with its secret: the credential servers use to " +
			"sign access tokens and call the server API. The API has no update, so a change to `description` or " +
			"`service_account` replaces the key, which `deletion_protection` refuses by default. Needs the admin " +
			"role (`3`) in the project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The API key (`API...`), the public half of the credential.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": projectIDAttribute("API key"),
			"description": schema.StringAttribute{
				MarkdownDescription: "A label for the key, such as where it is used. Defaults to `\"\"`. Changing it " +
					"replaces the key.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString(""),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"service_account": schema.BoolAttribute{
				MarkdownDescription: "Whether the key belongs to the project rather than to you. A service account " +
					"key has no owner and stays when its creator leaves the project; a user's key does not. " +
					"Defaults to `true`. Changing it replaces the key.",
				Optional:      true,
				Computed:      true,
				Default:       booldefault.StaticBool(true),
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "The API secret. The API returns it only when the key is created, so an " +
					"imported key has none. It is stored in the state.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"owner_email": schema.StringAttribute{
				MarkdownDescription: "Email of the user who owns the key; null for a service account key.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the key was created, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"deletion_protection": deletionProtectionAttribute("deleting a key cannot be undone, and every server " +
				"and webhook that uses it stops working at once."),
		},
	}
}

func (r *apiKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *apiKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := plan.ProjectID.ValueString()
	key, err := r.data.client.GenerateKey(ctx, projectID, plan.Description.ValueString(), plan.ServiceAccount.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Unable to create API key", err.Error())
		return
	}
	// The key exists now: record it, secret included, before anything else
	// can fail. The list fills in the rest.
	plan.ID = types.StringValue(key.APIKey)
	plan.Secret = types.StringValue(key.Secret)
	plan.OwnerEmail = types.StringNull()
	plan.CreatedAt = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	listed, err := r.data.client.GetKey(ctx, projectID, key.APIKey)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read the new API key", err.Error())
		return
	}
	plan.fill(listed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key, err := r.data.client.GetKey(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil {
		if livekit.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read API key", err.Error())
		return
	}
	state.fill(key)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only ever changes deletion_protection, which lives in the state.
func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if deletionProtected(state.DeletionProtection, "API key", &resp.Diagnostics) {
		return
	}
	err := r.data.client.DeleteKey(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !livekit.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete API key", err.Error())
	}
}

// ImportState adopts an existing key by "<project_id>/<api_key>". Its secret
// stays unknown to Terraform (null).
func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importProjectChild(ctx, req, resp, "id")
}
