resource "livekit_api_key" "webhooks" {
  description = "webhook signing"
}

resource "livekit_webhook" "backend" {
  name           = "backend"
  url            = "https://api.example.com/livekit/webhook"
  signing_key    = livekit_api_key.webhooks.id
  include_events = ["room_started", "room_finished", "participant_joined", "participant_left"]
}
