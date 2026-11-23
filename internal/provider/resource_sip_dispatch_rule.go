// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// trunkOrNumberIDPattern is a trunk (`ST_`) or phone number (`PN_`) ID. The
// API accepts IDs that do not exist (observed 2026-11-22).
var trunkOrNumberIDPattern = regexp.MustCompile(`^(ST|PN)_[A-Za-z0-9]+$`)

type sipDispatchRuleModel struct {
	ID              types.String `tfsdk:"id"`
	ProjectID       types.String `tfsdk:"project_id"`
	Name            types.String `tfsdk:"name"`
	Metadata        types.String `tfsdk:"metadata"`
	TrunkIDs        types.Set    `tfsdk:"trunk_ids"`
	Numbers         types.Set    `tfsdk:"numbers"`
	InboundNumbers  types.Set    `tfsdk:"inbound_numbers"`
	HidePhoneNumber types.Bool   `tfsdk:"hide_phone_number"`
	Attributes      types.Map    `tfsdk:"attributes"`
	RoomPreset      types.String `tfsdk:"room_preset"`
	KrispEnabled    types.Bool   `tfsdk:"krisp_enabled"`
	MediaEncryption types.String `tfsdk:"media_encryption"`
	Direct          types.Object `tfsdk:"direct"`
	Individual      types.Object `tfsdk:"individual"`
	Callee          types.Object `tfsdk:"callee"`
	Agents          types.List   `tfsdk:"agents"`
}

// The API's rule types, by their attribute in the resource.
var dispatchRuleTypes = []struct {
	attr, api string
	attrTypes map[string]attr.Type
}{
	{"direct", "dispatchRuleDirect", map[string]attr.Type{"room_name": types.StringType, "pin": types.StringType}},
	{"individual", "dispatchRuleIndividual", map[string]attr.Type{"room_prefix": types.StringType, "pin": types.StringType, "no_randomness": types.BoolType}},
	{"callee", "dispatchRuleCallee", map[string]attr.Type{"room_prefix": types.StringType, "pin": types.StringType, "randomize": types.BoolType}},
}

// The fields of each rule type: attribute name -> API name.
var dispatchRuleFields = map[string]string{
	"room_name": "roomName", "room_prefix": "roomPrefix", "pin": "pin",
	"no_randomness": "noRandomness", "randomize": "randomize",
}

var agentAttrTypes = map[string]attr.Type{"agent_name": types.StringType, "metadata": types.StringType}

func pinAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "A PIN the caller must enter before joining. The API returns it in clear; it is " +
			"stored in the state.",
		Optional:   true,
		Sensitive:  true,
		Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]+$`), "must be digits")},
	}
}

func roomPrefixAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Prefix of the names of the rooms the rule creates. Defaults to `\"\"`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(""),
	}
}

func newSIPDispatchRuleResource() resource.Resource {
	return &sipResource[sipDispatchRuleModel]{spec: sipSpec[sipDispatchRuleModel]{
		kind:     livekit.SIPDispatchRule,
		typeName: "sip_dispatch_rule",
		ids: func(m *sipDispatchRuleModel) (*types.String, *types.String) {
			return &m.ID, &m.ProjectID
		},
		configValidators: []resource.ConfigValidator{
			resourcevalidator.ExactlyOneOf(path.MatchRoot("direct"), path.MatchRoot("individual"), path.MatchRoot("callee")),
		},
		schema: schema.Schema{
			MarkdownDescription: "A SIP dispatch rule: which room an inbound call joins, and which agents are " +
				"dispatched to it. Set exactly one of `direct`, `individual` and `callee`. The API refuses a rule " +
				"that conflicts with another, such as one for the same trunks, numbers and PIN." + sipReplaceNote,
			Attributes: map[string]schema.Attribute{
				"id":         sipIDAttribute("Dispatch rule ID (`SDR_...`)."),
				"project_id": projectIDAttribute("dispatch rule"),
				"name":       sipNameAttribute("rule"),
				"metadata":   sipMetadataAttribute("rule; participants the rule creates inherit it"),
				"trunk_ids": schema.SetAttribute{
					MarkdownDescription: "Inbound trunks (`ST_...`) and phone numbers (`PN_...`) the rule applies to. " +
						"Not set, every trunk and number. The API does not check that they exist. To send a " +
						"`livekit_phone_number`'s calls to this rule, add its `id` here.",
					ElementType: types.StringType,
					Optional:    true,
					Validators: []validator.Set{setvalidator.ValueStringsAre(
						stringvalidator.RegexMatches(trunkOrNumberIDPattern, "must be a trunk (ST_...) or phone number (PN_...) ID"))},
				},
				"numbers":         phoneNumberSet("Accept only calls to these numbers. Not set, any."),
				"inbound_numbers": phoneNumberSet("Accept only calls from these numbers. Not set, any caller."),
				"hide_phone_number": schema.BoolAttribute{
					MarkdownDescription: "Hide the caller's number from the room. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
				"attributes": schema.MapAttribute{
					MarkdownDescription: "Attributes the participants the rule creates inherit.",
					ElementType:         types.StringType,
					Optional:            true,
				},
				"room_preset": schema.StringAttribute{
					MarkdownDescription: "Name of a room configuration preset of the project to create rooms with. " +
						"Defaults to `\"\"`.",
					Optional: true,
					Computed: true,
					Default:  stringdefault.StaticString(""),
				},
				"krisp_enabled": schema.BoolAttribute{
					MarkdownDescription: "Apply Krisp noise cancellation to callers. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
				"media_encryption": mediaEncryptionEnum.attribute("Media encryption (SRTP) for calls."),
				"direct": schema.SingleNestedAttribute{
					MarkdownDescription: "Put every caller into one existing room.",
					Optional:            true,
					Attributes: map[string]schema.Attribute{
						"room_name": schema.StringAttribute{
							MarkdownDescription: "The room callers join.",
							Required:            true,
							Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						},
						"pin": pinAttribute(),
					},
				},
				"individual": schema.SingleNestedAttribute{
					MarkdownDescription: "Put each caller into a new room of their own.",
					Optional:            true,
					Attributes: map[string]schema.Attribute{
						"room_prefix": roomPrefixAttribute(),
						"pin":         pinAttribute(),
						"no_randomness": schema.BoolAttribute{
							MarkdownDescription: "Name rooms after the caller only, without a random suffix. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
					},
				},
				"callee": schema.SingleNestedAttribute{
					MarkdownDescription: "Put callers into a room per number called.",
					Optional:            true,
					Attributes: map[string]schema.Attribute{
						"room_prefix": roomPrefixAttribute(),
						"pin":         pinAttribute(),
						"randomize": schema.BoolAttribute{
							MarkdownDescription: "Add a random suffix to room names. Defaults to `false`.",
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
						},
					},
				},
				"agents": schema.ListNestedAttribute{
					MarkdownDescription: "Agents dispatched to the rooms the rule creates, in the room configuration's " +
						"`agents`. Not set, the rule dispatches none. Other room configuration fields are kept.",
					Optional:   true,
					Validators: []validator.List{listvalidator.SizeAtLeast(1)},
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"agent_name": schema.StringAttribute{
								MarkdownDescription: "Name the agent registered with.",
								Required:            true,
								Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
							},
							"metadata": schema.StringAttribute{
								MarkdownDescription: "Metadata passed to the agent's job. Defaults to `\"\"`.",
								Optional:            true,
								Computed:            true,
								Default:             stringdefault.StaticString(""),
							},
						},
					},
				},
			},
		},
		write: writeDispatchRule,
		read:  readDispatchRule,
	}}
}

func writeDispatchRule(ctx context.Context, m *sipDispatchRuleModel, o livekit.SIPObject, diags *diag.Diagnostics) {
	w := sipWriter{o, ctx, diags}
	w.str("name", m.Name)
	w.str("metadata", m.Metadata)
	w.set("trunkIds", m.TrunkIDs)
	w.set("numbers", m.Numbers)
	w.set("inboundNumbers", m.InboundNumbers)
	w.boolean("hidePhoneNumber", m.HidePhoneNumber)
	w.strMap("attributes", m.Attributes)
	w.str("roomPreset", m.RoomPreset)
	w.boolean("krispEnabled", m.KrispEnabled)
	w.enum("mediaEncryption", mediaEncryptionEnum, m.MediaEncryption)

	rule := map[string]map[string]any{}
	for i, t := range dispatchRuleTypes {
		obj := []types.Object{m.Direct, m.Individual, m.Callee}[i]
		if obj.IsNull() || obj.IsUnknown() {
			continue
		}
		fields := map[string]any{}
		for name, v := range obj.Attributes() {
			switch v := v.(type) {
			case basetypes.StringValue:
				fields[dispatchRuleFields[name]] = v.ValueString()
			case basetypes.BoolValue:
				fields[dispatchRuleFields[name]] = v.ValueBool()
			}
		}
		rule[t.api] = fields
	}
	w.raw("rule", rule)

	// The agents live in the room configuration; its other fields stay.
	roomConfig := map[string]json.RawMessage{}
	if raw, ok := o["roomConfig"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &roomConfig); err != nil {
			diags.AddError("Unable to decode roomConfig", err.Error())
			return
		}
	}
	agents := []map[string]string{}
	if !m.Agents.IsNull() && !m.Agents.IsUnknown() {
		for _, a := range m.Agents.Elements() {
			attrs := a.(types.Object).Attributes()
			agents = append(agents, map[string]string{
				"agentName": attrs["agent_name"].(types.String).ValueString(),
				"metadata":  attrs["metadata"].(types.String).ValueString(),
			})
		}
	}
	b, _ := json.Marshal(agents)
	roomConfig["agents"] = b
	w.raw("roomConfig", roomConfig)
}

func readDispatchRule(ctx context.Context, o livekit.SIPObject, m *sipDispatchRuleModel, diags *diag.Diagnostics) {
	r := sipReader{o, ctx, diags}
	m.Name = r.str("name")
	m.Metadata = r.str("metadata")
	m.TrunkIDs = r.set("trunkIds", m.TrunkIDs)
	m.Numbers = r.set("numbers", m.Numbers)
	m.InboundNumbers = r.set("inboundNumbers", m.InboundNumbers)
	m.HidePhoneNumber = r.boolean("hidePhoneNumber")
	m.Attributes = r.strMap("attributes", m.Attributes)
	m.RoomPreset = r.str("roomPreset")
	m.KrispEnabled = r.boolean("krispEnabled")
	m.MediaEncryption = r.enum("mediaEncryption", mediaEncryptionEnum)

	var rule map[string]map[string]json.RawMessage
	r.decode("rule", &rule)
	objs := []*types.Object{&m.Direct, &m.Individual, &m.Callee}
	for i, t := range dispatchRuleTypes {
		fields, ok := rule[t.api]
		if !ok {
			*objs[i] = types.ObjectNull(t.attrTypes)
			continue
		}
		vals := map[string]attr.Value{}
		for name, typ := range t.attrTypes {
			raw := fields[dispatchRuleFields[name]]
			if typ == types.BoolType {
				var b bool
				_ = json.Unmarshal(raw, &b)
				vals[name] = types.BoolValue(b)
				continue
			}
			var s string
			_ = json.Unmarshal(raw, &s)
			vals[name] = types.StringValue(s)
			if name == "pin" {
				vals[name] = emptyToNull(types.StringValue(s))
			}
		}
		obj, d := types.ObjectValue(t.attrTypes, vals)
		diags.Append(d...)
		*objs[i] = obj
	}

	var roomConfig struct {
		Agents []struct {
			AgentName string `json:"agentName"`
			Metadata  string `json:"metadata"`
		} `json:"agents"`
	}
	r.decode("roomConfig", &roomConfig)
	agentType := types.ObjectType{AttrTypes: agentAttrTypes}
	if len(roomConfig.Agents) == 0 {
		m.Agents = types.ListNull(agentType)
		return
	}
	var agents []attr.Value
	for _, a := range roomConfig.Agents {
		obj, d := types.ObjectValue(agentAttrTypes, map[string]attr.Value{
			"agent_name": types.StringValue(a.AgentName), "metadata": types.StringValue(a.Metadata),
		})
		diags.Append(d...)
		agents = append(agents, obj)
	}
	list, d := types.ListValue(agentType, agents)
	diags.Append(d...)
	m.Agents = list
}
