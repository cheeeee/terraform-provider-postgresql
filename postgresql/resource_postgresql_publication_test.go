package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
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

// testPubInPlace adds to the checks of the steps the one that the publication is the same
// object as in the first step: a change which has to be done in place does not replace it.
func testPubInPlace(steps []resource.TestStep) []resource.TestStep {
	oid := ""
	for i := range steps {
		if steps[i].Check == nil {
			continue
		}
		check := steps[i].Check
		steps[i].Check = func(s *terraform.State) error {
			if err := check(s); err != nil {
				return err
			}
			rs, ok := s.RootModule().Resources[testPubResource]
			if !ok {
				return fmt.Errorf("Resource not found: %s", testPubResource)
			}
			got, err := testPubCatalog(
				rs.Primary.Attributes[pubDatabaseAttr], rs.Primary.Attributes[pubNameAttr],
				"SELECT oid::text FROM pg_catalog.pg_publication WHERE pubname = $1",
			)
			if err != nil {
				return err
			}
			if oid == "" {
				oid = got
			} else if oid != got {
				return fmt.Errorf("the publication has been replaced: its oid was %s and is %s", oid, got)
			}
			return nil
		}
	}
	return steps
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

	testPubTestCase(t, nil, testPubInPlace([]resource.TestStep{
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
	}))
}

// testCheckIncompatibleVersion skips the test if the feature exists: the tests of the
// refusal of a clause on the older versions.
func testCheckIncompatibleVersion(t *testing.T, feature featureName) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}
	if db.featureSupported(feature) {
		t.Skipf("Skip the test of the refusal of a clause for Postgres %s which has it", db.version)
	}
}

func TestAccPostgresqlPublication_Descendants(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubExec(t, dbName,
		"CREATE TABLE test_schema.par (id int, val text)",
		"CREATE TABLE test_schema.chi () INHERITS (test_schema.par)",
		"CREATE TABLE test_schema.grandchi () INHERITS (test_schema.chi)",
	)

	plain := testPubConfig(dbName, `tables = ["test_schema.par"]`)
	only := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
		only = true
	}`)
	star := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
	}`)
	explicit := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
		only = true
	}
	table {
		name = "test_schema.chi"
		only = true
	}`)

	testPubTestCase(t, nil, testPubInPlace([]resource.TestStep{
		{
			// the descendants are published (and not listed in `tables`)
			Config: plain,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPostgresqlPublicationExists(testPubResource),
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.grandchi,test_schema.par"),
				resource.TestCheckResourceAttr(testPubResource, "tables.#", "1"),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "0"),
			),
		},
		testPubImportStep(),
		{
			// ONLY, in place
			Config: only,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.par"),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "1"),
				resource.TestCheckResourceAttr(testPubResource, "tables.#", "1"),
			),
		},
		testPubImportStep(),
		{
			// the descendants (*) again
			Config: star,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.grandchi,test_schema.par"),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "1"),
			),
		},
		{
			// a table and a descendant, both with ONLY
			Config: explicit,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.par"),
		},
		// an imported descendant is not told from the ones which PostgreSQL adds with its parent
		testPubImportStep("table.", "tables."),
		{
			// a change made outside of Terraform: the descendants are removed
			Config: star,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.grandchi,test_schema.par"),
		},
		testPubDriftStep(t, star, dbName, "ALTER PUBLICATION pub DROP TABLE ONLY test_schema.grandchi"),
		{
			Config: star,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.grandchi,test_schema.par"),
		},
		// a new descendant is not published: it is a difference with `*`
		testPubDriftStep(t, plain, dbName, "CREATE TABLE test_schema.newchi () INHERITS (test_schema.par)"),
		{
			Config: plain,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.grandchi,test_schema.newchi,test_schema.par"),
		},
	}))
}

func TestAccPostgresqlPublication_Partitioned(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubExec(t, dbName,
		"CREATE TABLE test_schema.pt (id int) PARTITION BY RANGE (id)",
		"CREATE TABLE test_schema.pt1 PARTITION OF test_schema.pt FOR VALUES FROM (0) TO (10)",
	)

	plain := testPubConfig(dbName, `tables = ["test_schema.pt"]`)
	onlyTrue := testPubConfig(dbName, `
	table {
		name = "test_schema.pt"
		only = true
	}`)
	onlyFalse := testPubConfig(dbName, `
	table {
		name = "test_schema.pt"
	}`)

	// a partitioned table can be published since PostgreSQL 13
	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePublishViaRoot) }, testPubInPlace([]resource.TestStep{
		{
			// the partitioned table is in the publication, its partitions are not rows of it
			Config: plain,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.pt"),
				testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.pt1"),
			),
		},
		testPubImportStep(),
		{
			Config: onlyTrue,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.pt"),
		},
		{
			Config: onlyFalse,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.pt"),
		},
		{
			Config: plain,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.pt"),
		},
	}))
}

func TestAccPostgresqlPublication_PublishParams(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)

	var steps []resource.TestStep
	// each value alone, then combinations, every change in place
	for _, publish := range [][]string{
		{"insert"}, {"update"}, {"delete"}, {"truncate"},
		{"insert", "truncate"}, {"update", "delete"}, {"insert", "update", "delete", "truncate"}, {"insert", "delete"},
	} {
		quoted := `"` + strings.Join(publish, `", "`) + `"`
		want := []string{}
		for _, v := range []string{"insert", "update", "delete", "truncate"} {
			if sliceContainsStr(publish, v) {
				want = append(want, v)
			}
		}
		steps = append(steps, resource.TestStep{
			Config: testPubConfig(dbName, "publish_param = ["+quoted+"]"),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubPublishQuery, strings.Join(want, ",")),
				resource.TestCheckResourceAttr(testPubResource, "publish_param.#", fmt.Sprint(len(publish))),
			),
		})
	}
	last := testPubConfig(dbName, `publish_param = ["update", "truncate"]`)
	steps = append(steps,
		resource.TestStep{
			Config: last,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubPublishQuery, "update,truncate"),
		},
		testPubImportStep(),
		// a change made outside of Terraform
		testPubDriftStep(t, last, dbName, "ALTER PUBLICATION pub SET (publish = 'insert')"),
		resource.TestStep{
			Config: last,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubPublishQuery, "update,truncate"),
		},
		// an unknown operation is refused
		resource.TestStep{
			Config:      testPubConfig(dbName, `publish_param = ["insert", "merge"]`),
			ExpectError: regexp.MustCompile(`invalid value of .publish_param.: merge`),
		},
	)

	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePubTruncate) }, testPubInPlace(steps))
}

func TestAccPostgresqlPublication_ViaPartitionRoot(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubExec(t, dbName,
		"CREATE TABLE test_schema.pt (id int) PARTITION BY RANGE (id)",
		"CREATE TABLE test_schema.pt1 PARTITION OF test_schema.pt FOR VALUES FROM (0) TO (10)",
	)

	on := testPubConfig(dbName, `
	tables = ["test_schema.pt"]
	publish_via_partition_root_param = true`)
	off := testPubConfig(dbName, `
	tables = ["test_schema.pt"]
	publish_via_partition_root_param = false`)

	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePublishViaRoot) }, testPubInPlace([]resource.TestStep{
		{
			// at the creation
			Config: on,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubViaRootQuery, "true"),
				testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.pt"),
				resource.TestCheckResourceAttr(testPubResource, pubPublishViaPartitionRootAttr, "true"),
			),
		},
		testPubImportStep(),
		{
			Config: off,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubViaRootQuery, "false"),
				testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.pt1"),
			),
		},
		{
			Config: on,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubViaRootQuery, "true"),
		},
		testPubDriftStep(t, on, dbName, "ALTER PUBLICATION pub SET (publish_via_partition_root = false)"),
		{
			Config: on,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubViaRootQuery, "true"),
		},
	}))
}

func TestAccPostgresqlPublication_GeneratedColumns(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)

	stored := testPubConfig(dbName, `publish_generated_columns = "stored"`)
	none := testPubConfig(dbName, `publish_generated_columns = "none"`)

	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePubGeneratedColumns) }, testPubInPlace([]resource.TestStep{
		{
			Config: stored,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubGenColsQuery, "s"),
				resource.TestCheckResourceAttr(testPubResource, pubGeneratedColumnsAttr, "stored"),
			),
		},
		testPubImportStep(),
		{
			Config: none,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubGenColsQuery, "n"),
		},
		{
			Config: stored,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubGenColsQuery, "s"),
		},
		testPubDriftStep(t, stored, dbName, "ALTER PUBLICATION pub SET (publish_generated_columns = none)"),
		{
			Config: stored,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubGenColsQuery, "s"),
		},
		{
			// not set: the server's default
			Config: testPubConfig(dbName, ""),
			Check:  resource.TestCheckResourceAttr(testPubResource, pubGeneratedColumnsAttr, "stored"),
		},
	}))
}

func TestAccPostgresqlPublication_ColumnList(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1", "test_schema.test_table_2"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	columns1 := testPubConfig(dbName, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["val"]
	}
	table {
		name    = "test_schema.test_table_2"
		columns = ["val", "test_column_two"]
	}`)
	// a column list is a set, a table without list publishes all its columns
	columns2 := testPubConfig(dbName, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["test_column_one", "val"]
	}
	table {
		name = "test_schema.test_table_2"
	}`)
	plain := testPubConfig(dbName, `tables = ["test_schema.test_table_1", "test_schema.test_table_2"]`)

	attnames := `SELECT COALESCE(string_agg(tablename || '(' || array_to_string(attnames, ',') || ')', ';' ORDER BY tablename), '')
		FROM pg_catalog.pg_publication_tables WHERE pubname = $1`

	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePubColumnList) }, testPubInPlace([]resource.TestStep{
		{
			Config: columns1,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1(val);test_table_2(val,test_column_two)"),
				testAccCheckPublicationCatalog(dbName, "pub", attnames, "test_table_1(val);test_table_2(val,test_column_two)"),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "2"),
				resource.TestCheckResourceAttr(testPubResource, "tables.#", "2"),
			),
		},
		testPubImportStep(),
		{
			Config: columns2,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1(val,test_column_one);test_table_2()"),
				testAccCheckPublicationCatalog(dbName, "pub", attnames, "test_table_1(val,test_column_one);test_table_2(val,test_column_one,test_column_two)"),
			),
		},
		// a change made outside of Terraform
		testPubDriftStep(t, columns2, dbName, "ALTER PUBLICATION pub SET TABLE test_schema.test_table_1 (val), test_schema.test_table_2"),
		{
			Config: columns2,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1(val,test_column_one);test_table_2()"),
		},
		// no list anymore
		{
			Config: plain,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1();test_table_2()"),
		},
		testPubImportStep(),
		// a list made outside of Terraform for a table of `tables`
		testPubDriftStep(t, plain, dbName, "ALTER PUBLICATION pub SET TABLE test_schema.test_table_1 (val), test_schema.test_table_2"),
		{
			Config: plain,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1();test_table_2()"),
		},
		{
			Config: testPubConfig(dbName, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["nope"]
	}`),
			ExpectError: regexp.MustCompile(`nope`),
		},
		{
			Config: plain,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1();test_table_2()"),
		},
		{
			Config: testPubConfig(dbName, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["nope"]
	}`),
			ExpectError: regexp.MustCompile(`nope`),
		},
	}))
}

func TestAccPostgresqlPublication_RowFilter(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1", "test_schema.test_table_2"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	config := func(filter1, filter2 string) string {
		body := fmt.Sprintf(`
	table {
		name       = "test_schema.test_table_1"
		row_filter = "%s"
	}`, filter1)
		if filter2 != "" {
			body += fmt.Sprintf(`
	table {
		name       = "test_schema.test_table_2"
		columns    = ["val", "test_column_one"]
		row_filter = "%s"
	}`, filter2)
		}
		return testPubConfig(dbName, body)
	}
	written := config("test_column_one = 'x'", "")
	// the same expression written differently: PostgreSQL stores a normalized form
	rewritten := config("((test_column_one)::text = 'x'::text)", "")
	other := config("val <> 'y' AND test_column_two IS NOT NULL", "test_column_one IS NULL")
	// written as PostgreSQL stores it
	normalized := config("((val <> 'y'::text) AND (test_column_two IS NOT NULL))", "(test_column_one IS NULL)")
	removed := testPubConfig(dbName, `
	table {
		name = "test_schema.test_table_1"
	}
	table {
		name    = "test_schema.test_table_2"
		columns = ["val", "test_column_one"]
	}`)

	refiltered := testPubConfig(dbName, `
	table {
		name       = "test_schema.test_table_1"
		row_filter = "val = 'x'"
	}
	table {
		name    = "test_schema.test_table_2"
		columns = ["val", "test_column_one"]
	}`)

	testPubTestCase(t, func() { testCheckCompatibleVersion(t, featurePubRowFilter) }, testPubInPlace([]resource.TestStep{
		{
			// the plan after the apply is empty: the expression is kept as it is written
			Config: written,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_1=(test_column_one = 'x'::text)"),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "1"),
			),
		},
		{
			Config: rewritten,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_1=(test_column_one = 'x'::text)"),
		},
		{
			Config: other,
			Check: testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery,
				"test_table_1=((val <> 'y'::text) AND (test_column_two IS NOT NULL));test_table_2=(test_column_one IS NULL)"),
		},
		{
			Config: normalized,
			Check: testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery,
				"test_table_1=((val <> 'y'::text) AND (test_column_two IS NOT NULL));test_table_2=(test_column_one IS NULL)"),
		},
		// the imported filters are the stored ones: the same as the configuration
		testPubImportStep(),
		// a filter changed outside of Terraform
		testPubDriftStep(t, normalized, dbName, "ALTER PUBLICATION pub SET TABLE test_schema.test_table_1 WHERE (val = 'drift'), test_schema.test_table_2 (val, test_column_one) WHERE (test_column_one IS NULL)"),
		{
			Config: normalized,
			Check: testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery,
				"test_table_1=((val <> 'y'::text) AND (test_column_two IS NOT NULL));test_table_2=(test_column_one IS NULL)"),
		},
		{
			Config: removed,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_1=;test_table_2="),
				testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1();test_table_2(val,test_column_one)"),
			),
		},
		testPubDriftStep(t, removed, dbName, "ALTER PUBLICATION pub SET TABLE test_schema.test_table_1 WHERE (val = 'x'), test_schema.test_table_2 (val, test_column_one)"),
		{
			Config: removed,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_1=;test_table_2="),
		},
		{
			// a filter added to a table which has none
			Config: refiltered,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_1=(val = 'x'::text);test_table_2="),
		},
	}))
}

func TestAccPostgresqlPublication_Schemas(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1", "test_schema.test_table_2", "dev_schema.test_table_3"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	one := testPubConfig(dbName, `schemas = ["test_schema"]`)
	two := testPubConfig(dbName, `schemas = ["test_schema", "dev_schema"]`)
	combined := testPubConfig(dbName, `
	schemas = ["dev_schema"]
	tables  = ["test_schema.test_table_1"]`)
	// (a publication of a schema can not have a column list)
	blocks := testPubConfig(dbName, `
	schemas = ["dev_schema"]
	table {
		name       = "test_schema.test_table_2"
		row_filter = "val IS NOT NULL"
	}`)
	tablesOnly := testPubConfig(dbName, `tables = ["test_schema.test_table_1"]`)

	testPubTestCase(t,
		func() {
			testCheckCompatibleVersion(t, featurePubSchema)
			testCheckCanExecute(t, "create a publication for the tables of a schema", "CREATE PUBLICATION tf_probe_schema FOR TABLES IN SCHEMA public")
		},
		testPubInPlace([]resource.TestStep{
			{
				Config: one,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "test_schema"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, ""),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.test_table_1,test_schema.test_table_2"),
					resource.TestCheckResourceAttr(testPubResource, "schemas.#", "1"),
				),
			},
			testPubImportStep(),
			{
				// added in place
				Config: two,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "dev_schema,test_schema"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "dev_schema.test_table_3,test_schema.test_table_1,test_schema.test_table_2"),
				),
			},
			{
				// removed in place, with a table
				Config: combined,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "dev_schema"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.test_table_1"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "dev_schema.test_table_3,test_schema.test_table_1"),
					resource.TestCheckResourceAttr(testPubResource, "tables.#", "1"),
				),
			},
			testPubImportStep(),
			{
				Config: blocks,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "dev_schema"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubFiltersQuery, "test_table_2=(val IS NOT NULL)"),
				),
			},
			{
				Config: combined,
				Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.test_table_1"),
			},
			// a change made outside of Terraform
			testPubDriftStep(t, combined, dbName, "ALTER PUBLICATION pub ADD TABLES IN SCHEMA test_schema"),
			{
				Config: combined,
				Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "dev_schema"),
			},
			{
				// no schema anymore
				Config: tablesOnly,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, ""),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.test_table_1"),
				),
			},
			testPubDriftStep(t, tablesOnly, dbName, "ALTER PUBLICATION pub ADD TABLES IN SCHEMA dev_schema"),
			{
				Config: tablesOnly,
				Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, ""),
			},
		}))
}

func TestAccPostgresqlPublication_DropCascadeAndRemoval(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)

	config := testPubConfig(dbName, "drop_cascade = true")

	testPubTestCase(t, nil, []resource.TestStep{
		{
			Config: config,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPostgresqlPublicationExists(testPubResource),
				resource.TestCheckResourceAttr(testPubResource, pubDropCascadeAttr, "true"),
			),
		},
		{
			// dropped outside of Terraform: it is created again
			PreConfig:          func() { testPubExec(t, dbName, "DROP PUBLICATION pub") },
			Config:             config,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		{
			Config: config,
			Check:  testAccCheckPostgresqlPublicationExists(testPubResource),
		},
	})
}

func TestAccPostgresqlPublication_AllSequences(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	except1 := testPubConfig(dbName, `
	all_tables    = true
	except_tables = ["test_schema.test_table_1"]`)
	except2 := testPubConfig(dbName, `
	all_tables    = true
	except_tables = ["test_schema.test_table_1", "test_schema.test_table_2"]`)
	exceptSeq := testPubConfig(dbName, `
	all_tables    = true
	all_sequences = true
	except_tables = ["test_schema.test_table_1", "test_schema.test_table_2"]`)
	noExcept := testPubConfig(dbName, `
	all_tables    = true
	all_sequences = true`)
	seqOnly := testPubConfig(dbName, `
	all_tables    = false
	all_sequences = true`)

	testPubTestCase(t,
		func() {
			testCheckCompatibleVersion(t, featurePubAllSequences)
			testCheckCanCreatePublication(t, true)
		},
		[]resource.TestStep{
			{
				Config: except1,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllTablesQ, "true"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, "test_schema.test_table_1"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.test_table_2,test_schema.test_table_3"),
				),
			},
			testPubImportStep(),
			{
				// in place
				Config: except2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, "test_schema.test_table_1,test_schema.test_table_2"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, "test_schema.test_table_3"),
				),
			},
			testPubDriftStep(t, except2, dbName, "ALTER PUBLICATION pub SET ALL TABLES EXCEPT (TABLE test_schema.test_table_3)"),
			{
				Config: except2,
				Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, "test_schema.test_table_1,test_schema.test_table_2"),
			},
			{
				Config: exceptSeq,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllSequencesQ, "true"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, "test_schema.test_table_1,test_schema.test_table_2"),
				),
			},
			testPubImportStep(),
			{
				// the sequences are kept when the excluded tables change
				Config: noExcept,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllSequencesQ, "true"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllTablesQ, "true"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, ""),
				),
			},
			{
				Config: seqOnly,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllSequencesQ, "true"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubAllTablesQ, "false"),
					testAccCheckPublicationCatalog(dbName, "pub", testPubTablesViewQuery, ""),
				),
			},
			testPubImportStep(),
		})
}

func TestAccPostgresqlPublication_ConflictingAttributes(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)

	conflict := regexp.MustCompile(`(Conflicting configuration arguments|are required together|AtLeastOne|"[a-z_]+": all of)`)
	step := func(body string) resource.TestStep {
		return resource.TestStep{Config: testPubConfig(dbName, body), ExpectError: conflict, PlanOnly: true}
	}
	testPubTestCase(t, nil, []resource.TestStep{
		step(`
	tables = ["a.b"]
	table {
		name = "a.c"
	}`),
		step(`
	all_tables = true
	table {
		name = "a.c"
	}`),
		step(`
	all_tables = true
	schemas    = ["a"]`),
		step(`
	all_sequences = true
	tables        = ["a.b"]`),
		step(`
	all_sequences = true
	schemas       = ["a"]`),
		step(`except_tables = ["a.b"]`),
		{
			Config:      testPubConfig(dbName, `publish_generated_columns = "virtual"`),
			ExpectError: regexp.MustCompile(`expected publish_generated_columns to be one of`),
			PlanOnly:    true,
		},
		{
			// the values are case sensitive
			Config:      testPubConfig(dbName, `publish_generated_columns = "Stored"`),
			ExpectError: regexp.MustCompile(`expected publish_generated_columns to be one of`),
			PlanOnly:    true,
		},
	})
}

func TestAccPostgresqlPublication_UnsupportedClauses(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1"}, "")

	for name, c := range map[string]struct {
		feature featureName
		body    string
		message string
	}{
		"columns": {featurePubColumnList, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["val"]
	}`, `column list.*version 15`},
		"row_filter": {featurePubRowFilter, `
	table {
		name       = "test_schema.test_table_1"
		row_filter = "val = 'x'"
	}`, `row filter.*version 15`},
		"schemas":       {featurePubSchema, `schemas = ["test_schema"]`, `schemas.*version 15`},
		"generated":     {featurePubGeneratedColumns, `publish_generated_columns = "stored"`, `publish_generated_columns.*version 18`},
		"all_sequences": {featurePubAllSequences, `all_sequences = true`, `all_sequences.*version 19`},
		"except_tables": {featurePubAllSequences, `
	all_tables    = true
	except_tables = ["test_schema.test_table_1"]`, `except_tables.*version 19`},
	} {
		t.Run(name, func(t *testing.T) {
			testPubTestCase(t, func() { testCheckIncompatibleVersion(t, c.feature) }, []resource.TestStep{
				{Config: testPubConfig(dbName, c.body), ExpectError: regexp.MustCompile(c.message)},
			})
		})
	}

	// the refusal when the publication exists
	t.Run("schemas_update", func(t *testing.T) {
		testPubTestCase(t, func() { testCheckIncompatibleVersion(t, featurePubSchema) }, []resource.TestStep{
			{Config: testPubConfig(dbName, `tables = ["test_schema.test_table_1"]`)},
			{Config: testPubConfig(dbName, `schemas = ["test_schema"]`), ExpectError: regexp.MustCompile(`schemas.*version 15`)},
		})
	})
}

func TestPublicationSetToStringsNil(t *testing.T) {
	if got := publicationSetToStrings((*schema.Set)(nil)); got != nil {
		t.Fatalf("a nil set has no elements, got %v", got)
	}
	if got := publicationSetToStrings(nil); got != nil {
		t.Fatalf("no value has no elements, got %v", got)
	}
	if got := publicationSetToStrings(schema.NewSet(schema.HashString, []any{"a"})); len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
}

// the inheritance fixture of the tests of the descendants: par has the descendant chi
// which has the descendant grandchi, par1 has the only descendant chi1
func testPubInheritanceFixture(t *testing.T, dbName string) {
	testPubExec(t, dbName,
		"CREATE TABLE test_schema.par (id int, val text)",
		"CREATE TABLE test_schema.chi () INHERITS (test_schema.par)",
		"CREATE TABLE test_schema.grandchi () INHERITS (test_schema.chi)",
		"CREATE TABLE test_schema.par1 (id int)",
		"CREATE TABLE test_schema.chi1 () INHERITS (test_schema.par1)",
		"CREATE TABLE test_schema.other (id int)",
	)
}

// A table and one of its descendants are both in the configuration, one of them ONLY: the rows of the
// publication do not tell which one publishes the descendants of the descendant, the configuration does.
func TestAccPostgresqlPublication_NestedDescendants(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubInheritanceFixture(t, dbName)

	all := "test_schema.chi,test_schema.grandchi,test_schema.par"
	blocks := func(parOnly, chiOnly bool) string {
		return testPubConfig(dbName, fmt.Sprintf(`
	table {
		name = "test_schema.par"
		only = %t
	}
	table {
		name = "test_schema.chi"
		only = %t
	}`, parOnly, chiOnly))
	}
	other := testPubConfig(dbName, `
	table {
		name = "test_schema.other"
	}`)
	// the rows of pg_publication_rel: a table which is dropped and added again has a new row
	rows := `SELECT COALESCE(string_agg(c.relname || '=' || r.oid, ',' ORDER BY c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		WHERE p.pubname = $1`
	var kept string
	only := func(par, chi string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckTypeSetElemNestedAttrs(testPubResource, "table.*", map[string]string{"name": "test_schema.par", "only": par}),
			resource.TestCheckTypeSetElemNestedAttrs(testPubResource, "table.*", map[string]string{"name": "test_schema.chi", "only": chi}),
			resource.TestCheckResourceAttr(testPubResource, "table.#", "2"),
		)
	}

	testPubTestCase(t, nil, testPubInPlace([]resource.TestStep{
		{
			// grandchi is published with chi
			Config: blocks(true, false),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all),
				only("true", "false"),
				func(*terraform.State) (err error) {
					kept, err = testPubCatalog(dbName, "pub", rows)
					return err
				},
			),
		},
		{
			// grandchi is now published with par: no table is dropped
			Config: blocks(false, true),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all),
				only("false", "true"),
				func(*terraform.State) error {
					now, err := testPubCatalog(dbName, "pub", rows)
					if err != nil {
						return err
					}
					if now != kept {
						return fmt.Errorf("tables were dropped and added again: the rows were %s, they are %s", kept, now)
					}
					return nil
				},
			),
		},
		{
			Config: blocks(true, true),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.par"), only("true", "true"),
			),
		},
		{
			// a descendant added outside which no table of the configuration publishes
			PreConfig: func() {
				testPubExec(t, dbName, "ALTER PUBLICATION pub ADD TABLE ONLY test_schema.grandchi")
			},
			Config:             blocks(true, true),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		{
			Config: blocks(true, true),
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.par"), only("true", "true"),
			),
		},
		{
			// grandchi is a descendant of the two tables, it is added once
			Config: blocks(false, false),
			Check:  resource.ComposeTestCheckFunc(testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all), only("false", "false")),
		},
		{
			// a descendant dropped outside
			PreConfig: func() {
				testPubExec(t, dbName, "ALTER PUBLICATION pub DROP TABLE ONLY test_schema.grandchi")
			},
			Config:             blocks(false, false),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		{
			Config: blocks(false, false),
			Check:  resource.ComposeTestCheckFunc(testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all), only("false", "false")),
		},
		{
			Config: other,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.other"),
		},
		{
			// the two tables are added with their descendants at the same time
			Config: blocks(false, false),
			Check:  resource.ComposeTestCheckFunc(testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all), only("false", "false")),
		},
	}))
}

// a descendant asked for explicitly is a table of the configuration, not one which PostgreSQL adds with its parent
func TestAccPostgresqlPublication_ExplicitDescendants(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubInheritanceFixture(t, dbName)

	all := "test_schema.chi,test_schema.grandchi,test_schema.par"
	parOnly := testPubConfig(dbName, `tables = ["test_schema.par"]`)
	parAndChi := testPubConfig(dbName, `tables = ["test_schema.par", "test_schema.chi"]`)
	blocks := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
	}
	table {
		name = "test_schema.chi"
	}`)
	other := testPubConfig(dbName, `
	table {
		name = "test_schema.other"
	}`)
	bothOnly := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
		only = true
	}
	table {
		name = "test_schema.chi"
		only = true
	}`)
	parOnlyBlock := testPubConfig(dbName, `
	table {
		name = "test_schema.par"
		only = true
	}`)

	testPubTestCase(t, nil, testPubInPlace([]resource.TestStep{
		{
			Config: parOnly,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all),
				resource.TestCheckResourceAttr(testPubResource, "tables.#", "1"),
			),
		},
		{
			// chi is already in the publication, with the descendants of par: nothing to do, and it is listed
			Config: parAndChi,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all),
				resource.TestCheckResourceAttr(testPubResource, "tables.#", "2"),
			),
		},
		testPubImportStep("tables.", "table."),
		{
			// the same tables as blocks
			Config: blocks,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, all),
				resource.TestCheckResourceAttr(testPubResource, "table.#", "2"),
			),
		},
		{
			// grandchi belongs to par and to chi, it is dropped once
			Config: other,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.other"),
		},
		{
			Config: bothOnly,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi,test_schema.par"),
		},
		{
			// the descendant is removed from the configuration, the table is kept
			Config: parOnlyBlock,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.par"),
		},
	}))
}

// ONLY of a table which has exactly one descendant
func TestAccPostgresqlPublication_OneDescendant(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubInheritanceFixture(t, dbName)

	only := testPubConfig(dbName, `
	table {
		name = "test_schema.par1"
		only = true
	}`)
	star := testPubConfig(dbName, `
	table {
		name = "test_schema.par1"
	}`)

	testPubTestCase(t, nil, testPubInPlace([]resource.TestStep{
		{
			Config: only,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.par1"),
				resource.TestCheckResourceAttr(testPubResource, "table.0.only", "true"),
			),
		},
		// the imported table is ONLY: PostgreSQL has not added its descendant
		testPubImportStep(),
		{
			Config: star,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.chi1,test_schema.par1"),
		},
		testPubImportStep("table.", "tables."),
		{
			Config: only,
			Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "test_schema.par1"),
		},
	}))
}

// the tables which the configuration does not change are not dropped and added again
func TestAccPostgresqlPublication_UnchangedTablesAreKept(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1", "test_schema.test_table_2", "test_schema.test_table_3"}, "")
	dbName, _ := getTestDBNames(dbSuffix)
	testPubInheritanceFixture(t, dbName)

	blocks := `
	table {
		name = "test_schema.test_table_1"
		only = true
	}
	table {
		name       = "test_schema.test_table_2"
		columns    = ["val", "test_column_one"]
		row_filter = "test_column_one IS NOT NULL"
	}
	table {
		name = "test_schema.par"
	}
	table {
		name = "test_schema.par1"
		only = true
	}`
	before := testPubConfig(dbName, blocks)
	after := testPubConfig(dbName, blocks+`
	table {
		name = "test_schema.test_table_3"
	}`)

	// the rows of pg_publication_rel of the tables which are not changed
	oids := `SELECT COALESCE(string_agg(c.relname || '=' || r.oid, ',' ORDER BY c.relname), '')
		FROM pg_catalog.pg_publication_rel r
		JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid
		JOIN pg_catalog.pg_class c ON c.oid = r.prrelid
		WHERE p.pubname = $1 AND c.relname <> 'test_table_3'`
	var kept string
	testPubTestCase(t, func() {
		testCheckCompatibleVersion(t, featurePubColumnList)
		testCheckCompatibleVersion(t, featurePubRowFilter)
	}, []resource.TestStep{
		{
			Config: before,
			Check: func(_ *terraform.State) error {
				var err error
				kept, err = testPubCatalog(dbName, "pub", oids)
				return err
			},
		},
		{
			Config: after,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery,
					"test_schema.chi,test_schema.grandchi,test_schema.par,test_schema.par1,test_schema.test_table_1,test_schema.test_table_2,test_schema.test_table_3"),
				func(_ *terraform.State) error {
					got, err := testPubCatalog(dbName, "pub", oids)
					if err != nil {
						return err
					}
					if got != kept {
						return fmt.Errorf("the tables which did not change have been added again: %s, was %s", got, kept)
					}
					return nil
				},
			),
		},
	})
}

// the name of a table as it is written in the configuration, not the one with its schema
func TestAccPostgresqlPublication_UnqualifiedNames(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubExec(t, dbName, "CREATE TABLE public.unq (id int)", "CREATE TABLE public.unq2 (id int)")

	list := testPubConfig(dbName, `tables = ["unq"]`)
	block := testPubConfig(dbName, `
	table {
		name = "unq2"
	}`)

	testPubTestCase(t, nil, []resource.TestStep{
		{
			Config: list,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "public.unq"),
				resource.TestCheckResourceAttr(testPubResource, "tables.0", "unq"),
			),
		},
		{
			Config: block,
			Check: resource.ComposeTestCheckFunc(
				testAccCheckPublicationCatalog(dbName, "pub", testPubRelsQuery, "public.unq2"),
				resource.TestCheckResourceAttr(testPubResource, "table.0.name", "unq2"),
			),
		},
	})
}

// the tables are handled in the order of their names: the first one which does not exist is the one of the error
func TestAccPostgresqlPublication_MissingTables(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	list := func(names ...string) string {
		return testPubConfig(dbName, `tables = ["`+strings.Join(names, `", "`)+`"]`)
	}
	blocks := func(names ...string) string {
		body := ""
		for _, n := range names {
			body += `
	table {
		name = "` + n + `"
	}`
		}
		return testPubConfig(dbName, body)
	}
	dup := testPubConfig(dbName, `
	table {
		name = "test_schema.test_table_1"
	}
	table {
		name = "test_schema.test_table_1"
		only = true
	}`)

	testPubTestCase(t, nil, []resource.TestStep{
		{Config: list("test_schema.zz_missing", "test_schema.aa_missing"), ExpectError: regexp.MustCompile(`aa_missing`)},
		{Config: blocks("test_schema.zz_missing", "test_schema.aa_missing"), ExpectError: regexp.MustCompile(`aa_missing`)},
		{Config: dup, ExpectError: regexp.MustCompile(`'test_schema.test_table_1' is duplicated for attribute .table.`)},
		{Config: list("test_schema.test_table_1")},
		// the same checks when the publication exists
		{Config: list("test_schema.zz_missing", "test_schema.aa_missing"), ExpectError: regexp.MustCompile(`aa_missing`)},
		{Config: blocks("test_schema.zz_missing", "test_schema.aa_missing"), ExpectError: regexp.MustCompile(`aa_missing`)},
		{Config: dup, ExpectError: regexp.MustCompile(`'test_schema.test_table_1' is duplicated for attribute .table.`)},
	})
}

// the excluded tables of ALL TABLES EXCEPT: a descendant which is asked for is listed as it is written
func TestAccPostgresqlPublication_ExceptDescendants(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	dbName, _ := getTestDBNames(dbSuffix)
	testPubInheritanceFixture(t, dbName)

	config := testPubConfig(dbName, `
	all_tables    = true
	except_tables = ["test_schema.par", "test_schema.chi"]`)

	testPubTestCase(t,
		func() {
			testCheckCompatibleVersion(t, featurePubAllSequences)
			testCheckCanCreatePublication(t, true)
		},
		[]resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubExceptQuery, "test_schema.chi,test_schema.grandchi,test_schema.par"),
					resource.TestCheckResourceAttr(testPubResource, "except_tables.#", "2"),
				),
			},
		})
}

// the schema is dropped before a table with a column list of this schema is added
// (a publication of a schema can not have a column list)
func TestAccPostgresqlPublication_SchemaReplacedByColumnList(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()
	createTestTables(t, dbSuffix, []string{"test_schema.test_table_1"}, "")
	dbName, _ := getTestDBNames(dbSuffix)

	schema := testPubConfig(dbName, `schemas = ["test_schema"]`)
	columns := testPubConfig(dbName, `
	table {
		name    = "test_schema.test_table_1"
		columns = ["val"]
	}`)

	testPubTestCase(t,
		func() {
			testCheckCompatibleVersion(t, featurePubSchema)
			testCheckCanExecute(t, "create a publication for the tables of a schema", "CREATE PUBLICATION tf_probe_schema FOR TABLES IN SCHEMA public")
		},
		testPubInPlace([]resource.TestStep{
			{
				Config: schema,
				Check:  testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, "test_schema"),
			},
			{
				Config: columns,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPublicationCatalog(dbName, "pub", testPubSchemasQuery, ""),
					testAccCheckPublicationCatalog(dbName, "pub", testPubColumnsQuery, "test_table_1(val)"),
				),
			},
		}))
}
