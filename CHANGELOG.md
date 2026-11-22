# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
