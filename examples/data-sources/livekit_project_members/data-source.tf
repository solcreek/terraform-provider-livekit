data "livekit_project_members" "this" {}

output "admins" {
  value = [for m in data.livekit_project_members.this.members : m.email if m.role == "admin"]
}
