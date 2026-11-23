// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

var memberAttrTypes = map[string]attr.Type{
	"user_id": types.StringType,
	"email":   types.StringType,
	"role":    types.StringType,
}

func memberListAttribute(desc string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"user_id": schema.StringAttribute{MarkdownDescription: "The user's ID.", Computed: true},
			"email":   schema.StringAttribute{MarkdownDescription: "The user's email address.", Computed: true},
			"role":    schema.StringAttribute{MarkdownDescription: "`read`, `write`, `admin`, or `invited` for a pending invitee.", Computed: true},
		}},
	}
}

func memberList(members []livekit.Member, diags *diag.Diagnostics) types.List {
	objs := make([]attr.Value, 0, len(members))
	for _, m := range members {
		role := roleName(m.Role)
		if m.Role == 0 {
			role = "invited"
		}
		obj, d := types.ObjectValue(memberAttrTypes, map[string]attr.Value{
			"user_id": types.StringValue(m.UserID), "email": types.StringValue(m.Email), "role": types.StringValue(role),
		})
		diags.Append(d...)
		objs = append(objs, obj)
	}
	list, d := types.ListValue(types.ObjectType{AttrTypes: memberAttrTypes}, objs)
	diags.Append(d...)
	return list
}

// ------------------------------------------------- livekit_project_members

type projectMembersDataSource struct{ data *providerData }

func newProjectMembersDataSource() datasource.DataSource { return &projectMembersDataSource{} }

type membersModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	Members   types.List   `tfsdk:"members"`
}

func (d *projectMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_members"
}

func (d *projectMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The members of a project and their roles, ordered by user ID.",
		Attributes: map[string]schema.Attribute{
			"project_id": dataSourceProjectAttribute(),
			"members":    memberListAttribute("The members."),
		},
	}
}

func (d *projectMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *projectMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m membersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	id := dataSourceProjectID(m.ProjectID, d.data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	members, err := d.data.client.ListMembers(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list project members", err.Error())
		return
	}
	m.ProjectID = types.StringValue(id)
	m.Members = memberList(members, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ----------------------------------------------- livekit_workspace_members

type workspaceMembersDataSource struct{ data *providerData }

func newWorkspaceMembersDataSource() datasource.DataSource { return &workspaceMembersDataSource{} }

type workspaceMembersModel struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Members     types.List   `tfsdk:"members"`
}

func (d *workspaceMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_members"
}

func (d *workspaceMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The members of a workspace, ordered by user ID: the users `livekit_project_member` can " +
			"add to the workspace's private projects.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				MarkdownDescription: "Workspace (`wo_...`). Defaults to the workspace of the provider's `project_id`.",
				Optional:            true,
				Computed:            true,
			},
			"members": memberListAttribute("The members."),
		},
	}
}

func (d *workspaceMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *workspaceMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m workspaceMembersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	workspaceID := m.WorkspaceID.ValueString()
	if workspaceID == "" {
		projectID := dataSourceProjectID(types.StringNull(), d.data, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		p, err := d.data.client.GetProject(ctx, projectID)
		if err != nil {
			resp.Diagnostics.AddError("Unable to read project", err.Error())
			return
		}
		workspaceID = p.WorkspaceID
	}
	members, err := d.data.client.ListWorkspaceMembers(ctx, workspaceID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list workspace members", err.Error())
		return
	}
	m.WorkspaceID = types.StringValue(workspaceID)
	m.Members = memberList(members, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ------------------------------------- livekit_available_phone_numbers

type availableNumbersDataSource struct{ data *providerData }

func newAvailablePhoneNumbersDataSource() datasource.DataSource { return &availableNumbersDataSource{} }

type availableNumbersModel struct {
	ProjectID   types.String `tfsdk:"project_id"`
	CountryCode types.String `tfsdk:"country_code"`
	AreaCode    types.String `tfsdk:"area_code"`
	Limit       types.Int64  `tfsdk:"limit"`
	Numbers     types.List   `tfsdk:"numbers"`
}

var availableNumberAttrTypes = map[string]attr.Type{
	"phone_number": types.StringType,
	"number_type":  types.StringType,
	"locality":     types.StringType,
	"region":       types.StringType,
	"capabilities": types.ListType{ElemType: types.StringType},
}

func (d *availableNumbersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_available_phone_numbers"
}

func (d *availableNumbersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Phone numbers LiveKit has for sale. Reading it buys nothing. The list changes as " +
			"numbers sell, so pin the number you buy in `livekit_phone_number` rather than passing an element of " +
			"this list, or every refresh may plan a different purchase.",
		Attributes: map[string]schema.Attribute{
			"project_id": dataSourceProjectAttribute(),
			"country_code": schema.StringAttribute{
				MarkdownDescription: "Country, as an ISO 3166-1 alpha-2 code such as `US`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(countryOrEmptyPattern, "must be a two-letter country code such as US"), stringvalidator.LengthAtLeast(2)},
			},
			"area_code": schema.StringAttribute{
				MarkdownDescription: "Area code, such as `415`. Not set, any.",
				Optional:            true,
			},
			"limit": schema.Int64Attribute{
				MarkdownDescription: "How many numbers to return at most. Not set, the API's default.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.Between(1, 100)},
			},
			"numbers": schema.ListNestedAttribute{
				MarkdownDescription: "The numbers for sale.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"phone_number": schema.StringAttribute{MarkdownDescription: "The number in E.164 format.", Computed: true},
					"number_type":  schema.StringAttribute{MarkdownDescription: "`local`, `mobile`, `toll_free` or `unknown`.", Computed: true},
					"locality":     schema.StringAttribute{MarkdownDescription: "City or locality.", Computed: true},
					"region":       schema.StringAttribute{MarkdownDescription: "State or region.", Computed: true},
					"capabilities": schema.ListAttribute{MarkdownDescription: "Such as `voice` and `sms`.", ElementType: types.StringType, Computed: true},
				}},
			},
		},
	}
}

func (d *availableNumbersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *availableNumbersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m availableNumbersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	id := dataSourceProjectID(m.ProjectID, d.data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := d.data.client.SearchPhoneNumbers(ctx, id, m.CountryCode.ValueString(), m.AreaCode.ValueString(), int(m.Limit.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to search phone numbers", err.Error())
		return
	}
	objs := make([]attr.Value, 0, len(found))
	for _, n := range found {
		caps, diags := types.ListValueFrom(ctx, types.StringType, append([]string{}, n.Capabilities...))
		resp.Diagnostics.Append(diags...)
		obj, diags := types.ObjectValue(availableNumberAttrTypes, map[string]attr.Value{
			"phone_number": types.StringValue(n.E164),
			"number_type":  types.StringValue(shortEnum(n.NumberType, "PHONE_NUMBER_TYPE_")),
			"locality":     types.StringValue(n.Locality),
			"region":       types.StringValue(n.Region),
			"capabilities": caps,
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: availableNumberAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	m.ProjectID = types.StringValue(id)
	m.Numbers = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
