# A key for one deployment. Its secret is in the state; pass it on to where
# the servers read it, such as a Kubernetes secret.
resource "livekit_api_key" "backend" {
  description = "Production (GKE)"
}

output "livekit_api_key" {
  value = livekit_api_key.backend.id
}

output "livekit_api_secret" {
  value     = livekit_api_key.backend.secret
  sensitive = true
}
