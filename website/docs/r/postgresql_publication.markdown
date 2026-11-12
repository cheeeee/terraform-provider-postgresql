---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_publication"
sidebar_current: "docs-postgresql-resource-postgresql_publication"
description: |-
Creates and manages a publication in a PostgreSQL server database.
---

# postgresql_publication

The `postgresql_publication` resource creates and manages a publication on a PostgreSQL
server.

## Usage

```hcl
resource "postgresql_publication" "publication" {
  name  = "publication"
  tables = ["public.test","another_schema.test"]
}
```

A column list, a row filter, ONLY (PostgreSQL 15 and above for the first two):

```hcl
resource "postgresql_publication" "orders" {
  name = "orders"

  table {
    name       = "public.orders"
    columns    = ["id", "status"]
    row_filter = "status = 'paid'"
  }

  table {
    name = "public.old_orders"
    only = true
  }
}
```

The tables of schemas (PostgreSQL 15 and above, reserved to superusers by PostgreSQL):

```hcl
resource "postgresql_publication" "schemas" {
  name    = "schemas"
  schemas = ["app", "audit"]
  tables  = ["public.other"]
}
```

## Argument Reference

- `name` - (Required) The name of the publication. It can be changed in place.
- `database` - (Optional) Which database to create the publication on. Defaults to provider database.
- `tables` - (Optional) Which tables add to the publication. By defaults no tables added. Format of table is `<schema_name>.<table_name>`. If `<schema_name>` is not specified - default database schema will be used. The descendants of a table (inheritance) are published with it, like `FOR TABLE <table>` does. A table whose name contains a dot can not be written. Conflicts with `table`, `all_tables` and `all_sequences`.
- `table` - (Optional, repeatable block) A table of the publication with its options, to use instead of `tables` when a table has an option. A table must not be written in both. The block lists all the tables of the publication. Conflicts with `tables`, `all_tables` and `all_sequences`. See below.
- `schemas` - (Optional) The schemas whose tables are all published (`FOR TABLES IN SCHEMA`), now and in the future. PostgreSQL 15 and above. PostgreSQL reserves it to superusers, and refuses the column lists in a publication which has a schema. Can be combined with `tables` or `table`. Conflicts with `all_tables` and `all_sequences`.
- `all_tables` - (Optional) Should be ALL TABLES added to the publication. Defaults to 'false'. Only a superuser can create such a publication. Changing it replaces the publication.
- `except_tables` - (Optional) With `all_tables`, the tables which are not published (`ALL TABLES EXCEPT`). PostgreSQL 19 and above. Same format as `tables`. Can be changed in place.
- `all_sequences` - (Optional) Should ALL SEQUENCES be added to the publication (can be used with `all_tables`). PostgreSQL 19 and above. Changing it replaces the publication.
- `owner` - (Optional) Who owns the publication. Defaults to provider user.
- `drop_cascade` - (Optional) Should all subsequent resources of the publication be dropped. Defaults to 'false'
- `publish_param` - (Optional) Which 'publish' options should be turned on: `insert`, `update`, `delete`, `truncate` (the last one since PostgreSQL 11), in this order. Default to all of them.
- `publish_via_partition_root_param` - (Optional) Should be option 'publish_via_partition_root' be turned on. PostgreSQL 13 and above. Default to 'false'
- `publish_generated_columns` - (Optional) Whether the generated columns are published: `none` or `stored`. PostgreSQL 18 and above. Defaults to the one of the server (`none`).

The attributes which the server does not have are refused with an error.

Everything is changed in place (tables, schemas, parameters, owner, name) except `all_tables` and `all_sequences`, which replace the publication.

### The `table` block

- `name` - (Required) The table, `<schema_name>.<table_name>`.
- `only` - (Optional) Publish this table only (`ONLY`), not its descendants. Otherwise the descendants are published too (`*`). Defaults to 'false'. PostgreSQL can not tell ONLY from the default for a table which has no descendant, and for a partitioned table (its partitions are not descendants for a publication): the value of the configuration is kept.
- `columns` - (Optional) The columns to publish, in any order. All the columns when it is not set. PostgreSQL 15 and above.
- `row_filter` - (Optional) The expression (the content of `WHERE (...)`) which selects the rows to publish. PostgreSQL 15 and above. PostgreSQL stores a normalized form of the expression (parentheses, casts...): the expression is kept as it is written in the configuration as long as PostgreSQL has the form it gave to it when it was applied, so the plan is empty after the apply whatever the way it is written. Writing the same expression differently is seen as a change, applied without any effect. A filter changed outside of Terraform is seen in its normalized form.
- `row_filter_normalized` - (Computed) The expression as PostgreSQL stores it.

A change of the options of a table removes it from the publication and adds it again in the same transaction. A table and one of its descendants must not be both in the configuration unless both have `only = true`.

When the publication is imported, or when a change is made outside of Terraform, the tables which have an option are read as `table` blocks (all the tables are when the configuration has blocks) and all of them are listed in `tables`. A descendant added by PostgreSQL with its parent is not listed.

## Import Example

Publication can be imported using this format:

```
$ terraform import postgresql_publication.publication {{database_name}}.{{publication_name}}
```
