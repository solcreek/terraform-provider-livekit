// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestMain serves the in-process provider under the solcreek namespace, which
// the test configurations name in required_providers, so the same tests run
// under Terraform and OpenTofu (TF_ACC_TERRAFORM_PATH=$(command -v tofu)).
func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC_PROVIDER_NAMESPACE") == "" {
		_ = os.Setenv("TF_ACC_PROVIDER_NAMESPACE", "solcreek")
	}
	if os.Getenv("TF_ACC_PROVIDER_HOST") == "" && filepath.Base(os.Getenv("TF_ACC_TERRAFORM_PATH")) == "tofu" {
		_ = os.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	}
	os.Exit(m.Run())
}

// requiredProviders pins the provider source in every test configuration.
const requiredProviders = `
terraform {
  required_providers {
    livekit = {
      source = "solcreek/livekit"
    }
  }
}
`

// testProtoV6ProviderFactories wires the in-process provider for tests.
var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"livekit": providerserver.NewProtocol6WithError(New("test")()),
}

// writeCLIConfig writes a LiveKit CLI configuration with one signed-in user
// and points LIVEKIT_CLI_CONFIG at it.
func writeCLIConfig(t *testing.T, token string, expiry time.Time, apiURL string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli-config.yaml")
	cfg := fmt.Sprintf(`default_project: acme
default_user: %s
projects:
    - name: acme
      project_id: %s
      url: wss://acme-abc123.livekit.cloud
      api_key: APIstale
      api_secret: stale
users:
    - id: %s
      name: Owner
      email: %s
      session_token: %s
      session_expiry: %d
      api_url: %s
`, fakeUserID, fakeProjectID, fakeUserID, fakeUserEmail, token, expiry.Unix(), apiURL)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVEKIT_CLI_CONFIG", path)
}

func TestProviderMissingCredentials(t *testing.T) {
	newFakeLiveKit(t) // points LIVEKIT_CLI_CONFIG at a file that does not exist
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      requiredProviders + `data "livekit_project" "this" { project_id = "p_x" }`,
			ExpectError: regexp.MustCompile(`Missing LiveKit Cloud credentials`),
		}},
	})
}

// The session that `lk cloud auth` saved is used, with its API URL, when no
// token is configured.
func TestProviderCLISession(t *testing.T) {
	f := newFakeLiveKit(t)
	writeCLIConfig(t, fakeToken, time.Now().Add(30*24*time.Hour), f.srv.URL)
	t.Setenv("LIVEKIT_PROJECT_ID", fakeProjectID)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `data "livekit_project" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.livekit_project.this", "project_id", fakeProjectID),
				resource.TestCheckResourceAttr("data.livekit_project.this", "name", "Acme"),
				resource.TestCheckResourceAttr("data.livekit_project.this", "url", "wss://acme-abc123.livekit.cloud"),
				resource.TestCheckResourceAttr("data.livekit_project.this", "user_data_region", "us"),
				resource.TestCheckResourceAttr("data.livekit_project.this", "is_private", "false"),
			),
		}},
	})
}

func TestProviderCLISessionExpired(t *testing.T) {
	f := newFakeLiveKit(t)
	writeCLIConfig(t, fakeToken, time.Now().Add(-time.Hour), f.srv.URL)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      requiredProviders + `data "livekit_project" "this" { project_id = "` + fakeProjectID + `" }`,
			ExpectError: regexp.MustCompile(`(?s)LiveKit CLI session expired.*lk cloud auth`),
		}},
	})
}

// LIVEKIT_SESSION_TOKEN wins over the CLI's session, and a rejected token is
// reported with what to do about it.
func TestProviderInvalidToken(t *testing.T) {
	f := newFakeLiveKit(t)
	writeCLIConfig(t, fakeToken, time.Now().Add(30*24*time.Hour), f.srv.URL)
	t.Setenv("LIVEKIT_SESSION_TOKEN", "wrong")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `
provider "livekit" {
  api_url = "` + f.srv.URL + `"
}
data "livekit_project" "this" { project_id = "` + fakeProjectID + `" }
`,
			ExpectError: regexp.MustCompile(`(?s)Unable to read project.*authentication required.*lk cloud\s+auth`),
		}},
	})
}

func TestProviderMissingProject(t *testing.T) {
	f := newFakeLiveKit(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: requiredProviders + `
provider "livekit" {
  session_token = "` + fakeToken + `"
  api_url       = "` + f.srv.URL + `"
}
resource "livekit_webhook" "w" { url = "https://hooks.example.com/livekit" }
`,
			ExpectError: regexp.MustCompile(`Missing project`),
		}},
	})
}

func TestDataSources(t *testing.T) {
	f := newFakeLiveKit(t)
	f.mutate(func(f *fakeLiveKit) {
		p := f.project()
		p.Keys = append(p.Keys,
			&fakeKey{ID: "APIserviceacct", Description: "ci"},
			&fakeKey{ID: "APIagentkey", AgentID: "CA_x", Owner: fakeUserID})
		f.keyPageSize = 1 // walk the cursor
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
data "livekit_webhook_events" "all" {}
data "livekit_api_keys" "all" {}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.livekit_webhook_events.all", "events.#", "12"),
				resource.TestCheckResourceAttr("data.livekit_webhook_events.all", "events.0", "room_started"),
				// The agent's key is left out.
				resource.TestCheckResourceAttr("data.livekit_api_keys.all", "keys.#", "2"),
				resource.TestCheckResourceAttr("data.livekit_api_keys.all", "keys.0.service_account", "false"),
				resource.TestCheckResourceAttr("data.livekit_api_keys.all", "keys.0.owner_email", fakeUserEmail),
				resource.TestCheckResourceAttr("data.livekit_api_keys.all", "keys.1.id", "APIserviceacct"),
				resource.TestCheckResourceAttr("data.livekit_api_keys.all", "keys.1.service_account", "true"),
				resource.TestCheckNoResourceAttr("data.livekit_api_keys.all", "keys.1.owner_email"),
			),
		}},
	})
}

// randomSuffix returns a short random hex string for acceptance test names.
func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
