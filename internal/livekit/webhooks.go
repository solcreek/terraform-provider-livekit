// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
)

// Webhook is a destination a project delivers its events to.
type Webhook struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	// SigningKey is the ID of the API key whose secret signs deliveries. The
	// API keeps it when that key is deleted (observed 2026-11-22).
	SigningKey   string       `json:"signingKey"`
	FilterParams FilterParams `json:"filterParams"`
}

// FilterParams names the events a webhook delivers. With both lists empty
// it delivers every event.
type FilterParams struct {
	IncludeEvents []string `json:"includeEvents"`
	ExcludeEvents []string `json:"excludeEvents"`
}

// WebhookCreate is a new webhook. An empty SigningKey makes the API save the
// project's oldest API key.
type WebhookCreate struct {
	Name         string       `json:"name,omitempty"`
	URL          string       `json:"url"`
	SigningKey   string       `json:"signingKey,omitempty"`
	FilterParams FilterParams `json:"filterParams"`
}

// WebhookUpdate changes a webhook. A nil field keeps its value; a set
// FilterParams replaces the whole filter. An empty SigningKey also keeps
// the key.
type WebhookUpdate struct {
	Name         *string       `json:"name,omitempty"`
	URL          *string       `json:"url,omitempty"`
	SigningKey   *string       `json:"signingKey,omitempty"`
	FilterParams *FilterParams `json:"filterParams,omitempty"`
}

type webhookEnvelope struct {
	Webhook Webhook `json:"webhook"`
}

func webhooksPath(projectID string) string {
	return "/v0/projects/" + seg(projectID) + "/webhooks"
}

// ListWebhooks returns a project's webhooks in the order they were added.
// There is no endpoint for one webhook.
func (c *Client) ListWebhooks(ctx context.Context, projectID string) ([]Webhook, error) {
	return list[Webhook](ctx, c, webhooksPath(projectID), nil)
}

// GetWebhook finds one webhook in the list. A webhook that is not there is
// an APIError with status 404, like a missing project.
func (c *Client) GetWebhook(ctx context.Context, projectID, id string) (*Webhook, error) {
	all, err := c.ListWebhooks(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &APIError{StatusCode: http.StatusNotFound, Code: 5, Message: "webhook not found"}
}

// CreateWebhook creates a webhook. The call is not idempotent, and the API
// accepts a URL that another webhook of the project already has.
func (c *Client) CreateWebhook(ctx context.Context, projectID string, w WebhookCreate) (*Webhook, error) {
	var out webhookEnvelope
	if err := c.do(ctx, http.MethodPost, webhooksPath(projectID), nil, w, &out); err != nil {
		return nil, err
	}
	return &out.Webhook, nil
}

// UpdateWebhook changes a webhook and returns it.
func (c *Client) UpdateWebhook(ctx context.Context, projectID, id string, u WebhookUpdate) (*Webhook, error) {
	var out webhookEnvelope
	if err := c.do(ctx, http.MethodPatch, webhooksPath(projectID)+"/"+seg(id), nil, u, &out); err != nil {
		return nil, err
	}
	return &out.Webhook, nil
}

// DeleteWebhook deletes a webhook. A repeat answers 404.
func (c *Client) DeleteWebhook(ctx context.Context, projectID, id string) error {
	return c.do(ctx, http.MethodDelete, webhooksPath(projectID)+"/"+seg(id), nil, nil, nil)
}

// ListWebhookEvents returns the event names a project's webhooks can filter
// on.
func (c *Client) ListWebhookEvents(ctx context.Context, projectID string) ([]string, error) {
	return list[string](ctx, c, "/v0/projects/"+seg(projectID)+"/webhook-events", nil)
}
