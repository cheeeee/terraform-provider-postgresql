# Database for an application

This module creates everything an application which manages its own tables needs
on a PostgreSQL server:

| Object | Purpose |
|---|---|
| role `<name>_owner` | owns the database and its tables, nobody logs in with it |
| role `<name>_migrator` | used by the application to create and migrate its tables, its sessions act as the owner |
| database `<name>` | nothing in it is accessible to the other roles of the server |
| role `<name>_rw` | reads and writes the data, cannot create, alter or drop the tables |
| roles of `roles` | read the data, or read and write it |
| privileges and default privileges | the tables created by the owner, now or later, are accessible to the roles of the module |
| extensions | the ones the application cannot create by itself |
| parameters | pgaudit for the database or for one role, and any other setting, e.g. `statement_timeout` |

The tables, indexes and data stay in the migrations of the application.
Only the objects of the module are managed: the other roles and databases of
the server are not read or changed.

```hcl
# The password of the reporting role is in AWS Secrets Manager.
# It is read at every run and never stored in the plan or in the state.
ephemeral "aws_secretsmanager_secret_version" "reporting" {
  secret_id = "prod/orders/reporting"
}

module "orders" {
  source = "github.com/cheeeee/terraform-provider-postgresql//examples/app-database"

  name       = "orders"
  audit      = "ddl, write"
  extensions = ["pgcrypto"]

  # On AWS RDS: the roles log in with an IAM token and have no password
  iam_auth = true

  roles = {
    orders_ro        = {}                              # reads the data
    orders_batch     = { access = "rw" }               # reads and writes the data
    orders_reporting = { audit = "none", iam = false } # not audited, logs in with a password
  }

  passwords = {
    orders_reporting = ephemeral.aws_secretsmanager_secret_version.reporting.secret_string
  }
  password_versions = {
    orders_reporting = "1" # change it to apply a new password
  }

  database_parameters = {
    statement_timeout = "30s"
  }
}
```

It needs Terraform or OpenTofu 1.11 or later (write-only passwords).

## Variables

| Name | Default | Description |
|---|---|---|
| `name` | required | Name of the database, and prefix of the `_owner`, `_migrator` and `_rw` roles. |
| `roles` | `{}` | Other roles, by name. `access`: `"ro"` (default) or `"rw"`. `audit`: value of `pgaudit.log` for this role, it overrides the one of the database. `iam`: overrides `iam_auth` for this role. |
| `iam_auth` | `false` | On AWS RDS, the roles log in with an IAM token (they are members of `rds_iam`). |
| `passwords` | `{}` | Passwords of the roles which do not use IAM, by name of role. Write-only: never stored in the plan or in the state. |
| `password_versions` | `{}` | Version of the password, by name of role. A role only gets a password if it has a version, a new password is only applied when its version changes. |
| `audit` | `null` | Value of `pgaudit.log` for the database: `"ddl, write"`, `"all"`, `"none"`... `null` to not set it. pgaudit has to be loaded on the server. |
| `database_parameters` | `{}` | Other configuration parameters of the database. The ones set outside of Terraform are removed. |
| `extensions` | `[]` | Extensions to create in the database. |
| `connection_limit` | `-1` | Maximum number of concurrent connections to the database. |

## Outputs

`database`, `owner_role`, `migrator_role`, `readwrite_role`, `roles`.

## IAM authentication on AWS RDS

The members of `rds_iam` can only log in with an IAM token, and the role which
owns a database has to be granted for a moment to the user which runs Terraform
to create it. If this user logs in with a password, it must never become a member
of `rds_iam`: it could not connect anymore. This is why the owner of the database
is a role which nobody logs in with, and the provider refuses to create a database
owned by a member of `rds_iam` with a user which is not one.

## Tests

The module is applied, used and destroyed by the end-to-end test of the provider
(`make teste2e`), with Terraform and with OpenTofu: the privileges of every role
are checked with real queries. On AWS RDS, the IAM logins and the password from
Secrets Manager are checked by `tests/e2e/rds_secret.sh`.
