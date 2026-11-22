data "livekit_api_keys" "all" {}

# Keys that belong to a user rather than to the project, which stop working
# when that user leaves it.
output "user_owned_keys" {
  value = [for k in data.livekit_api_keys.all.keys : k.id if !k.service_account]
}
