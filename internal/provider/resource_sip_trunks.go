// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

const sipReplaceNote = " Every change is made in place. Settings the provider does not model, such as media " +
	"codecs, are kept as they are."

func authUsernameAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Username for SIP digest authentication. Defaults to `\"\"`, no authentication.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(""),
	}
}

func authPasswordAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Password for SIP digest authentication. The API never returns it, so a password " +
			"changed outside Terraform is not detected; one that was removed is. It is stored in the state.",
		Optional:   true,
		Sensitive:  true,
		Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
	}
}

// ------------------------------------------------------ inbound trunk

type sipInboundTrunkModel struct {
	ID                  types.String `tfsdk:"id"`
	ProjectID           types.String `tfsdk:"project_id"`
	Name                types.String `tfsdk:"name"`
	Metadata            types.String `tfsdk:"metadata"`
	Numbers             types.Set    `tfsdk:"numbers"`
	AllowedAddresses    types.Set    `tfsdk:"allowed_addresses"`
	AllowedNumbers      types.Set    `tfsdk:"allowed_numbers"`
	AuthUsername        types.String `tfsdk:"auth_username"`
	AuthPassword        types.String `tfsdk:"auth_password"`
	AuthRealm           types.String `tfsdk:"auth_realm"`
	Headers             types.Map    `tfsdk:"headers"`
	HeadersToAttributes types.Map    `tfsdk:"headers_to_attributes"`
	AttributesToHeaders types.Map    `tfsdk:"attributes_to_headers"`
	IncludeHeaders      types.String `tfsdk:"include_headers"`
	RingingTimeout      types.Int64  `tfsdk:"ringing_timeout"`
	MaxCallDuration     types.Int64  `tfsdk:"max_call_duration"`
	KrispEnabled        types.Bool   `tfsdk:"krisp_enabled"`
	MediaEncryption     types.String `tfsdk:"media_encryption"`
}

func newSIPInboundTrunkResource() resource.Resource {
	return &sipResource[sipInboundTrunkModel]{spec: sipSpec[sipInboundTrunkModel]{
		kind: livekit.SIPInboundTrunk,
		// The API refuses a trunk open to anyone (observed 2026-11-23).
		configValidators: []resource.ConfigValidator{
			resourcevalidator.AtLeastOneOf(path.MatchRoot("numbers"), path.MatchRoot("allowed_addresses"), path.MatchRoot("auth_password")),
		},
		typeName: "sip_inbound_trunk",
		ids: func(m *sipInboundTrunkModel) (*types.String, *types.String) {
			return &m.ID, &m.ProjectID
		},
		schema: schema.Schema{
			MarkdownDescription: "An inbound SIP trunk: which calls LiveKit accepts from a SIP provider, to which " +
				"numbers and from which addresses and callers. A `livekit_sip_dispatch_rule` then decides which " +
				"room a call goes to. The API refuses a trunk that conflicts with another, such as one that " +
				"accepts the same number from the same callers, and one open to anyone: set `numbers`, " +
				"`allowed_addresses`, or `auth_username` with `auth_password`." + sipReplaceNote,
			Attributes: map[string]schema.Attribute{
				"id":         sipIDAttribute("Trunk ID (`ST_...`)."),
				"project_id": projectIDAttribute("trunk"),
				"name":       sipNameAttribute("trunk"),
				"metadata":   sipMetadataAttribute("trunk"),
				"numbers": phoneNumberSet("Numbers the trunk accepts calls to. Several trunks with different " +
					"numbers give one provider different rules."),
				"allowed_addresses": stringSetAttribute("IP addresses or CIDR ranges calls are accepted from. Not " +
					"set, from anywhere."),
				"allowed_numbers": phoneNumberSet("Caller numbers the trunk accepts. Not set, any caller."),
				"auth_username":   authUsernameAttribute(),
				"auth_password":   authPasswordAttribute(),
				"auth_realm": schema.StringAttribute{
					MarkdownDescription: "Realm for SIP digest authentication. Defaults to `\"\"`.",
					Optional:            true,
					Computed:            true,
					Default:             stringdefault.StaticString(""),
				},
				"headers": headerMap("SIP X-* headers to include in the 200 OK response, by name.", true, false),
				"headers_to_attributes": headerMap("Maps SIP X-* headers of the INVITE (keys) to participant "+
					"attributes (values).", true, false),
				"attributes_to_headers": headerMap("Maps participant attributes (keys) to SIP X-* headers (values) "+
					"sent with BYE or REFER.", false, true),
				"include_headers":   includeHeadersEnum.attribute("Which INVITE headers become `sip.h.*` participant attributes automatically."),
				"ringing_timeout":   durationAttribute("How long the caller waits for the call to be answered."),
				"max_call_duration": durationAttribute("Longest a call may last."),
				"krisp_enabled": schema.BoolAttribute{
					MarkdownDescription: "Apply Krisp noise cancellation to callers. Defaults to `false`.",
					Optional:            true,
					Computed:            true,
					Default:             booldefault.StaticBool(false),
				},
				"media_encryption": mediaEncryptionEnum.attribute("Media encryption (SRTP) for calls."),
			},
		},
		write: func(ctx context.Context, m *sipInboundTrunkModel, o livekit.SIPObject, diags *diag.Diagnostics) {
			w := sipWriter{o, ctx, diags}
			w.str("name", m.Name)
			w.str("metadata", m.Metadata)
			w.set("numbers", m.Numbers)
			w.set("allowedAddresses", m.AllowedAddresses)
			w.set("allowedNumbers", m.AllowedNumbers)
			w.str("authUsername", m.AuthUsername)
			w.str("authPassword", m.AuthPassword)
			w.str("authRealm", m.AuthRealm)
			w.strMap("headers", m.Headers)
			w.strMap("headersToAttributes", m.HeadersToAttributes)
			w.strMap("attributesToHeaders", m.AttributesToHeaders)
			w.enum("includeHeaders", includeHeadersEnum, m.IncludeHeaders)
			w.duration("ringingTimeout", m.RingingTimeout)
			w.duration("maxCallDuration", m.MaxCallDuration)
			w.boolean("krispEnabled", m.KrispEnabled)
			w.enum("mediaEncryption", mediaEncryptionEnum, m.MediaEncryption)
		},
		read: func(ctx context.Context, o livekit.SIPObject, m *sipInboundTrunkModel, diags *diag.Diagnostics) {
			r := sipReader{o, ctx, diags}
			m.Name = r.str("name")
			m.Metadata = r.str("metadata")
			m.Numbers = r.set("numbers", m.Numbers)
			m.AllowedAddresses = r.set("allowedAddresses", m.AllowedAddresses)
			m.AllowedNumbers = r.set("allowedNumbers", m.AllowedNumbers)
			m.AuthUsername = r.str("authUsername")
			m.AuthPassword = r.password("authPassword", m.AuthPassword)
			m.AuthRealm = r.str("authRealm")
			m.Headers = r.strMap("headers", m.Headers)
			m.HeadersToAttributes = r.strMap("headersToAttributes", m.HeadersToAttributes)
			m.AttributesToHeaders = r.strMap("attributesToHeaders", m.AttributesToHeaders)
			m.IncludeHeaders = r.enum("includeHeaders", includeHeadersEnum)
			m.RingingTimeout = r.duration("ringingTimeout")
			m.MaxCallDuration = r.duration("maxCallDuration")
			m.KrispEnabled = r.boolean("krispEnabled")
			m.MediaEncryption = r.enum("mediaEncryption", mediaEncryptionEnum)
		},
	}}
}

// ----------------------------------------------------- outbound trunk

type sipOutboundTrunkModel struct {
	ID                  types.String `tfsdk:"id"`
	ProjectID           types.String `tfsdk:"project_id"`
	Name                types.String `tfsdk:"name"`
	Metadata            types.String `tfsdk:"metadata"`
	Address             types.String `tfsdk:"address"`
	Transport           types.String `tfsdk:"transport"`
	DestinationCountry  types.String `tfsdk:"destination_country"`
	FromHost            types.String `tfsdk:"from_host"`
	Numbers             types.Set    `tfsdk:"numbers"`
	AuthUsername        types.String `tfsdk:"auth_username"`
	AuthPassword        types.String `tfsdk:"auth_password"`
	Headers             types.Map    `tfsdk:"headers"`
	HeadersToAttributes types.Map    `tfsdk:"headers_to_attributes"`
	AttributesToHeaders types.Map    `tfsdk:"attributes_to_headers"`
	IncludeHeaders      types.String `tfsdk:"include_headers"`
	MediaEncryption     types.String `tfsdk:"media_encryption"`
}

func newSIPOutboundTrunkResource() resource.Resource {
	return &sipResource[sipOutboundTrunkModel]{spec: sipSpec[sipOutboundTrunkModel]{
		kind:     livekit.SIPOutboundTrunk,
		typeName: "sip_outbound_trunk",
		ids: func(m *sipOutboundTrunkModel) (*types.String, *types.String) {
			return &m.ID, &m.ProjectID
		},
		schema: schema.Schema{
			MarkdownDescription: "An outbound SIP trunk: the SIP provider LiveKit places calls through, and the " +
				"numbers it calls from." + sipReplaceNote,
			Attributes: map[string]schema.Attribute{
				"id":         sipIDAttribute("Trunk ID (`ST_...`)."),
				"project_id": projectIDAttribute("trunk"),
				"name":       sipNameAttribute("trunk"),
				"metadata":   sipMetadataAttribute("trunk"),
				"address": schema.StringAttribute{
					MarkdownDescription: "Hostname or IP address, optionally with a port, that calls are sent to, " +
						"such as `example.pstn.twilio.com`. Not a SIP URI: no `sip:` prefix.",
					Required: true,
					Validators: []validator.String{
						stringvalidator.LengthAtLeast(1),
						stringvalidator.RegexMatches(hostPortPattern, "must be a hostname or IP address, optionally with a port, without `sip:`"),
					},
				},
				"transport": transportEnum.attribute("SIP transport."),
				"destination_country": schema.StringAttribute{
					MarkdownDescription: "Country the calls end in, as an ISO 3166-1 alpha-2 code such as `US`, which " +
						"LiveKit routes by. Defaults to `\"\"`.",
					Optional:   true,
					Computed:   true,
					Default:    stringdefault.StaticString(""),
					Validators: []validator.String{stringvalidator.RegexMatches(countryOrEmptyPattern, "must be a two-letter country code such as US")},
				},
				"from_host": schema.StringAttribute{
					MarkdownDescription: "Hostname for the `From` header of outbound INVITEs, instead of the project's " +
						"SIP domain. Defaults to `\"\"`.",
					Optional: true,
					Computed: true,
					Default:  stringdefault.StaticString(""),
				},
				"numbers":       phoneNumberSet("Numbers calls are made from; LiveKit picks one at random."),
				"auth_username": authUsernameAttribute(),
				"auth_password": authPasswordAttribute(),
				"headers":       headerMap("SIP X-* headers to send with each INVITE, by name.", true, false),
				"headers_to_attributes": headerMap("Maps SIP X-* headers of the 200 OK (keys) to participant "+
					"attributes (values).", true, false),
				"attributes_to_headers": headerMap("Maps participant attributes (keys) to SIP X-* headers (values) "+
					"sent with BYE or REFER.", false, true),
				"include_headers":  includeHeadersEnum.attribute("Which 200 OK headers become `sip.h.*` participant attributes automatically."),
				"media_encryption": mediaEncryptionEnum.attribute("Media encryption (SRTP) for calls."),
			},
		},
		write: func(ctx context.Context, m *sipOutboundTrunkModel, o livekit.SIPObject, diags *diag.Diagnostics) {
			w := sipWriter{o, ctx, diags}
			w.str("name", m.Name)
			w.str("metadata", m.Metadata)
			w.str("address", m.Address)
			w.enum("transport", transportEnum, m.Transport)
			w.str("destinationCountry", m.DestinationCountry)
			w.str("fromHost", m.FromHost)
			w.set("numbers", m.Numbers)
			w.str("authUsername", m.AuthUsername)
			w.str("authPassword", m.AuthPassword)
			w.strMap("headers", m.Headers)
			w.strMap("headersToAttributes", m.HeadersToAttributes)
			w.strMap("attributesToHeaders", m.AttributesToHeaders)
			w.enum("includeHeaders", includeHeadersEnum, m.IncludeHeaders)
			w.enum("mediaEncryption", mediaEncryptionEnum, m.MediaEncryption)
		},
		read: func(ctx context.Context, o livekit.SIPObject, m *sipOutboundTrunkModel, diags *diag.Diagnostics) {
			r := sipReader{o, ctx, diags}
			m.Name = r.str("name")
			m.Metadata = r.str("metadata")
			m.Address = r.str("address")
			m.Transport = r.enum("transport", transportEnum)
			m.DestinationCountry = r.str("destinationCountry")
			m.FromHost = r.str("fromHost")
			m.Numbers = r.set("numbers", m.Numbers)
			m.AuthUsername = r.str("authUsername")
			m.AuthPassword = r.password("authPassword", m.AuthPassword)
			m.Headers = r.strMap("headers", m.Headers)
			m.HeadersToAttributes = r.strMap("headersToAttributes", m.HeadersToAttributes)
			m.AttributesToHeaders = r.strMap("attributesToHeaders", m.AttributesToHeaders)
			m.IncludeHeaders = r.enum("includeHeaders", includeHeadersEnum)
			m.MediaEncryption = r.enum("mediaEncryption", mediaEncryptionEnum)
		},
	}}
}
