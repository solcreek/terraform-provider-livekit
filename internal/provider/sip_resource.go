// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// sipSpec describes one SIP resource: its schema and how its model maps onto
// the API's raw object. Every SIP model has `id` and `project_id`.
type sipSpec[M any] struct {
	kind     livekit.SIPKind
	typeName string
	schema   schema.Schema
	// write sets the fields the resource manages on o.
	write func(ctx context.Context, m *M, o livekit.SIPObject, diags *diag.Diagnostics)
	// read fills m from o; m holds the prior values (plan or state).
	read             func(ctx context.Context, o livekit.SIPObject, m *M, diags *diag.Diagnostics)
	ids              func(m *M) (id, project *types.String)
	configValidators []resource.ConfigValidator
}

type sipResource[M any] struct {
	spec sipSpec[M]
	data *providerData
}

var (
	_ resource.ResourceWithConfigure        = (*sipResource[struct{}])(nil)
	_ resource.ResourceWithImportState      = (*sipResource[struct{}])(nil)
	_ resource.ResourceWithModifyPlan       = (*sipResource[struct{}])(nil)
	_ resource.ResourceWithConfigValidators = (*sipResource[struct{}])(nil)
)

func (r *sipResource[M]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.spec.typeName
}

func (r *sipResource[M]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.spec.schema
}

func (r *sipResource[M]) ConfigValidators(context.Context) []resource.ConfigValidator {
	return r.spec.configValidators
}

func (r *sipResource[M]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureData(req.ProviderData, &resp.Diagnostics)
}

func (r *sipResource[M]) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	planProjectID(ctx, r.data, req, resp)
}

func (r *sipResource[M]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, project := r.spec.ids(&m)
	o := livekit.SIPObject{}
	r.spec.write(ctx, &m, o, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.data.client.CreateSIP(ctx, r.spec.kind, project.ValueString(), o)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create "+r.spec.kind.Noun, err.Error())
		return
	}
	r.store(ctx, created, &m, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sipResource[M]) store(ctx context.Context, o livekit.SIPObject, m *M, diags *diag.Diagnostics) {
	id, _ := r.spec.ids(m)
	*id = types.StringValue(r.spec.kind.ID(o))
	r.spec.read(ctx, o, m, diags)
}

func (r *sipResource[M]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, project := r.spec.ids(&m)
	o, err := r.data.client.GetSIP(ctx, r.spec.kind, project.ValueString(), id.ValueString())
	if err != nil {
		if livekit.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read "+r.spec.kind.Noun, err.Error())
		return
	}
	r.store(ctx, o, &m, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Update reads the object, overwrites the fields the resource manages, and
// replaces it, so the fields the provider does not model keep their values.
func (r *sipResource[M]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, project := r.spec.ids(&m)
	current, err := r.data.client.GetSIP(ctx, r.spec.kind, project.ValueString(), id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read "+r.spec.kind.Noun, err.Error())
		return
	}
	r.spec.write(ctx, &m, current, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.data.client.ReplaceSIP(ctx, r.spec.kind, project.ValueString(), id.ValueString(), current)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update "+r.spec.kind.Noun, err.Error())
		return
	}
	r.store(ctx, updated, &m, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *sipResource[M]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m M
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, project := r.spec.ids(&m)
	err := r.data.client.DeleteSIP(ctx, r.spec.kind, project.ValueString(), id.ValueString())
	if err != nil && !livekit.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete "+r.spec.kind.Noun, err.Error())
	}
}

// ImportState adopts an existing object by "<project_id>/<id>".
func (r *sipResource[M]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importProjectChild(ctx, req, resp, "id")
}
