---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_event_trigger"
sidebar_current: "docs-postgresql-resource-postgresql_event_trigger"
description: |-
  Creates and manages an event trigger on a PostgreSQL server.
---

# postgresql\_event_trigger

The ``postgresql_event_trigger`` resource creates and manages [event trigger
objects](https://www.postgresql.org/docs/current/static/event-triggers.html)
within a PostgreSQL server instance.

## Usage

```hcl
resource "postgresql_function" "function" {
    name = "test_function"

    returns = "event_trigger"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RAISE EXCEPTION 'command % is disabled', tg_tag;
        END;
    EOF
}

resource "postgresql_event_trigger" "event_trigger" {
  name            = "event_trigger_test"
  function        = postgresql_function.function.name
  function_schema = postgresql_function.function.schema
  on              = "ddl_command_start"
  owner           = "postgres"

  filter {
    variable = "TAG"
    values = [
      "DROP TABLE"
    ]
  }
}
```

## Argument Reference

* `name` - (Required) The name of the event trigger.

* `on` - (Required) The name of the on event the trigger will listen to. The allowed names are "ddl_command_start", "ddl_command_end", "sql_drop", "table_rewrite" or "login". The "login" event requires PostgreSQL 17 or above.

* `function` - (Required) A function that is declared as taking no argument and returning type event_trigger.

* `function_schema` - (Required) Schema where the function is located.

* `filter` - (Optional) Filter to restrict the firing of the trigger. Currently the only supported filter_variable is TAG, and only one `filter` block is allowed because PostgreSQL does not accept the same filter variable twice.
  * `variable` - (Required) The name of a variable used to filter events. Currently the only supported value is TAG.
  * `values` - (Required) A list of values for the filter variable for which the trigger fires. For TAG, these are command tags (e.g., 'DROP FUNCTION').

* `database` - (Optional) The database where the event trigger is located. If not specified, the provider default database is used.

* `status` - (Optional) These configure the firing of event triggers. The allowed names are "disable", "enable", "enable_replica" or "enable_always". Default is "enable".

* `owner` - (Required) The user name of the owner of the event trigger. You can't use 'current_role', 'current_user' or 'session_user' in order to avoid drifts. On PostgreSQL, the owner of an event trigger must be a superuser.

* `drop_cascade` - (Optional) Automatically drop objects that depend on the event trigger, and in turn all objects that depend on those objects. Drop RESTRICT is the default. (Default: false)

## Import Example

It is possible to import a `postgresql_event_trigger` resource with the following
command:

```
$ terraform import postgresql_event_trigger.event_trigger_test "database.event_trigger"
```
