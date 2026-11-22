// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestWebhookLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_webhook.this"
	var firstKey, webhookID string
	f.mutate(func(f *fakeLiveKit) { firstKey = f.project().Keys[0].ID })

	webhook := func(f *fakeLiveKit) *fakeWebhook {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, wh := range f.project().Webhooks {
			if wh.ID == webhookID {
				return wh
			}
		}
		return nil
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if webhook(f) != nil {
				return fmt.Errorf("webhook %s still exists", webhookID)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_webhook" "this" {
  url            = "https://hooks.example.com/livekit"
  include_events = ["room_started", "room_finished"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { webhookID = v; return nil }),
					resource.TestCheckResourceAttr(name, "project_id", fakeProjectID),
					resource.TestCheckResourceAttr(name, "name", ""),
					// The API picks the oldest key.
					resource.TestCheckResourceAttr(name, "signing_key", firstKey),
					resource.TestCheckResourceAttr(name, "include_events.#", "2"),
					resource.TestCheckNoResourceAttr(name, "exclude_events"),
				),
			},
			{
				// Everything changes in place, and the filter is replaced whole.
				Config: f.providerConfig() + `
resource "livekit_api_key" "signing" {
  description         = "webhooks"
  deletion_protection = false
}

resource "livekit_webhook" "this" {
  name           = "backend"
  url            = "https://hooks.example.com/v2/livekit"
  signing_key    = livekit_api_key.signing.id
  exclude_events = ["track_published", "track_unpublished"]
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error {
						if v != webhookID {
							return fmt.Errorf("webhook was replaced: %s != %s", v, webhookID)
						}
						return nil
					}),
					resource.TestCheckResourceAttr(name, "name", "backend"),
					resource.TestCheckResourceAttrPair(name, "signing_key", "livekit_api_key.signing", "id"),
					resource.TestCheckNoResourceAttr(name, "include_events"),
					func(*terraform.State) error {
						wh := webhook(f)
						if wh == nil || len(wh.Include) != 0 || !slices.Contains(wh.Exclude, "track_published") {
							return fmt.Errorf("fake not updated: %+v", wh)
						}
						return nil
					},
				),
			},
			{
				// An empty filter delivers every event; `[]` stays `[]`.
				Config: f.providerConfig() + `
resource "livekit_api_key" "signing" {
  description         = "webhooks"
  deletion_protection = false
}

resource "livekit_webhook" "this" {
  name           = "backend"
  url            = "https://hooks.example.com/v2/livekit"
  signing_key    = livekit_api_key.signing.id
  exclude_events = []
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "exclude_events.#", "0"),
					func(*terraform.State) error {
						if wh := webhook(f); len(wh.Exclude) != 0 {
							return fmt.Errorf("filter not cleared: %+v", wh)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return fakeProjectID + "/" + webhookID, nil },
				ImportStateVerify: true,
				// Null and [] both read back as no events.
				ImportStateVerifyIgnore: []string{"exclude_events"},
			},
			{
				// A webhook deleted in the dashboard is created again.
				PreConfig: func() { f.mutate(func(f *fakeLiveKit) { f.project().Webhooks = nil }) },
				Config: f.providerConfig() + `
resource "livekit_api_key" "signing" {
  description         = "webhooks"
  deletion_protection = false
}

resource "livekit_webhook" "this" {
  name           = "backend"
  url            = "https://hooks.example.com/v2/livekit"
  signing_key    = livekit_api_key.signing.id
  exclude_events = []
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error { webhookID = v; return nil }),
			},
		},
	})
}

func TestWebhookValidation(t *testing.T) {
	f := newFakeLiveKit(t)
	for _, tc := range []struct{ body, want string }{
		{`url = "ftp://example.com"`, `http:// or https:// URL`},
		{`url = "http://localhost:8080/hook"`, `public address`},                                                 // from the API
		{"url = \"https://hooks.example.com/h\"\ninclude_events = [\"room_exploded\"]", `invalid webhook event`}, // from the API
		{"url = \"https://hooks.example.com/h\"\nsigning_key = \"APInope\"", `not an API key of this project`},   // from the API
	} {
		t.Run(tc.want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      f.providerConfig() + "resource \"livekit_webhook\" \"this\" {\n" + tc.body + "\n}\n",
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
	// None of the refused webhooks was created.
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.project().Webhooks); n != 0 {
		t.Fatalf("%d webhooks were created", n)
	}
}

func TestWebhookInvalidImportID(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:        f.providerConfig() + `resource "livekit_webhook" "this" { url = "https://hooks.example.com/h" }`,
			ResourceName:  "livekit_webhook.this",
			ImportState:   true,
			ImportStateId: "WHonlyAnID",
			ExpectError:   regexp.MustCompile(`Expected\s+` + "`<project_id>/<id>`"),
		}},
	})
}

// A rate-limited create is repeated, and makes one webhook.
func TestWebhookRateLimited(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) { f.rateLimitNext = 2 })
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `resource "livekit_webhook" "this" { url = "https://hooks.example.com/h" }`,
			Check: func(*terraform.State) error {
				if n := f.count("POST /v0/projects/" + fakeProjectID + "/webhooks"); n != 3 {
					return fmt.Errorf("%d POSTs, want 3 (two refused with 429)", n)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if n := len(f.project().Webhooks); n != 1 {
					return fmt.Errorf("%d webhooks", n)
				}
				return nil
			},
		}},
	})
}
