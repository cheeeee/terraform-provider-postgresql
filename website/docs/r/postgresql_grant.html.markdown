---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_grant"
sidebar_current: "docs-postgresql-resource-postgresql_grant"
description: |-
  Creates and manages privileges given to a user for a database schema.
---

# postgresql\_grant

The ``postgresql_grant`` resource creates and manages privileges given to a user for a database schema.

See [PostgreSQL documentation](https://www.postgresql.org/docs/current/sql-grant.html)

~> **Note:** This resource needs Postgresql version 9 or above.
~> **Note:** Using column & table grants on the _same_ table with the _same_ privileges can lead to unexpected behaviours.
~> **Note:** The `parameter` object type needs PostgreSQL 15 or above.

## Usage

```hcl
# Grant SELECT privileges on 2 tables
resource "postgresql_grant" "readonly_tables" {
  database    = "test_db"
  role        = "test_role"
  schema      = "public"
  object_type = "table"
  objects     = ["table1", "table2"]
  privileges  = ["SELECT"]
}

# Grant SELECT & INSERT privileges on 2 columns in 1 table
resource "postgresql_grant" "read_insert_column" {
  database    = "test_db"
  role        = "test_role"
  schema      = "public"
  object_type = "column"
  objects     = ["table1"]
  columns     = ["col1", "col2"]
  privileges  = ["UPDATE", "INSERT"]
}
```

## Argument Reference

* `role` - (Required) The name of the role to grant privileges on, Set it to "public" for all roles.
* `database` - (Required) The database to grant privileges on for this role.
* `schema` - The database schema to grant privileges on for this role (Required except if object_type is "database", "foreign_data_wrapper", "foreign_server", "language", "large_object", "parameter" or "tablespace")
* `object_type` - (Required) The PostgreSQL object type to grant the privileges on (one of: database, schema, table, sequence, function, procedure, routine, foreign_data_wrapper, foreign_server, column, domain, type, language, large_object, parameter, tablespace).
* `privileges` - (Required) The list of privileges to grant. There are different kinds of privileges: SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER, MAINTAIN (PostgreSQL 17 or above), CREATE, CONNECT, TEMPORARY, EXECUTE, USAGE, SET, ALTER SYSTEM, and ALL. An empty list could be provided to revoke all privileges for this role. The privileges of an object type are the ones of the PostgreSQL documentation for this object type, `ALL` stands for all of them.
* `objects` - (Optional) The objects upon which to grant the privileges. An empty list (the default) means to grant permissions on *all* objects of the specified type. You cannot specify this option if the `object_type` is `database` or `schema`. When `object_type` is `column`, only one value is allowed. When `object_type` is `foreign_data_wrapper` or `foreign_server`, only one value is allowed.
  The object types `domain`, `type`, `language`, `large_object`, `parameter` and `tablespace` have no "all objects" form in PostgreSQL: their `objects` are mandatory and they are named as follows:
  * `domain` and `type`: the names of the domains or types, in the `schema`.
  * `language` and `tablespace`: the names of the languages or tablespaces.
  * `large_object`: the OIDs of the large objects, as strings (e.g.: `["16384"]`).
  * `parameter`: the names of the configuration parameters (e.g.: `["work_mem"]`).
  The objects of the types `function`, `procedure` and `routine` are given by their name, which applies to all the overloads of that name, or by their name and argument types, written as in the PostgreSQL statement and without the schema (e.g.: `["f(integer)", "g(varchar(10), numeric(10,2))"]`). The argument list may contain argument names and modes (`IN a text`), array types (`integer[]`), schema-qualified types and, between double quotes, types whose name needs quoting. Type modifiers (`varchar(10)`) are accepted but ignored by PostgreSQL to identify a function: `f(varchar(10))` and `f(varchar)` are the same function. The signature is compared with the functions of the database, not as a string, so that two overloads of the same name are told apart.
* `columns` - (Optional) The columns upon which to grant the privileges. Required when `object_type` is `column`. You cannot specify this option if the `object_type` is not `column`.
* `with_grant_option` - (Optional) Whether the recipient of these privileges can grant the same privileges to others. Defaults to false. It cannot be set when `role` is "public": PostgreSQL does not allow to grant options to PUBLIC.

Some object types can only be granted by a superuser, or by the owner of the object: `language`, `tablespace` and `parameter` are owned by the superuser, and `foreign_data_wrapper` and `foreign_server` need the privilege to be granted by the owner. The owners of the `type`, `domain` and `large_object` objects are granted to the connected user during the change, when it is not a superuser.


## Examples

Grant USAGE on a type, and SET on a configuration parameter:

```hcl
resource "postgresql_grant" "type_usage" {
  database    = "test_db"
  role        = "test_role"
  schema      = "public"
  object_type = "type"
  objects     = ["my_enum"]
  privileges  = ["USAGE"]
}

resource "postgresql_grant" "work_mem_set" {
  database    = "test_db"
  role        = "test_role"
  object_type = "parameter"
  objects     = ["work_mem"]
  privileges  = ["SET"]
}
```

Revoke default accesses for public schema:

```hcl
resource "postgresql_grant" "revoke_public" {
  database    = "test_db"
  role        = "public"
  schema      = "public"
  object_type = "schema"
  privileges  = []
}
```
