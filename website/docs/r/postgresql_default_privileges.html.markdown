---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_default_privileges"
sidebar_current: "docs-postgresql-resource-postgresql_default_privileges"
description: |-
  Creates and manages default privileges given to a user for a database schema.
---

# postgresql\_default\_privileges

The ``postgresql_default_privileges`` resource creates and manages default privileges given to a user for a database schema.

~> **Note:** This resource needs Postgresql version 9 or above.

## Usage

```hcl
resource "postgresql_default_privileges" "read_only_tables" {
  role     = "test_role"
  database = "test_db"
  schema   = "public"

  owner       = "db_owner"
  object_type = "table"
  privileges  = ["SELECT"]
}
```

## Argument Reference

* `role` - (Required) The role that will automatically be granted the specified privileges on new objects created by the owner.
* `database` - (Required) The database to grant default privileges for this role.
* `owner` - (Required) Specifies the role that creates objects for which the default privileges will be applied.
* `schema` - (Optional) The database schema to set default privileges for this role. Omit it to set global default privileges, which apply to new objects in every schema. It cannot be set when `object_type` is `schema` or `large_object`, as PostgreSQL has no default privileges for schemas inside a schema nor for large objects per schema.
* `object_type` - (Required) The PostgreSQL object type to set the default privileges on (one of: table, sequence, function, routine, type, schema, large_object). `large_object` requires PostgreSQL 18 or above.
* `privileges` - (Required) List of privileges to grant on new objects created by the owner. An empty list could be provided to revoke all default privileges for this role. Allowed privileges per `object_type`:
  * `table`: SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN (PostgreSQL 17 or above), ALL
  * `sequence`: USAGE, SELECT, UPDATE, ALL
  * `function` and `routine`: EXECUTE, ALL
  * `type`: USAGE, ALL
  * `schema`: CREATE, USAGE, ALL
  * `large_object`: SELECT, UPDATE, ALL
* `with_grant_option` - (Optional) Permit the grant recipient to grant the privileges to others. Defaults to `false`. It cannot be `true` for the `public` role.


## Examples

### Grant default privileges for tables to "current_role" role:

```hcl
resource "postgresql_default_privileges" "grant_table_privileges" {
  database    = postgresql_database.example_db.name
  role        = "current_role"
  owner       = "owner_role"
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT", "INSERT", "UPDATE"]
}
```
Whenever the `owner_role` creates a new table in the `public` schema, the `current_role` is automatically granted SELECT, INSERT, and UPDATE privileges on that table.

### Grant default privileges on large objects (PostgreSQL 18 or above):

```hcl
resource "postgresql_default_privileges" "large_objects_read" {
  database    = postgresql_database.example_db.name
  role        = "current_role"
  owner       = "owner_role"
  object_type = "large_object"
  privileges  = ["SELECT"]
}
```

### Revoke default privileges for functions for "public" role:

```hcl
resource "postgresql_default_privileges" "revoke_public" {
  database    = postgresql_database.example_db.name
  role        = "public"
  owner       = "object_owner"
  object_type = "function"
  privileges  = []
}
```
