// SPDX-License-Identifier: MPL-2.0

// Package livekit is a minimal, dependency-free (stdlib only) client for the
// subset of the LiveKit Cloud API (https://beta-api.livekit.cloud/v0/openapi)
// that this Terraform provider needs: project settings, webhooks and API
// keys.
//
// It follows the API's OpenAPI specification and was checked against
// beta-api.livekit.cloud (2026-11-22). Where the live API differs from the
// specification, the code follows the live API and says so. The API is the
// pre-GA `v0`: endpoints and fields may change without notice.
package livekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the LiveKit Cloud API. The paths below carry the /v0
// prefix.
const DefaultBaseURL = "https://beta-api.livekit.cloud"

// maxConcurrent caps requests in flight. The API publishes no rate limit;
// Terraform runs up to ten operations at once.
const maxConcurrent = 4

// maxNetworkRetries is how often a GET is repeated after a transport error
// (timeout, reset connection).
const maxNetworkRetries = 2

// maxRetryWait bounds how long one rate-limit back-off may sleep.
const maxRetryWait = 60 * time.Second

// Client is a LiveKit Cloud API client. It is safe for concurrent use.
type Client struct {
	http       *http.Client
	baseURL    string
	token      string
	userAgent  string
	maxRetries int
	retryBase  time.Duration
	slots      chan struct{}
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL. A trailing slash is ignored.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithTimeout sets the per-request HTTP timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.http.Timeout = d
		}
	}
}

// WithHTTPClient injects a custom *http.Client (mainly for tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// withRetryBase shortens back-offs in tests.
func withRetryBase(d time.Duration) Option {
	return func(c *Client) { c.retryBase = d }
}

// New builds a Client authenticated with a LiveKit Cloud user session token,
// sent as a bearer token. The API accepts no other credential (2026-11-22):
// a session belongs to a user and reaches every project the user is a member
// of, with the user's role in it.
func New(sessionToken string, opts ...Option) *Client {
	c := &Client{
		http:       &http.Client{Timeout: 60 * time.Second},
		baseURL:    DefaultBaseURL,
		token:      sessionToken,
		userAgent:  "terraform-provider-livekit",
		maxRetries: 5,
		retryBase:  time.Second,
		slots:      make(chan struct{}, maxConcurrent),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is returned for any non-2xx response.
type APIError struct {
	StatusCode int
	// Code is the google.rpc.Code of the error, e.g. 5 for NotFound.
	Code    int
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("LiveKit Cloud API error %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("LiveKit Cloud API error %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// IsNotFound reports whether err is an API 404. The API also answers 404 for
// a project the user is not a member of.
func IsNotFound(err error) bool {
	return StatusCode(err) == http.StatusNotFound
}

// StatusCode returns the HTTP status of an APIError, or 0.
func StatusCode(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode
	}
	return 0
}

// do performs a request, retrying on rate limiting, and decodes a 2xx
// response into out (if non-nil).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		payload = b
	}

	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	for attempt := 0; ; attempt++ {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		// The slot is held only for the HTTP round trip, never while a
		// back-off sleeps.
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		resp, err := c.http.Do(req)
		var respBody []byte
		if err == nil {
			respBody, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
		<-c.slots
		if err != nil {
			// A read that died on the network is safe to repeat; a write is
			// not: creating a webhook or a key is not idempotent, and a
			// repeat would make a second one.
			if method == http.MethodGet && ctx.Err() == nil && attempt < maxNetworkRetries {
				if err := c.sleep(ctx, time.Duration(1<<attempt)*c.retryBase); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%s %s: %w", method, path, err)
		}

		// A gateway error on a read is repeated like a network error; on a
		// write it is not, since the write may have landed behind it.
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			if method == http.MethodGet && attempt < maxNetworkRetries {
				if err := c.sleep(ctx, time.Duration(1<<attempt)*c.retryBase); err != nil {
					return err
				}
				continue
			}
		}

		// A 429 (ResourceExhausted) is refused before the API acts on the
		// request, so repeating it is safe for writes too.
		if resp.StatusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
			if err := c.sleep(ctx, c.retryWait(resp.Header, attempt)); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return parseError(resp.StatusCode, respBody)
		}
		if out != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decode %s %s response: %w", method, path, err)
			}
		}
		return nil
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// retryWait is how long to back off after a 429: Retry-After when present,
// else exponentially: 1s, 2s, 4s, 8s, 16s.
func (c *Client) retryWait(h http.Header, attempt int) time.Duration {
	wait := time.Duration(1<<attempt) * c.retryBase
	if secs, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && secs >= 0 {
		wait = time.Duration(secs) * time.Second
	}
	return min(wait, maxRetryWait)
}

func parseError(status int, body []byte) error {
	ae := &APIError{StatusCode: status, Body: string(body)}
	ae.Code, ae.Message = errorMessage(body)
	// Make credential problems actionable.
	switch status {
	case http.StatusUnauthorized:
		ae.Message = strings.TrimSpace(ae.Message + " (the session token is missing, expired or revoked: sign in again with `lk cloud auth`, or replace LIVEKIT_SESSION_TOKEN)")
	case http.StatusForbidden:
		ae.Message = strings.TrimSpace(ae.Message + " (your role in the project is too low for this call: reading needs 1, changing 2, and API keys and settings 3, admin)")
	}
	return ae
}

// errorMessage extracts the code and message of an error body: a
// google.rpc.Status {"code", "message", "details"}, or the
// {"error": {"code", "message"}} the API's authentication answers with.
func errorMessage(body []byte) (int, string) {
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Error   *struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return 0, ""
	}
	if e.Message == "" && e.Error != nil {
		return e.Code, e.Error.Message
	}
	return e.Code, e.Message
}

// seg escapes one path segment.
func seg(s string) string { return url.PathEscape(s) }

// pageInfo is the paging state of a list response.
type pageInfo struct {
	NextCursor string `json:"nextCursor"`
	HasMore    bool   `json:"hasMore"`
}

// list fetches every page of a list endpoint, following `nextCursor`.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	cursor := ""
	for range 1000 {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		if cursor != "" {
			q.Set("page.cursor", cursor)
		}
		var page struct {
			Items    []T      `json:"items"`
			PageInfo pageInfo `json:"pageInfo"`
		}
		if err := c.do(ctx, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if !page.PageInfo.HasMore || page.PageInfo.NextCursor == "" || page.PageInfo.NextCursor == cursor {
			return all, nil
		}
		cursor = page.PageInfo.NextCursor
	}
	return nil, fmt.Errorf("GET %s: more than 1000 pages", path)
}

func itoa(n int) string { return strconv.Itoa(n) }
