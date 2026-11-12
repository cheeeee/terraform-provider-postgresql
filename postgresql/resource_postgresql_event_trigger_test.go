package postgresql

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlEventTrigger_Basic(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	// Create the database outside of resource.Test
	// because we need to create test schemas.
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	schemas := []string{"test_schema1"}
	createTestSchemas(t, dbSuffix, schemas, "")

	dbName, _ := getTestDBNames(dbSuffix)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, schemas[0], "test_event", owner, "enable"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlEventTriggerExists("postgresql_event_trigger.event_trigger", dbName),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "name", "test_event"),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "on", "ddl_command_end"),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "status", "enable"),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "filter.0.values.#", "2"),
					testAccCheckEventTriggerStatus(dbName, "test_event", "O"),
				),
			},
			{
				ResourceName:      "postgresql_event_trigger.event_trigger",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// The trigger is renamed and disabled.
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, schemas[0], "test_event_renamed", owner, "disable"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlEventTriggerExists("postgresql_event_trigger.event_trigger", dbName),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "name", "test_event_renamed"),
					testAccCheckEventTriggerStatus(dbName, "test_event_renamed", "D"),
				),
			},
			// A change made outside of Terraform is detected...
			{
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), "ALTER EVENT TRIGGER test_event_renamed ENABLE")
				},
				Config:             fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, schemas[0], "test_event_renamed", owner, "disable"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// ...and reverted.
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, schemas[0], "test_event_renamed", owner, "disable"),
				Check:  testAccCheckEventTriggerStatus(dbName, "test_event_renamed", "D"),
			},
		},
	})
}

// testCheckCanCreateEventTrigger skips the test if the connected user cannot create
// an event trigger (it needs to be a superuser, or a member of rds_superuser on AWS RDS).
func testCheckCanCreateEventTrigger(t *testing.T) {
	testCheckCanExecute(
		t, "create an event trigger",
		"CREATE FUNCTION tf_probe_event_trigger() RETURNS event_trigger LANGUAGE plpgsql AS 'BEGIN END'",
		"CREATE EVENT TRIGGER tf_probe_event_trigger ON ddl_command_end EXECUTE FUNCTION tf_probe_event_trigger()",
	)
}

// testAccCheckEventTriggerStatus checks pg_event_trigger.evtenabled of an event trigger.
func testAccCheckEventTriggerStatus(database, name, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var status string
		if err := txn.QueryRow("SELECT evtenabled FROM pg_catalog.pg_event_trigger WHERE evtname = $1", name).Scan(&status); err != nil {
			return fmt.Errorf("could not read the status of the event trigger %s: %w", name, err)
		}
		if status != expected {
			return fmt.Errorf("status of the event trigger %s: expected %q, got %q", name, expected, status)
		}
		return nil
	}
}

func testAccCheckPostgresqlEventTriggerExists(n string, database string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkEventTriggerExists(txn, rs.Primary.Attributes["name"])

		if err != nil {
			return fmt.Errorf("Error checking event trigger %s", err)
		}

		if !exists {
			return fmt.Errorf("Event trigger not found")
		}

		return nil
	}
}

func testAccCheckPostgresqlEventTriggerDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_event_trigger" {
			continue
		}

		var database string
		for k, v := range rs.Primary.Attributes {
			if k == "database" {
				database = v
			}
		}

		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkEventTriggerExists(txn, rs.Primary.Attributes["name"])

		if err != nil {
			return fmt.Errorf("Error checking event trigger %s", err)
		}

		if exists {
			return fmt.Errorf("Event trigger still exists after destroy")
		}
	}

	return nil
}

func checkEventTriggerExists(txn *sql.Tx, signature string) (bool, error) {
	var _rez string
	err := txn.QueryRow("SELECT oid FROM pg_catalog.pg_event_trigger WHERE evtname=$1", signature).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("Error reading info about event trigger: %s", err)
	}

	return true, nil
}

var testAccPostgreSQLEventTriggerConfig = `
resource "postgresql_function" "function" {
    name = "test_function"
	database = "%[1]s"
	schema = "%[2]s"

    returns = "event_trigger"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RAISE EXCEPTION 'command %% is disabled', tg_tag;
        END;
    EOF
}

resource "postgresql_event_trigger" "event_trigger" {
  name = "%[3]s"
  database = "%[1]s"
  function = postgresql_function.function.name
  function_schema = postgresql_function.function.schema
  on = "ddl_command_end"
  owner = "%[4]s"
  status = "%[5]s"

  filter {
    variable = "TAG"
    values = [
      "CREATE TABLE",
      "ALTER TABLE",
    ]
  }
}
`
