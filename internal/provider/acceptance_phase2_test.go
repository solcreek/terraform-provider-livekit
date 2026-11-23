// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// Phone numbers are never bought by these tests: buying costs money. Only
// the free search is exercised live; livekit_phone_number is tested against
// the fake alone.

// accNumber makes a phone number unique to a test run, in the 555-01xx
// fictional range.
func accNumber(suffix string, n int) string {
	return fmt.Sprintf("+1415555%02d%02d", int(suffix[0])%10, n)
}

func TestAccSIP(t *testing.T) {
	client, projectID := testAccPreCheck(t)
	suffix := randomSuffix()
	var inboundID, outboundID, ruleID string
	config := func(roomPrefix, agent string) string {
		return requiredProviders + fmt.Sprintf(`
resource "livekit_sip_inbound_trunk" "in" {
  name              = "tf-acc-%[1]s"
  numbers           = [%[2]q]
  allowed_addresses = ["203.0.113.0/24"]
  auth_username     = "tf-acc"
  auth_password     = "tf-acc-%[1]s"
  ringing_timeout   = 30
  headers_to_attributes = { "X-Customer" = "customer" }
}

resource "livekit_sip_outbound_trunk" "out" {
  name                = "tf-acc-%[1]s"
  address             = "sip.example.com"
  transport           = "tls"
  destination_country = "US"
  numbers             = [%[2]q]
}

resource "livekit_sip_dispatch_rule" "rule" {
  name       = "tf-acc-%[1]s"
  trunk_ids  = [livekit_sip_inbound_trunk.in.id]
  individual = { room_prefix = %[3]q }
  agents     = [{ agent_name = %[4]q }]
}
`, suffix, accNumber(suffix, 1), roomPrefix, agent)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			ctx := context.Background()
			for kind, id := range map[livekit.SIPKind]string{livekit.SIPInboundTrunk: inboundID, livekit.SIPOutboundTrunk: outboundID, livekit.SIPDispatchRule: ruleID} {
				if _, err := client.GetSIP(ctx, kind, projectID, id); !livekit.IsNotFound(err) {
					return fmt.Errorf("%s %s: %v", kind.Noun, id, err)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("call-", "tf-acc-agent"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("livekit_sip_inbound_trunk.in", "id", func(v string) error { inboundID = v; return nil }),
					resource.TestCheckResourceAttrWith("livekit_sip_outbound_trunk.out", "id", func(v string) error { outboundID = v; return nil }),
					resource.TestCheckResourceAttrWith("livekit_sip_dispatch_rule.rule", "id", func(v string) error { ruleID = v; return nil }),
					resource.TestCheckResourceAttr("livekit_sip_inbound_trunk.in", "ringing_timeout", "30"),
					resource.TestCheckResourceAttr("livekit_sip_outbound_trunk.out", "transport", "tls"),
					resource.TestCheckResourceAttr("livekit_sip_dispatch_rule.rule", "agents.0.agent_name", "tf-acc-agent"),
				),
			},
			{
				Config: config("support-", "tf-acc-agent-2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("livekit_sip_dispatch_rule.rule", "individual.room_prefix", "support-"),
					resource.TestCheckResourceAttrWith("livekit_sip_dispatch_rule.rule", "id", func(v string) error {
						if v != ruleID {
							return fmt.Errorf("rule was replaced")
						}
						return nil
					}),
				),
			},
			{
				ResourceName:            "livekit_sip_inbound_trunk.in",
				ImportState:             true,
				ImportStateIdFunc:       func(*terraform.State) (string, error) { return projectID + "/" + inboundID, nil },
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"auth_password"},
			},
			{
				ResourceName:      "livekit_sip_outbound_trunk.out",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return projectID + "/" + outboundID, nil },
				ImportStateVerify: true,
			},
			{
				ResourceName:      "livekit_sip_dispatch_rule.rule",
				ImportState:       true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return projectID + "/" + ruleID, nil },
				ImportStateVerify: true,
			},
		},
	})
}

// Members and numbers are read only: adding a member needs a second user,
// and buying a number costs money.
func TestAccMembersAndNumbersReadOnly(t *testing.T) {
	_, projectID := testAccPreCheck(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `
data "livekit_project" "this" {}
data "livekit_project_members" "this" {}
data "livekit_workspace_members" "this" {}
data "livekit_available_phone_numbers" "sf" {
  country_code = "US"
  area_code    = "415"
  limit        = 2
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.livekit_project.this", "is_private", "true"),
				resource.TestCheckResourceAttr("data.livekit_project_members.this", "project_id", projectID),
				resource.TestCheckResourceAttr("data.livekit_project_members.this", "members.0.role", "admin"),
				resource.TestCheckResourceAttrPair("data.livekit_workspace_members.this", "workspace_id", "data.livekit_project.this", "workspace_id"),
				resource.TestCheckResourceAttrSet("data.livekit_available_phone_numbers.sf", "numbers.0.phone_number"),
			),
		}},
	})
}

// TestAccSIPFacts checks the SIP and member behaviour the fake mirrors.
func TestAccSIPFacts(t *testing.T) {
	client, projectID := testAccPreCheck(t)
	ctx := context.Background()
	suffix := randomSuffix()
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	str := func(o livekit.SIPObject, k string) string {
		var s string
		_ = json.Unmarshal(o[k], &s)
		return s
	}

	trunk, err := client.CreateSIP(ctx, livekit.SIPInboundTrunk, projectID, livekit.SIPObject{
		"name": raw("tf-acc-" + suffix), "numbers": raw([]string{accNumber(suffix, 2)[1:]}),
		"authUsername": raw("u"), "authPassword": raw("p"), "krispEnabled": raw(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	id := livekit.SIPInboundTrunk.ID(trunk)
	t.Cleanup(func() { _ = client.DeleteSIP(ctx, livekit.SIPInboundTrunk, projectID, id) })
	var nums []string
	_ = json.Unmarshal(trunk["numbers"], &nums)
	if len(nums) != 1 || nums[0] != accNumber(suffix, 2) {
		t.Errorf("a number without + reads back as %v", nums)
	}
	if str(trunk, "authPassword") != passwordMask {
		t.Errorf("password reads back as %q", str(trunk, "authPassword"))
	}
	if _, ok := trunk["ringingTimeout"]; ok {
		t.Error("an unset duration is in the response")
	}
	// A trunk needs auth, allowed addresses or numbers.
	if _, err := client.ReplaceSIP(ctx, livekit.SIPInboundTrunk, projectID, id, livekit.SIPObject{"name": raw("x")}); livekit.StatusCode(err) != 400 {
		t.Errorf("trunk open to anyone: %v", err)
	}
	// A replace without a field resets it.
	replaced, err := client.ReplaceSIP(ctx, livekit.SIPInboundTrunk, projectID, id, livekit.SIPObject{"name": raw("tf-acc-" + suffix), "numbers": raw(nums)})
	if err != nil {
		t.Fatal(err)
	}
	if str(replaced, "authPassword") != "" || str(replaced, "authUsername") != "" || string(replaced["krispEnabled"]) != "false" {
		t.Errorf("replace kept fields it did not get: %s", replaced["authPassword"])
	}
	if err := client.DeleteSIP(ctx, livekit.SIPInboundTrunk, projectID, id); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteSIP(ctx, livekit.SIPInboundTrunk, projectID, id); !livekit.IsNotFound(err) {
		t.Errorf("deleting a trunk twice: %v", err)
	}

	// A dispatch rule takes trunk IDs that do not exist, and needs a rule.
	rule, err := client.CreateSIP(ctx, livekit.SIPDispatchRule, projectID, livekit.SIPObject{
		"name": raw("tf-acc-" + suffix), "trunkIds": raw([]string{"ST_tfacc" + suffix}),
		"rule": raw(map[string]any{"dispatchRuleDirect": map[string]any{"roomName": "r", "pin": "4321"}}),
	})
	if err != nil {
		t.Errorf("rule with a trunk that does not exist: %v", err)
	} else {
		ruleID := livekit.SIPDispatchRule.ID(rule)
		t.Cleanup(func() { _ = client.DeleteSIP(ctx, livekit.SIPDispatchRule, projectID, ruleID) })
		if _, ok := rule["roomConfig"]; ok {
			t.Error("an unset roomConfig is in the response")
		}
		var r map[string]map[string]string
		_ = json.Unmarshal(rule["rule"], &r)
		if r["dispatchRuleDirect"]["pin"] != "4321" {
			t.Errorf("pin reads back as %q", r["dispatchRuleDirect"]["pin"])
		}
	}
	if _, err := client.CreateSIP(ctx, livekit.SIPDispatchRule, projectID, livekit.SIPObject{"name": raw("tf-acc-" + suffix)}); livekit.StatusCode(err) != 400 {
		t.Errorf("rule without a rule: %v", err)
	}

	// The session's own membership.
	me, err := client.CurrentUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := client.GetMember(ctx, projectID, me.ID); err != nil || m.Role != 3 || m.Email != me.Email {
		t.Errorf("own membership %+v, %v", m, err)
	}
	if _, err := client.GetMember(ctx, projectID, "UnotAMember"); !livekit.IsNotFound(err) {
		t.Errorf("a user who is not a member: %v", err)
	}
	// Searching for numbers is free and returns numbers without an ID.
	found, err := client.SearchPhoneNumbers(ctx, projectID, "US", "415", 1)
	if err != nil || len(found) == 0 || found[0].ID != "" || found[0].E164 == "" {
		t.Errorf("search: %+v, %v", found, err)
	}
}
