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
* `privileges` - (Required) List of privileges to grant on new objects created by the owner. An empty list could be provided to revoke all default privileges for this role (see "Default privileges per schema and global default privileges" below for a resource with a `schema`). Allowed privileges per `object_type`:
  * `table`: SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN (PostgreSQL 17 or above), ALL
  * `sequence`: USAGE, SELECT, UPDATE, ALL
  * `function` and `routine`: EXECUTE, ALL
  * `type`: USAGE, ALL
  * `schema`: CREATE, USAGE, ALL
  * `large_object`: SELECT, UPDATE, ALL
* `with_grant_option` - (Optional) Permit the grant recipient to grant the privileges to others. Defaults to `false`. It cannot be `true` for the `public` role.

## Default privileges per schema and global default privileges

A resource with a `schema` manages only the default privileges of that schema, and a resource without `schema` manages only the global ones. PostgreSQL ADDS them: ["Default privileges that are specified per-schema are added to whatever the global default privileges are for the particular object type."](https://www.postgresql.org/docs/current/sql-alterdefaultprivileges.html) For example, with the two resources below, a table created by `owner_role` in the `public` schema is readable and writable by `app_role` (SELECT and INSERT), and a table created in any other schema only readable (SELECT).

```hcl
resource "postgresql_default_privileges" "global_select" {
  database    = "example_db"
  role        = "app_role"
  owner       = "owner_role"
  object_type = "table"
  privileges  = ["SELECT"]
}

resource "postgresql_default_privileges" "public_insert" {
  database    = "example_db"
  role        = "app_role"
  owner       = "owner_role"
  schema      = "public"
  object_type = "table"
  privileges  = ["INSERT"]
}
```

Each resource reads back only its own entry of the catalog `pg_default_acl`. A resource with a `schema` which has no entry has no privileges. The built-in default privileges (such as EXECUTE on functions for PUBLIC) belong to the global scope only.

~> **Note:** A revoke per schema cannot remove a privilege which is granted globally or by the built-in default. PostgreSQL says: "This means you cannot revoke privileges per-schema if they are granted globally (either by default or according to an ALTER DEFAULT PRIVILEGES command that does not specify a schema). Per-schema REVOKE is only useful to reverse the effects of a previous per-schema GRANT." A resource with a `schema` and `privileges = []` for a role which has such privileges would therefore do nothing, so the provider returns an error: use a resource without `schema` instead, as in the last example below.

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
