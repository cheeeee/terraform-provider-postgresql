# A database for one application which creates its tables itself:
#
#   <name>_owner     owns the database and its tables, nobody logs in with it
#   <name>_migrator  used by the application to create and migrate its tables:
#                    its sessions act as <name>_owner
#   <name>_rw        reads and writes the data, without being able to change the tables
#   var.roles        other roles which read, or read and write, the data
#
# Everything created later by the migrator is accessible to the other roles with
# the default privileges, nothing is accessible to the other roles of the server.
#
# The owner is not a role which logs in: on AWS RDS, the members of rds_iam can
# only log in with an IAM token, and the user which runs Terraform is granted the
# owner for a moment to create the database. It must not become a member of rds_iam.

locals {
  owner    = "${var.name}_owner"
  migrator = "${var.name}_migrator"

  # All the roles which use the data, with the settings of each of them
  users = merge(
    { "${var.name}_rw" = { access = "rw", audit = null, iam = null } },
    var.roles,
  )

  # All the roles of the module: is IAM used, and which version of password
  logins = {
    for name, role in merge({ (local.migrator) = { iam = null } }, local.users) : name => {
      iam              = coalesce(role.iam, var.iam_auth)
      password_version = lookup(var.password_versions, name, null)
    }
  }

  privileges = {
    ro = { table = ["SELECT"], sequence = ["SELECT"] }
    rw = { table = ["SELECT", "INSERT", "UPDATE", "DELETE"], sequence = ["USAGE", "SELECT"] }
  }
}

resource "postgresql_role" "owner" {
  name  = local.owner
  login = false
}

resource "postgresql_role" "migrator" {
  name  = local.migrator
  login = true

  # On AWS RDS, a member of rds_iam logs in with an IAM token
  roles = concat([postgresql_role.owner.name], local.logins[local.migrator].iam ? ["rds_iam"] : [])

  # Everything it creates belongs to the owner
  assume_role = postgresql_role.owner.name

  password_wo         = local.logins[local.migrator].password_version == null ? null : var.passwords[local.migrator]
  password_wo_version = local.logins[local.migrator].password_version
}

resource "postgresql_role" "user" {
  for_each = local.users

  name  = each.key
  login = true
  roles = local.logins[each.key].iam ? ["rds_iam"] : []

  password_wo         = local.logins[each.key].password_version == null ? null : var.passwords[each.key]
  password_wo_version = local.logins[each.key].password_version

  # The setting of a role overrides the one of the database
  parameters = each.value.audit == null ? {} : { "pgaudit.log" = each.value.audit }
}

resource "postgresql_database" "this" {
  name             = var.name
  owner            = postgresql_role.owner.name
  connection_limit = var.connection_limit

  parameters = merge(
    var.database_parameters,
    var.audit == null ? {} : { "pgaudit.log" = var.audit },
  )
}

resource "postgresql_extension" "this" {
  for_each = toset(var.extensions)

  name     = each.value
  database = postgresql_database.this.name
}

# --- Nothing for the other roles of the server

resource "postgresql_grant" "public_database" {
  database    = postgresql_database.this.name
  role        = "public"
  object_type = "database"
  privileges  = []
}

resource "postgresql_grant" "public_schema" {
  database    = postgresql_database.this.name
  role        = "public"
  schema      = "public"
  object_type = "schema"
  privileges  = []
}

# --- The migrator creates the tables, as the owner

resource "postgresql_grant" "owner_schema" {
  database    = postgresql_database.this.name
  role        = postgresql_role.owner.name
  schema      = "public"
  object_type = "schema"
  privileges  = ["CREATE", "USAGE"]

  depends_on = [postgresql_grant.public_schema]
}

# --- The roles which use the data

resource "postgresql_grant" "database" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  role        = each.value.name
  object_type = "database"
  privileges  = ["CONNECT"]

  depends_on = [postgresql_grant.public_database]
}

resource "postgresql_grant" "schema" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  role        = each.value.name
  schema      = "public"
  object_type = "schema"
  privileges  = ["USAGE"]

  depends_on = [postgresql_grant.public_schema]
}

# The tables and the sequences which will be created by the owner
resource "postgresql_default_privileges" "tables" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  owner       = postgresql_role.owner.name
  role        = each.value.name
  schema      = "public"
  object_type = "table"
  privileges  = local.privileges[local.users[each.key].access].table
}

resource "postgresql_default_privileges" "sequences" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  owner       = postgresql_role.owner.name
  role        = each.value.name
  schema      = "public"
  object_type = "sequence"
  privileges  = local.privileges[local.users[each.key].access].sequence
}

# The default privileges only apply to the tables created afterwards: the
# privileges are also granted on the existing ones, for a role which is added
# to a database already used. They are revoked when the role is removed.
resource "postgresql_grant" "tables" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  role        = each.value.name
  schema      = "public"
  object_type = "table"
  privileges  = local.privileges[local.users[each.key].access].table

  depends_on = [postgresql_grant.schema]
}

resource "postgresql_grant" "sequences" {
  for_each = postgresql_role.user

  database    = postgresql_database.this.name
  role        = each.value.name
  schema      = "public"
  object_type = "sequence"
  privileges  = local.privileges[local.users[each.key].access].sequence

  depends_on = [postgresql_grant.schema]
}
