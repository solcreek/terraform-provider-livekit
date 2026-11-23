// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	fakeToken     = "fake-session-token"
	fakeProjectID = "p_fake0001"
	fakeUserID    = "UfakeUser0001"
	fakeUserEmail = "owner@example.com"
)

// fakeLiveKit is an in-memory stand-in for the parts of the LiveKit Cloud API
// the provider uses, so every resource's Terraform lifecycle can be tested
// without credentials. It mirrors behaviour observed on
// beta-api.livekit.cloud (verified 2026-11-22 with direct API calls on a
// throwaway project):
//
//   - responses carry every field, false and empty ones included; errors are
//     a google.rpc.Status {"code", "message", "details": []}; a bad session
//     is 401 with code 16; an unknown project, webhook or key is 404 with
//     code 5, and deleting one twice is a 404 too;
//   - a project update keeps every field it does not get; an empty
//     enabledCodecs or piiRedactionCategories list is ignored; an unknown
//     codec answers 500 and an unknown region 400; turning PII redaction on
//     with no saved categories saves the default set, and turning it off
//     keeps the categories;
//   - a webhook needs an http(s) URL with a public address, events from the
//     event list, and a signing key of the project; an empty signing key
//     saves the project's oldest key; URLs may repeat; an update keeps the
//     fields it does not get, sets an empty name, keeps the key when given
//     "", and replaces the whole filter when given one; a webhook keeps its
//     signing key ID after that key is deleted;
//   - there is no GET for one webhook or one key, only the lists; the key
//     list leaves out cloud agent keys unless asked, and a service account
//     key has an empty owner.
type fakeLiveKit struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	next          int
	projects      map[string]*fakeProject
	requests      []string
	rateLimitNext int
	// keyPageSize makes the key list page, to exercise the cursor.
	keyPageSize int
	// workspaceMembers are the members of wo_fake0001.
	workspaceMembers []*fakeMember
	// forSale are the phone numbers that can be bought; purchases counts buys.
	forSale   []string
	purchases int
	// meID overrides the ID of the session's user.
	meID string
	// renameFailNext answers that many phone number renames with 500.
	renameFailNext int
}

type fakeProject struct {
	ID, Name, Subdomain, WorkspaceID, Region string
	Analytics, AutoCreate, RemoteUnmute      bool
	Codecs, Regions, PII                     []string
	Recording, Training, ExplicitDispatch    bool
	PIIOn, InferenceRestriction, Private     bool
	NoTokenEndpoint, SFUPause                bool
	EgressBackup, EgressRetry                bool
	Webhooks                                 []*fakeWebhook
	Keys                                     []*fakeKey
	SIP                                      map[string]map[string]map[string]any // kind -> id -> object
	Members                                  []*fakeMember
	Numbers                                  []*fakeNumber
}

type fakeWebhook struct {
	ID, Name, URL, SigningKey string
	Include, Exclude          []string
}

type fakeKey struct {
	ID, Description, Owner, AgentID string
}

var fakeEvents = []string{
	"room_started", "room_finished", "participant_joined", "participant_left", "participant_connection_aborted",
	"track_published", "track_unpublished", "egress_started", "egress_updated", "egress_ended",
	"ingress_started", "ingress_ended",
}

var fakeRegions = []string{"us", "eu", "in", "jp", "sa", "au", "me"}

var fakeCodecs = []string{"video/H264", "video/VP8", "video/VP9", "video/AV1", "audio/red", "audio/opus"}

func newFakeLiveKit(t *testing.T) *fakeLiveKit {
	t.Helper()
	for _, k := range []string{"LIVEKIT_SESSION_TOKEN", "LIVEKIT_API_URL", "LIVEKIT_PROJECT_ID"} {
		t.Setenv(k, "")
	}
	// Never fall back to the developer's real lk session.
	t.Setenv("LIVEKIT_CLI_CONFIG", t.TempDir()+"/absent.yaml")
	f := &fakeLiveKit{t: t, projects: map[string]*fakeProject{}, keyPageSize: 100}
	f.addProject(fakeProjectID, "Acme")
	f.initPhase2()
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// addProject creates a project as the dashboard would, with one key owned by
// its creator.
func (f *fakeLiveKit) addProject(id, name string) *fakeProject {
	p := &fakeProject{
		ID: id, Name: name, Subdomain: strings.ToLower(name) + "-abc123", WorkspaceID: "wo_fake0001", Region: "us",
		Analytics: true, AutoCreate: true, NoTokenEndpoint: true,
		Codecs: slices.Clone(fakeCodecs), Regions: []string{}, PII: []string{},
	}
	p.Keys = append(p.Keys, &fakeKey{ID: f.id("API"), Owner: fakeUserID})
	p.SIP = map[string]map[string]map[string]any{}
	p.Members = []*fakeMember{{UserID: fakeUserID, Email: fakeUserEmail, Role: 3}}
	f.projects[id] = p
	return p
}

func (f *fakeLiveKit) providerConfig() string {
	return requiredProviders + fmt.Sprintf(`
provider "livekit" {
  session_token       = %q
  api_url             = %q
  project_id          = %q
  requests_per_minute = 0
}
`, fakeToken, f.srv.URL, fakeProjectID)
}

func (f *fakeLiveKit) id(prefix string) string {
	f.next++
	return fmt.Sprintf("%sFake%06d", prefix, f.next)
}

// ------------------------------------------------------------ test helpers

// mutate changes the fake's state out of band, as a person in the dashboard
// would.
func (f *fakeLiveKit) mutate(fn func(f *fakeLiveKit)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeLiveKit) project() *fakeProject { return f.projects[fakeProjectID] }

// count returns how many requests matched "METHOD /path-prefix".
func (f *fakeLiveKit) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// ------------------------------------------------------------------- server

type fakeStatus struct {
	status int
	code   int
	msg    string
}

func (e *fakeStatus) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": e.code, "message": e.msg, "details": []any{}})
}

func badRequest(msg string) *fakeStatus { return &fakeStatus{http.StatusBadRequest, 3, msg} }
func notFound(msg string) *fakeStatus   { return &fakeStatus{http.StatusNotFound, 5, msg} }

func (f *fakeLiveKit) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		w.Header().Set("WWW-Authenticate", `Bearer realm="public-api"`)
		(&fakeStatus{http.StatusUnauthorized, 16, "authentication required"}).write(w)
		return
	}
	if f.rateLimitNext > 0 {
		f.rateLimitNext--
		w.Header().Set("Retry-After", "0")
		(&fakeStatus{http.StatusTooManyRequests, 8, "rate limit exceeded"}).write(w)
		return
	}

	if f.serveGlobal(w, r) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v0/projects/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/v0/projects/") || parts[0] == "" {
		notFound("not found").write(w)
		return
	}
	p := f.projects[parts[0]]
	if p == nil {
		notFound("project not found").write(w)
		return
	}
	var body map[string]json.RawMessage
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badRequest("invalid JSON body").write(w)
			return
		}
	}

	var out any
	var fail *fakeStatus
	route := r.Method + " " + strings.Join(parts[1:], "/")
	switch {
	case route == "GET ":
		out = map[string]any{"project": f.projectJSON(p)}
	case route == "PATCH ":
		fail = f.updateProject(p, body)
		out = map[string]any{"project": f.projectJSON(p)}
	case route == "GET webhook-events":
		out = map[string]any{"items": fakeEvents, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}
	case route == "GET webhooks":
		items := []any{}
		for _, wh := range p.Webhooks {
			items = append(items, webhookJSON(wh))
		}
		out = map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": "", "hasMore": false}}
	case route == "POST webhooks":
		wh := &fakeWebhook{Include: []string{}, Exclude: []string{}}
		if fail = f.applyWebhook(p, wh, body, true); fail == nil {
			wh.ID = f.id("WH")
			p.Webhooks = append(p.Webhooks, wh)
		}
		out = map[string]any{"webhook": webhookJSON(wh)}
	case len(parts) == 3 && parts[1] == "webhooks" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete):
		i := slices.IndexFunc(p.Webhooks, func(wh *fakeWebhook) bool { return wh.ID == parts[2] })
		if i < 0 {
			fail = notFound("webhook not found")
			break
		}
		if r.Method == http.MethodDelete {
			p.Webhooks = slices.Delete(p.Webhooks, i, i+1)
			out = map[string]any{}
			break
		}
		// Validate on a copy, so a refused update changes nothing.
		wh := *p.Webhooks[i]
		if fail = f.applyWebhook(p, &wh, body, false); fail == nil {
			*p.Webhooks[i] = wh
		}
		out = map[string]any{"webhook": webhookJSON(&wh)}
	case route == "GET keys":
		out = f.listKeys(p, r.URL.Query())
	case route == "POST keys":
		var desc string
		var sa bool
		_ = json.Unmarshal(body["description"], &desc)
		_ = json.Unmarshal(body["isServiceAccount"], &sa)
		k := &fakeKey{ID: f.id("API"), Description: desc, Owner: fakeUserID}
		if sa {
			k.Owner = ""
		}
		p.Keys = append(p.Keys, k)
		out = map[string]any{"apiKey": k.ID, "secret": "secret-of-" + k.ID}
	case len(parts) == 3 && parts[1] == "keys" && r.Method == http.MethodDelete:
		i := slices.IndexFunc(p.Keys, func(k *fakeKey) bool { return k.ID == parts[2] })
		if i < 0 {
			fail = notFound("API key not found")
			break
		}
		p.Keys = slices.Delete(p.Keys, i, i+1)
		out = map[string]any{}
	default:
		out, fail = f.servePhase2(r, p, parts, body)
	}
	if fail != nil {
		fail.write(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeLiveKit) projectJSON(p *fakeProject) map[string]any {
	webhooks := []any{}
	for _, wh := range p.Webhooks {
		webhooks = append(webhooks, webhookJSON(wh))
	}
	return map[string]any{
		"id": p.ID, "name": p.Name, "workspaceId": p.WorkspaceID, "createdAt": "2026-11-22T16:26:23.977793Z",
		"webhooks": webhooks, "creatorId": fakeUserID, "subdomain": p.Subdomain,
		"enableAnalytics": p.Analytics, "analyticsAccessKey": "", "enableAutoCreate": p.AutoCreate,
		"enableRemoteUnmute": p.RemoteUnmute, "enableEnhancedNoiseCancellation": false, "enableHostedAgents": false,
		"preferences": map[string]any{
			"hideOnboarding": false, "discoverableByDomain": false, "joinableByDomain": false,
			"hideSampleAppGallery": false, "disableTokenEndpoint": p.NoTokenEndpoint, "tokenEndpointKey": "",
			"sfuAllowPause": p.SFUPause, "enableEgressBackupStorage": p.EgressBackup, "featureFlags": map[string]any{},
			"enableEgressAutoRetry": p.EgressRetry, "hipaaCompliant": false, "roomConfigurations": map[string]any{},
		},
		"enabledCodecs": p.Codecs, "pinnedRegions": p.Regions, "egressClusterId": "", "enableSafeMode": false,
		"enableUserDataRecording": p.Recording, "enableUserDataTraining": p.Training, "userDataRegion": p.Region,
		"userDataLifetimeDays": 0, "enableSipCustomDomain": false, "requireExplicitDispatch": p.ExplicitDispatch,
		"isPrivate": p.Private, "enablePiiRedaction": p.PIIOn, "piiRedactionCategories": p.PII,
		"enableInferenceRegionRestriction": p.InferenceRestriction,
		"url":                              "wss://" + p.Subdomain + ".livekit.cloud",
	}
}

// updateProject applies a PATCH. Validation happens first, so a refused
// update changes nothing.
func (f *fakeLiveKit) updateProject(p *fakeProject, body map[string]json.RawMessage) *fakeStatus {
	var codecs, pii []string
	_ = json.Unmarshal(body["enabledCodecs"], &codecs)
	_ = json.Unmarshal(body["piiRedactionCategories"], &pii)
	for _, c := range codecs {
		if !slices.Contains(fakeCodecs, c) && c != "video/H265" {
			return &fakeStatus{http.StatusInternalServerError, 13, "internal error"}
		}
	}
	var pins *struct {
		Regions []string `json:"regions"`
	}
	if raw, ok := body["pinnedRegions"]; ok {
		if err := json.Unmarshal(raw, &pins); err != nil {
			return badRequest("invalid pinnedRegions")
		}
		for _, r := range pins.Regions {
			if !slices.Contains(fakeRegions, r) {
				return badRequest("pinned_regions names an unknown region")
			}
		}
	}
	for _, c := range pii {
		if !strings.HasPrefix(c, piiCategoryPrefix) || !slices.Contains(piiCategories, piiFromAPI(c)) {
			return badRequest("invalid value for enum field piiRedactionCategories")
		}
	}
	setBool := func(key string, dst *bool) {
		if raw, ok := body[key]; ok {
			_ = json.Unmarshal(raw, dst)
		}
	}
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &p.Name)
	}
	setBool("enableAnalytics", &p.Analytics)
	setBool("enableAutoCreate", &p.AutoCreate)
	setBool("enableRemoteUnmute", &p.RemoteUnmute)
	setBool("enableUserDataRecording", &p.Recording)
	setBool("enableUserDataTraining", &p.Training)
	setBool("requireExplicitDispatch", &p.ExplicitDispatch)
	setBool("enableInferenceRegionRestriction", &p.InferenceRestriction)
	setBool("disableTokenEndpoint", &p.NoTokenEndpoint)
	setBool("sfuAllowPause", &p.SFUPause)
	setBool("enableEgressBackupStorage", &p.EgressBackup)
	setBool("enableEgressAutoRetry", &p.EgressRetry)
	if len(codecs) > 0 {
		p.Codecs = codecs
	}
	if pins != nil {
		p.Regions = append([]string{}, pins.Regions...)
	}
	if len(pii) > 0 {
		p.PII = pii
	}
	wasOn := p.PIIOn
	setBool("enablePiiRedaction", &p.PIIOn)
	if p.PIIOn && !wasOn && len(p.PII) == 0 {
		p.PII = []string{piiToAPI("ssn"), piiToAPI("credit-card"), piiToAPI("email-address")} // abridged default set
	}
	return nil
}

func webhookJSON(wh *fakeWebhook) map[string]any {
	return map[string]any{
		"id": wh.ID, "name": wh.Name, "url": wh.URL, "signingKey": wh.SigningKey,
		"filterParams": map[string]any{"includeEvents": wh.Include, "excludeEvents": wh.Exclude},
	}
}

// applyWebhook validates and applies a create or update body to wh.
func (f *fakeLiveKit) applyWebhook(p *fakeProject, wh *fakeWebhook, body map[string]json.RawMessage, create bool) *fakeStatus {
	if raw, ok := body["name"]; ok {
		_ = json.Unmarshal(raw, &wh.Name)
	}
	if raw, ok := body["url"]; ok || create {
		var u string
		_ = json.Unmarshal(raw, &u)
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return badRequest("webhook url must be an absolute http or https URL")
		}
		if h := parsed.Hostname(); h == "localhost" || strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "10.") {
			return badRequest("webhook url must resolve to a public address")
		}
		wh.URL = u
	}
	var key string
	_ = json.Unmarshal(body["signingKey"], &key)
	switch {
	case key != "":
		if !slices.ContainsFunc(p.Keys, func(k *fakeKey) bool { return k.ID == key }) {
			return badRequest("signing_key is not an API key of this project")
		}
		wh.SigningKey = key
	case create:
		if len(p.Keys) == 0 {
			return &fakeStatus{http.StatusBadRequest, 9, "the project has no API key to sign webhooks with"}
		}
		wh.SigningKey = p.Keys[0].ID
	}
	if raw, ok := body["filterParams"]; ok {
		var fp struct {
			Include []string `json:"includeEvents"`
			Exclude []string `json:"excludeEvents"`
		}
		_ = json.Unmarshal(raw, &fp)
		for _, e := range append(slices.Clone(fp.Include), fp.Exclude...) {
			if !slices.Contains(fakeEvents, e) {
				return badRequest("invalid webhook event")
			}
		}
		wh.Include = append([]string{}, fp.Include...)
		wh.Exclude = append([]string{}, fp.Exclude...)
	}
	return nil
}

func (f *fakeLiveKit) listKeys(p *fakeProject, q url.Values) any {
	var keys []*fakeKey
	for _, k := range p.Keys {
		if k.AgentID == "" || q.Get("includeAgentKeys") == "true" {
			keys = append(keys, k)
		}
	}
	start, _ := strconv.Atoi(q.Get("page.cursor"))
	end := min(start+f.keyPageSize, len(keys))
	items := []any{}
	for _, k := range keys[start:end] {
		email := ""
		if k.Owner != "" {
			email = fakeUserEmail
		}
		item := map[string]any{
			"apiKey": k.ID, "createdAt": "2026-11-22T16:27:04.564015Z", "updatedAt": "2026-11-22T16:27:04.564015Z",
			"description": k.Description, "ownerId": k.Owner, "ownerEmail": email,
		}
		if k.AgentID != "" {
			item["agentId"] = k.AgentID
		}
		items = append(items, item)
	}
	next := ""
	if end < len(keys) {
		next = strconv.Itoa(end)
	}
	return map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": next, "hasMore": next != ""}}
}
