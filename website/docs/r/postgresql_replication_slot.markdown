---
layout: "postgresql"
page_title: "PostgreSQL: postgresql_replication_slot"
sidebar_current: "docs-postgresql-resource-postgresql_replication_slot"
description: |-
Creates and manages a replication slot on a PostgreSQL server.
---

# postgresql\_replication\_slot

The ``postgresql_replication_slot`` resource creates and manages a replication slot on a PostgreSQL
server.


## Usage

```hcl
resource "postgresql_replication_slot" "my_slot" {
  name  = "my_slot"
  plugin = "test_decoding"
}
```

## Argument Reference

* `name` - (Required) The name of the replication slot.
* `plugin` - (Required) Sets the output plugin.
* `database` - (Optional) Which database to create the replication slot on. Defaults to provider database.
* `two_phase` - (Optional) Enables two-phase decoding of the replication slot. Requires PostgreSQL 14 or newer. Changing it replaces the slot.
* `failover` - (Optional) Enables the replication slot to be synchronized to standbys, for failover. Requires PostgreSQL 17 or newer. Changing it replaces the slot.

~> **Note:** `two_phase` and `failover` can only be set when the slot is created: a change replaces the slot, which drops it.

~> **Note:** A subscription changes the slot it uses: `postgresql_subscription` with `two_phase = true` and
`create_slot = false` enables two-phase decoding on its slot once its tables are synchronized, and a change of
`failover` on an existing subscription changes it on its slot too (a subscription created with `failover = true` on
an existing slot leaves the slot as it is). The next plan then reports the new value of the slot as a change made
outside of Terraform and proposes to replace the slot, which would break the replication. Give the slot the same
values as the subscription, or let the subscription decide:

```hcl
resource "postgresql_replication_slot" "sub" {
  name   = "sub"
  plugin = "pgoutput"

  lifecycle {
    ignore_changes = [two_phase, failover]
  }
}
```
