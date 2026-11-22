<a href="https://terraform.io">
    <img src=".github/tf.svg" alt="Terraform logo" title="Terraform" align="left" height="50" />
</a>
<a href="https://opentofu.org">
    <picture>
        <source media="(prefers-color-scheme: dark)" srcset=".github/opentofu-dark.svg" />
        <img src=".github/opentofu-light.svg" alt="OpenTofu logo" title="OpenTofu" align="left" height="50" />
    </picture>
</a>


# Terraform / OpenTofu Provider for LiveKit Cloud

[![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/solcreek/terraform-provider-livekit?label=release&style=for-the-badge)](https://github.com/solcreek/terraform-provider-livekit/releases/latest) [![License](https://img.shields.io/github/license/solcreek/terraform-provider-livekit.svg?style=for-the-badge)](LICENSE) [![Tests](https://img.shields.io/github/actions/workflow/status/solcreek/terraform-provider-livekit/test.yml?branch=main&style=for-the-badge)](https://github.com/solcreek/terraform-provider-livekit/actions)

A provider for [LiveKit Cloud](https://livekit.io/cloud): the configuration of
LiveKit Cloud projects, as code. Project settings, webhooks and API keys. It uses
a **dependency-free, standard-library-only** client written against the
[LiveKit Cloud API](https://beta-api.livekit.cloud/v0/openapi), so the provider
owns its HTTP behaviour: it caps concurrent requests, backs off when rate
limited, never repeats a write that may have landed, follows pagination, and
turns credential errors into actionable messages.

> [!NOTE]
> This is an **unofficial, community-maintained** provider. It is **not**
> affiliated with, endorsed by, or supported by LiveKit. "LiveKit" is a
> trademark of its respective owner and is used here only to describe what the
> provider integrates with.

> [!WARNING]
> **Early release, on a pre-GA API.** The LiveKit Cloud API is LiveKit's `v0`:
> its endpoints and fields can change without notice. Every resource and data
> source passed its acceptance test against beta-api.livekit.cloud on
> 2026-11-22.

Proudly built for the open-source IaC ecosystem and dedicated to **OpenTofu**.

## Usage

> [!IMPORTANT]
> The provider is distributed through the **OpenTofu Registry**, so always use the
> **full** `registry.opentofu.org/solcreek/livekit` address. It works with both
> OpenTofu and Terraform. The short `solcreek/livekit` form only works with
> OpenTofu: Terraform resolves short addresses against `registry.terraform.io`.

```hcl
terraform {
  required_providers {
    livekit = {
      source = "registry.opentofu.org/solcreek/livekit"
    }
  }
}

# The session comes from `lk cloud auth` (or LIVEKIT_SESSION_TOKEN).
provider "livekit" {
  project_id = "p_abc123"
}

resource "livekit_project_settings" "this" {
  require_explicit_dispatch = true
  enable_pii_redaction      = true
  pii_redaction_categories  = ["ssn", "credit-card", "email-address"]
}

resource "livekit_api_key" "backend" {
  description = "Production (GKE)"
}

resource "livekit_webhook" "backend" {
  url         = "https://api.example.com/livekit/webhook"
  signing_key = livekit_api_key.backend.id
}
```

### Adopting what already exists

Projects already have keys and settings in use. Bring them in rather than
creating new ones:

```hcl
data "livekit_api_keys" "all" {}

output "keys" {
  value = { for k in data.livekit_api_keys.all.keys : k.id => k.description }
}
```

```sh
tofu import livekit_project_settings.this p_abc123
tofu import livekit_api_key.backend p_abc123/APIabcdef123456
tofu plan   # shows what differs from the configuration before anything is changed
```

## Resources

| Resource | Manages |
|----------|---------|
| `livekit_project_settings` | The settings of an existing project: codecs, pinned regions, auto-create, agent dispatch, observability recording and training, PII redaction, egress retries |
| `livekit_webhook` | A URL the project sends room, participant, track, egress and ingress events to |
| `livekit_api_key` | An API key and its secret, for servers to sign tokens and call the server API |

| Data source | Reads |
|-------------|-------|
| `livekit_project` | A project's name, server URL, workspace and data region |
| `livekit_webhook_events` | The event names a webhook can filter on |
| `livekit_api_keys` | A project's API keys, without secrets |

Full argument reference: [docs/](docs/index.md).

Rooms, participants, egress and agent dispatches are not covered: an
application creates them at run time. Agent deployments are build output, not
configuration. Creating projects, members, SIP trunks and phone numbers may
follow.

## Authentication

The LiveKit Cloud API accepts only a **user session token**; as of 2026-11-22
there is no service account or long-lived token for it. The provider takes the
session from the `session_token` argument, else `LIVEKIT_SESSION_TOKEN`, else
the LiveKit CLI configuration that `lk cloud auth` writes
(`~/.livekit/cli-config.yaml`, its default user, or `cli_user`).

- **A session lasts about 30 days** and cannot be refreshed. From the CLI
  configuration, an expired session is refused with *"LiveKit CLI session
  expired"*, and one expiring within a week gets a warning. In CI, keep a token
  in a secret and replace it before it expires.
- **A session reaches every project its user belongs to.** Name the project
  with the provider's `project_id` (or `LIVEKIT_PROJECT_ID`) or per resource,
  and use one provider alias per project. Your role in the project decides what
  works: read (`1`) for data sources, write (`2`) for webhooks, admin (`3`) for
  settings and keys.
- **Bad credentials**: no session fails with *"Missing LiveKit Cloud
  credentials"*; a wrong, expired or revoked one gets the API's `401` with a
  hint to sign in again; a role too low gets its `403`. A project you are not a
  member of answers `404`, like one that does not exist.

A project's API key and secret, which the LiveKit server API uses, do not work
here.

## Behaviour worth knowing

- **Settings are managed one by one.** `livekit_project_settings` changes and
  watches only the arguments the configuration sets; the rest stay as they are
  and follow the dashboard without a diff. Destroying the resource leaves the
  project untouched.
- **Some lists cannot be emptied.** The API ignores an empty `enabled_codecs` or
  `pii_redaction_categories`, so the provider requires at least one element.
  Turning PII redaction on with no categories saved applies LiveKit's default
  set; turning it off keeps the categories. An unknown codec makes the API fail
  with a `500`, so codecs are checked for shape at plan time.
- **Deleting a key is guarded.** `livekit_api_key` defaults to
  `deletion_protection = true`: a deleted key cannot be restored, and every
  server and webhook that uses it stops at once. Keys have no update, so a
  changed `description` or `service_account` replaces the key, which the
  protection also refuses. New keys are service account keys by default, owned
  by the project rather than by you, so they survive you leaving it.
- **A webhook keeps a deleted key.** Its `signing_key` still names a key after
  that key is deleted, and its deliveries can no longer be verified. Move the
  webhook to another key first.
- **Nothing is adopted silently.** The API accepts several webhooks with the
  same URL, and creating one is not idempotent, so existing objects come in
  only through `import`.
- **Secrets live in the state.** `livekit_api_key.secret` is marked sensitive,
  which hides it from plan output only. The API returns it once, at creation, so
  it is empty after an import.
- **Moving the provider's default project moves nothing.** A resource created
  in the provider's default `project_id` stays there when that default changes;
  only its own `project_id` argument replaces it.
- **Retries.** At most four requests are in flight. A `429` is retried after
  `Retry-After` or exponentially, at most five times. A read that fails on the
  network is repeated up to twice; a write never is.

## Development

```sh
make build        # compile the provider
make test         # unit and lifecycle tests against the in-memory API (needs terraform on PATH)
make test-tofu    # the same lifecycle tests through OpenTofu
make generate     # regenerate docs/ from schema, examples/ and templates/
make lint         # golangci-lint
```

Acceptance tests (`make testacc`) call the real LiveKit Cloud API. They need
`LIVEKIT_ACC_PROJECT_ID`, a project whose name starts with `terraform-acc` (they
refuse any other), and a session (`lk cloud auth`, or `LIVEKIT_SESSION_TOKEN`).
They create and delete objects marked `tf-acc-*`, put back the settings they
change, and touch nothing else.

## License

[MPL-2.0](LICENSE). See [NOTICE](NOTICE).
