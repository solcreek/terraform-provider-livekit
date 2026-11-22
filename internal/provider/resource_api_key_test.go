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

func TestAPIKeyLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_api_key.this"
	var keyID string
	exists := func(id string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return slices.ContainsFunc(f.project().Keys, func(k *fakeKey) bool { return k.ID == id })
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if exists(keyID) {
				return fmt.Errorf("key %s still exists", keyID)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_api_key" "this" {
  description = "Production (GKE)"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { keyID = v; return nil }),
					resource.TestCheckResourceAttrWith(name, "secret", func(v string) error {
						if v != "secret-of-"+keyID {
							return fmt.Errorf("secret %q", v)
						}
						return nil
					}),
					resource.TestCheckResourceAttr(name, "service_account", "true"),
					resource.TestCheckNoResourceAttr(name, "owner_email"),
					resource.TestCheckResourceAttrSet(name, "created_at"),
					resource.TestCheckResourceAttr(name, "deletion_protection", "true"),
				),
			},
			{
				// A new description replaces the key, which protection refuses.
				Config: f.providerConfig() + `
resource "livekit_api_key" "this" {
  description = "Production (Vercel)"
}
`,
				ExpectError: regexp.MustCompile(`Deletion protection is enabled`),
			},
			{
				Config: f.providerConfig() + `
resource "livekit_api_key" "this" {
  description         = "Production (GKE)"
  deletion_protection = false
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if !exists(keyID) {
						return fmt.Errorf("key %s was deleted", keyID)
					}
					return nil
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return fakeProjectID + "/" + keyID, nil },
				ImportStateVerify: true,
				// The API never returns the secret again; protection is state only.
				ImportStateVerifyIgnore: []string{"secret", "deletion_protection"},
			},
			{
				// A key revoked in the dashboard is created again.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) {
						p := f.project()
						p.Keys = slices.DeleteFunc(p.Keys, func(k *fakeKey) bool { return k.ID == keyID })
					})
				},
				Config: f.providerConfig() + `
resource "livekit_api_key" "this" {
  description         = "Production (GKE)"
  deletion_protection = false
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionCreate)},
				},
				Check: resource.TestCheckResourceAttrWith(name, "id", func(v string) error { keyID = v; return nil }),
			},
		},
	})
}

// A user's own key reports its owner.
func TestAPIKeyUserOwned(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "livekit_api_key" "this" {
  service_account     = false
  deletion_protection = false
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("livekit_api_key.this", "service_account", "false"),
				resource.TestCheckResourceAttr("livekit_api_key.this", "owner_email", fakeUserEmail),
				resource.TestCheckResourceAttr("livekit_api_key.this", "description", ""),
			),
		}},
	})
}

// A change of the provider's default project does not move (replace) an
// existing key; an explicit project_id does.
func TestAPIKeyProjectDefault(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) { f.addProject("p_other", "Other") })
	other := requiredProviders + fmt.Sprintf(`
provider "livekit" {
  session_token = %q
  api_url       = %q
  project_id    = "p_other"
}
`, fakeToken, f.srv.URL)
	key := `
resource "livekit_api_key" "this" {
  deletion_protection = false
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: f.providerConfig() + key},
			{
				Config: other + key,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: other + `
resource "livekit_api_key" "this" {
  project_id          = "p_other"
  deletion_protection = false
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("livekit_api_key.this", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr("livekit_api_key.this", "project_id", "p_other"),
			},
		},
	})
}
