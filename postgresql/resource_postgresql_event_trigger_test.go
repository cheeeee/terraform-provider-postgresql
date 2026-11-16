package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
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

func TestAccPostgresqlEventTrigger_Events(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	// The event is ForceNew: each step replaces the trigger.
	var steps []resource.TestStep
	for _, on := range []string{"ddl_command_start", "sql_drop", "table_rewrite"} {
		steps = append(steps, resource.TestStep{
			Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", on, owner, "enable", "", ""),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPostgresqlEventTriggerExists("postgresql_event_trigger.event_trigger", dbName),
				resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "on", on),
				testAccCheckEventTriggerValue(dbName, "test_event", "evtevent", on),
			),
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps:        steps,
	})
}

func TestAccPostgresqlEventTrigger_Status(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	// Each status is checked in pg_event_trigger.evtenabled.
	statuses := []struct {
		status    string
		evtenable string
	}{
		{"enable_replica", "R"},
		{"enable_always", "A"},
		{"disable", "D"},
		{"enable", "O"},
	}

	var steps []resource.TestStep
	for _, s := range statuses {
		steps = append(steps, resource.TestStep{
			Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", owner, s.status, "", ""),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "status", s.status),
				testAccCheckEventTriggerStatus(dbName, "test_event", s.evtenable),
			),
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps:        steps,
	})
}

func TestAccPostgresqlEventTrigger_Filter(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			// Without filter, the trigger has no command tag.
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", owner, "enable", "", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "filter.#", "0"),
					testAccCheckEventTriggerValue(dbName, "test_event", "array_to_string(evttags, ',')", ""),
				),
			},
			// With several values, all the values are stored in the trigger.
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", owner, "enable",
					testAccEventTriggerFilter("DROP TABLE", "CREATE TABLE", "ALTER TABLE"), ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "filter.0.values.#", "3"),
					testAccCheckEventTriggerValue(dbName, "test_event", "array_to_string(evttags, ',')", "DROP TABLE,CREATE TABLE,ALTER TABLE"),
				),
			},
			{
				ResourceName:      "postgresql_event_trigger.event_trigger",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccPostgresqlEventTrigger_Owner(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	// The owner of an event trigger must be a superuser.
	newOwner := fmt.Sprintf("tf_event_trigger_owner_%s", dbSuffix)
	defer dbExecute(t, config.connStr("postgres"), fmt.Sprintf("DROP ROLE IF EXISTS %s", newOwner))

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
			testCheckCanExecute(t, "create a superuser role", "CREATE ROLE tf_probe_superuser SUPERUSER")
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("CREATE ROLE %s SUPERUSER NOLOGIN", newOwner))
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", owner, "enable", "", ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckEventTriggerValue(dbName, "test_event", "pg_get_userbyid(evtowner)", owner),
				),
			},
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", newOwner, "enable", "", ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "owner", newOwner),
					testAccCheckEventTriggerValue(dbName, "test_event", "pg_get_userbyid(evtowner)", newOwner),
				),
			},
			{
				ResourceName:      "postgresql_event_trigger.event_trigger",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// The owner is changed outside of Terraform.
			{
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER EVENT TRIGGER test_event OWNER TO %s", owner))
				},
				Config:             fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", newOwner, "enable", "", ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", newOwner, "enable", "", ""),
				Check:  testAccCheckEventTriggerValue(dbName, "test_event", "pg_get_userbyid(evtowner)", newOwner),
			},
		},
	})
}

func TestAccPostgresqlEventTrigger_Fires(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateEventTrigger(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			// The function refuses CREATE TABLE while the trigger is enabled...
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, "test_schema", "test_event", owner, "enable"),
				Check:  testAccCheckEventTriggerFires(t, dbName, true),
			},
			// ...and lets it run once the trigger is disabled.
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerConfig, dbName, "test_schema", "test_event", owner, "disable"),
				Check:  testAccCheckEventTriggerFires(t, dbName, false),
			},
		},
	})
}

func TestAccPostgresqlEventTrigger_Login(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	// The function does nothing: a login trigger which raises an error would
	// prevent every connection to the database.
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureEventTriggerLogin)
			testCheckCanExecute(
				t, "create a login event trigger",
				"CREATE FUNCTION tf_probe_login() RETURNS event_trigger LANGUAGE plpgsql AS 'BEGIN END'",
				"CREATE EVENT TRIGGER tf_probe_login ON login EXECUTE FUNCTION tf_probe_login()",
			)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_login", "login", owner, "enable", "", ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlEventTriggerExists("postgresql_event_trigger.event_trigger", dbName),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "on", "login"),
					testAccCheckEventTriggerValue(dbName, "test_login", "evtevent", "login"),
					testAccCheckEventTriggerStatus(dbName, "test_login", "O"),
				),
			},
			{
				ResourceName:      "postgresql_event_trigger.event_trigger",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// The login event is refused with a clear error on PostgreSQL versions which do not support it.
func TestAccPostgresqlEventTrigger_LoginUnsupported(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			client := testAccProvider.Meta().(*Client)
			db, err := client.Connect()
			if err != nil {
				t.Fatalf("could not connect to database: %v", err)
			}
			if db.featureSupported(featureEventTriggerLogin) {
				t.Skip("Skip test: the login event is supported by this Postgres version")
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlEventTriggerDestroy,
		Steps: []resource.TestStep{
			{
				Config:      fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_login", "login", owner, "enable", "", ""),
				ExpectError: regexp.MustCompile(`event trigger on the login event is not supported for this Postgres version`),
			},
		},
	})
}

func TestAccPostgresqlEventTrigger_DropCascade(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	owner := config.Username

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

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
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, dbName, "test_schema", "test_event", "ddl_command_end", owner, "enable", "",
					"drop_cascade = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlEventTriggerExists("postgresql_event_trigger.event_trigger", dbName),
					resource.TestCheckResourceAttr("postgresql_event_trigger.event_trigger", "drop_cascade", "true"),
				),
			},
			// drop_cascade is not stored in PostgreSQL, so it cannot be imported.
			{
				ResourceName:            "postgresql_event_trigger.event_trigger",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
		},
	})
}

// testAccCheckEventTriggerValue checks the value of an expression computed from the
// catalog for an event trigger, such as evtevent or array_to_string(evttags, ',').
func testAccCheckEventTriggerValue(database, name, expression, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var value string
		query := fmt.Sprintf("SELECT COALESCE((%s)::text, '') FROM pg_catalog.pg_event_trigger WHERE evtname = $1", expression)
		if err := txn.QueryRow(query, name).Scan(&value); err != nil {
			return fmt.Errorf("could not read %s of the event trigger %s: %w", expression, name, err)
		}
		if value != expected {
			return fmt.Errorf("%s of the event trigger %s: expected %q, got %q", expression, name, expected, value)
		}
		return nil
	}
}

// testAccCheckEventTriggerFires checks that CREATE TABLE is refused by the event trigger
// when fired is true, and that it runs when fired is false.
func testAccCheckEventTriggerFires(t *testing.T, database string, fired bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := getTestConfig(t)
		db, err := sql.Open("postgres", config.connStr(database))
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		_, err = db.Exec("CREATE TABLE test_event_trigger_fires (id int)")
		if !fired {
			if err != nil {
				return fmt.Errorf("CREATE TABLE should not be refused: %w", err)
			}
			_, err = db.Exec("DROP TABLE test_event_trigger_fires")
			return err
		}
		if err == nil {
			return fmt.Errorf("CREATE TABLE should have been refused by the event trigger")
		}
		if !strings.Contains(err.Error(), "is disabled") {
			return fmt.Errorf("CREATE TABLE refused for an unexpected reason: %w", err)
		}
		return nil
	}
}

// testAccEventTriggerFilter returns a filter block on the TAG variable with the given values.
func testAccEventTriggerFilter(values ...string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}

	return fmt.Sprintf("filter {\n    variable = \"TAG\"\n    values = [%s]\n  }", strings.Join(quoted, ", "))
}

// testAccPostgreSQLEventTriggerGenericConfig has a function which does nothing. Arguments:
// database, function schema, name, on, owner, status, filter block, extra attributes.
var testAccPostgreSQLEventTriggerGenericConfig = `
resource "postgresql_function" "function" {
  name = "test_function"
  database = "%[1]s"
  schema = "%[2]s"

  returns = "event_trigger"
  language = "plpgsql"
  body = <<-EOF
      BEGIN
      END;
  EOF
}

resource "postgresql_event_trigger" "event_trigger" {
  name = "%[3]s"
  database = "%[1]s"
  function = postgresql_function.function.name
  function_schema = postgresql_function.function.schema
  on = "%[4]s"
  owner = "%[5]s"
  status = "%[6]s"
  %[8]s

  %[7]s
}
`

// PostgreSQL refuses a filter variable specified more than once, so only one filter block is allowed.
func TestAccPostgresqlEventTrigger_FilterMultiple(t *testing.T) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccPostgreSQLEventTriggerGenericConfig, "postgres", "public", "test_event", "ddl_command_end", "postgres", "enable",
					testAccEventTriggerFilter("CREATE TABLE")+"\n"+testAccEventTriggerFilter("ALTER TABLE"), ""),
				ExpectError: regexp.MustCompile(`Too many filter blocks`),
			},
		},
	})
}
