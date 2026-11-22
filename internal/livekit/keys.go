// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
	"net/url"
)

// APIKey is an API key of a project, without its secret.
type APIKey struct {
	APIKey      string `json:"apiKey"`
	Description string `json:"description"`
	// OwnerID is empty for a service account key, which belongs to the
	// project rather than to a user.
	OwnerID    string `json:"ownerId"`
	OwnerEmail string `json:"ownerEmail"`
	CreatedAt  string `json:"createdAt"`
}

// GeneratedKey is a new API key with its secret, which the API never returns
// again.
type GeneratedKey struct {
	APIKey string `json:"apiKey"`
	Secret string `json:"secret"`
}

func keysPath(projectID string) string {
	return "/v0/projects/" + seg(projectID) + "/keys"
}

// ListKeys returns a project's API keys, oldest first, leaving out the keys
// of cloud agents.
func (c *Client) ListKeys(ctx context.Context, projectID string) ([]APIKey, error) {
	return list[APIKey](ctx, c, keysPath(projectID), nil)
}

// GetKey finds one API key in the list; there is no endpoint for one key. A
// key that is not there is an APIError with status 404.
func (c *Client) GetKey(ctx context.Context, projectID, apiKey string) (*APIKey, error) {
	all, err := list[APIKey](ctx, c, keysPath(projectID), url.Values{"includeAgentKeys": {"true"}})
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].APIKey == apiKey {
			return &all[i], nil
		}
	}
	return nil, &APIError{StatusCode: http.StatusNotFound, Code: 5, Message: "API key not found"}
}

// GenerateKey creates an API key. A service account key has no owner and
// stays when the user who created it leaves the project. The call is not
// idempotent.
func (c *Client) GenerateKey(ctx context.Context, projectID, description string, serviceAccount bool) (*GeneratedKey, error) {
	body := struct {
		Description      string `json:"description,omitempty"`
		IsServiceAccount bool   `json:"isServiceAccount,omitempty"`
	}{description, serviceAccount}
	var out GeneratedKey
	if err := c.do(ctx, http.MethodPost, keysPath(projectID), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteKey revokes an API key. It cannot be undone; a repeat answers 404.
func (c *Client) DeleteKey(ctx context.Context, projectID, apiKey string) error {
	return c.do(ctx, http.MethodDelete, keysPath(projectID)+"/"+seg(apiKey), nil, nil, nil)
}
