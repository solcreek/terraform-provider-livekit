# Give a member of the workspace read access to a private project.
data "livekit_workspace_members" "all" {}

locals {
  users = { for m in data.livekit_workspace_members.all.members : m.email => m.user_id }
}

resource "livekit_project_member" "support" {
  user_id = local.users["support@example.com"]
  role    = "read"
}
