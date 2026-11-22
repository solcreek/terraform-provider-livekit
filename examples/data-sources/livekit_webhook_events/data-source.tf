data "livekit_webhook_events" "all" {}

# Every event but track events.
resource "livekit_webhook" "rooms" {
  url = "https://api.example.com/livekit/webhook"
  include_events = [
    for e in data.livekit_webhook_events.all.events : e if !startswith(e, "track_")
  ]
}
