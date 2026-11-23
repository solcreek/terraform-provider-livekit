resource "livekit_sip_inbound_trunk" "main" {
  numbers = ["+14155550100"]
}

# Each caller gets a room of their own, with a voice agent dispatched to it.
resource "livekit_sip_dispatch_rule" "support" {
  name      = "support"
  trunk_ids = [livekit_sip_inbound_trunk.main.id]

  individual = {
    room_prefix = "support-"
  }

  agents = [{
    agent_name = "support-agent"
    metadata   = jsonencode({ language = "en" })
  }]

  attributes = {
    queue = "support"
  }
}

# Every caller with the PIN joins one room.
resource "livekit_sip_dispatch_rule" "standup" {
  name      = "standup"
  trunk_ids = [livekit_sip_inbound_trunk.main.id]
  numbers   = ["+14155550100"]

  direct = {
    room_name = "daily-standup"
    pin       = "4321"
  }
}
