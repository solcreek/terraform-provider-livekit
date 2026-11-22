// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// Acceptance tests make real LiveKit Cloud API calls and require:
//   - TF_ACC=1
//   - LIVEKIT_ACC_PROJECT_ID  a project whose name starts with "terraform-acc"
//   - a session: LIVEKIT_SESSION_TOKEN, or `lk cloud auth` on this machine
//
// They refuse any other project, create and delete only objects marked
// tf-acc-*, and put back the project settings they change. Without TF_ACC
// they are skipped.

const accProjectPrefix = "terraform-acc"

func testAccPreCheck(t *testing.T) (*livekit.Client, string) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests are skipped unless TF_ACC is set")
	}
	projectID := os.Getenv("LIVEKIT_ACC_PROJECT_ID")
	if projectID == "" {
		t.Fatal("LIVEKIT_ACC_PROJECT_ID must be set for acceptance tests")
	}
	token := os.Getenv("LIVEKIT_SESSION_TOKEN")
	apiURL := os.Getenv("LIVEKIT_API_URL")
	if token == "" {
		s, err := readCLISession(firstNonEmpty(os.Getenv("LIVEKIT_CLI_CONFIG"), defaultCLIConfigPath()), "")
		if err != nil {
			t.Fatalf("no session: set LIVEKIT_SESSION_TOKEN or run `lk cloud auth`: %v", err)
		}
		token, apiURL = s.token, firstNonEmpty(apiURL, s.apiURL)
	}
	client := livekit.New(token, livekit.WithBaseURL(apiURL))
	p, err := client.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("read project %s: %v", projectID, err)
	}
	if !strings.HasPrefix(p.Name, accProjectPrefix) {
		t.Fatalf("acceptance tests only run against a project named %s*, and %s is %q", accProjectPrefix, projectID, p.Name)
	}
	t.Setenv("LIVEKIT_PROJECT_ID", projectID)
	return client, projectID
}

func TestAccProjectSettings(t *testing.T) {
	client, projectID := testAccPreCheck(t)
	ctx := context.Background()
	before, err := client.GetProject(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	// Put back what the test changes, whatever happens.
	t.Cleanup(func() {
		_, err := client.UpdateProject(ctx, projectID, livekit.ProjectUpdate{
			RequireExplicitDispatch: ptr(before.RequireExplicitDispatch),
			SFUAllowPause:           ptr(before.Preferences.SFUAllowPause),
			EnabledCodecs:           before.EnabledCodecs,
			PinnedRegions:           &livekit.PinnedRegions{Regions: append([]string{}, before.PinnedRegions...)},
		})
		if err != nil {
			t.Errorf("restore settings: %v", err)
		}
	})
	const name = "livekit_project_settings.this"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: requiredProviders + `
resource "livekit_project_settings" "this" {
  require_explicit_dispatch = true
  sfu_allow_pause           = true
  enabled_codecs            = ["video/VP8", "video/H264", "audio/opus", "audio/red"]
  pinned_regions            = ["us"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "project_id", projectID),
					resource.TestCheckResourceAttr(name, "require_explicit_dispatch", "true"),
					resource.TestCheckResourceAttr(name, "enabled_codecs.#", "4"),
					resource.TestCheckTypeSetElemAttr(name, "pinned_regions.*", "us"),
					resource.TestCheckResourceAttr(name, "name", before.Name),
				),
			},
			{
				Config: requiredProviders + `
resource "livekit_project_settings" "this" {
  require_explicit_dispatch = false
  sfu_allow_pause           = false
  enabled_codecs            = ["video/VP8", "audio/opus"]
  pinned_regions            = []
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "require_explicit_dispatch", "false"),
					resource.TestCheckResourceAttr(name, "enabled_codecs.#", "2"),
					resource.TestCheckResourceAttr(name, "pinned_regions.#", "0"),
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     projectID,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccWebhookAndAPIKey(t *testing.T) {
	client, projectID := testAccPreCheck(t)
	suffix := randomSuffix()
	var keyID, webhookID string
	config := func(webhookName, events string) string {
		return requiredProviders + fmt.Sprintf(`
resource "livekit_api_key" "signing" {
  description         = "tf-acc-%[1]s"
  deletion_protection = false
}

resource "livekit_webhook" "this" {
  name           = "tf-acc-%[1]s-%[2]s"
  url            = "https://example.com/tf-acc/%[1]s"
  signing_key    = livekit_api_key.signing.id
  include_events = %[3]s
}
`, suffix, webhookName, events)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			ctx := context.Background()
			if _, err := client.GetWebhook(ctx, projectID, webhookID); !livekit.IsNotFound(err) {
				return fmt.Errorf("webhook %s: %v", webhookID, err)
			}
			if _, err := client.GetKey(ctx, projectID, keyID); !livekit.IsNotFound(err) {
				return fmt.Errorf("key %s: %v", keyID, err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("a", `["room_started", "room_finished"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("livekit_api_key.signing", "id", func(v string) error { keyID = v; return nil }),
					resource.TestCheckResourceAttrSet("livekit_api_key.signing", "secret"),
					resource.TestCheckResourceAttr("livekit_api_key.signing", "service_account", "true"),
					resource.TestCheckNoResourceAttr("livekit_api_key.signing", "owner_email"),
					resource.TestCheckResourceAttrWith("livekit_webhook.this", "id", func(v string) error { webhookID = v; return nil }),
					resource.TestCheckResourceAttrPair("livekit_webhook.this", "signing_key", "livekit_api_key.signing", "id"),
					resource.TestCheckResourceAttr("livekit_webhook.this", "include_events.#", "2"),
				),
			},
			{
				Config: config("b", `["participant_joined"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("livekit_webhook.this", "name", "tf-acc-"+suffix+"-b"),
					resource.TestCheckResourceAttr("livekit_webhook.this", "include_events.#", "1"),
					resource.TestCheckResourceAttrWith("livekit_webhook.this", "id", func(v string) error {
						if v != webhookID {
							return fmt.Errorf("webhook was replaced")
						}
						return nil
					}),
				),
			},
			{
				ResourceName:      "livekit_webhook.this",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return projectID + "/" + webhookID, nil },
				ImportStateVerify: true,
			},
			{
				ResourceName:            "livekit_api_key.signing",
				ImportState:             true,
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return projectID + "/" + keyID, nil },
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret", "deletion_protection"},
			},
		},
	})
}

// TestAccAPIFacts checks the API behaviour the fake in fake_livekit_test.go
// mirrors. When it fails, change the fake and its dated comment.
func TestAccAPIFacts(t *testing.T) {
	client, projectID := testAccPreCheck(t)
	ctx := context.Background()
	suffix := randomSuffix()

	key, err := client.GenerateKey(ctx, projectID, "tf-acc-"+suffix, true)
	if err != nil {
		t.Fatal(err)
	}
	keyDeleted := false
	t.Cleanup(func() {
		if !keyDeleted {
			_ = client.DeleteKey(ctx, projectID, key.APIKey)
		}
	})
	listed, err := client.GetKey(ctx, projectID, key.APIKey)
	if err != nil || listed.OwnerID != "" || listed.Description != "tf-acc-"+suffix {
		t.Errorf("service account key %+v, %v", listed, err)
	}

	wh, err := client.CreateWebhook(ctx, projectID, livekit.WebhookCreate{
		Name: "tf-acc-" + suffix, URL: "https://example.com/tf-acc/" + suffix, SigningKey: key.APIKey,
		FilterParams: livekit.FilterParams{IncludeEvents: []string{}, ExcludeEvents: []string{"room_started"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.DeleteWebhook(ctx, projectID, wh.ID) })

	// An update keeps what it does not get; "" keeps the signing key.
	empty := ""
	got, err := client.UpdateWebhook(ctx, projectID, wh.ID, livekit.WebhookUpdate{SigningKey: &empty})
	if err != nil || got.Name != wh.Name || got.SigningKey != key.APIKey || !slices.Equal(got.FilterParams.ExcludeEvents, []string{"room_started"}) {
		t.Errorf("update with nothing set changed the webhook: %+v, %v", got, err)
	}
	// A webhook keeps its signing key ID after the key is deleted.
	if err := client.DeleteKey(ctx, projectID, key.APIKey); err != nil {
		t.Fatal(err)
	}
	keyDeleted = true
	if err := client.DeleteKey(ctx, projectID, key.APIKey); !livekit.IsNotFound(err) {
		t.Errorf("deleting a key twice: %v", err)
	}
	if got, err := client.GetWebhook(ctx, projectID, wh.ID); err != nil || got.SigningKey != key.APIKey {
		t.Errorf("webhook after its key was deleted: %+v, %v", got, err)
	}
	if err := client.DeleteWebhook(ctx, projectID, wh.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteWebhook(ctx, projectID, wh.ID); !livekit.IsNotFound(err) {
		t.Errorf("deleting a webhook twice: %v", err)
	}

	// An empty codec list is ignored rather than clearing the codecs.
	before, err := client.GetProject(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := client.UpdateProject(ctx, projectID, livekit.ProjectUpdate{EnabledCodecs: []string{}})
	if err != nil || !slices.Equal(after.EnabledCodecs, before.EnabledCodecs) {
		t.Errorf("empty codec list: %v -> %v, %v", before.EnabledCodecs, after.EnabledCodecs, err)
	}
	// An unknown region is refused with 400.
	if _, err := client.UpdateProject(ctx, projectID, livekit.ProjectUpdate{PinnedRegions: &livekit.PinnedRegions{Regions: []string{"mars"}}}); livekit.StatusCode(err) != 400 {
		t.Errorf("unknown region: %v", err)
	}
	// A URL without a public address is refused.
	if _, err := client.CreateWebhook(ctx, projectID, livekit.WebhookCreate{URL: "http://localhost/hook"}); livekit.StatusCode(err) != 400 {
		t.Errorf("localhost webhook: %v", err)
	}
}
