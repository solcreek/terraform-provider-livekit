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

func TestProjectSettingsLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_project_settings.this"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		// Destroying only stops managing: the settings stay.
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.project().ExplicitDispatch {
				return fmt.Errorf("destroy changed the project's settings")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_project_settings" "this" {
  enable_auto_create        = false
  require_explicit_dispatch = true
  enabled_codecs            = ["video/VP8", "audio/opus"]
  pinned_regions            = ["us"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", fakeProjectID),
					resource.TestCheckResourceAttr(name, "project_id", fakeProjectID),
					resource.TestCheckResourceAttr(name, "enable_auto_create", "false"),
					resource.TestCheckResourceAttr(name, "require_explicit_dispatch", "true"),
					resource.TestCheckResourceAttr(name, "enabled_codecs.#", "2"),
					resource.TestCheckTypeSetElemAttr(name, "pinned_regions.*", "us"),
					// Settings the configuration leaves out are read, not changed.
					resource.TestCheckResourceAttr(name, "name", "Acme"),
					resource.TestCheckResourceAttr(name, "enable_analytics", "true"),
					resource.TestCheckResourceAttr(name, "disable_token_endpoint", "true"),
					resource.TestCheckResourceAttr(name, "pii_redaction_categories.#", "0"),
					resource.TestCheckResourceAttr(name, "url", "wss://acme-abc123.livekit.cloud"),
					resource.TestCheckResourceAttr(name, "workspace_id", "wo_fake0001"),
					func(*terraform.State) error {
						f.mu.Lock()
						defer f.mu.Unlock()
						p := f.project()
						if p.AutoCreate || !p.ExplicitDispatch || !slices.Equal(p.Regions, []string{"us"}) || len(p.Codecs) != 2 {
							return fmt.Errorf("fake not updated: %+v", p)
						}
						return nil
					},
				),
			},
			{
				// A setting changed in the dashboard is drift, and is put back.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) { f.project().ExplicitDispatch = false; f.project().Name = "Renamed" })
				},
				Config: f.providerConfig() + `
resource "livekit_project_settings" "this" {
  enable_auto_create        = false
  require_explicit_dispatch = true
  enabled_codecs            = ["video/VP8", "audio/opus"]
  pinned_regions            = ["us"]
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "require_explicit_dispatch", "true"),
					// An unmanaged setting follows the API without a diff.
					resource.TestCheckResourceAttr(name, "name", "Renamed"),
				),
			},
			{
				// PII redaction with chosen categories; an empty set unpins.
				Config: f.providerConfig() + `
resource "livekit_project_settings" "this" {
  enable_auto_create        = false
  require_explicit_dispatch = true
  enabled_codecs            = ["video/VP8", "audio/opus"]
  pinned_regions            = []
  enable_pii_redaction      = true
  pii_redaction_categories  = ["ssn", "credit-card-expiration"]
  sfu_allow_pause           = true
  enable_egress_auto_retry  = true
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "pinned_regions.#", "0"),
					resource.TestCheckResourceAttr(name, "enable_pii_redaction", "true"),
					resource.TestCheckTypeSetElemAttr(name, "pii_redaction_categories.*", "credit-card-expiration"),
					resource.TestCheckResourceAttr(name, "sfu_allow_pause", "true"),
					resource.TestCheckResourceAttr(name, "enable_egress_auto_retry", "true"),
					func(*terraform.State) error {
						f.mu.Lock()
						defer f.mu.Unlock()
						p := f.project()
						if !slices.Contains(p.PII, "PII_REDACTION_CATEGORY_CREDIT_CARD_EXPIRATION") || !p.SFUPause {
							return fmt.Errorf("fake not updated: %+v", p)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     fakeProjectID,
				ImportStateVerify: true,
			},
		},
	})
}

// Turning redaction on with no categories gets LiveKit's default set, which
// the state then holds.
func TestProjectSettingsPIIDefaults(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "livekit_project_settings" "this" {
  enable_pii_redaction = true
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("livekit_project_settings.this", "pii_redaction_categories.#", "3"),
				resource.TestCheckTypeSetElemAttr("livekit_project_settings.this", "pii_redaction_categories.*", "email-address"),
			),
		}},
	})
}

func TestProjectSettingsValidation(t *testing.T) {
	f := newFakeLiveKit(t)
	for _, tc := range []struct{ body, want string }{
		{`enabled_codecs = []`, `at least 1`},
		{`enabled_codecs = ["vp8"]`, `MIME type`},
		{`pii_redaction_categories = ["SSN"]`, `value must be one of`},
		{`project_id = "teachify"`, `Invalid project ID`},
		{`pinned_regions = ["mars"]`, `unknown region`}, // from the API
	} {
		t.Run(tc.want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      f.providerConfig() + "resource \"livekit_project_settings\" \"this\" {\n" + tc.body + "\n}\n",
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}

// A project the session cannot see is gone from the state, not an error.
func TestProjectSettingsProjectGone(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) { f.addProject("p_other", "Other") })
	cfg := f.providerConfig() + `
resource "livekit_project_settings" "this" {
  project_id                = "p_other"
  require_explicit_dispatch = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:          func() { f.mutate(func(f *fakeLiveKit) { delete(f.projects, "p_other") }) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
