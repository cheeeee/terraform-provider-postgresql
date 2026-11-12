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

func testAccCheckPostgresqlPublicationDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_publication" {
			continue
		}

		database, ok := rs.Primary.Attributes[pubDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkPublicationExists(txn, getPublicationNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking publication %s", err)
		}

		if exists {
			return fmt.Errorf("Publication still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlPublicationExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		database, ok := rs.Primary.Attributes[pubDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}

		pubName, ok := rs.Primary.Attributes[pubNameAttr]
		if !ok {
			return fmt.Errorf("No Attribute for publication name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkPublicationExists(txn, pubName)

		if err != nil {
			return fmt.Errorf("error checking publication %s", err)
		}

		if !exists {
			return fmt.Errorf("Publication not found")
		}

		return nil
	}
}

func TestAccPostgresqlPublication_Database(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlPublicationDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_role" "test" {
		name = "test"
	}
	# The owner of a publication needs the CREATE privilege on the database
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = postgresql_role.test.name
		object_type = "database"
		privileges  = ["CREATE"]
	}
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = postgresql_grant.test.database
		owner = postgresql_role.test.name
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "owner", "test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_UpdateTables(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	testTables := []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlPublicationBaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
		tables = ["test_schema.test_table_1", "test_schema.test_table_2"]
	}
	`, dbName)

	testAccPostgresqlPublicationUpdateTablesConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
		tables = ["test_schema.test_table_1", "test_schema.test_table_3"]
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:  testAccPostgresqlPublicationBaseConfig,
				Destroy: false,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubAllTablesAttr, "false"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubTablesAttr), "test_schema.test_table_1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubTablesAttr), "test_schema.test_table_2"),
				),
			},
			{
				Config: testAccPostgresqlPublicationUpdateTablesConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubAllTablesAttr, "false"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubTablesAttr), "test_schema.test_table_1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubTablesAttr), "test_schema.test_table_3"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_UpdatePublishParams(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	testTables := []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlPublicationBaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
	}
	`, dbName)

	testAccPostgresqlPublicationUpdateParamsConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
		publish_param = ["update", "truncate"]
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCompatibleVersion(t, featurePubTruncate)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:  testAccPostgresqlPublicationBaseConfig,
				Destroy: false,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "4"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "insert"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubPublishAttr), "delete"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.3", pubPublishAttr), "truncate"),
				),
			},
			{
				Config: testAccPostgresqlPublicationUpdateParamsConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "2"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "truncate"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_UpdatePublishParamsWithoutTruncate(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	testTables := []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlPublicationBaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
	}
	`, dbName)

	testAccPostgresqlPublicationUpdateParamsConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
		publish_param = ["update"]
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCompatibleVersion(t, featurePubWithoutTruncate)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:  testAccPostgresqlPublicationBaseConfig,
				Destroy: false,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "insert"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubPublishAttr), "delete"),
				),
			},
			{
				Config: testAccPostgresqlPublicationUpdateParamsConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "update"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_UpdateOwner(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	testOwner := "test_owner"

	testAccPostgresqlPublicationBaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = "%s"
	}
	`, dbName)

	testAccPostgresqlPublicationUpdateOwnerConfig := fmt.Sprintf(`
	resource "postgresql_role" "test_owner_2" {
		name = "%s_2"
		login = true
	}
	# The owner of a publication needs the CREATE privilege on the database
	resource "postgresql_grant" "test_owner_2" {
		database    = "%s"
		role        = postgresql_role.test_owner_2.name
		object_type = "database"
		privileges  = ["CREATE"]
	}
	resource "postgresql_publication" "test" {
		name     = "publication"
		database = postgresql_grant.test_owner_2.database
		owner = "${postgresql_role.test_owner_2.name}"
	}
	`, testOwner, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:  testAccPostgresqlPublicationBaseConfig,
				Destroy: false,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "owner", getTestConfig(t).Username),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
			{
				Config: testAccPostgresqlPublicationUpdateOwnerConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_role.test_owner_2", "name", fmt.Sprintf("%s_2", testOwner)),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "owner", fmt.Sprintf("%s_2", testOwner)),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_UpdateName(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlPublicationBaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "%s_publication_1"
		database = "%s"
	}
	`, dbName, dbName)

	testAccPostgresqlPublicationUpdateNameConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test" {
		name     = "%s_publication_2"
		database = "%s"
	}
	`, dbName, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,

		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationBaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", fmt.Sprintf("%s_publication_1", dbName)),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
			{
				Config: testAccPostgresqlPublicationUpdateNameConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", fmt.Sprintf("%s_publication_2", dbName)),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
			{
				Config: testAccPostgresqlPublicationBaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", fmt.Sprintf("%s_publication_1", dbName)),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "database", dbName),
				),
			},
		},
	})
}

func checkPublicationExists(txn *sql.Tx, pubName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE from pg_catalog.pg_publication WHERE pubname=$1", pubName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about publication: %s", err)
	}

	return true, nil
}

func TestAccPostgresqlPublication_Basic(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	testTables := []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, _ := getTestDBNames(dbSuffix)
	testAccPostgresqlPublicationBasicConfig := fmt.Sprintf(`
resource "postgresql_role" "test_owner" {
	name = "test_owner"
	login = true
}

resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	owner = "${postgresql_role.test_owner.name}"
	all_tables = true
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCompatibleVersion(t, featurePubTruncate)
			testCheckCanCreatePublication(t, true)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationBasicConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubAllTablesAttr, "true"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubOwnerAttr, "test_owner"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubTablesAttr), "3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubTablesAttr), "test_schema.test_table_1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubTablesAttr), "test_schema.test_table_2"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubTablesAttr), "test_schema.test_table_3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "4"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "insert"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubPublishAttr), "delete"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.3", pubPublishAttr), "truncate"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_BasicWithoutTruncate(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	testTables := []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, _ := getTestDBNames(dbSuffix)
	testAccPostgresqlPublicationBasicConfig := fmt.Sprintf(`
resource "postgresql_role" "test_owner" {
	name = "test_owner"
	login = true
}

resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	owner = "${postgresql_role.test_owner.name}"
	all_tables = true
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCompatibleVersion(t, featurePubWithoutTruncate)
			testCheckCanCreatePublication(t, true)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationBasicConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubAllTablesAttr, "true"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubOwnerAttr, "test_owner"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubTablesAttr), "3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubTablesAttr), "test_schema.test_table_1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubTablesAttr), "test_schema.test_table_2"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubTablesAttr), "test_schema.test_table_3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "3"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "insert"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.2", pubPublishAttr), "delete"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_ConflictTables(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	testAccPostgresqlPublicationBasicConfig := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	tables = ["test.table1","test.table2"]
	all_tables = true
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, true)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config:      testAccPostgresqlPublicationBasicConfig,
				ExpectError: regexp.MustCompile("Conflicting configuration arguments.*"),
			},
		},
	})
}

func TestAccPostgresqlPublication_CheckPublishViaRoot(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	testAccPostgresqlPublicationBasicConfig := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
}
`, dbName)

	testAccPostgresqlPublicationBasicUpdateKeysWithPartitionRoot := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	publish_param = ["update","delete"]
	publish_via_partition_root_param = true
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCompatibleVersion(t, featurePublishViaRoot)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationBasicConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubPublishViaPartitionRootAttr, "false"),
				),
			},
			{
				Config: testAccPostgresqlPublicationBasicUpdateKeysWithPartitionRoot,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "2"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "delete"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubPublishViaPartitionRootAttr, "true"),
				),
			},
		},
	})
}

func TestAccPostgresqlPublication_CheckPublishParams(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	testAccPostgresqlPublicationBasicConfig := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	publish_param = ["insert"]
}
`, dbName)
	testAccPostgresqlPublicationWrongKeys := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	publish_param = ["insert","wrong_param"]
}
`, dbName)

	testAccPostgresqlPublicationDuplicateKeys := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	publish_param = ["insert","insert"]
}
`, dbName)
	testAccPostgresqlPublicationBasicUpdateKeys := fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "publication"
	database = "%s"
	publish_param = ["update","delete"]
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlPublicationBasicConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "1"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "insert"),
				),
			},
			{
				Config: testAccPostgresqlPublicationBasicUpdateKeys,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlPublicationExists("postgresql_publication.test"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", "name", "publication"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", pubDatabaseAttr, dbName),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.#", pubPublishAttr), "2"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.0", pubPublishAttr), "update"),
					resource.TestCheckResourceAttr(
						"postgresql_publication.test", fmt.Sprintf("%s.1", pubPublishAttr), "delete"),
				),
			},
			{
				Config:      testAccPostgresqlPublicationWrongKeys,
				ExpectError: regexp.MustCompile("could not update publication tables: error getting publication parameters: invalid value of `publish_param`: wrong_param. Should be at least one of 'insert, update, delete, truncate'"),
			},
			{
				Config:      testAccPostgresqlPublicationDuplicateKeys,
				ExpectError: regexp.MustCompile("'insert' is duplicated for attribute `tables`")},
		},
	})
}

// testPubResource is the address of the publication of the configurations below.
const testPubResource = "postgresql_publication.test"

// testPubConfig returns the configuration of a publication named "pub" in dbName.
func testPubConfig(dbName, body string) string {
	return fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "pub"
	database = "%s"
	%s
}
`, dbName, body)
}

// testPubCatalog queries the catalog of the database of the publication, $1 is its
// name, and returns the first column of the first row.
func testPubCatalog(dbName, pubName, query string) (string, error) {
	client := testAccProvider.Meta().(*Client)
	txn, err := startTransaction(client, dbName)
	if err != nil {
		return "", err
	}
	defer deferredRollback(txn)

	var result sql.NullString
	if err := txn.QueryRow(query, pubName).Scan(&result); err != nil {
		return "", fmt.Errorf("query %s failed: %w", query, err)
	}
	return result.String, nil
}

// testAccCheckPublicationCatalog checks the first column of the first row of a
// query on the catalog of the database of the publication ($1 is its name).
func testAccCheckPublicationCatalog(dbName, pubName, query, want string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := testPubCatalog(dbName, pubName, query)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("publication %s: query %s returned %q, want %q", pubName, query, got, want)
		}
		return nil
	}
}

const (
	// the tables of the publication (the excluded tables of PostgreSQL 19 are not listed)
	testPubRelsQuery = `SELECT COALESCE(string_agg(n.nspname || '.' || c.relname, ',' ORDER BY n.nspname, c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE p.pubname = $1 AND to_jsonb(r) ->> 'prexcept' IS DISTINCT FROM 'true'`
	// the column list of a table: table(col,col);...
	testPubColumnsQuery = `SELECT COALESCE(string_agg(c.relname || '(' || COALESCE((SELECT string_agg(a.attname, ',' ORDER BY a.attnum)
			FROM pg_catalog.pg_attribute a WHERE a.attrelid = r.prrelid AND a.attnum = ANY(r.prattrs::int2[])), '') || ')', ';' ORDER BY c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		WHERE p.pubname = $1`
	// the row filter of a table: table=filter;...
	testPubFiltersQuery = `SELECT COALESCE(string_agg(c.relname || '=' || COALESCE(pg_catalog.pg_get_expr(r.prqual, r.prrelid), ''), ';' ORDER BY c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		WHERE p.pubname = $1`
	testPubSchemasQuery = `SELECT COALESCE(string_agg(n.nspname, ',' ORDER BY n.nspname), '')
		FROM pg_catalog.pg_publication_namespace pn
		JOIN pg_catalog.pg_publication p ON p.oid = pn.pnpubid
		JOIN pg_catalog.pg_namespace n ON n.oid = pn.pnnspid
		WHERE p.pubname = $1`
	// the tables which the subscribers get (pg_publication_tables)
	testPubTablesViewQuery = `SELECT COALESCE(string_agg(schemaname || '.' || tablename, ',' ORDER BY schemaname, tablename), '')
		FROM pg_catalog.pg_publication_tables WHERE pubname = $1`
	testPubOwnerQuery   = `SELECT r.rolname FROM pg_catalog.pg_publication p JOIN pg_catalog.pg_roles r ON r.oid = p.pubowner WHERE p.pubname = $1`
	testPubPublishQuery = `SELECT concat_ws(',', CASE WHEN pubinsert THEN 'insert' END, CASE WHEN pubupdate THEN 'update' END,
		CASE WHEN pubdelete THEN 'delete' END, CASE WHEN pubtruncate THEN 'truncate' END) FROM pg_catalog.pg_publication WHERE pubname = $1`
	testPubViaRootQuery  = `SELECT pubviaroot::text FROM pg_catalog.pg_publication WHERE pubname = $1`
	testPubGenColsQuery  = `SELECT pubgencols::text FROM pg_catalog.pg_publication WHERE pubname = $1`
	testPubAllTablesQ    = `SELECT puballtables::text FROM pg_catalog.pg_publication WHERE pubname = $1`
	testPubAllSequencesQ = `SELECT puballsequences::text FROM pg_catalog.pg_publication WHERE pubname = $1`
	testPubExceptQuery   = `SELECT COALESCE(string_agg(n.nspname || '.' || c.relname, ',' ORDER BY n.nspname, c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE p.pubname = $1 AND r.prexcept`
)

// testPubExec executes statements as the administrator in the database, as a change
// made outside of Terraform.
func testPubExec(t *testing.T, dbName string, statements ...string) {
	config := getTestConfig(t)
	for _, statement := range statements {
		dbExecute(t, config.connStr(dbName), statement)
	}
}

// testPubTestCase is a test case of a publication, which runs its checks on the catalog.
func testPubTestCase(t *testing.T, precheck func(), steps []resource.TestStep) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePublication)
			testCheckCanCreatePublication(t, false)
			if precheck != nil {
				precheck()
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlPublicationDestroy,
		Steps:        steps,
	})
}

// testPubImportStep imports the publication and checks that the state is the one of the previous step.
func testPubImportStep(ignore ...string) resource.TestStep {
	return resource.TestStep{
		ResourceName:            testPubResource,
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: append([]string{pubDropCascadeAttr}, ignore...),
	}
}

// testPubDriftStep checks that a change made outside of Terraform is seen in the plan.
func testPubDriftStep(t *testing.T, config, dbName string, statements ...string) resource.TestStep {
	return resource.TestStep{
		PreConfig:          func() { testPubExec(t, dbName, statements...) },
		Config:             config,
		PlanOnly:           true,
		ExpectNonEmptyPlan: true,
	}
}

func TestAccPostgresqlPublication_QuotedNames(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{`test_schema."Quoted-Table-1"`, `test_schema."Quoted-Table-2"`}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	roles := `
resource "postgresql_role" "owner1" {
	name  = "Quoted-Owner-1"
	login = true
}
resource "postgresql_role" "owner2" {
	name  = "Quoted-Owner-2"
	login = true
}
# The owner of a publication needs the CREATE privilege on the database
resource "postgresql_grant" "owner1" {
	database    = "` + dbName + `"
	role        = postgresql_role.owner1.name
	object_type = "database"
	privileges  = ["CREATE"]
}
resource "postgresql_grant" "owner2" {
	database    = "` + dbName + `"
	role        = postgresql_role.owner2.name
	object_type = "database"
	privileges  = ["CREATE"]
}
`
	config := func(name, owner, tables string) string {
		return roles + fmt.Sprintf(`
resource "postgresql_publication" "test" {
	name     = "%s"
	database = "%s"
	owner    = postgresql_role.%s.name
	tables   = [%s]
	depends_on = [postgresql_grant.owner1, postgresql_grant.owner2]
}
`, strings.ReplaceAll(name, `"`, `\"`), dbName, owner, tables)
	}
	step1 := config("Quoted-Pub", "owner1", `"test_schema.Quoted-Table-1"`)
	step2 := config("Quoted-Pub", "owner1", `"test_schema.Quoted-Table-1", "test_schema.Quoted-Table-2"`)
	step3 := config("Quoted Pub's \"name\"", "owner2", `"test_schema.Quoted-Table-2"`)

	testPubTestCase(t, nil, []resource.TestStep{
		{
			Config: step1,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPostgresqlPublicationExists(testPubResource),
				testAccCheckPublicationCatalog(dbName, "Quoted-Pub", testPubRelsQuery, "test_schema.Quoted-Table-1"),
				testAccCheckPublicationCatalog(dbName, "Quoted-Pub", testPubOwnerQuery, "Quoted-Owner-1"),
			),
		},
		testPubImportStep(),
		{
			Config: step2,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "Quoted-Pub", testPubRelsQuery, "test_schema.Quoted-Table-1,test_schema.Quoted-Table-2"),
			),
		},
		{
			// the name, the owner and the tables change at once
			Config: step3,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPostgresqlPublicationExists(testPubResource),
				testAccCheckPublicationCatalog(dbName, `Quoted Pub's "name"`, testPubRelsQuery, "test_schema.Quoted-Table-2"),
				testAccCheckPublicationCatalog(dbName, `Quoted Pub's "name"`, testPubOwnerQuery, "Quoted-Owner-2"),
			),
		},
		testPubImportStep(),
	})
}
