---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_function"
sidebar_current: "docs-postgresql-resource-postgresql_function"
description: |-
Creates and manages a function on a PostgreSQL server.
---

# postgresql\_function

The ``postgresql_function`` resource creates and manages a function on a PostgreSQL
server.

## Usage

```hcl
resource "postgresql_function" "increment" {
    name = "increment"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN i + 1;
        END;
    EOF
}
```

## Argument Reference

* `name` - (Required) The name of the function. Changing it renames the function in place
  (`ALTER FUNCTION ... RENAME TO`).

* `schema` - (Optional) The schema where the function is located.
  If not specified, the function is created in the current schema.
  Changing it moves the function in place (`ALTER FUNCTION ... SET SCHEMA`).

* `database` - (Optional) The database where the function is located.
  If not specified, the function is created in the current database.

* `arg` - (Optional) List of arguments for the function.
  * `type` - (Required) The type of the argument.
  * `name` - (Optional) The name of the argument.
  * `mode` - (Optional) Can be one of IN, INOUT, OUT, or VARIADIC. Default is IN.
    The columns of a `RETURNS TABLE` are not arguments: they are part of `returns`.
  * `default` - (Optional) An expression to be used as default value if the parameter is not specified.

* `returns` - (Optional) Type that the function returns, including `SETOF type` and
  `TABLE(column type, ...)`. PostgreSQL normalizes the names of the types, so write them the way
  PostgreSQL prints them (e.g. `TABLE(id integer, label text)`), otherwise the plan is never empty.
  It can be computed from the OUT and INOUT arguments (`record` when there are several of them).
  Default is void.

* `language` - (Optional) The function programming language. Can be one of internal, sql, c, plpgsql. Default is plpgsql.
  The internal and c languages are reserved to superusers.

* `parallel` - (Optional) Indicates if the function is parallel safe. Can be one of UNSAFE, RESTRICTED, or SAFE. Default is UNSAFE.

* `security_definer` - (Optional) If the function should execute with the permissions of the owner, rather than the permissions of the caller. Default is false.

* `strict` - (Optional) If the function should always return NULL when any of the inputs is NULL
  (`STRICT`, a synonym of `RETURNS NULL ON NULL INPUT`; `CALLED ON NULL INPUT` when false). Default is false.

* `volatility` - (Optional) Defines the volatility of the function. Can be one of VOLATILE, STABLE, or IMMUTABLE. Default is VOLATILE.

* `leakproof` - (Optional) If the function has no side effects and reveals nothing about its arguments
  except by its result (`LEAKPROOF`). Reserved to superusers. Default is false.

* `cost` - (Optional) Estimated execution cost of the function, in cost units (`COST`).
  If not specified, PostgreSQL chooses 100 (1 for the functions in the c and internal languages).

* `rows` - (Optional) Estimated number of rows returned by a function which returns a set (`ROWS`).
  It can only be set for such a function. If not specified, PostgreSQL chooses 1000.

* `support` - (Optional) Planner support function (`SUPPORT`), schema qualified, e.g.
  `pg_catalog.generate_series_int4_support`. Reserved to superusers. PostgreSQL has no `ALTER FUNCTION`
  to remove it: removing the attribute replaces the function with `CREATE OR REPLACE FUNCTION`.

* `window` - (Optional) If the function is a window function (`WINDOW`). Only useful for the functions in the
  c and internal languages. Changing it replaces the function. Default is false.

* `transform` - (Optional) List of the types for which the function applies a transform
  (`TRANSFORM FOR TYPE`). The transforms must already exist. Write the names of the types the way
  PostgreSQL prints them (e.g. `integer`, not `int4`). A change uses `CREATE OR REPLACE FUNCTION`.

* `parameters` - (Optional) Map of configuration parameters set while the function runs
  (`SET configuration_parameter TO value`). The names must be lowercase. The value of a list
  parameter such as `search_path` is a comma separated list. The parameters are added, changed
  and removed with `ALTER FUNCTION ... SET / RESET`.

* `owner` - (Optional) The role which owns the function (`ALTER FUNCTION ... OWNER TO`).
  It is read from the database when it is not specified. The connection has to be allowed to
  become that role, and that role needs the `CREATE` privilege on the schema.

* `depends_on_extensions` - (Optional) Set of extensions the function depends on: the function is dropped
  when one of them is dropped (`DEPENDS ON EXTENSION`). Removing an extension from the set requires
  PostgreSQL 13 or later (`NO DEPENDS ON EXTENSION`).

* `body` - (Optional) Function body. Required, except when `sql_body` is used.
  This should be the body content within the `AS $$` and the final `$$`. It will also accept the `AS $$` and `$$` if added.
  For the c language, it is the link symbol (it defaults to the name of the function).

* `obj_file` - (Optional) Object file (shared library) of a function in the c language
  (`AS 'obj_file', 'link_symbol'`, the link symbol is `body`). Required for, and only for, the c language.
  Reserved to superusers.

* `sql_body` - (Optional) SQL-standard body of a function in the sql language (PostgreSQL 14 or later):
  `RETURN expression` or `BEGIN ATOMIC ... END`. It conflicts with `body`.
  PostgreSQL parses the body when the function is created and returns it deparsed
  (e.g. `RETURN i+1` is returned as `RETURN (i + 1)`). The provider keeps the text of the
  configuration in the state as long as the body stored in the database is the one it applied (the
  read-only `sql_body_deparsed` attribute), so the plan is empty after an apply. A body changed outside of
  Terraform is detected and shown in the plan. After an import the state holds the deparsed text:
  the next plan shows a change if the configuration is written differently, and applying it is harmless.

* `drop_cascade` - (Optional) True to automatically drop objects that depend on the function (such as
  operators or triggers), and in turn all objects that depend on those objects. Default is false.

## Attribute Reference

* `sql_body_deparsed` - The SQL-standard body as returned by PostgreSQL (empty without `sql_body`).

## How the changes are applied

A change of the name, the schema, the owner, the extensions, `volatility`, `strict`, `security_definer`,
`parallel`, `leakproof`, `cost`, `rows`, `support` (but not its removal) and `parameters` is done in place
with `ALTER FUNCTION`: the function keeps its identity and the objects which depend on it.
A change of the body (`body`, `sql_body`, `obj_file`), of `transform` or of the defaults of the arguments uses
`CREATE OR REPLACE FUNCTION`. A change of the arguments (apart from their defaults), of `returns`,
`language` or `window` replaces the function (PostgreSQL does not allow anything else).

Procedures and aggregates are not managed by this resource.

## Import

It is possible to import a `postgresql_function` resource with the following
command:

```
$ terraform import postgresql_function.function_foo "my_database.my_schema.my_function_name(arguments)"
```

Where `my_database` is the name of the database containing the schema,
`my_schema` is the name of the schema in the PostgreSQL database, `my_function_name` is the function name to be imported, `arguments` is the argument signature of the function including all non OUT types and
`postgresql_schema.function_foo` is the name of the resource whose state will be
populated as a result of the command.
