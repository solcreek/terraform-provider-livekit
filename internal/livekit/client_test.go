// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("tok", WithBaseURL(srv.URL+"/"), withRetryBase(time.Millisecond))
}

func TestBearerAuthAndUserAgent(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/v0/projects/p_1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"project":{"id":"p_1","name":"A","preferences":{"sfuAllowPause":true}}}`))
	})
	p, err := c.GetProject(context.Background(), "p_1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "A" || !p.Preferences.SFUAllowPause {
		t.Fatalf("project = %+v", p)
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{404, `{"code":5, "message":"project not found", "details":[]}`, "project not found"},
		{401, `{"code":16, "message":"authentication required", "details":[]}`, "lk cloud auth"},
		{403, `{"error":{"code":"permission_denied","message":"role too low"}}`, "role too low"},
		{502, `<html>bad gateway</html>`, "bad gateway"},
	} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.GetProject(context.Background(), "p_1")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: error %v, want %q", tc.status, err, tc.want)
		}
		if StatusCode(err) != tc.status || IsNotFound(err) != (tc.status == 404) {
			t.Errorf("%d: StatusCode %d IsNotFound %v", tc.status, StatusCode(err), IsNotFound(err))
		}
	}
}

// A 429 is repeated, writes included: the API refused it before acting.
func TestRateLimitRetry(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"apiKey":"API1","secret":"s"}`))
	})
	k, err := c.GenerateKey(context.Background(), "p_1", "", true)
	if err != nil || k.APIKey != "API1" || calls.Load() != 3 {
		t.Fatalf("key %+v, err %v, calls %d", k, err, calls.Load())
	}
}

// A write that died on the network is not repeated: it may have landed, and
// creating a key or a webhook again would make a second one.
func TestNoRetryOfWritesOnNetworkError(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	})
	if _, err := c.CreateWebhook(context.Background(), "p_1", WebhookCreate{URL: "https://example.com"}); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST sent %d times", calls.Load())
	}
	calls.Store(0)
	if _, err := c.ListWebhooks(context.Background(), "p_1"); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1+maxNetworkRetries {
		t.Fatalf("GET sent %d times", calls.Load())
	}
}

func TestListFollowsCursor(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeAgentKeys") != "true" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		switch r.URL.Query().Get("page.cursor") {
		case "":
			_, _ = w.Write([]byte(`{"items":[{"apiKey":"API1"}],"pageInfo":{"nextCursor":"c2","hasMore":true}}`))
		case "c2":
			_, _ = w.Write([]byte(`{"items":[{"apiKey":"API2","ownerId":""}],"pageInfo":{"nextCursor":"","hasMore":false}}`))
		}
	})
	k, err := c.GetKey(context.Background(), "p_1", "API2")
	if err != nil || k.APIKey != "API2" {
		t.Fatalf("key %+v, err %v", k, err)
	}
	if _, err := c.GetKey(context.Background(), "p_1", "API3"); !IsNotFound(err) {
		t.Fatalf("missing key: %v", err)
	}
}

// The request bodies carry only what is set, as the API's PATCH semantics
// need, but always both lists of a filter.
func TestRequestBodies(t *testing.T) {
	var got map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()
	_, _ = c.UpdateProject(ctx, "p_1", ProjectUpdate{SFUAllowPause: new(bool), PinnedRegions: &PinnedRegions{Regions: []string{}}})
	if b, _ := json.Marshal(got); string(b) != `{"pinnedRegions":{"regions":[]},"sfuAllowPause":false}` {
		t.Errorf("project update body %s", b)
	}
	empty := ""
	_, _ = c.UpdateWebhook(ctx, "p_1", "w1", WebhookUpdate{Name: &empty, FilterParams: &FilterParams{IncludeEvents: []string{}, ExcludeEvents: []string{"a"}}})
	if b, _ := json.Marshal(got); string(b) != `{"filterParams":{"excludeEvents":["a"],"includeEvents":[]},"name":""}` {
		t.Errorf("webhook update body %s", b)
	}
}
