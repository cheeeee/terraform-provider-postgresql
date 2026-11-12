package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlExtension_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			// TODO: Need to check how RDS manage to allow `rds_superuser` to create extension
			// even it's not a real superuser
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.myextension", "name", "pg_trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.myextension", "schema", "public"),

					// NOTE(sean): The version number drifts.  PG 9.6 ships with pg_trgm
					// version 1.3 and PG 9.2 ships with pg_trgm 1.0.
					resource.TestCheckResourceAttrSet(
						"postgresql_extension.myextension", "version"),
				),
			},
			{
				ResourceName:            "postgresql_extension.myextension",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{extCreateCascadeAttr, extDropCascadeAttr},
			},
		},
	})
}

func testAccCheckPostgresqlExtensionDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_extension" {
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

		exists, err := checkExtensionExists(txn, getExtensionNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking extension %s", err)
		}

		if exists {
			return fmt.Errorf("Extension still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlExtensionExists(n string) resource.TestCheckFunc {
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
			return fmt.Errorf("No Attribute for extension name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkExtensionExists(txn, extName)

		if err != nil {
			return fmt.Errorf("error checking extension %s", err)
		}

		if !exists {
			return fmt.Errorf("Extension not found")
		}

		return nil
	}
}

// testAccCheckExtensionCatalog checks in the catalog of the database of the resource that the
// extension is in the expected schema and, when version is not empty, at the expected version.
func testAccCheckExtensionCatalog(n, schema, version string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, rs.Primary.Attributes[extDatabaseAttr])
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var extSchema, extVersion string
		err = txn.QueryRow(
			`SELECT n.nspname, e.extversion FROM pg_catalog.pg_extension e, pg_catalog.pg_namespace n `+
				`WHERE n.oid = e.extnamespace AND e.extname = $1`,
			rs.Primary.Attributes[extNameAttr],
		).Scan(&extSchema, &extVersion)
		if err != nil {
			return fmt.Errorf("error reading the catalog of the extension: %w", err)
		}

		if extSchema != schema {
			return fmt.Errorf("extension is in schema %q, expected %q", extSchema, schema)
		}
		if version != "" && extVersion != version {
			return fmt.Errorf("extension is at version %q, expected %q", extVersion, version)
		}

		return nil
	}
}

// testAccCheckExtensionFunctionsSchema checks that the functions which belong to the extension
// are all in the expected schema (and that there is at least one).
func testAccCheckExtensionFunctionsSchema(n, schema string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, rs.Primary.Attributes[extDatabaseAttr])
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		rows, err := txn.Query(`SELECT n.nspname, count(*) `+
			`FROM pg_catalog.pg_depend d `+
			`JOIN pg_catalog.pg_proc p ON p.oid = d.objid `+
			`JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace `+
			`JOIN pg_catalog.pg_extension e ON e.oid = d.refobjid `+
			`WHERE d.classid = 'pg_catalog.pg_proc'::regclass `+
			`AND d.refclassid = 'pg_catalog.pg_extension'::regclass `+
			`AND d.deptype = 'e' AND e.extname = $1 GROUP BY n.nspname`,
			rs.Primary.Attributes[extNameAttr],
		)
		if err != nil {
			return fmt.Errorf("error reading the functions of the extension: %w", err)
		}
		defer rows.Close()

		var total int
		for rows.Next() {
			var nspname string
			var count int
			if err := rows.Scan(&nspname, &count); err != nil {
				return err
			}
			if nspname != schema {
				return fmt.Errorf("%d functions of the extension are in schema %q, expected %q", count, nspname, schema)
			}
			total += count
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if total == 0 {
			return fmt.Errorf("no function of the extension found")
		}

		return nil
	}
}

// testAccCheckExtensionAbsent checks that the extension is not installed in the database.
func testAccCheckExtensionAbsent(database, extName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkExtensionExists(txn, extName)
		if err != nil {
			return fmt.Errorf("error checking extension %s", err)
		}
		if exists {
			return fmt.Errorf("Extension %s still exists in database %s", extName, database)
		}

		return nil
	}
}

// testAccExtensionOldAndDefaultVersion returns an installable version of the extension which is
// older than its default version, and the default version. It skips the test if there is none.
func testAccExtensionOldAndDefaultVersion(t *testing.T, extName string) (string, string) {
	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open connection pool: %v", err)
	}
	defer db.Close()

	var oldVersion, defaultVersion string
	err = db.QueryRow(
		`SELECT v.version, e.default_version `+
			`FROM pg_catalog.pg_available_extensions e `+
			`JOIN pg_catalog.pg_available_extension_versions v ON v.name = e.name AND v.version <> e.default_version `+
			`WHERE e.name = $1 ORDER BY v.version LIMIT 1`,
		extName,
	).Scan(&oldVersion, &defaultVersion)
	switch {
	case err == sql.ErrNoRows:
		t.Skipf("no older version of the extension %s is available", extName)
	case err != nil:
		t.Fatalf("could not read the versions of the extension %s: %v", extName, err)
	}

	return oldVersion, defaultVersion
}

func testAccPostgresqlExtensionVersionConfig(version string) string {
	versionLine := ""
	if version != "" {
		versionLine = fmt.Sprintf("  version = %q\n", version)
	}

	return fmt.Sprintf(`
resource "postgresql_extension" "myextension" {
  name = "pg_trgm"
%s}
`, versionLine)
}

func TestAccPostgresqlExtension_Version(t *testing.T) {
	skipIfNotAcc(t)
	oldVersion, defaultVersion := testAccExtensionOldAndDefaultVersion(t, "pg_trgm")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				// version at creation
				Config: testAccPostgresqlExtensionVersionConfig(oldVersion),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", oldVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", oldVersion),
				),
			},
			{
				ResourceName:            "postgresql_extension.myextension",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{extCreateCascadeAttr, extDropCascadeAttr},
			},
			{
				// ALTER EXTENSION ... UPDATE TO when the version changes
				Config: testAccPostgresqlExtensionVersionConfig(defaultVersion),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", defaultVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", defaultVersion),
				),
			},
			{
				// version removed from the configuration: the installed version is kept, no change
				Config: testAccPostgresqlExtensionVersionConfig(""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", defaultVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", defaultVersion),
				),
			},
		},
	})
}

// The extension is installed outside of Terraform at a version which is not the default one:
// without version in the configuration, the installed version is adopted and nothing changes.
func TestAccPostgresqlExtension_InstalledVersionNotDefault(t *testing.T) {
	skipIfNotAcc(t)
	oldVersion, defaultVersion := testAccExtensionOldAndDefaultVersion(t, "pg_trgm")
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), fmt.Sprintf(
						"DROP EXTENSION IF EXISTS pg_trgm; CREATE EXTENSION pg_trgm VERSION '%s'", oldVersion))
				},
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", oldVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", oldVersion),
				),
			},
			{
				Config: testAccPostgresqlExtensionVersionConfig(defaultVersion),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", defaultVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", defaultVersion),
				),
			},
		},
	})
}

// The version is changed outside of Terraform: the plan must show it.
func TestAccPostgresqlExtension_VersionChangedOutside(t *testing.T) {
	skipIfNotAcc(t)
	oldVersion, defaultVersion := testAccExtensionOldAndDefaultVersion(t, "pg_trgm")
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionVersionConfig(oldVersion),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", oldVersion),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), fmt.Sprintf(
						"ALTER EXTENSION pg_trgm UPDATE TO '%s'", defaultVersion))
				},
				Config:             testAccPostgresqlExtensionVersionConfig(oldVersion),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccPostgresqlExtensionVersionConfig(defaultVersion),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "version", defaultVersion),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "public", defaultVersion),
				),
			},
		},
	})
}

func TestAccPostgresqlExtension_SchemaRename(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionSchemaChange1,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.ext1trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_schema.ext1foo", "name", "foo"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.ext1trgm", "name", "pg_trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.ext1trgm", "name", "pg_trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.ext1trgm", "schema", "foo"),
				),
			},
			{
				Config: testAccPostgresqlExtensionSchemaChange2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.ext1trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_schema.ext1foo", "name", "bar"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.ext1trgm", "name", "pg_trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.ext1trgm", "schema", "bar"),
				),
			},
		},
	})
}

func testAccPostgresqlExtensionSetSchemaConfig(extSchemaResource string) string {
	schemaLine := ""
	if extSchemaResource != "" {
		schemaLine = fmt.Sprintf("  schema = postgresql_schema.%s.name\n", extSchemaResource)
	}

	return fmt.Sprintf(`
resource "postgresql_schema" "foo" {
  name = "foo"
}

resource "postgresql_schema" "bar" {
  name = "bar"
}

resource "postgresql_extension" "myextension" {
  name = "pg_trgm"
%s}
`, schemaLine)
}

// The schema changes: SET SCHEMA must move the extension and all its objects.
func TestAccPostgresqlExtension_SetSchema(t *testing.T) {
	skipIfNotAcc(t)
	// PostgreSQL reserves ALTER EXTENSION ... SET SCHEMA to the owners of the member objects:
	// the objects of a trusted extension created by a non-superuser are owned by the bootstrap superuser
	skipIfNotSuperuser(t)
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionSetSchemaConfig("foo"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "foo", ""),
					testAccCheckExtensionFunctionsSchema("postgresql_extension.myextension", "foo"),
				),
			},
			{
				Config: testAccPostgresqlExtensionSetSchemaConfig("bar"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "schema", "bar"),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "bar", ""),
					testAccCheckExtensionFunctionsSchema("postgresql_extension.myextension", "bar"),
				),
			},
			{
				// moved outside of Terraform: the plan must show it, and the apply moves it back
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), "ALTER EXTENSION pg_trgm SET SCHEMA foo")
				},
				Config:             testAccPostgresqlExtensionSetSchemaConfig("bar"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccPostgresqlExtensionSetSchemaConfig("bar"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "bar", ""),
					testAccCheckExtensionFunctionsSchema("postgresql_extension.myextension", "bar"),
				),
			},
			{
				// schema removed from the configuration: the current schema is kept, no change
				Config: testAccPostgresqlExtensionSetSchemaConfig(""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "schema", "bar"),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "bar", ""),
					testAccCheckExtensionFunctionsSchema("postgresql_extension.myextension", "bar"),
				),
			},
			{
				ResourceName:            "postgresql_extension.myextension",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{extCreateCascadeAttr, extDropCascadeAttr},
			},
		},
	})
}

func TestAccPostgresqlExtension_SchemaChangedOutside(t *testing.T) {
	skipIfNotAcc(t)
	// PostgreSQL reserves ALTER EXTENSION ... SET SCHEMA to the owners of the member objects:
	// the objects of a trusted extension created by a non-superuser are owned by the bootstrap superuser
	skipIfNotSuperuser(t)
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionSetSchemaConfig("foo"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "foo", ""),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), "ALTER EXTENSION pg_trgm SET SCHEMA bar")
				},
				Config:             testAccPostgresqlExtensionSetSchemaConfig("foo"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccPostgresqlExtensionSetSchemaConfig("foo"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "schema", "foo"),
					testAccCheckExtensionCatalog("postgresql_extension.myextension", "foo", ""),
					testAccCheckExtensionFunctionsSchema("postgresql_extension.myextension", "foo"),
				),
			},
		},
	})
}

func TestAccPostgresqlExtension_DroppedOutside(t *testing.T) {
	skipIfNotAcc(t)
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), "DROP EXTENSION pg_trgm")
				},
				Config:             testAccPostgresqlExtensionConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
				),
			},
		},
	})
}

func checkExtensionExists(txn *sql.Tx, extensionName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE from pg_catalog.pg_extension d WHERE extname=$1", extensionName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about extension: %s", err)
	}

	return true, nil
}

func TestAccPostgresqlExtension_Database(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlExtensionDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_extension" "myextension" {
		name     = "pg_trgm"
		database = "%s"
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.myextension", "name", "pg_trgm"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.myextension", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_extension.myextension", "database", dbName),
					resource.TestCheckResourceAttrSet(
						"postgresql_extension.myextension", "version"),
				),
			},
			{
				ResourceName:            "postgresql_extension.myextension",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{extCreateCascadeAttr, extDropCascadeAttr},
			},
		},
	})
}

// The database changes: the extension is replaced in the new database and removed from the old one.
func TestAccPostgresqlExtension_DatabaseChange(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix1, teardown1 := setupTestDatabase(t, true, true)
	defer teardown1()
	dbSuffix2, teardown2 := setupTestDatabase(t, true, true)
	defer teardown2()

	dbName1, _ := getTestDBNames(dbSuffix1)
	dbName2, _ := getTestDBNames(dbSuffix2)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pg_trgm")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionDatabaseConfigFor(dbName1),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "database", dbName1),
				),
			},
			{
				Config: testAccPostgresqlExtensionDatabaseConfigFor(dbName2),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.myextension"),
					resource.TestCheckResourceAttr("postgresql_extension.myextension", "database", dbName2),
					testAccCheckExtensionAbsent(dbName1, "pg_trgm"),
				),
			},
		},
	})
}

func testAccPostgresqlExtensionDatabaseConfigFor(dbName string) string {
	return fmt.Sprintf(`
	resource "postgresql_extension" "myextension" {
		name     = "pg_trgm"
		database = "%s"
	}
	`, dbName)
}

func TestAccPostgresqlExtension_DropCascade(t *testing.T) {
	skipIfNotAcc(t)

	var testAccPostgresqlExtensionConfig = `
resource "postgresql_extension" "cascade" {
  name = "pgcrypto"
  drop_cascade = true
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pgcrypto")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.cascade"),
					resource.TestCheckResourceAttr("postgresql_extension.cascade", "name", "pgcrypto"),
					// This will create a dependency on the extension.
					testAccCreateExtensionDependency("test_extension_cascade"),
				),
			},
		},
	})
}

// The dependency is a column default calling a function of pgcrypto: gen_random_uuid is
// in the core of PostgreSQL 13 and later, so it would not create a dependency there.
func testAccCreateExtensionDependency(tableName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {

		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		_, err = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s; CREATE TABLE %s (id bytea DEFAULT digest('x', 'sha256'))", tableName, tableName))
		if err != nil {
			return fmt.Errorf("could not create test table in schema: %s", err)
		}

		return nil
	}
}

// RESTRICT (the default) refuses to drop an extension which other objects depend on.
func TestAccPostgresqlExtension_DropRestrict(t *testing.T) {
	skipIfNotAcc(t)
	config := getTestConfig(t)

	var testAccPostgresqlExtensionConfig = `
resource "postgresql_extension" "restrict" {
  name = "pgcrypto"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "pgcrypto")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.restrict"),
					testAccCreateExtensionDependency("test_extension_restrict"),
				),
			},
			{
				Config:      testAccPostgresqlExtensionNoResource,
				ExpectError: regexp.MustCompile(`cannot drop extension pgcrypto because other objects depend on it`),
			},
			{
				// the dependent object is removed outside of Terraform: the extension can now be dropped
				PreConfig: func() {
					dbExecute(t, config.connStr("postgres"), "DROP TABLE test_extension_restrict")
				},
				Config: testAccPostgresqlExtensionNoResource,
				Check:  testAccCheckExtensionAbsent("postgres", "pgcrypto"),
			},
		},
	})
}

const testAccPostgresqlExtensionNoResource = "# no resource: the extension is removed\n"

func TestAccPostgresqlExtension_CreateCascade(t *testing.T) {
	skipIfNotAcc(t)

	var testAccPostgresqlExtensionConfig = `
resource "postgresql_extension" "cascade" {
  name = "earthdistance"
  create_cascade = true
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureExtension)
			testCheckCanCreateExtension(t, "earthdistance")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlExtensionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlExtensionConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlExtensionExists("postgresql_extension.cascade"),
					resource.TestCheckResourceAttr("postgresql_extension.cascade", "name", "earthdistance"),
					// The cube extension should be installed as a dependency of earthdistance.
					testAccCheckExtensionDependency("cube"),
				),
			},
		},
	})
}

func testAccCheckExtensionDependency(extName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, client.databaseName)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkExtensionExists(txn, extName)
		if err != nil {
			return fmt.Errorf("error checking extension %s", err)
		}
		if !exists {
			return fmt.Errorf("Extension %s not found", extName)
		}

		return nil
	}
}

var testAccPostgresqlExtensionConfig = `
resource "postgresql_extension" "myextension" {
  name = "pg_trgm"
}
`

var testAccPostgresqlExtensionSchemaChange1 = `
resource "postgresql_schema" "ext1foo" {
  name = "foo"
}

resource "postgresql_extension" "ext1trgm" {
  name = "pg_trgm"
  schema = "${postgresql_schema.ext1foo.name}"
}
`

var testAccPostgresqlExtensionSchemaChange2 = `
resource "postgresql_schema" "ext1foo" {
  name = "bar"
}

resource "postgresql_extension" "ext1trgm" {
  name = "pg_trgm"
  schema = "${postgresql_schema.ext1foo.name}"
}
`
