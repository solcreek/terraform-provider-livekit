# Import an inbound trunk by "<project_id>/<trunk_id>". Its password cannot be
# read, so set auth_password in the configuration and apply once.
terraform import livekit_sip_inbound_trunk.twilio p_abc123/ST_abcdef123456
