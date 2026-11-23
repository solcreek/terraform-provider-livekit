data "livekit_workspace_members" "this" {}

output "user_ids" {
  value = { for m in data.livekit_workspace_members.this.members : m.email => m.user_id }
}
