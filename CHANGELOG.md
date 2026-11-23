# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `livekit_sip_inbound_trunk`, `livekit_sip_outbound_trunk` and
  `livekit_sip_dispatch_rule` (with agent dispatch), changed in place while
  keeping the fields the provider does not model. Passed their acceptance tests
  against beta-api.livekit.cloud on 2026-11-23.
- `livekit_project_member`, for private projects: refuses to adopt an existing
  member, to remove the user Terraform runs as, or a member who owns API keys.
- `livekit_phone_number`, which buys a number, with `deletion_protection` on by
  default. Buying, naming, routing, importing and releasing passed an opt-in
  acceptance test against the live API on 2026-11-23.
- Data sources `livekit_project_members`, `livekit_workspace_members` and
  `livekit_available_phone_numbers`.

### Changed

- The provider paces its requests to the API's rate limit, about 60 a minute
  per session: 55 a minute after a burst of 50, set by the new
  `requests_per_minute`. A `429` is now retried for about three minutes instead
  of 31 seconds, enough for the limit's one-minute window to pass.
- Reads are retried after a 502, 503 or 504, like after a network error;
  writes still never are.

### Fixed

- Acceptance runs no longer fail on `api request limit exceeded` once a run has
  used up the rate limit.

## [0.1.0] - 2026-11-22

### Added

First release. Every resource and data source passed its acceptance test against
beta-api.livekit.cloud on 2026-11-22.

- Provider configuration with a LiveKit Cloud user session: `session_token`,
  `LIVEKIT_SESSION_TOKEN`, or the session `lk cloud auth` saved; a default
  `project_id`.
- `livekit_project_settings`: codecs, pinned regions, room auto-creation, agent
  dispatch, observability recording and training, PII redaction, the token
  endpoint, SFU pausing and egress retries and backup storage, each managed only
  when set.
- `livekit_webhook`, changed in place, with event filters.
- `livekit_api_key`, service account keys by default, with `deletion_protection`
  on by default.
- Data sources `livekit_project`, `livekit_webhook_events` and `livekit_api_keys`.
