package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlPhysicalReplicationSlot_Basic(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	slotConfig := func(name string) string {
		return fmt.Sprintf(`
resource "postgresql_physical_replication_slot" "myslot" {
  name = "%s"
}
`, name)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreatePhysicalReplicationSlot(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPhysicalReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: slotConfig("physical_slot"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
					resource.TestCheckResourceAttr("postgresql_physical_replication_slot.myslot", "name", "physical_slot"),
					resource.TestCheckResourceAttr("postgresql_physical_replication_slot.myslot", "id", "physical_slot"),
				),
			},
			{
				ResourceName:      "postgresql_physical_replication_slot.myslot",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// The slot is created again if it has been dropped outside of Terraform.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT pg_drop_replication_slot('physical_slot')")
				},
				Config: slotConfig("physical_slot"),
				Check:  testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
			},
			// A slot cannot be renamed, it is replaced.
			{
				Config: slotConfig("physical_slot2"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", false),
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot2", true),
					resource.TestCheckResourceAttr("postgresql_physical_replication_slot.myslot", "name", "physical_slot2"),
				),
			},
		},
	})
}

func TestAccPostgresqlPhysicalReplicationSlot_ImmediatelyReserve(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	slotConfig := func(reserve bool) string {
		return fmt.Sprintf(`
resource "postgresql_physical_replication_slot" "myslot" {
  name                = "physical_slot"
  immediately_reserve = %t
}
`, reserve)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreatePhysicalReplicationSlot(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPhysicalReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: slotConfig(true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
					testAccCheckPostgresqlPhysicalReplicationSlotReserved("physical_slot", true),
					resource.TestCheckResourceAttr("postgresql_physical_replication_slot.myslot", "immediately_reserve", "true"),
				),
			},
			{
				ResourceName:      "postgresql_physical_replication_slot.myslot",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// A slot which has not reserved WAL is created when the configuration does not reserve it.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT pg_drop_replication_slot('physical_slot')")
				},
				Config: slotConfig(false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
					testAccCheckPostgresqlPhysicalReplicationSlotReserved("physical_slot", false),
					resource.TestCheckResourceAttr("postgresql_physical_replication_slot.myslot", "immediately_reserve", "false"),
				),
			},
			// Reserving the WAL of an existing slot replaces it.
			{
				Config: slotConfig(true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
					testAccCheckPostgresqlPhysicalReplicationSlotReserved("physical_slot", true),
				),
			},
			// The WAL reserved by a slot cannot be released: a configuration which does not reserve it does not replace the slot.
			{
				Config: slotConfig(false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotReserved("physical_slot", true),
				),
			},
			// A slot which has not reserved WAL, created outside of Terraform, shows up in the plan.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT pg_drop_replication_slot('physical_slot')")
					dbExecute(t, dsn, "SELECT FROM pg_create_physical_replication_slot('physical_slot')")
				},
				Config:             slotConfig(true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// testAccCheckPostgresqlPhysicalReplicationSlotReserved checks whether the slot has reserved WAL,
// which is the case when restart_lsn is set.
func testAccCheckPostgresqlPhysicalReplicationSlotReserved(name string, expected bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}

		var reserved bool
		err = db.QueryRow(
			"SELECT restart_lsn IS NOT NULL FROM pg_catalog.pg_replication_slots WHERE slot_name = $1 AND slot_type = 'physical'", name,
		).Scan(&reserved)
		if err != nil {
			return fmt.Errorf("error reading physical replication slot %s: %w", name, err)
		}
		if reserved != expected {
			return fmt.Errorf("physical replication slot %s: expected reserved=%t, got %t", name, expected, reserved)
		}
		return nil
	}
}

// testAccCheckPostgresqlPhysicalReplicationSlotCatalog checks in pg_replication_slots that the slot is physical:
// a physical slot has neither an output plugin nor a database.
func testAccCheckPostgresqlPhysicalReplicationSlotCatalog(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}

		var slotType string
		var plugin, database sql.NullString
		err = db.QueryRow(
			"SELECT slot_type, plugin, database FROM pg_catalog.pg_replication_slots WHERE slot_name = $1", name,
		).Scan(&slotType, &plugin, &database)
		if err != nil {
			return fmt.Errorf("error reading replication slot %s: %w", name, err)
		}
		if slotType != "physical" {
			return fmt.Errorf("replication slot %s: expected slot_type physical, got %s", name, slotType)
		}
		if plugin.Valid {
			return fmt.Errorf("replication slot %s: physical slot has plugin %s", name, plugin.String)
		}
		if database.Valid {
			return fmt.Errorf("replication slot %s: physical slot has database %s", name, database.String)
		}
		return nil
	}
}

// A logical slot is not a physical slot: the resource neither reads it nor imports it, and a physical
// slot cannot be created with its name.
func TestAccPostgresqlPhysicalReplicationSlot_NotLogical(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Removes the logical slot left by the test, if any.
	defer dbExecute(t, dsn, "SELECT pg_drop_replication_slot(slot_name) FROM pg_catalog.pg_replication_slots WHERE slot_name = 'logical_slot'")

	physicalConfig := `
resource "postgresql_physical_replication_slot" "myslot" {
  name = "physical_slot"
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateLogicalReplicationSlot(t)
			testCheckCanCreatePhysicalReplicationSlot(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPhysicalReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: physicalConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPhysicalReplicationSlotExists("physical_slot", true),
					testAccCheckPostgresqlPhysicalReplicationSlotCatalog("physical_slot"),
				),
			},
			// The logical slot is not imported as a physical slot.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT FROM pg_create_logical_replication_slot('logical_slot', 'test_decoding')")
				},
				Config:        physicalConfig,
				ResourceName:  "postgresql_physical_replication_slot.myslot",
				ImportState:   true,
				ImportStateId: "logical_slot",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
			// A physical slot is not created with the name of the logical slot.
			{
				Config: `
resource "postgresql_physical_replication_slot" "myslot" {
  name = "logical_slot"
}
`,
				ExpectError: regexp.MustCompile(`already exists`),
			},
		},
	})
}

// testCheckCanCreatePhysicalReplicationSlot skips the test if the connected user
// is not allowed to create a physical replication slot (it needs to be a superuser
// or to have the REPLICATION attribute) or if the server does not allow it.
func testCheckCanCreatePhysicalReplicationSlot(t *testing.T) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}

	if _, err := db.Exec("SELECT FROM pg_create_physical_replication_slot('physical_slot_probe')"); err != nil {
		t.Skipf("Skip test: the connected user cannot create a physical replication slot: %v", err)
	}
	if _, err := db.Exec("SELECT pg_drop_replication_slot('physical_slot_probe')"); err != nil {
		t.Fatalf("could not drop the replication slot: %v", err)
	}
}

func checkPhysicalReplicationSlotExists(client *Client, name string) (bool, error) {
	db, err := client.Connect()
	if err != nil {
		return false, err
	}

	var unused int
	err = db.QueryRow(
		"SELECT 1 FROM pg_catalog.pg_replication_slots WHERE slot_name = $1 AND slot_type = 'physical'", name,
	).Scan(&unused)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about physical replication slot: %w", err)
	}
	return true, nil
}

func testAccCheckPostgresqlPhysicalReplicationSlotExists(name string, expected bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		exists, err := checkPhysicalReplicationSlotExists(testAccProvider.Meta().(*Client), name)
		if err != nil {
			return err
		}
		if exists != expected {
			return fmt.Errorf("physical replication slot %s: expected exists=%t, got %t", name, expected, exists)
		}
		return nil
	}
}

func testAccCheckPostgresqlPhysicalReplicationSlotDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_physical_replication_slot" {
			continue
		}

		exists, err := checkPhysicalReplicationSlotExists(client, rs.Primary.ID)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("physical replication slot %s still exists after destroy", rs.Primary.ID)
		}
	}

	return nil
}
