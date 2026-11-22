# Only the settings written here are managed; the rest are left as they are.
resource "livekit_project_settings" "this" {
  enable_auto_create        = false
  require_explicit_dispatch = true
  enabled_codecs            = ["video/VP8", "video/H264", "audio/opus", "audio/red"]
  pinned_regions            = ["us"]

  enable_user_data_recording = true
  enable_user_data_training  = false
  enable_pii_redaction       = true
  pii_redaction_categories   = ["ssn", "credit-card", "email-address", "phone-number"]
}
