// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"encoding/json"
	"net/http"
)

// SIP objects (inbound trunks, outbound trunks and dispatch rules) are kept
// as raw JSON objects. A change is sent as a `replace`, in which every field
// left out takes its default (observed 2026-11-22), so the provider reads the
// object, overwrites the fields it manages and sends the rest back as it was:
// settings the provider does not model, such as media codecs, survive.
//
// Observed 2026-11-22 on beta-api.livekit.cloud:
//   - responses carry every field, except unset durations and messages
//     (ringingTimeout, maxCallDuration, media, roomConfig), which are left out;
//   - authPassword reads back as "********" when set and "" when not, and a
//     replace without it clears it;
//   - a number without a leading "+" is stored with one added;
//   - a replace whose ID differs from the path is refused with 400;
//   - a dispatch rule accepts trunk IDs that do not exist.

// SIPObject is a SIP trunk or dispatch rule as the API returns it.
type SIPObject map[string]json.RawMessage

// readOnlySIPFields are dropped before a replace.
var readOnlySIPFields = []string{"sipTrunkId", "sipDispatchRuleId", "createdAt", "updatedAt"}

// SIPKind names one of the three SIP collections.
type SIPKind struct {
	path    string // under /v0/projects/{id}/sip/
	field   string // the request and response field: "trunk" or "dispatchRule"
	idField string
	Noun    string
}

var (
	SIPInboundTrunk  = SIPKind{"inbound-trunks", "trunk", "sipTrunkId", "inbound trunk"}
	SIPOutboundTrunk = SIPKind{"outbound-trunks", "trunk", "sipTrunkId", "outbound trunk"}
	SIPDispatchRule  = SIPKind{"dispatch-rules", "dispatchRule", "sipDispatchRuleId", "dispatch rule"}
)

func (k SIPKind) collection(projectID string) string {
	return "/v0/projects/" + seg(projectID) + "/sip/" + k.path
}

// ID returns the object's ID.
func (k SIPKind) ID(o SIPObject) string {
	var id string
	_ = json.Unmarshal(o[k.idField], &id)
	return id
}

func (c *Client) sipCall(ctx context.Context, k SIPKind, method, path string, body any) (SIPObject, error) {
	var out map[string]SIPObject
	if err := c.do(ctx, method, path, nil, body, &out); err != nil {
		return nil, err
	}
	return out[k.field], nil
}

// GetSIP returns one SIP object.
func (c *Client) GetSIP(ctx context.Context, k SIPKind, projectID, id string) (SIPObject, error) {
	return c.sipCall(ctx, k, http.MethodGet, k.collection(projectID)+"/"+seg(id), nil)
}

// CreateSIP creates a SIP object. The call is not idempotent.
func (c *Client) CreateSIP(ctx context.Context, k SIPKind, projectID string, o SIPObject) (SIPObject, error) {
	return c.sipCall(ctx, k, http.MethodPost, k.collection(projectID), map[string]any{k.field: o})
}

// ReplaceSIP replaces a SIP object whole and returns it.
func (c *Client) ReplaceSIP(ctx context.Context, k SIPKind, projectID, id string, o SIPObject) (SIPObject, error) {
	clean := SIPObject{}
	for f, v := range o {
		clean[f] = v
	}
	for _, f := range readOnlySIPFields {
		delete(clean, f)
	}
	return c.sipCall(ctx, k, http.MethodPatch, k.collection(projectID)+"/"+seg(id), map[string]any{"replace": clean})
}

// DeleteSIP deletes a SIP object. A repeat answers 404.
func (c *Client) DeleteSIP(ctx context.Context, k SIPKind, projectID, id string) error {
	return c.do(ctx, http.MethodDelete, k.collection(projectID)+"/"+seg(id), nil, nil, nil)
}
