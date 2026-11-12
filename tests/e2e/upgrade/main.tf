# Configuration used by upgrade.sh: one resource of every type, with the arguments
# which exist in the released versions of the provider. It is applied with a released
# version, then planned with the provider built from the sources: nothing must change.
#
# The database upg_data, its table, its sequence, the role upg_member and the
# replication slot of the subscription are created by upgrade.sh.

variable "pg_host" {
  type = string
}

variable "pg_port" {
  type    = number
  default = 5432
}

variable "pg_user" {
  type = string
}

variable "pg_password" {
  type      = string
  sensitive = true
}

variable "pg_sslmode" {
  type    = string
  default = "require"
}

variable "publisher_conninfo" {
  description = "How the server reaches itself, to subscribe to its own publication"
  type        = string
  sensitive   = true
}

provider "postgresql" {
  host      = var.pg_host
  port      = var.pg_port
  username  = var.pg_user
  password  = var.pg_password
  sslmode   = var.pg_sslmode
  superuser = true
}

locals {
  data = "upg_data"
}

# --- roles and memberships

resource "postgresql_role" "owner" {
  name = "upg_owner"
}

resource "postgresql_role" "app" {
  name             = "upg_app"
  login            = true
  password         = "upg-password"
  connection_limit = 5
  valid_until      = "2040-01-01 00:00:00+00"
  search_path      = ["upg", "public"]
  roles            = [postgresql_role.owner.name]

  statement_timeout                   = 30000
  idle_in_transaction_session_timeout = 60000
}

resource "postgresql_role" "reader" {
  name    = "upg_reader"
  login   = true
  inherit = false
}

# (the member is created by upgrade.sh: a postgresql_role would report this membership
# as a change of its "roles")
resource "postgresql_grant_role" "member" {
  role              = "upg_member"
  grant_role        = postgresql_role.owner.name
  with_admin_option = true
}

# --- database

resource "postgresql_database" "db" {
  name              = "upg_db"
  owner             = postgresql_role.owner.name
  template          = "template0"
  encoding          = "UTF8"
  lc_collate        = "C"
  lc_ctype          = "C"
  connection_limit  = 10
  allow_connections = true
}

# --- schema, extension, function

resource "postgresql_schema" "upg" {
  name     = "upg"
  database = local.data
  owner    = postgresql_role.owner.name

  policy {
    usage = true
    role  = postgresql_role.app.name
  }

  policy {
    create            = true
    create_with_grant = true
    usage             = true
    role              = postgresql_role.reader.name
  }
}

resource "postgresql_extension" "hstore" {
  name     = "hstore"
  database = local.data
  schema   = postgresql_schema.upg.name
}

resource "postgresql_function" "add" {
  name     = "upg_add"
  database = local.data
  schema   = postgresql_schema.upg.name

  arg {
    name = "a"
    type = "integer"
  }

  arg {
    name    = "b"
    type    = "integer"
    default = "1"
  }

  returns    = "integer"
  language   = "plpgsql"
  volatility = "IMMUTABLE"
  strict     = true
  parallel   = "SAFE"
  body       = <<-EOF
    BEGIN
      RETURN a + b;
    END;
  EOF
}

# --- privileges

resource "postgresql_grant" "database" {
  database    = local.data
  role        = postgresql_role.app.name
  object_type = "database"
  privileges  = ["CONNECT", "TEMPORARY"]
}

resource "postgresql_grant" "schema" {
  database    = local.data
  role        = postgresql_role.reader.name
  schema      = "public"
  object_type = "schema"
  privileges  = ["USAGE"]
}

resource "postgresql_grant" "tables" {
  database    = local.data
  role        = postgresql_role.app.name
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT", "INSERT", "UPDATE"]
}

resource "postgresql_grant" "table" {
  database          = local.data
  role              = postgresql_role.reader.name
  schema            = "public"
  object_type       = "table"
  objects           = ["upg_items"]
  privileges        = ["SELECT"]
  with_grant_option = true
}

resource "postgresql_grant" "columns" {
  database    = local.data
  role        = postgresql_role.reader.name
  schema      = "public"
  object_type = "column"
  objects     = ["upg_items"]
  columns     = ["name"]
  privileges  = ["UPDATE"]
}

resource "postgresql_grant" "sequences" {
  database    = local.data
  role        = postgresql_role.app.name
  schema      = "public"
  object_type = "sequence"
  privileges  = ["USAGE", "SELECT"]
}

resource "postgresql_grant" "functions" {
  database    = local.data
  role        = postgresql_role.app.name
  schema      = postgresql_schema.upg.name
  object_type = "function"
  privileges  = ["EXECUTE"]

  depends_on = [postgresql_function.add, postgresql_extension.hstore]
}

resource "postgresql_default_privileges" "global" {
  database    = local.data
  owner       = postgresql_role.owner.name
  role        = postgresql_role.app.name
  object_type = "table"
  privileges  = ["SELECT"]
}

resource "postgresql_default_privileges" "schema" {
  database    = local.data
  owner       = postgresql_role.owner.name
  role        = postgresql_role.app.name
  schema      = postgresql_schema.upg.name
  object_type = "table"
  privileges  = ["INSERT", "UPDATE"]
}

resource "postgresql_default_privileges" "sequences" {
  database    = local.data
  owner       = postgresql_role.owner.name
  role        = postgresql_role.reader.name
  schema      = postgresql_schema.upg.name
  object_type = "sequence"
  privileges  = ["USAGE"]
}

# --- logical replication

resource "postgresql_publication" "items" {
  name          = "upg_pub"
  database      = local.data
  tables        = ["public.upg_items"]
  publish_param = ["insert", "update"]
}

# (the subscriber has no table: it subscribes to a publication without any)
resource "postgresql_publication" "empty" {
  name     = "upg_pub_empty"
  database = local.data
}

resource "postgresql_replication_slot" "logical" {
  name     = "upg_logical"
  database = local.data
  plugin   = "test_decoding"
}

resource "postgresql_physical_replication_slot" "physical" {
  name = "upg_physical"
}

resource "postgresql_subscription" "items" {
  name         = "upg_sub"
  database     = postgresql_database.db.name
  conninfo     = var.publisher_conninfo
  publications = [postgresql_publication.empty.name]
  create_slot  = false
  slot_name    = "upg_sub"
}

# --- foreign data

# (a foreign server is created in the database of the connection)
resource "postgresql_extension" "fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "remote" {
  server_name = "upg_server"
  fdw_name    = "postgres_fdw"
  options = {
    host   = "remote.example.com"
    dbname = "remote"
    port   = "5432"
  }

  depends_on = [postgresql_extension.fdw]
}

resource "postgresql_user_mapping" "remote" {
  server_name = postgresql_server.remote.server_name
  user_name   = postgresql_role.app.name
  options = {
    user     = "remote_user"
    password = "remote-password"
  }
}

# --- security label (the provider "dummy" is loaded in the test database)

resource "postgresql_security_label" "app" {
  object_type    = "role"
  object_name    = postgresql_role.app.name
  label_provider = "dummy"
  label          = "classified"
}
