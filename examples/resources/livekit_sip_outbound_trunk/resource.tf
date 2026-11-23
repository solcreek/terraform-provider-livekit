variable "sip_password" {
  type      = string
  sensitive = true
}

resource "livekit_sip_outbound_trunk" "twilio" {
  name                = "twilio"
  address             = "example.pstn.twilio.com"
  transport           = "tls"
  destination_country = "US"
  numbers             = ["+14155550100"]
  auth_username       = "livekit"
  auth_password       = var.sip_password
  media_encryption    = "require"
}
