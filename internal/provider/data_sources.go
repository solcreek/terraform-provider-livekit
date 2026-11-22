// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// dataSourceProjectAttribute is the optional `project_id` of a data source.
func dataSourceProjectAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project (`p_...`). Defaults to the provider's `project_id`.",
		Optional:            true,
		Computed:            true,
	}
}

// ------------------------------------------------------------ livekit_project

var _ datasource.DataSourceWithConfigure = (*projectDataSource)(nil)

type projectDataSource struct{ data *providerData }

func newProjectDataSource() datasource.DataSource { return &projectDataSource{} }

type projectDataModel struct {
	ProjectID      types.String `tfsdk:"project_id"`
	Name           types.String `tfsdk:"name"`
	Subdomain      types.String `tfsdk:"subdomain"`
	URL            types.String `tfsdk:"url"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	UserDataRegion types.String `tfsdk:"user_data_region"`
	IsPrivate      types.Bool   `tfsdk:"is_private"`
	CreatedAt      types.String `tfsdk:"created_at"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A LiveKit Cloud project: its name and the server URL clients connect to. Useful to " +
			"pass `url` on, and to check that a configuration targets the intended project.",
		Attributes: map[string]schema.Attribute{
			"project_id":       dataSourceProjectAttribute(),
			"name":             str("The project name."),
			"subdomain":        str("The project subdomain, which forms `url`."),
			"url":              str("The project's LiveKit server URL (`wss://...`), the `LIVEKIT_URL` of its servers and clients."),
			"workspace_id":     str("The workspace that holds the project (`wo_...`)."),
			"user_data_region": str("Where the project keeps session data."),
			"is_private":       schema.BoolAttribute{MarkdownDescription: "Whether the project keeps its own member list instead of its workspace's.", Computed: true},
			"created_at":       str("When the project was created, as an RFC 3339 timestamp."),
		},
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m projectDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	id := dataSourceProjectID(m.ProjectID, d.data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.data.client.GetProject(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	m.ProjectID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	m.Subdomain = types.StringValue(p.Subdomain)
	m.URL = types.StringValue(p.URL)
	m.WorkspaceID = types.StringValue(p.WorkspaceID)
	m.UserDataRegion = types.StringValue(p.UserDataRegion)
	m.IsPrivate = types.BoolValue(p.IsPrivate)
	m.CreatedAt = types.StringValue(p.CreatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ------------------------------------------------------ livekit_webhook_events

var _ datasource.DataSourceWithConfigure = (*webhookEventsDataSource)(nil)

type webhookEventsDataSource struct{ data *providerData }

func newWebhookEventsDataSource() datasource.DataSource { return &webhookEventsDataSource{} }

type webhookEventsModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	Events    types.List   `tfsdk:"events"`
}

func (d *webhookEventsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook_events"
}

func (d *webhookEventsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The event names a project's webhooks can filter on, for `livekit_webhook`'s " +
			"`include_events` and `exclude_events`.",
		Attributes: map[string]schema.Attribute{
			"project_id": dataSourceProjectAttribute(),
			"events": schema.ListAttribute{
				MarkdownDescription: "Event names, such as `room_started`, in the API's order.",
				ElementType:         types.StringType,
				Computed:            true,
			},
		},
	}
}

func (d *webhookEventsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *webhookEventsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m webhookEventsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	id := dataSourceProjectID(m.ProjectID, d.data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	events, err := d.data.client.ListWebhookEvents(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list webhook events", err.Error())
		return
	}
	if events == nil {
		events = []string{}
	}
	m.ProjectID = types.StringValue(id)
	list, diags := types.ListValueFrom(ctx, types.StringType, events)
	resp.Diagnostics.Append(diags...)
	m.Events = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ----------------------------------------------------------- livekit_api_keys

var _ datasource.DataSourceWithConfigure = (*apiKeysDataSource)(nil)

type apiKeysDataSource struct{ data *providerData }

func newAPIKeysDataSource() datasource.DataSource { return &apiKeysDataSource{} }

type apiKeysModel struct {
	ProjectID types.String `tfsdk:"project_id"`
	Keys      types.List   `tfsdk:"keys"`
}

var apiKeyAttrTypes = map[string]attr.Type{
	"id":              types.StringType,
	"description":     types.StringType,
	"service_account": types.BoolType,
	"owner_email":     types.StringType,
	"created_at":      types.StringType,
}

func (d *apiKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_keys"
}

func (d *apiKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The API keys of a project, oldest first, without their secrets and without the keys " +
			"of cloud agents. Useful to pick a webhook's `signing_key`, or to audit which keys exist.",
		Attributes: map[string]schema.Attribute{
			"project_id": dataSourceProjectAttribute(),
			"keys": schema.ListNestedAttribute{
				MarkdownDescription: "The keys.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":              schema.StringAttribute{MarkdownDescription: "The API key (`API...`).", Computed: true},
						"description":     schema.StringAttribute{MarkdownDescription: "The key's label.", Computed: true},
						"service_account": schema.BoolAttribute{MarkdownDescription: "Whether the key has no owner and belongs to the project.", Computed: true},
						"owner_email":     schema.StringAttribute{MarkdownDescription: "Email of the key's owner; null for a service account key.", Computed: true},
						"created_at":      schema.StringAttribute{MarkdownDescription: "When the key was created.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *apiKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (d *apiKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m apiKeysModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	id := dataSourceProjectID(m.ProjectID, d.data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	keys, err := d.data.client.ListKeys(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list API keys", err.Error())
		return
	}
	objs := make([]attr.Value, 0, len(keys))
	for _, k := range keys {
		obj, diags := types.ObjectValue(apiKeyAttrTypes, map[string]attr.Value{
			"id":              types.StringValue(k.APIKey),
			"description":     types.StringValue(k.Description),
			"service_account": types.BoolValue(k.OwnerID == ""),
			"owner_email":     stringOrNull(k.OwnerEmail),
			"created_at":      types.StringValue(k.CreatedAt),
		})
		resp.Diagnostics.Append(diags...)
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: apiKeyAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	m.ProjectID = types.StringValue(id)
	m.Keys = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
