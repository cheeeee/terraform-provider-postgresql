package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestAccPostgresqlReplicationSlot_TwoPhase(t *testing.T) {
	testAccPostgresqlReplicationSlotFlag(t, "two_phase",
		"SELECT FROM pg_create_logical_replication_slot('slot', 'test_decoding', false, true)",
		featureLogicalSlotTwoPhase)
}

func TestAccPostgresqlReplicationSlot_TwoPhaseNotSupported(t *testing.T) {
	testAccPostgresqlReplicationSlotNotSupported(t, "two_phase", "14", featureLogicalSlotTwoPhase)
}

func TestAccPostgresqlReplicationSlot_Failover(t *testing.T) {
	testAccPostgresqlReplicationSlotFlag(t, "failover",
		"SELECT FROM pg_create_logical_replication_slot('slot', 'test_decoding', false, false, true)",
		featureLogicalSlotFailover)
}

func TestAccPostgresqlReplicationSlot_FailoverNotSupported(t *testing.T) {
	testAccPostgresqlReplicationSlotNotSupported(t, "failover", "17", featureLogicalSlotFailover)
}

// testAccPostgresqlReplicationSlotFlag checks the lifecycle of an attribute which
// can only be set at creation: creation, import, replacement and a slot which is
// dropped or created with another value outside of Terraform.
// createSQL creates the slot outside of Terraform with the attribute set to true.
func testAccPostgresqlReplicationSlotFlag(t *testing.T, attribute, createSQL string, feature featureName) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateLogicalReplicationSlot(t)
			testCheckCompatibleVersion(t, feature)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: replicationSlotFlagConfig(attribute, true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlReplicationSlotExists("postgresql_replication_slot.myslot"),
					testAccCheckPostgresqlReplicationSlotFlag("slot", attribute, true),
					resource.TestCheckResourceAttr("postgresql_replication_slot.myslot", attribute, "true"),
				),
			},
			{
				ResourceName:      "postgresql_replication_slot.myslot",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// The slot is created again if it has been dropped outside of Terraform.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT pg_drop_replication_slot('slot')")
				},
				Config: replicationSlotFlagConfig(attribute, true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlReplicationSlotExists("postgresql_replication_slot.myslot"),
					testAccCheckPostgresqlReplicationSlotFlag("slot", attribute, true),
				),
			},
			// The attribute cannot be changed in place, the slot is replaced.
			{
				Config: replicationSlotFlagConfig(attribute, false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlReplicationSlotExists("postgresql_replication_slot.myslot"),
					testAccCheckPostgresqlReplicationSlotFlag("slot", attribute, false),
					resource.TestCheckResourceAttr("postgresql_replication_slot.myslot", attribute, "false"),
				),
			},
			// A slot created outside of Terraform with another value shows up in the plan.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "SELECT pg_drop_replication_slot('slot')")
					dbExecute(t, dsn, createSQL)
				},
				Config:             replicationSlotFlagConfig(attribute, false),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// testAccPostgresqlReplicationSlotNotSupported checks that the attribute is refused
// with a clear error by a server which does not support it.
func testAccPostgresqlReplicationSlotNotSupported(t *testing.T, attribute, version string, feature featureName) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckUnsupportedFeature(t, feature)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      replicationSlotFlagConfig(attribute, true),
				ExpectError: regexp.MustCompile(fmt.Sprintf("attribute %s requires PostgreSQL %s or newer", attribute, version)),
			},
		},
	})
}

func replicationSlotFlagConfig(attribute string, value bool) string {
	return fmt.Sprintf(`
resource "postgresql_replication_slot" "myslot" {
  name   = "slot"
  plugin = "test_decoding"
  %s     = %t
}
`, attribute, value)
}

// testAccCheckPostgresqlReplicationSlotFlag checks the value of a column of pg_replication_slots
// for a logical replication slot, read from the catalog.
func testAccCheckPostgresqlReplicationSlotFlag(name, column string, expected bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}

		var value bool
		query := fmt.Sprintf(
			"SELECT %s FROM pg_catalog.pg_replication_slots WHERE slot_name = $1 AND slot_type = 'logical'",
			pq.QuoteIdentifier(column),
		)
		if err := db.QueryRow(query, name).Scan(&value); err != nil {
			return fmt.Errorf("error reading %s of replication slot %s: %w", column, name, err)
		}
		if value != expected {
			return fmt.Errorf("replication slot %s: expected %s=%t, got %t", name, column, expected, value)
		}
		return nil
	}
}

// testCheckUnsupportedFeature skips the test if the server supports the feature.
func testCheckUnsupportedFeature(t *testing.T, feature featureName) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}
	if db.featureSupported(feature) {
		t.Skipf("Skip test: PostgreSQL %s supports the feature", db.version)
	}
}

func TestAccPostgresqlReplicationSlot_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateLogicalReplicationSlot(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "postgresql_replication_slot" "myslot" {
					name   = "slot"
					plugin = "test_decoding"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlReplicationSlotExists("postgresql_replication_slot.myslot"),
					resource.TestCheckResourceAttr(
						"postgresql_replication_slot.myslot", "name", "slot"),
					resource.TestCheckResourceAttr(
						"postgresql_replication_slot.myslot", "plugin", "test_decoding"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlReplicationSlotDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_replication_slot" {
			continue
		}

		database, ok := rs.Primary.Attributes[extDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkReplicationSlotExists(txn, getReplicationSlotNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking replication slot %s", err)
		}

		if exists {
			return fmt.Errorf("ReplicationSlot still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlReplicationSlotExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		database, ok := rs.Primary.Attributes[extDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}

		extName, ok := rs.Primary.Attributes[extNameAttr]
		if !ok {
			return fmt.Errorf("No Attribute for replication slot name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkReplicationSlotExists(txn, extName)

		if err != nil {
			return fmt.Errorf("error checking replication slot %s", err)
		}

		if !exists {
			return fmt.Errorf("ReplicationSlot not found")
		}

		return nil
	}
}

func TestAccPostgresqlReplicationSlot_Database(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlReplicationSlotDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_replication_slot" "myslot" {
		name     = "slot"
		plugin   = "test_decoding"
		database = "%s"
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateLogicalReplicationSlot(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlReplicationSlotDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlReplicationSlotDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlReplicationSlotExists("postgresql_replication_slot.myslot"),
					resource.TestCheckResourceAttr(
						"postgresql_replication_slot.myslot", "name", "slot"),
					resource.TestCheckResourceAttr(
						"postgresql_replication_slot.myslot", "plugin", "test_decoding"),
					resource.TestCheckResourceAttr(
						"postgresql_replication_slot.myslot", "database", dbName),
				),
			},
		},
	})
}

func checkReplicationSlotExists(txn *sql.Tx, slotName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE from pg_catalog.pg_replication_slots d WHERE slot_name=$1", slotName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about replication slot: %s", err)
	}

	return true, nil
}
