data "livekit_project" "this" {}

# The server URL clients and servers connect to.
output "livekit_url" {
  value = data.livekit_project.this.url
}
