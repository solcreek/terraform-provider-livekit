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
LiveKit Cloud projects, as code. Project settings, webhooks, API keys, members,
SIP trunks and dispatch rules, and phone numbers. It uses
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
| `livekit_sip_inbound_trunk` | Which calls LiveKit accepts from a SIP provider: numbers, addresses, callers, authentication |
| `livekit_sip_outbound_trunk` | The SIP provider LiveKit places calls through, and the numbers it calls from |
| `livekit_sip_dispatch_rule` | Which room an inbound call joins, and the agents dispatched to it |
| `livekit_project_member` | A member of a private project and their role |
| `livekit_phone_number` | A phone number **bought** from LiveKit (creating it costs money) |

| Data source | Reads |
|-------------|-------|
| `livekit_project` | A project's name, server URL, workspace and data region |
| `livekit_webhook_events` | The event names a webhook can filter on |
| `livekit_api_keys` | A project's API keys, without secrets |
| `livekit_project_members` | A project's members and roles |
| `livekit_workspace_members` | A workspace's members, to find the user IDs to add to a project |
| `livekit_available_phone_numbers` | Phone numbers for sale, by country and area code (free) |

Full argument reference: [docs/](docs/index.md).

Rooms, participants, egress and agent dispatches are not covered: an
application creates them at run time. Agent deployments are build output, not
configuration. Invites are not covered either: they send email and expire after
48 hours.

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
- **SIP objects are changed in place, and keep what the provider does not
  model.** The API replaces a trunk or rule whole, resetting every field it is
  not sent, so the provider reads the object, overwrites the fields it manages
  and sends the rest back: media codecs or room configuration set in the
  dashboard survive. The fields it manages follow the configuration, unset ones
  included.
- **SIP passwords are write-only.** The API answers `********` for a set
  password, so a password changed in the dashboard is not detected (a removed
  one is). PINs of dispatch rules come back in clear. Both are sensitive and
  stored in the state.
- **Numbers must be E.164** (`+14155550100`): the API silently adds a missing
  `+`, which would read back as a different value. An inbound trunk must
  restrict something (numbers, addresses or a password); the API refuses one
  open to anyone.
- **Members are guarded.** `livekit_project_member` manages members of
  *private* projects only: in any other project a role change or a removal
  reaches the whole workspace and every project in it. It refuses to adopt an
  existing member (the API would silently change their role; import instead),
  to remove the user Terraform runs as, and to remove a member who owns API
  keys, which the API would delete with them.
- **Phone numbers cost money.** Creating a `livekit_phone_number` buys the
  number and destroying it releases it for good; it defaults to
  `deletion_protection = true`, and changing `phone_number` (which would buy
  another) is refused by it too. Route a number by adding its `id` to a dispatch
  rule's `trunk_ids`. Buying, naming, routing, importing and releasing passed an
  acceptance test against the live API on 2026-11-23; it only runs when asked
  (`LIVEKIT_ACC_BUY_PHONE_NUMBER=1`), never in CI. The API's responses to buying
  and renaming are incomplete, so the provider reads the number back after
  each.
- **Moving the provider's default project moves nothing.** A resource created
  in the provider's default `project_id` stays there when that default changes;
  only its own `project_id` argument replaces it.
- **Rate limit.** The API allows a session about 60 requests in any 60 seconds
  and answers the rest with `429`, with no `Retry-After` (observed 2026-11-23).
  The provider paces itself to 55 a minute after a burst of 50
  (`requests_per_minute`), so a large plan takes about a second per object past
  the first fifty. Several Terraform runs at once share the limit; a `429` is
  retried with waits of 2 s up to 60 s, about three minutes in all.
- **Retries.** At most four requests are in flight. A read that fails on the
  network or with a `502`, `503` or `504` is repeated up to twice; a write never
  is, since it may have landed.

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
change, and touch nothing else. They never add a member, and buy (and release)
a phone number only with `LIVEKIT_ACC_BUY_PHONE_NUMBER=1`, which costs money.

## License

[MPL-2.0](LICENSE). See [NOTICE](NOTICE).
