variable "sip_password" {
  type      = string
  sensitive = true
}

# Calls from a SIP provider to one number, from its addresses only.
resource "livekit_sip_inbound_trunk" "twilio" {
  name              = "twilio"
  numbers           = ["+14155550100"]
  allowed_addresses = ["203.0.113.0/24"]
  auth_username     = "livekit"
  auth_password     = var.sip_password
  ringing_timeout   = 30
  max_call_duration = 3600
  krisp_enabled     = true

  headers_to_attributes = {
    "X-Customer-Id" = "customer_id"
  }
}
