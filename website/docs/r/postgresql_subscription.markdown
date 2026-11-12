---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_subscription"
sidebar_current: "docs-postgresql-resource-postgresql_subscription"
description: |-
Creates and manages a subscription in a PostgreSQL server database.
---

# postgresql_subscription

The `postgresql_subscription` resource creates and manages a logical replication subscription on a PostgreSQL
server.

## Usage

```hcl
resource "postgresql_subscription" "subscription" {
  name          = "subscription"
  conninfo      = "host=localhost port=5432 dbname=mydb user=postgres password=postgres"
  publications  = ["publication"]
}
```

## Argument Reference

- `name` - (Required) The name of the subscription. Changing it renames the subscription (`ALTER SUBSCRIPTION ... RENAME TO`); the replication slot is not renamed.
- `conninfo` - (Required) The connection string to the publisher. It should follow the [keyword/value format](https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING). Changing it runs `ALTER SUBSCRIPTION ... CONNECTION`. It can only be read back by a user which is allowed to read `pg_subscription.subconninfo` (a superuser): for the others, a change made outside of Terraform is not detected.
- `publications` - (Required) Names of the publications on the publisher to subscribe to. Changing the list runs `ALTER SUBSCRIPTION ... SET PUBLICATION`, with `refresh = true` when the subscription is enabled (the tables of the new publications are copied, the tables of the removed ones are no more replicated) and `refresh = false` when it is disabled (the server refuses to refresh a disabled subscription: enable it, or refresh it yourself).
- `database` - (Optional) Which database to create the subscription on. Defaults to provider database. Changing it replaces the subscription.
- `enabled` - (Optional) Whether the subscription replicates (`ENABLE` / `DISABLE`). The server default is `true`. The value is read back: when it is set, a subscription which is enabled or disabled outside of Terraform is put back in the state of the configuration.
- `owner` - (Optional) The owner of the subscription (`ALTER SUBSCRIPTION ... OWNER TO`). Defaults to the user of the provider. The new owner has to be a superuser, or, with PostgreSQL 16 and later, a member of `pg_create_subscription` which has the `CREATE` privilege on the database. Take care that a subscription which is owned by a user which is not a superuser can only be dropped by connecting to the publisher with a password, as the `password_required` option does.
- `slot_name` - (Optional) Name of the replication slot to use, or `NONE` for a subscription without slot (`SET (slot_name = ...)`). The default behavior is to use the name of the subscription for the slot name. Setting it to `NONE` disables the subscription first, as the server requires. The value is read back (`NONE` for no slot).
- `synchronous_commit` - (Optional) `local`, `remote_write`, `remote_apply`, `on` or `off`. The server default is `off`.
- `binary` - (Optional) Whether the publisher sends the data in binary format. The server default is `false`. PostgreSQL 14 and later.
- `streaming` - (Optional) `off`, `on` or `parallel` (`parallel` needs PostgreSQL 16 or later). The server default is `off`. PostgreSQL 14 and later.
- `two_phase` - (Optional) Whether two-phase commit is enabled. The server default is `false`. PostgreSQL 15 and later. PostgreSQL 18 and later alter it in place (the provider disables the subscription meanwhile); with the previous versions, changing it replaces the subscription.
- `disable_on_error` - (Optional) Whether the subscription is disabled when its apply worker fails. The server default is `false`. PostgreSQL 15 and later.
- `password_required` - (Optional) Whether the connection to the publisher has to use a password. The server default is `true`; only a superuser can set it to `false`. PostgreSQL 16 and later.
- `run_as_owner` - (Optional) Whether the changes are applied with the privileges of the owner of the subscription. The server default is `false`. PostgreSQL 16 and later.
- `origin` - (Optional) `any` or `none`. The server default is `any`. PostgreSQL 16 and later.
- `failover` - (Optional) Whether the slot of the subscription is synchronized to the standbys of the publisher. The server default is `false`. PostgreSQL 17 and later. The server only alters it for a disabled subscription: the provider disables the subscription and enables it again.
- `retain_dead_tuples` - (Optional) Whether the information to detect the conflicts is retained. The server default is `false`. PostgreSQL 18 and later. Altering it disables and enables the subscription again.
- `max_retention_duration` - (Optional) The time in milliseconds the dead tuples are retained, 0 for no limit. PostgreSQL 19 and later.
- `wal_receiver_timeout` - (Optional) The `wal_receiver_timeout` of the apply worker, for example `30s`. PostgreSQL 19 and later.
- `create_slot` - (Optional) Specifies whether the command should create the replication slot on the publisher. Default behavior is true. See below.
- `connect` - (Optional) Specifies whether the command should connect to the publisher. The server default is `true`. See below.
- `copy_data` - (Optional) Specifies whether the existing data of the publications is copied when the replication starts. The server default is `true`. See below.

The options `synchronous_commit` to `wal_receiver_timeout` and `enabled`, `owner` and `slot_name` are optional: when they are not in the configuration, the server chooses the value (or keeps the current one), which is read back. An option which the version of the server does not know is refused with an error at the creation or the change of the subscription.

### The options of the creation

`create_slot`, `connect` and `copy_data` are only used to create the subscription (`CREATE SUBSCRIPTION ... WITH (...)`). They are not stored in the catalog, so they are not read back: changing them afterwards does not change the subscription (and does not replace it), and a subscription which has been imported keeps their default.

- `create_slot = false` (and the replication slot is kept on the publisher when the subscription is destroyed: the provider disables the subscription and sets its slot to `NONE` before dropping it). With `create_slot = true` the slot is dropped with the subscription, which needs the publisher to be reachable. Creating the slot on the server which the subscription lives on does not work (`CREATE SUBSCRIPTION` waits for itself): create the slot with `postgresql_replication_slot` and use `create_slot = false` and `slot_name`.
- `connect = false` creates the subscription without connecting to the publisher: it is disabled and has no slot. The server refuses `connect = false` together with `create_slot = true`, `enabled = true` or `copy_data = true`: the provider refuses them with an error too (`create_slot` defaults to `true`, so the unset value is replaced by `false`).

### Not managed

`ALTER SUBSCRIPTION ... REFRESH PUBLICATION`, `SKIP`, `ADD PUBLICATION` and `DROP PUBLICATION` (the provider sets the whole list of publications), the `SERVER` form of PostgreSQL 19 and the keyword owners (`CURRENT_USER`...) are not managed.

## Import

A subscription is imported with its database and its name:

```
terraform import postgresql_subscription.subscription mydb.subscription
```

## Postgres documentation
- https://www.postgresql.org/docs/current/sql-createsubscription.html
- https://www.postgresql.org/docs/current/sql-altersubscription.html
- https://www.postgresql.org/docs/current/sql-dropsubscription.html
