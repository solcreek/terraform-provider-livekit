// SPDX-License-Identifier: MPL-2.0

package livekit

import (
	"context"
	"net/http"
	"net/url"
)

// Member is a user's membership of a project or a workspace. Role is 0
// (invited), 1 (read), 2 (write) or 3 (admin).
type Member struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Role   int    `json:"role"`
}

// User is a LiveKit Cloud user.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

func membersPath(projectID string) string {
	return "/v0/projects/" + seg(projectID) + "/members"
}

// CurrentUser returns the user the session belongs to.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var out struct {
		User User `json:"user"`
	}
	if err := c.do(ctx, http.MethodGet, "/v0/users/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.User, nil
}

// GetMember returns one member of a project; a user who is not a member is
// a 404.
func (c *Client) GetMember(ctx context.Context, projectID, userID string) (*Member, error) {
	var out struct {
		Member Member `json:"member"`
	}
	if err := c.do(ctx, http.MethodGet, membersPath(projectID)+"/"+seg(userID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Member, nil
}

// ListMembers returns a project's members.
func (c *Client) ListMembers(ctx context.Context, projectID string) ([]Member, error) {
	return list[Member](ctx, c, membersPath(projectID), nil)
}

// ListWorkspaceMembers returns a workspace's members.
func (c *Client) ListWorkspaceMembers(ctx context.Context, workspaceID string) ([]Member, error) {
	return list[Member](ctx, c, "/v0/workspaces/"+seg(workspaceID)+"/members", nil)
}

// AddWorkspaceMember adds a member of the project's workspace to a private
// project. Careful: a user who is already a member silently gets the new
// role, so callers check first.
func (c *Client) AddWorkspaceMember(ctx context.Context, projectID, userID string, role int) error {
	body := map[string]any{"userIds": []string{userID}, "role": role}
	return c.do(ctx, http.MethodPost, membersPath(projectID)+":addWorkspaceMembers", nil, body, nil)
}

// UpdateMemberRole changes a member's role. On a project that is not private
// this changes the user's role on the whole workspace.
func (c *Client) UpdateMemberRole(ctx context.Context, projectID, userID string, role int) (*Member, error) {
	var out struct {
		Member Member `json:"member"`
	}
	if err := c.do(ctx, http.MethodPatch, membersPath(projectID)+"/"+seg(userID), nil, map[string]any{"role": role}, &out); err != nil {
		return nil, err
	}
	return &out.Member, nil
}

// RemoveMember removes a member, agreeing to delete none of their API keys:
// when they own keys in the project, the API refuses with 400
// (FailedPrecondition) and changes nothing. On a project that is not private
// it removes the user from the whole workspace.
func (c *Client) RemoveMember(ctx context.Context, projectID, userID string) error {
	return c.do(ctx, http.MethodDelete, membersPath(projectID)+"/"+seg(userID), url.Values{"confirmedNumKeys": {"0"}}, nil, nil)
}
