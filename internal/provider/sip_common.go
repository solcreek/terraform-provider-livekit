// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// e164Pattern is a phone number in E.164 format. The API adds a "+" to a
// number without one (observed 2026-11-22), which would read back as a
// different value, so the format is required at plan time.
var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// sipHeaderPattern is a SIP X-* header name.
var sipHeaderPattern = regexp.MustCompile(`^[Xx]-[A-Za-z0-9-]+$`)

// passwordMask is what the API answers for a set password.
const passwordMask = "********"

// sipEnum maps the provider's short names onto the API's enum values.
type sipEnum struct {
	prefix string
	names  []string
	def    string
}

var (
	includeHeadersEnum  = sipEnum{"", []string{"none", "x_headers", "all"}, "none"}
	mediaEncryptionEnum = sipEnum{"SIP_MEDIA_ENCRYPT_", []string{"disable", "allow", "require"}, "disable"}
	transportEnum       = sipEnum{"SIP_TRANSPORT_", []string{"auto", "udp", "tcp", "tls"}, "auto"}
)

var includeHeadersAPI = map[string]string{"none": "SIP_NO_HEADERS", "x_headers": "SIP_X_HEADERS", "all": "SIP_ALL_HEADERS"}

func (e sipEnum) toAPI(name string) string {
	if e.prefix == "" {
		return includeHeadersAPI[name]
	}
	return e.prefix + strings.ToUpper(name)
}

func (e sipEnum) fromAPI(v string) string {
	if v == "" {
		return e.def
	}
	if e.prefix == "" {
		for k, a := range includeHeadersAPI {
			if a == v {
				return k
			}
		}
		return v
	}
	return strings.ToLower(strings.TrimPrefix(v, e.prefix))
}

func (e sipEnum) attribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc + " One of `" + strings.Join(e.names, "`, `") + "`. Defaults to `" + e.def + "`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(e.def),
		Validators:          []validator.String{stringvalidator.OneOf(e.names...)},
	}
}

// ------------------------------------------------------------- attributes

func sipNameAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "A human-readable name for the " + what + ". Defaults to `\"\"`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(""),
	}
}

func sipMetadataAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Free-form metadata for the " + what + ". Defaults to `\"\"`.",
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(""),
	}
}

func sipIDAttribute(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func phoneNumberSet(desc string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc + " Numbers are in E.164 format, such as `+14155550100`.",
		ElementType:         types.StringType,
		Optional:            true,
		Validators: []validator.Set{setvalidator.ValueStringsAre(
			stringvalidator.RegexMatches(e164Pattern, "must be a phone number in E.164 format, such as +14155550100"))},
	}
}

func stringSetAttribute(desc string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc,
		ElementType:         types.StringType,
		Optional:            true,
		Validators:          []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
	}
}

func headerMap(desc string, keysAreHeaders, valuesAreHeaders bool) schema.MapAttribute {
	var v []validator.Map
	headerName := stringvalidator.RegexMatches(sipHeaderPattern, "must be a SIP X-* header name, such as X-Customer-Id")
	if keysAreHeaders {
		v = append(v, mapvalidator.KeysAre(headerName))
	}
	if valuesAreHeaders {
		v = append(v, mapvalidator.ValueStringsAre(headerName))
	}
	return schema.MapAttribute{MarkdownDescription: desc, ElementType: types.StringType, Optional: true, Validators: v}
}

func durationAttribute(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc + " In seconds. LiveKit's default applies when not set.",
		Optional:            true,
		Validators:          []validator.Int64{int64validator.AtLeast(1)},
	}
}

// --------------------------------------------------------- raw JSON fields

// sipWriter sets the fields of a raw SIP object.
type sipWriter struct {
	o     livekit.SIPObject
	ctx   context.Context
	diags *diag.Diagnostics
}

func (w sipWriter) raw(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		w.diags.AddError("Unable to encode "+key, err.Error())
		return
	}
	w.o[key] = b
}

func (w sipWriter) str(key string, v types.String) { w.raw(key, v.ValueString()) }
func (w sipWriter) boolean(key string, v types.Bool) {
	w.raw(key, v.ValueBool())
}
func (w sipWriter) enum(key string, e sipEnum, v types.String) {
	w.raw(key, e.toAPI(firstNonEmpty(v.ValueString(), e.def)))
}

func (w sipWriter) set(key string, v types.Set) {
	vals := setStrings(w.ctx, v, w.diags)
	if vals == nil {
		vals = []string{}
	}
	w.raw(key, vals)
}

func (w sipWriter) strMap(key string, v types.Map) {
	m := map[string]string{}
	if !v.IsNull() && !v.IsUnknown() {
		w.diags.Append(v.ElementsAs(w.ctx, &m, false)...)
	}
	w.raw(key, m)
}

// duration sets a protobuf Duration ("30s"), or removes it so the default
// applies.
func (w sipWriter) duration(key string, v types.Int64) {
	if v.IsNull() || v.IsUnknown() {
		delete(w.o, key)
		return
	}
	w.raw(key, (time.Duration(v.ValueInt64()) * time.Second).String())
}

// sipReader reads the fields of a raw SIP object.
type sipReader struct {
	o     livekit.SIPObject
	ctx   context.Context
	diags *diag.Diagnostics
}

func (r sipReader) decode(key string, out any) {
	if raw, ok := r.o[key]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, out); err != nil {
			r.diags.AddError("Unable to decode "+key, err.Error())
		}
	}
}

func (r sipReader) str(key string) types.String {
	var s string
	r.decode(key, &s)
	return types.StringValue(s)
}

func (r sipReader) boolean(key string) types.Bool {
	var b bool
	r.decode(key, &b)
	return types.BoolValue(b)
}

func (r sipReader) enum(key string, e sipEnum) types.String {
	var s string
	r.decode(key, &s)
	return types.StringValue(e.fromAPI(s))
}

func (r sipReader) set(key string, prior types.Set) types.Set {
	var vals []string
	r.decode(key, &vals)
	return stringSet(r.ctx, vals, prior, r.diags)
}

func (r sipReader) strMap(key string, prior types.Map) types.Map {
	var m map[string]string
	r.decode(key, &m)
	if len(m) == 0 && (prior.IsNull() || prior.IsUnknown()) {
		return types.MapNull(types.StringType)
	}
	if m == nil {
		m = map[string]string{}
	}
	out, d := types.MapValueFrom(r.ctx, types.StringType, m)
	r.diags.Append(d...)
	return out
}

func (r sipReader) duration(key string) types.Int64 {
	var s string
	r.decode(key, &s)
	if s == "" {
		return types.Int64Null()
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		r.diags.AddError("Unable to decode "+key, err.Error())
		return types.Int64Null()
	}
	return types.Int64Value(int64(d / time.Second))
}

// password keeps the state's password: the API only says whether one is set.
// One that is gone reads back as null, so a configured password shows as a
// change.
func (r sipReader) password(key string, prior types.String) types.String {
	var s string
	r.decode(key, &s)
	switch s {
	case "":
		return types.StringNull()
	case passwordMask:
		return prior
	default:
		return types.StringValue(s)
	}
}

// emptyToNull turns "" into null for an Optional (not Computed) string.
func emptyToNull(v types.String) types.String {
	if v.ValueString() == "" {
		return types.StringNull()
	}
	return v
}

// hostPortPattern is a hostname or IPv4 address with an optional port. The
// API refuses a SIP URI ("sip:...").
var hostPortPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?$`)

// countryOrEmptyPattern is an ISO 3166-1 alpha-2 code, or nothing.
var countryOrEmptyPattern = regexp.MustCompile(`^([A-Z]{2})?$`)
