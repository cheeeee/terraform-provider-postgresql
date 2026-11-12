# What only exists in the fork (cheeeee/postgresql): it is added to main.tf when the
# released version which is upgraded is a version of the fork.

resource "postgresql_role" "audited" {
  name  = "upg_audited"
  login = true

  parameters = {
    "pgaudit.log" = "ddl, write"
    "work_mem"    = "64MB"
  }
}

resource "postgresql_database" "audited" {
  name  = "upg_audited"
  owner = postgresql_role.owner.name

  parameters = {
    "pgaudit.log" = "none"
    "timezone"    = "UTC"
  }
}

resource "postgresql_function" "event" {
  name     = "upg_event"
  database = local.data
  returns  = "event_trigger"
  language = "plpgsql"
  body     = <<-EOF
    BEGIN
      RAISE NOTICE 'upg_event';
    END;
  EOF
}

resource "postgresql_event_trigger" "ddl" {
  name            = "upg_event_trigger"
  database        = local.data
  function        = postgresql_function.event.name
  function_schema = postgresql_function.event.schema
  on              = "ddl_command_end"
  owner           = var.pg_user

  filter {
    variable = "TAG"
    values   = ["CREATE TABLE", "DROP TABLE"]
  }
}
