// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// The SIP, member and phone number half of fakeLiveKit. It mirrors behaviour
// observed on beta-api.livekit.cloud (verified 2026-11-22 with direct API
// calls on a throwaway project):
//
//   - a SIP object reads back with every field, except unset durations,
//     media (on trunks) and roomConfig, which are left out; a set
//     authPassword reads "********"; a number without "+" gets one; a
//     replace sets every field it leaves out to its default, and one whose
//     ID differs from the path is a 400; an outbound trunk needs an address
//     that is not a SIP URI; a dispatch rule needs a rule, accepts trunk IDs
//     that do not exist, and conflicts with a rule on the same trunks,
//     numbers and PIN; an inbound trunk conflicts with one on the same
//     numbers when neither restricts its callers; an agent in a room
//     configuration gets restartPolicy JRP_ON_FAILURE; an inbound trunk needs
//     numbers, allowed addresses, or a username and password;
//   - members are {projectId, userId, email, role}; adding a member who is
//     one already changes their role; adding to a project that is not
//     private is a 400; removing a member who owns keys without agreeing to
//     delete them is a 400 (FailedPrecondition) that changes nothing;
//   - phone number search lists numbers for sale with an empty id, and so
//     does the purchase response; the number's ID (PN_PPN_...) is in the
//     list, which shows active numbers unless given statuses; a released
//     number reads back with status RELEASED. Renaming and releasing were NOT
//     observed: they follow the OpenAPI specification.

type fakeMember struct {
	UserID, Email string
	Role          int
}

type fakeNumber struct {
	ID, E164, Name, Status string
}

func (f *fakeLiveKit) initPhase2() {
	f.workspaceMembers = []*fakeMember{
		{UserID: fakeUserID, Email: fakeUserEmail, Role: 3},
		{UserID: "UfakeUser0002", Email: "dev@example.com", Role: 2},
		{UserID: "UfakeUser0003", Email: "ops@example.com", Role: 1},
	}
	f.forSale = []string{"+14155550100", "+14155550101", "+14155550102"}
}

var sipKinds = map[string]struct{ field, idField, prefix string }{
	"inbound-trunks":  {"trunk", "sipTrunkId", "ST_"},
	"outbound-trunks": {"trunk", "sipTrunkId", "ST_"},
	"dispatch-rules":  {"dispatchRule", "sipDispatchRuleId", "SDR_"},
}

func sipDefaults(kind string) map[string]any {
	common := map[string]any{
		"name": "", "metadata": "", "numbers": []any{}, "krispEnabled": false,
		"mediaEncryption": "SIP_MEDIA_ENCRYPT_DISABLE",
	}
	trunk := map[string]any{
		"authUsername": "", "authPassword": "", "headers": map[string]any{}, "headersToAttributes": map[string]any{},
		"attributesToHeaders": map[string]any{}, "includeHeaders": "SIP_NO_HEADERS",
	}
	out := maps.Clone(common)
	switch kind {
	case "inbound-trunks":
		maps.Copy(out, trunk)
		maps.Copy(out, map[string]any{"allowedAddresses": []any{}, "allowedNumbers": []any{}, "authRealm": "",
			"ringingTimeoutStatus": "SIP_STATUS_UNKNOWN"})
	case "outbound-trunks":
		maps.Copy(out, trunk)
		delete(out, "krispEnabled")
		maps.Copy(out, map[string]any{"address": "", "destinationCountry": "", "transport": "SIP_TRANSPORT_AUTO", "fromHost": ""})
	case "dispatch-rules":
		maps.Copy(out, map[string]any{"trunkIds": []any{}, "hidePhoneNumber": false, "inboundNumbers": []any{},
			"attributes": map[string]any{}, "roomPreset": "",
			"media": map[string]any{"onlyListedCodecs": false, "codecs": []any{}, "dropAudioDuringDtmf": false}})
	}
	return out
}

// sipBuild makes a stored object from a create or replace body.
func (f *fakeLiveKit) sipBuild(p *fakeProject, kind, id string, in map[string]any) (map[string]any, *fakeStatus) {
	o := sipDefaults(kind)
	for k, v := range in {
		if v != nil {
			o[k] = v
		}
	}
	for _, key := range []string{"numbers", "allowedNumbers", "inboundNumbers"} {
		if nums, ok := o[key].([]any); ok {
			for i, n := range nums {
				if s, _ := n.(string); !strings.HasPrefix(s, "+") {
					nums[i] = "+" + s
				}
			}
		}
	}
	if rc, ok := o["roomConfig"].(map[string]any); ok {
		if agents, ok := rc["agents"].([]any); ok {
			for _, a := range agents {
				a := a.(map[string]any)
				for k, v := range map[string]any{"metadata": "", "restartPolicy": "JRP_ON_FAILURE", "deployment": "", "attributes": map[string]any{}, "threadId": ""} {
					if _, ok := a[k]; !ok {
						a[k] = v
					}
				}
			}
		}
	}
	switch kind {
	case "outbound-trunks":
		addr, _ := o["address"].(string)
		if addr == "" {
			return nil, badRequest("no outbound address specified")
		}
		if strings.HasPrefix(strings.ToLower(addr), "sip:") {
			return nil, badRequest("trunk address should be a hostname or IP, not SIP URI")
		}
	case "dispatch-rules":
		rule, _ := o["rule"].(map[string]any)
		if len(rule) != 1 {
			return nil, badRequest("missing rule")
		}
		pin := ""
		for _, r := range rule {
			pin, _ = r.(map[string]any)["pin"].(string)
		}
		for oid, other := range p.SIP[kind] {
			if oid != id && sameStrings(other["trunkIds"], o["trunkIds"]) && sameStrings(other["numbers"], o["numbers"]) &&
				sameStrings(other["inboundNumbers"], o["inboundNumbers"]) && rulePin(other) == pin {
				return nil, badRequest(fmt.Sprintf("Dispatch rule for the same trunk, inbound number, number, and PIN combination already exists in dispatch rule %q", oid))
			}
		}
	case "inbound-trunks":
		if len(asStrings(o["numbers"])) == 0 && len(asStrings(o["allowedAddresses"])) == 0 && (o["authUsername"] == "" || o["authPassword"] == "") {
			return nil, badRequest("for security, one of the fields must be set: AuthUsername+AuthPassword, AllowedAddresses or Numbers")
		}
		for oid, other := range p.SIP[kind] {
			if oid == id || len(asStrings(other["allowedNumbers"])) > 0 || len(asStrings(o["allowedNumbers"])) > 0 {
				continue
			}
			for _, n := range asStrings(o["numbers"]) {
				if slices.Contains(asStrings(other["numbers"]), n) {
					return nil, badRequest(fmt.Sprintf("Conflicting inbound SIP Trunks: %q and %q", "<new>", oid))
				}
			}
		}
	}
	o[sipKinds[kind].idField] = id
	return o, nil
}

func asStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			s, _ := x.(string)
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b any) bool { return slices.Equal(asStrings(a), asStrings(b)) }

func rulePin(o map[string]any) string {
	rule, _ := o["rule"].(map[string]any)
	for _, r := range rule {
		pin, _ := r.(map[string]any)["pin"].(string)
		return pin
	}
	return ""
}

// sipOut is a stored object as the API returns it.
func sipOut(o map[string]any) map[string]any {
	out := maps.Clone(o)
	if pw, _ := out["authPassword"].(string); pw != "" {
		out["authPassword"] = passwordMask
	}
	return out
}

func decodeObject(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func (f *fakeLiveKit) servePhase2(r *http.Request, p *fakeProject, parts []string, body map[string]json.RawMessage) (any, *fakeStatus) {
	switch {
	case len(parts) >= 3 && parts[1] == "sip":
		return f.serveSIP(r, p, parts[2:], body)
	case len(parts) >= 2 && strings.HasPrefix(parts[1], "members"):
		return f.serveMembers(r, p, parts[1:], body)
	case len(parts) >= 2 && strings.HasPrefix(parts[1], "phone-numbers"):
		return f.serveNumbers(r, p, parts[1:], body)
	}
	return nil, &fakeStatus{http.StatusNotImplemented, 12, "fake: unhandled " + r.Method + " " + r.URL.Path}
}

func (f *fakeLiveKit) serveSIP(r *http.Request, p *fakeProject, parts []string, body map[string]json.RawMessage) (any, *fakeStatus) {
	kind := parts[0]
	k, ok := sipKinds[kind]
	if !ok {
		return nil, notFound("not found")
	}
	if p.SIP[kind] == nil {
		p.SIP[kind] = map[string]map[string]any{}
	}
	noun := map[string]string{"inbound-trunks": "inbound trunk", "outbound-trunks": "outbound trunk", "dispatch-rules": "dispatch rule"}[kind]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			ids := slices.Sorted(maps.Keys(p.SIP[kind]))
			items := []any{}
			for _, id := range ids {
				items = append(items, sipOut(p.SIP[kind][id]))
			}
			return map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}, nil
		case http.MethodPost:
			id := f.id(k.prefix)
			o, fail := f.sipBuild(p, kind, id, decodeObject(body[k.field]))
			if fail != nil {
				return nil, fail
			}
			p.SIP[kind][id] = o
			return map[string]any{k.field: sipOut(o)}, nil
		}
	}
	id := parts[1]
	o, ok := p.SIP[kind][id]
	if !ok {
		return nil, notFound(noun + " not found")
	}
	switch r.Method {
	case http.MethodGet:
		return map[string]any{k.field: sipOut(o)}, nil
	case http.MethodDelete:
		delete(p.SIP[kind], id)
		return map[string]any{}, nil
	case http.MethodPatch:
		repl := decodeObject(body["replace"])
		if repl == nil {
			return nil, &fakeStatus{http.StatusNotImplemented, 12, "fake: only replace is modelled"}
		}
		if other, _ := repl[k.idField].(string); other != "" && other != id {
			return nil, badRequest("ID mismatch")
		}
		if pw, _ := repl["authPassword"].(string); pw == passwordMask {
			return nil, &fakeStatus{http.StatusInternalServerError, 13, "fake: the provider sent the password mask back"}
		}
		n, fail := f.sipBuild(p, kind, id, repl)
		if fail != nil {
			return nil, fail
		}
		p.SIP[kind][id] = n
		return map[string]any{k.field: sipOut(n)}, nil
	}
	return nil, &fakeStatus{http.StatusNotImplemented, 12, "fake: unhandled"}
}

func memberJSON(projectID string, m *fakeMember) map[string]any {
	return map[string]any{"projectId": projectID, "userId": m.UserID, "email": m.Email, "role": m.Role}
}

func (f *fakeLiveKit) serveMembers(r *http.Request, p *fakeProject, parts []string, body map[string]json.RawMessage) (any, *fakeStatus) {
	if parts[0] == "members:addWorkspaceMembers" && r.Method == http.MethodPost {
		if !p.Private {
			return nil, badRequest("the project is not private")
		}
		var ids []string
		var role int
		_ = json.Unmarshal(body["userIds"], &ids)
		_ = json.Unmarshal(body["role"], &role)
		for _, id := range ids {
			if !slices.ContainsFunc(f.workspaceMembers, func(m *fakeMember) bool { return m.UserID == id }) {
				return nil, notFound("user is not a member of the workspace")
			}
		}
		for _, id := range ids {
			if i := slices.IndexFunc(p.Members, func(m *fakeMember) bool { return m.UserID == id }); i >= 0 {
				p.Members[i].Role = role // silently, as the API does
				continue
			}
			wm := f.workspaceMembers[slices.IndexFunc(f.workspaceMembers, func(m *fakeMember) bool { return m.UserID == id })]
			p.Members = append(p.Members, &fakeMember{UserID: id, Email: wm.Email, Role: role})
		}
		return map[string]any{}, nil
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		items := []any{}
		for _, m := range p.Members {
			items = append(items, memberJSON(p.ID, m))
		}
		return map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}, nil
	}
	if len(parts) != 2 {
		return nil, notFound("not found")
	}
	i := slices.IndexFunc(p.Members, func(m *fakeMember) bool { return m.UserID == parts[1] })
	if i < 0 {
		return nil, notFound("member not found")
	}
	m := p.Members[i]
	switch r.Method {
	case http.MethodGet:
		return map[string]any{"member": memberJSON(p.ID, m)}, nil
	case http.MethodPatch:
		var role int
		_ = json.Unmarshal(body["role"], &role)
		m.Role = role
		return map[string]any{"member": memberJSON(p.ID, m)}, nil
	case http.MethodDelete:
		owned := 0
		for _, k := range p.Keys {
			if k.Owner == m.UserID {
				owned++
			}
		}
		if owned > 0 && r.URL.Query().Get("confirmedNumKeys") != fmt.Sprint(owned) {
			return nil, &fakeStatus{http.StatusBadRequest, 9, fmt.Sprintf("the member owns %d API keys; confirm their deletion", owned)}
		}
		p.Members = slices.Delete(p.Members, i, i+1)
		return map[string]any{"deletedKeys": 0}, nil
	}
	return nil, &fakeStatus{http.StatusNotImplemented, 12, "fake: unhandled"}
}

func numberJSON(n *fakeNumber) map[string]any {
	return map[string]any{
		"id": n.ID, "name": n.Name, "e164Format": n.E164, "countryCode": "USA", "areaCode": n.E164[2:5],
		"numberType": "PHONE_NUMBER_TYPE_LOCAL", "locality": "SAN FRANCISCO", "region": "CA", "spamScore": 0,
		"capabilities": []string{"voice"}, "status": n.Status, "sipDispatchRuleId": "", "sipDispatchRuleIds": []string{},
	}
}

func (f *fakeLiveKit) serveNumbers(r *http.Request, p *fakeProject, parts []string, body map[string]json.RawMessage) (any, *fakeStatus) {
	switch {
	case parts[0] == "phone-numbers:search" && r.Method == http.MethodGet:
		if r.URL.Query().Get("countryCode") == "" {
			return nil, badRequest("country_code is required")
		}
		items := []any{}
		for _, e := range f.forSale {
			items = append(items, numberJSON(&fakeNumber{E164: e, Status: "PHONE_NUMBER_STATUS_ACTIVE"}))
		}
		return map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}, nil
	case len(parts) == 1 && r.Method == http.MethodPost:
		var e string
		_ = json.Unmarshal(body["phoneNumber"], &e)
		i := slices.Index(f.forSale, e)
		if i < 0 {
			return nil, &fakeStatus{http.StatusConflict, 6, "phone number is not available"}
		}
		f.forSale = slices.Delete(f.forSale, i, i+1)
		f.purchases++
		n := &fakeNumber{ID: f.id("PN_"), E164: e, Status: "PHONE_NUMBER_STATUS_ACTIVE"}
		p.Numbers = append(p.Numbers, n)
		// The purchase response carries no ID (observed 2026-11-23).
		out := numberJSON(n)
		out["id"] = ""
		return map[string]any{"phoneNumber": out}, nil
	case len(parts) == 1 && r.Method == http.MethodGet:
		want := r.URL.Query()["statuses"]
		if len(want) == 0 {
			want = []string{"PHONE_NUMBER_STATUS_ACTIVE"}
		}
		items := []any{}
		for _, n := range p.Numbers {
			if slices.Contains(want, n.Status) {
				items = append(items, numberJSON(n))
			}
		}
		return map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}, nil
	case len(parts) == 2:
		i := slices.IndexFunc(p.Numbers, func(n *fakeNumber) bool { return n.ID == parts[1] })
		if i < 0 {
			return nil, notFound("phone number not found")
		}
		n := p.Numbers[i]
		switch r.Method {
		case http.MethodGet:
			return map[string]any{"phoneNumber": numberJSON(n)}, nil
		case http.MethodPatch:
			if raw, ok := body["name"]; ok {
				_ = json.Unmarshal(raw, &n.Name)
			}
			return map[string]any{"phoneNumber": numberJSON(n)}, nil
		case http.MethodDelete:
			n.Status = "PHONE_NUMBER_STATUS_RELEASED"
			return map[string]any{}, nil
		}
	}
	return nil, &fakeStatus{http.StatusNotImplemented, 12, "fake: unhandled"}
}

// serveGlobal answers the routes outside a project. The caller holds f.mu.
func (f *fakeLiveKit) serveGlobal(w http.ResponseWriter, r *http.Request) bool {
	var out any
	switch {
	case r.URL.Path == "/v0/users/me" && r.Method == http.MethodGet:
		out = map[string]any{"user": map[string]any{"id": firstNonEmpty(f.meID, fakeUserID), "email": fakeUserEmail}}
	case r.URL.Path == "/v0/workspaces/wo_fake0001/members" && r.Method == http.MethodGet:
		items := []any{}
		for _, m := range f.workspaceMembers {
			items = append(items, map[string]any{"workspaceId": "wo_fake0001", "userId": m.UserID, "email": m.Email, "role": m.Role})
		}
		out = map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}
	default:
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
	return true
}
