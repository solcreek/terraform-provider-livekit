// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// sipObject returns a stored SIP object of the fake's default project.
func (f *fakeLiveKit) sipObject(kind, id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.project().SIP[kind][id]
}

func TestSIPInboundTrunkLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_sip_inbound_trunk.this"
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.sipObject("inbound-trunks", id) != nil {
				return fmt.Errorf("trunk %s still exists", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_sip_inbound_trunk" "this" {
  name              = "twilio"
  numbers           = ["+14155550100"]
  allowed_addresses = ["203.0.113.0/24"]
  auth_username     = "livekit"
  auth_password     = "s3cret"
  headers           = { "X-Env" = "prod" }
  ringing_timeout   = 30
  krisp_enabled     = true
  media_encryption  = "allow"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(name, "auth_password", "s3cret"),
					resource.TestCheckResourceAttr(name, "include_headers", "none"),
					resource.TestCheckResourceAttr(name, "ringing_timeout", "30"),
					resource.TestCheckNoResourceAttr(name, "max_call_duration"),
					resource.TestCheckNoResourceAttr(name, "allowed_numbers"),
					resource.TestCheckResourceAttr(name, "headers.X-Env", "prod"),
					func(*terraform.State) error {
						o := f.sipObject("inbound-trunks", id)
						if o["authPassword"] != "s3cret" || o["ringingTimeout"] != "30s" || o["mediaEncryption"] != "SIP_MEDIA_ENCRYPT_ALLOW" {
							return fmt.Errorf("fake: %v", o)
						}
						return nil
					},
				),
			},
			{
				// A field set in the dashboard that the provider does not model
				// survives an update; the password is sent again, not cleared.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) {
						f.project().SIP["inbound-trunks"][id]["media"] = map[string]any{"onlyListedCodecs": true}
					})
				},
				Config: f.providerConfig() + `
resource "livekit_sip_inbound_trunk" "this" {
  name              = "twilio-main"
  numbers           = ["+14155550100", "+14155550101"]
  allowed_addresses = ["203.0.113.0/24"]
  auth_username     = "livekit"
  auth_password     = "s3cret"
  max_call_duration = 3600
  include_headers   = "x_headers"
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "numbers.#", "2"),
					resource.TestCheckNoResourceAttr(name, "ringing_timeout"),
					resource.TestCheckNoResourceAttr(name, "headers"),
					resource.TestCheckResourceAttr(name, "krisp_enabled", "false"),
					func(*terraform.State) error {
						o := f.sipObject("inbound-trunks", id)
						if o["media"] == nil || o["authPassword"] != "s3cret" || o["ringingTimeout"] != nil {
							return fmt.Errorf("fake: %v", o)
						}
						return nil
					},
				),
			},
			{
				// A password removed in the dashboard is drift.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) { f.project().SIP["inbound-trunks"][id]["authPassword"] = "" })
				},
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return fakeProjectID + "/" + id, nil },
				ImportStateVerify: true,
				// The API never returns the password.
				ImportStateVerifyIgnore: []string{"auth_password"},
			},
		},
	})
}

func TestSIPInboundTrunkValidation(t *testing.T) {
	f := newFakeLiveKit(t)
	for _, tc := range []struct{ body, want string }{
		{`numbers = ["4155550100"]`, `E.164`},
		{`headers = { "Env" = "x" }`, `X-\* header`},
		{`media_encryption = "on"`, `value must be one of`},
		{`ringing_timeout = 0`, `at least 1`},
		{`name = "open to anyone"`, `(?s)At least one of these attributes.*numbers`},
	} {
		t.Run(tc.want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      f.providerConfig() + "resource \"livekit_sip_inbound_trunk\" \"this\" {\n" + tc.body + "\n}\n",
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
	// A conflict is the API's to find.
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "livekit_sip_inbound_trunk" "a" { numbers = ["+14155550100"] }
resource "livekit_sip_inbound_trunk" "b" {
  numbers    = ["+14155550100"]
  depends_on = [livekit_sip_inbound_trunk.a]
}
`,
			ExpectError: regexp.MustCompile(`Conflicting inbound SIP Trunks`),
		}},
	})
}

func TestSIPOutboundTrunkLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_sip_outbound_trunk.this"
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "livekit_sip_outbound_trunk" "this" {
  address       = "example.pstn.twilio.com"
  numbers       = ["+14155550100"]
  auth_username = "livekit"
  auth_password = "s3cret"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(name, "transport", "auto"),
					resource.TestCheckResourceAttr(name, "destination_country", ""),
				),
			},
			{
				Config: f.providerConfig() + `
resource "livekit_sip_outbound_trunk" "this" {
  address             = "sip.example.com:5061"
  transport           = "tls"
  destination_country = "US"
  from_host           = "calls.example.com"
  numbers             = ["+14155550100"]
  media_encryption    = "require"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "transport", "tls"),
					resource.TestCheckNoResourceAttr(name, "auth_password"),
					func(*terraform.State) error {
						o := f.sipObject("outbound-trunks", id)
						if o["transport"] != "SIP_TRANSPORT_TLS" || o["authPassword"] != "" {
							return fmt.Errorf("fake: %v", o)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return fakeProjectID + "/" + id, nil },
				ImportStateVerify: true,
			},
		},
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      f.providerConfig() + `resource "livekit_sip_outbound_trunk" "x" { address = "sip:example.com" }`,
			ExpectError: regexp.MustCompile(`without\s+` + "`sip:`"),
		}},
	})
}

func TestSIPDispatchRuleLifecycle(t *testing.T) {
	f := newFakeLiveKit(t)
	const name = "livekit_sip_dispatch_rule.this"
	var id string
	trunk := `
resource "livekit_sip_inbound_trunk" "main" {
  numbers = ["+14155550100"]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + trunk + `
resource "livekit_sip_dispatch_rule" "this" {
  name       = "support"
  trunk_ids  = [livekit_sip_inbound_trunk.main.id]
  individual = { room_prefix = "call-" }
  agents     = [{ agent_name = "support-agent" }]
  attributes = { team = "support" }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(name, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(name, "individual.room_prefix", "call-"),
					resource.TestCheckResourceAttr(name, "individual.no_randomness", "false"),
					resource.TestCheckNoResourceAttr(name, "individual.pin"),
					resource.TestCheckNoResourceAttr(name, "direct"),
					resource.TestCheckResourceAttr(name, "agents.0.agent_name", "support-agent"),
					resource.TestCheckResourceAttr(name, "agents.0.metadata", ""),
					resource.TestCheckResourceAttrPair(name, "trunk_ids.0", "livekit_sip_inbound_trunk.main", "id"),
				),
			},
			{
				// The rule type changes in place; room configuration fields
				// set elsewhere survive, the agents follow the configuration.
				PreConfig: func() {
					f.mutate(func(f *fakeLiveKit) {
						f.project().SIP["dispatch-rules"][id]["roomConfig"].(map[string]any)["emptyTimeout"] = float64(60)
					})
				},
				Config: f.providerConfig() + trunk + `
resource "livekit_sip_dispatch_rule" "this" {
  name      = "support"
  trunk_ids = [livekit_sip_inbound_trunk.main.id]
  direct    = { room_name = "lobby", pin = "1234" }
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(name, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "direct.room_name", "lobby"),
					resource.TestCheckResourceAttr(name, "direct.pin", "1234"),
					resource.TestCheckNoResourceAttr(name, "individual"),
					resource.TestCheckNoResourceAttr(name, "agents"),
					resource.TestCheckNoResourceAttr(name, "attributes"),
					func(*terraform.State) error {
						rc, _ := f.sipObject("dispatch-rules", id)["roomConfig"].(map[string]any)
						if rc["emptyTimeout"] != float64(60) || len(rc["agents"].([]any)) != 0 {
							return fmt.Errorf("fake roomConfig: %v", rc)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return fakeProjectID + "/" + id, nil },
				ImportStateVerify: true,
			},
		},
	})
}

func TestSIPDispatchRuleValidation(t *testing.T) {
	f := newFakeLiveKit(t)
	for _, tc := range []struct{ body, want string }{
		{`name = "x"`, `(?s)Exactly one of these attributes.*direct`},
		{"direct = { room_name = \"a\" }\ncallee = {}", `(?s)Invalid Attribute Combination`},
		{"direct = { room_name = \"a\" }\ntrunk_ids = [\"SDR_x\"]", `trunk \(ST_...\) or phone number`},
		{`direct = { room_name = "a", pin = "12ab" }`, `must be digits`},
	} {
		t.Run(tc.want, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      f.providerConfig() + "resource \"livekit_sip_dispatch_rule\" \"this\" {\n" + tc.body + "\n}\n",
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}
