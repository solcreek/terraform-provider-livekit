// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
)

// Project is a LiveKit Cloud project with its settings. The API sends every
// field, false and empty ones included (observed 2026-11-22).
type Project struct {
	ID                               string             `json:"id"`
	Name                             string             `json:"name"`
	WorkspaceID                      string             `json:"workspaceId"`
	CreatedAt                        string             `json:"createdAt"`
	Webhooks                         []Webhook          `json:"webhooks"`
	Subdomain                        string             `json:"subdomain"`
	URL                              string             `json:"url"`
	EnableAnalytics                  bool               `json:"enableAnalytics"`
	EnableAutoCreate                 bool               `json:"enableAutoCreate"`
	EnableRemoteUnmute               bool               `json:"enableRemoteUnmute"`
	EnabledCodecs                    []string           `json:"enabledCodecs"`
	PinnedRegions                    []string           `json:"pinnedRegions"`
	EnableUserDataRecording          bool               `json:"enableUserDataRecording"`
	EnableUserDataTraining           bool               `json:"enableUserDataTraining"`
	UserDataRegion                   string             `json:"userDataRegion"`
	RequireExplicitDispatch          bool               `json:"requireExplicitDispatch"`
	IsPrivate                        bool               `json:"isPrivate"`
	EnablePIIRedaction               bool               `json:"enablePiiRedaction"`
	PIIRedactionCategories           []string           `json:"piiRedactionCategories"`
	EnableInferenceRegionRestriction bool               `json:"enableInferenceRegionRestriction"`
	Preferences                      ProjectPreferences `json:"preferences"`
}

// ProjectPreferences are the settings the API nests under `preferences`.
type ProjectPreferences struct {
	DisableTokenEndpoint      bool `json:"disableTokenEndpoint"`
	SFUAllowPause             bool `json:"sfuAllowPause"`
	EnableEgressBackupStorage bool `json:"enableEgressBackupStorage"`
	EnableEgressAutoRetry     bool `json:"enableEgressAutoRetry"`
}

// ProjectUpdate is a change to a project's settings. A nil field keeps its
// value. The update request is flat: the preferences are top-level fields.
type ProjectUpdate struct {
	Name                             *string  `json:"name,omitempty"`
	EnableAnalytics                  *bool    `json:"enableAnalytics,omitempty"`
	EnableAutoCreate                 *bool    `json:"enableAutoCreate,omitempty"`
	EnableRemoteUnmute               *bool    `json:"enableRemoteUnmute,omitempty"`
	EnabledCodecs                    []string `json:"enabledCodecs,omitempty"`
	EnableUserDataRecording          *bool    `json:"enableUserDataRecording,omitempty"`
	EnableUserDataTraining           *bool    `json:"enableUserDataTraining,omitempty"`
	RequireExplicitDispatch          *bool    `json:"requireExplicitDispatch,omitempty"`
	EnablePIIRedaction               *bool    `json:"enablePiiRedaction,omitempty"`
	PIIRedactionCategories           []string `json:"piiRedactionCategories,omitempty"`
	EnableInferenceRegionRestriction *bool    `json:"enableInferenceRegionRestriction,omitempty"`
	DisableTokenEndpoint             *bool    `json:"disableTokenEndpoint,omitempty"`
	SFUAllowPause                    *bool    `json:"sfuAllowPause,omitempty"`
	EnableEgressBackupStorage        *bool    `json:"enableEgressBackupStorage,omitempty"`
	EnableEgressAutoRetry            *bool    `json:"enableEgressAutoRetry,omitempty"`
	// PinnedRegions replaces the pins when set; an empty Regions list
	// removes them all.
	PinnedRegions *PinnedRegions `json:"pinnedRegions,omitempty"`
}

// PinnedRegions is the set of SFU regions a project is pinned to.
type PinnedRegions struct {
	Regions []string `json:"regions"`
}

// GetProject returns a project with its settings and webhooks.
func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	var out struct {
		Project Project `json:"project"`
	}
	if err := c.do(ctx, http.MethodGet, "/v0/projects/"+seg(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Project, nil
}

// UpdateProject changes the settings that are set and returns the project.
//
// Observed 2026-11-22: an empty enabledCodecs or piiRedactionCategories list
// is ignored rather than clearing the setting; turning PII redaction on with
// no saved categories saves the default set, and turning it off keeps the
// categories; an unknown codec answers 500, an unknown region 400.
func (c *Client) UpdateProject(ctx context.Context, projectID string, u ProjectUpdate) (*Project, error) {
	var out struct {
		Project Project `json:"project"`
	}
	if err := c.do(ctx, http.MethodPatch, "/v0/projects/"+seg(projectID), nil, u, &out); err != nil {
		return nil, err
	}
	return &out.Project, nil
}
