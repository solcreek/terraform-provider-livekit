# Creating this BUYS the number. Pin the number itself rather than reading it
# from data.livekit_available_phone_numbers, whose list changes as numbers sell.
resource "livekit_phone_number" "support" {
  phone_number = "+14155550100"
  name         = "support line"
}

# Route the number's calls.
resource "livekit_sip_dispatch_rule" "support" {
  trunk_ids  = [livekit_phone_number.support.id]
  individual = { room_prefix = "support-" }
  agents     = [{ agent_name = "support-agent" }]
}
