package postgresql

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func resourcePostgreSQLPhysicalReplicationSlot() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLPhysicalReplicationSlotCreate),
		Read:   PGResourceFunc(resourcePostgreSQLPhysicalReplicationSlotRead),
		Delete: PGResourceFunc(resourcePostgreSQLPhysicalReplicationSlotDelete),
		Exists: PGResourceExistsFunc(resourcePostgreSQLPhysicalReplicationSlotExists),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"immediately_reserve": {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "Reserves the WAL immediately at creation, so that a standby can always catch up. Read back as whether the slot has reserved WAL",
				// WAL which is reserved cannot be released without dropping the slot: a configuration
				// which sets false does not replace a slot which has reserved WAL.
				DiffSuppressFunc: func(_, old, new string, _ *schema.ResourceData) bool {
					return old == "true" && new == "false"
				},
			},
		},
	}
}

func resourcePostgreSQLPhysicalReplicationSlotCreate(db *DBConnection, d *schema.ResourceData) error {
	name := d.Get("name").(string)
	sql := "SELECT FROM pg_create_physical_replication_slot($1)"
	args := []interface{}{name}
	if d.Get("immediately_reserve").(bool) {
		sql = "SELECT FROM pg_create_physical_replication_slot($1, $2)"
		args = append(args, true)
	}
	if _, err := db.Exec(sql, args...); err != nil {
		return fmt.Errorf("could not create physical ReplicationSlot %s: %w", name, err)
	}
	d.SetId(name)

	return resourcePostgreSQLPhysicalReplicationSlotRead(db, d)
}

func resourcePostgreSQLPhysicalReplicationSlotExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	query := "SELECT 1 FROM pg_catalog.pg_replication_slots WHERE slot_name = $1 and slot_type = 'physical'"
	var unused int
	err := db.QueryRow(query, d.Id()).Scan(&unused)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}

	return true, nil
}

func resourcePostgreSQLPhysicalReplicationSlotRead(db *DBConnection, d *schema.ResourceData) error {
	var reserved bool
	query := "SELECT restart_lsn IS NOT NULL FROM pg_catalog.pg_replication_slots WHERE slot_name = $1 AND slot_type = 'physical'"
	err := db.QueryRow(query, d.Id()).Scan(&reserved)
	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL physical ReplicationSlot (%s) not found", d.Id())
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading physical ReplicationSlot: %w", err)
	}

	d.Set("name", d.Id())
	d.Set("immediately_reserve", reserved)
	return nil
}

func resourcePostgreSQLPhysicalReplicationSlotDelete(db *DBConnection, d *schema.ResourceData) error {

	replicationSlotName := d.Get("name").(string)

	if _, err := db.Exec("SELECT pg_drop_replication_slot($1)", replicationSlotName); err != nil {
		return err
	}

	d.SetId("")
	return nil
}
