terraform {
  required_providers {
    livekit = {
      source = "registry.opentofu.org/solcreek/livekit"
    }
  }
}

# The session comes from LIVEKIT_SESSION_TOKEN, or else from the LiveKit CLI
# after `lk cloud auth`. A session reaches every project its user belongs to,
# so name the project to manage.
provider "livekit" {
  project_id = "p_abc123"
}

# To manage several projects, such as production and staging, give each a
# provider alias.
provider "livekit" {
  alias      = "staging"
  project_id = "p_def456"
}

# Guard against applying a configuration to the wrong project.
data "livekit_project" "this" {}

check "project" {
  assert {
    condition     = data.livekit_project.this.name == "Acme"
    error_message = "The default provider must point at the Acme project."
  }
}
