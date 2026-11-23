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

func (f *fakeLiveKit) member(userID string) *fakeMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.project().Members, func(m *fakeMember) bool { return m.UserID == userID })
	if i < 0 {
		return nil
	}
	return f.project().Members[i]
}

func TestProjectMemberLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) { f.project().Private = true })
	const name = "livekit_project_member.dev"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.member("UfakeUser0002") != nil {
				return fmt.Errorf("member still there")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_project_member" "dev" {
  user_id = "UfakeUser0002"
  role    = "read"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", fakeProjectID+"/UfakeUser0002"),
					resource.TestCheckResourceAttr(name, "email", "dev@example.com"),
					resource.TestCheckResourceAttr(name, "role", "read"),
				),
			},
			{
				Config: f.providerConfig() + `
resource "livekit_project_member" "dev" {
  user_id = "UfakeUser0002"
  role    = "write"
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if m := f.member("UfakeUser0002"); m == nil || m.Role != 2 {
						return fmt.Errorf("fake: %+v", m)
					}
					return nil
				},
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     fakeProjectID + "/UfakeUser0002",
				ImportStateVerify: true,
			},
		},
	})
}

// The API would silently change an existing member's role; the provider
// refuses and points to import.
func TestProjectMemberRefusesToAdopt(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) {
		f.project().Private = true
		f.project().Members = append(f.project().Members, &fakeMember{UserID: "UfakeUser0003", Email: "ops@example.com", Role: 1})
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `resource "livekit_project_member" "ops" {
  user_id = "UfakeUser0003"
  role    = "admin"
}`,
			ExpectError: regexp.MustCompile(`(?s)Already a member.*import`),
		}},
	})
	if m := f.member("UfakeUser0003"); m.Role != 1 {
		t.Fatalf("role changed to %d", m.Role)
	}
}

func TestProjectMemberGuards(t *testing.T) {
	f := newFakeLiveKit(t)
	// Not private: members come from the workspace.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `resource "livekit_project_member" "dev" {
  user_id = "UfakeUser0002"
  role    = "read"
}`,
			ExpectError: regexp.MustCompile(`Project is not private`),
		}},
	})

	f.mutate(func(f *fakeLiveKit) { f.project().Private = true })
	// Yourself: imported fine, never removed.
	me := f.providerConfig() + `resource "livekit_project_member" "me" {
  user_id = "` + fakeUserID + `"
  role    = "admin"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             me,
				ResourceName:       "livekit_project_member.me",
				ImportState:        true,
				ImportStateId:      fakeProjectID + "/" + fakeUserID,
				ImportStatePersist: true,
			},
			{Config: me, Destroy: true, ExpectError: regexp.MustCompile(`Refusing to remove yourself`)},
			{
				PreConfig: func() {
					if f.member(fakeUserID) == nil {
						t.Fatal("removed the session's own user")
					}
					// Become someone else, so the test can clean up.
					f.mutate(func(f *fakeLiveKit) {
						f.meID = "UsomeoneElse"
						for _, k := range f.project().Keys {
							k.Owner = "" // or the removal is refused for owning keys
						}
					})
				},
				Config: f.providerConfig(),
			},
		},
	})
}

// A member who owns API keys is not removed: that would delete the keys.
func TestProjectMemberWithKeys(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) { f.project().Private = true })
	cfg := f.providerConfig() + `resource "livekit_project_member" "dev" {
  user_id = "UfakeUser0002"
  role    = "write"
}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) {
						f.project().Keys = append(f.project().Keys, &fakeKey{ID: "APIdevkey", Owner: "UfakeUser0002"})
					})
				},
				Config:      cfg,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`(?s)owns API keys.*never deletes keys`),
			},
			{
				// Once the key is gone, the member can be removed.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) {
						f.project().Keys = slices.DeleteFunc(f.project().Keys, func(k *fakeKey) bool { return k.ID == "APIdevkey" })
					})
				},
				Config: cfg,
			},
		},
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.ContainsFunc(f.project().Keys, func(k *fakeKey) bool { return k.Owner == fakeUserID }) {
		t.Fatal("a key was deleted")
	}
}

func TestPhoneNumberLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	// The first rename after the purchase fails, as one did live.
	f.mutate(func(f *fakeLiveKit) { f.renameFailNext = 1 })
	const name = "livekit_phone_number.main"
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_phone_number" "main" {
  phone_number = "+14155550101"
  name         = "support line"
}

resource "livekit_sip_dispatch_rule" "support" {
  trunk_ids  = [livekit_phone_number.main.id]
  individual = {}
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(name, "name", "support line"),
					resource.TestCheckResourceAttr(name, "status", "active"),
					resource.TestCheckResourceAttr(name, "number_type", "local"),
					resource.TestCheckResourceAttr(name, "deletion_protection", "true"),
					resource.TestCheckResourceAttrPair("livekit_sip_dispatch_rule.support", "trunk_ids.0", name, "id"),
					func(*terraform.State) error {
						if f.purchases != 1 {
							return fmt.Errorf("%d purchases", f.purchases)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            name,
				ImportState:             true,
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return fakeProjectID + "/" + id, nil },
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"deletion_protection"},
			},
			{
				// Another number would release this one: refused.
				Config: f.providerConfig() + `
resource "livekit_phone_number" "main" {
  phone_number = "+14155550102"
}
`,
				ExpectError: regexp.MustCompile(`Deletion protection is enabled`),
			},
			{
				Config: f.providerConfig() + `
resource "livekit_phone_number" "main" {
  phone_number        = "+14155550101"
  name                = "support"
  deletion_protection = false
}
`,
				Check: resource.TestCheckResourceAttr(name, "name", "support"),
			},
			{
				// A released number is gone from the state.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) { f.project().Numbers[0].Status = "PHONE_NUMBER_STATUS_RELEASED" })
				},
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
	if f.purchases != 1 {
		t.Fatalf("%d purchases, want 1", f.purchases)
	}
}

func TestPhoneNumberNotForSale(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      f.providerConfig() + `resource "livekit_phone_number" "x" { phone_number = "+14155559999" }`,
			ExpectError: regexp.MustCompile(`(?s)Unable to buy phone number.*not available`),
		}},
	})
}

func TestPhase2DataSources(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
data "livekit_project_members" "this" {}
data "livekit_workspace_members" "this" {}
data "livekit_available_phone_numbers" "sf" {
  country_code = "US"
  area_code    = "415"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.livekit_project_members.this", "members.#", "1"),
				resource.TestCheckResourceAttr("data.livekit_project_members.this", "members.0.role", "admin"),
				resource.TestCheckResourceAttr("data.livekit_workspace_members.this", "workspace_id", "wo_fake0001"),
				resource.TestCheckResourceAttr("data.livekit_workspace_members.this", "members.#", "3"),
				resource.TestCheckResourceAttr("data.livekit_workspace_members.this", "members.2.role", "read"),
				resource.TestCheckResourceAttr("data.livekit_available_phone_numbers.sf", "numbers.#", "3"),
				resource.TestCheckResourceAttr("data.livekit_available_phone_numbers.sf", "numbers.0.phone_number", "+14155550100"),
				resource.TestCheckResourceAttr("data.livekit_available_phone_numbers.sf", "numbers.0.capabilities.0", "voice"),
				func(*terraform.State) error {
					if f.purchases != 0 {
						return fmt.Errorf("searching bought a number")
					}
					return nil
				},
			),
		}},
	})
}
