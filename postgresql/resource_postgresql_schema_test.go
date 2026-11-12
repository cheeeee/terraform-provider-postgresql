package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlSchema_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test1", "foo"),
					resource.TestCheckResourceAttr("postgresql_role.role_all_without_grant", "name", "role_all_without_grant"),
					resource.TestCheckResourceAttr("postgresql_role.role_all_without_grant", "login", "true"),

					resource.TestCheckResourceAttr("postgresql_role.role_all_with_grant", "name", "role_all_with_grant"),

					resource.TestCheckResourceAttr("postgresql_schema.test1", "name", "foo"),

					resource.TestCheckResourceAttr("postgresql_schema.test2", "name", "bar"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "owner", "role_all_without_grant"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "if_not_exists", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.#", "1"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.0.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.0.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.0.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.0.usage_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test2", "policy.0.role", "role_all_without_grant"),

					resource.TestCheckResourceAttr("postgresql_schema.test3", "name", "baz"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "owner", "role_all_without_grant"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "if_not_exists", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.#", "2"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.0.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.0.usage_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.0.role", "role_all_with_grant"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.1.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.1.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test3", "policy.1.role", "role_all_without_grant"),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_AddPolicy(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			// TODO: Need to check if removing policy is buggy
			// because non-superuser fails to drop a role
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaGrant1,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test4", "test4"),

					resource.TestCheckResourceAttr("postgresql_role.all_without_grant_stay", "name", "all_without_grant_stay"),
					resource.TestCheckResourceAttr("postgresql_role.all_without_grant_drop", "name", "all_without_grant_drop"),
					resource.TestCheckResourceAttr("postgresql_role.policy_compose", "name", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_role.policy_move", "name", "policy_move"),

					resource.TestCheckResourceAttr("postgresql_role.all_with_grantstay", "name", "all_with_grantstay"),
					resource.TestCheckResourceAttr("postgresql_role.all_with_grantdrop", "name", "all_with_grantdrop"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "name", "test4"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "owner", "all_without_grant_stay"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.#", "7"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.role", "all_with_grantdrop"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.role", "all_with_grantstay"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.role", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.role", "all_without_grant_drop"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.usage_with_grant", "false"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.role", "all_without_grant_stay"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.usage_with_grant", "false"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.role", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.usage_with_grant", "false"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.6.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.6.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.6.role", "policy_move"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.6.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.6.usage_with_grant", "false"),
				),
			},
			{
				Config: testAccPostgresqlSchemaGrant2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test4", "test4"),
					resource.TestCheckResourceAttr("postgresql_role.all_without_grant_stay", "name", "all_without_grant_stay"),
					resource.TestCheckResourceAttr("postgresql_role.all_without_grant_drop", "name", "all_without_grant_drop"),
					resource.TestCheckResourceAttr("postgresql_role.policy_compose", "name", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_role.policy_move", "name", "policy_move"),

					resource.TestCheckResourceAttr("postgresql_role.all_with_grantstay", "name", "all_with_grantstay"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "name", "test4"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "owner", "all_without_grant_stay"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.#", "6"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.role", "all_with_grantstay"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.0.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.role", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.1.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.create", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.create_with_grant", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.role", "policy_move"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.usage", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.2.usage_with_grant", "true"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.role", "all_without_grant_stay"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.3.usage_with_grant", "false"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.role", "policy_compose"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.4.usage_with_grant", "false"),

					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.create", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.create_with_grant", "false"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.role", "policy_new"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.usage", "true"),
					resource.TestCheckResourceAttr("postgresql_schema.test4", "policy.5.usage_with_grant", "false"),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_Database(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	testAccPostgresqlSchemaDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_schema" "test_database" {
		name     = "test_database"
		database = "%s"
	}
	`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test_database", "test_database"),
					resource.TestCheckResourceAttr(
						"postgresql_schema.test_database", "name", "test_database"),
					resource.TestCheckResourceAttr(
						"postgresql_schema.test_database", "database", dbName),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_DropCascade(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	var testAccPostgresqlSchemaConfig = fmt.Sprintf(`
resource "postgresql_schema" "test_cascade" {
  name = "foo"
  database = "%s"
  drop_cascade = true
}
`, dbName)
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test_cascade", "foo"),
					resource.TestCheckResourceAttr("postgresql_schema.test_cascade", "name", "foo"),

					// This will create a table in the schema to check if the drop will work thanks to the cascade
					testAccCreateSchemaTable(dbName, "foo"),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_AlreadyExists(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, roleName := getTestDBNames(dbSuffix)

	// Test to create the schema 'public' that already exists
	// to assert it does not fail.
	var testAccPostgresqlSchemaConfig = fmt.Sprintf(`
resource "postgresql_schema" "public" {
  name = "public"
  database = "%s"
  owner = "%s"
}
`, dbName, roleName)
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.public", "public"),
					testAccCheckSchemaOwner(dbName, "public", roleName),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_Rename(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaNameConfig(dbName, "tf_rename_before"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test", "tf_rename_before"),
				),
			},
			{
				Config: testAccPostgresqlSchemaNameConfig(dbName, "tf_rename_after"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test", "tf_rename_after"),
					testAccCheckPostgresqlSchemaNotExists(dbName, "tf_rename_before"),
				),
			},
			{
				ResourceName:      "postgresql_schema.test",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s.tf_rename_after", dbName),
				ImportStateVerify: true,
				// These arguments are not stored in the catalogs, so they are not read back.
				ImportStateVerifyIgnore: []string{"if_not_exists", "drop_cascade"},
			},
			{
				// The schema is renamed outside of Terraform: the next plan must not be empty.
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), "ALTER SCHEMA tf_rename_after RENAME TO tf_rename_outside")
				},
				Config:             testAccPostgresqlSchemaNameConfig(dbName, "tf_rename_after"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), "ALTER SCHEMA tf_rename_outside RENAME TO tf_rename_after")
				},
				Config: testAccPostgresqlSchemaNameConfig(dbName, "tf_rename_after"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test", "tf_rename_after"),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_UpdateOwner(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, roleName := getTestDBNames(dbSuffix)
	newOwner := roleName + "_new"
	config := getTestConfig(t)

	// Registered after setupTestDatabase, so that it runs before the teardown of the database.
	defer createTestRole(t, newOwner)()

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaOwnerConfig(dbName, roleName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test", "tf_owner"),
					testAccCheckSchemaOwner(dbName, "tf_owner", roleName),
				),
			},
			{
				// Changing the owner of an existing schema is an ALTER SCHEMA ... OWNER TO on update.
				Config: testAccPostgresqlSchemaOwnerConfig(dbName, newOwner),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test", "tf_owner"),
					testAccCheckSchemaOwner(dbName, "tf_owner", newOwner),
				),
			},
			{
				ResourceName:      "postgresql_schema.test",
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s.tf_owner", dbName),
				ImportStateVerify: true,
				// These arguments are not stored in the catalogs, so they are not read back.
				ImportStateVerifyIgnore: []string{"if_not_exists", "drop_cascade"},
			},
			{
				// The owner is changed outside of Terraform: the next plan must not be empty.
				PreConfig: func() {
					// The schema is owned by newOwner: the connected user needs to be a member of both roles.
					dbExecute(t, config.connStr(dbName), fmt.Sprintf(
						"GRANT %[1]s, %[2]s TO CURRENT_USER; ALTER SCHEMA tf_owner OWNER TO %[2]s",
						newOwner, roleName,
					))
				},
				Config:             testAccPostgresqlSchemaOwnerConfig(dbName, newOwner),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testAccPostgresqlSchemaOwnerConfig(dbName, newOwner),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSchemaOwner(dbName, "tf_owner", newOwner),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_AdoptExisting(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, roleName := getTestDBNames(dbSuffix)

	// dev_schema already exists in the database (see setupTestDatabase): with
	// if_not_exists (the default), the resource adopts it and sets its owner.
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_schema" "test_adopt" {
  name     = "dev_schema"
  database = "%s"
  owner    = "%s"
}
`, dbName, roleName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test_adopt", "dev_schema"),
					testAccCheckSchemaOwner(dbName, "dev_schema", roleName),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_DropRestrict(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)
	config := getTestConfig(t)

	// Without drop_cascade, the schema is dropped with RESTRICT: a non-empty schema is refused.
	withSchema := fmt.Sprintf(`
resource "postgresql_schema" "test_restrict" {
  name     = "tf_restrict"
  database = "%s"
}
`, dbName)
	withoutSchema := `
resource "postgresql_role" "placeholder" {
  name = "tf_restrict_placeholder"
}
`

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: withSchema,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaExists("postgresql_schema.test_restrict", "tf_restrict"),
					// Creates a table in the schema: the schema is no longer empty.
					testAccCreateSchemaTable(dbName, "tf_restrict"),
				),
			},
			{
				Config:      withoutSchema,
				ExpectError: regexp.MustCompile(`cannot drop schema tf_restrict because other objects depend on it`),
			},
			{
				// Once the table is gone, the schema can be dropped.
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), "DROP TABLE tf_restrict.test_table")
				},
				Config: withoutSchema,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSchemaNotExists(dbName, "tf_restrict"),
				),
			},
		},
	})
}

func TestAccPostgresqlSchema_PolicyGrantOption(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, roleName := getTestDBNames(dbSuffix)
	otherRole := roleName + "_other"
	defer createTestRole(t, otherRole)()

	// Each policy is checked in the catalog (nspacl): a privilege is either absent,
	// granted, or granted WITH GRANT OPTION.
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSchemaDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSchemaPolicyConfig(dbName, roleName, "usage = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "USAGE", "granted"),
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "CREATE", "absent"),
				),
			},
			{
				Config: testAccPostgresqlSchemaPolicyConfig(dbName, roleName, "usage_with_grant = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "USAGE", "grantable"),
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "CREATE", "absent"),
				),
			},
			{
				Config: testAccPostgresqlSchemaPolicyConfig(dbName, roleName, "create_with_grant = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "USAGE", "absent"),
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "CREATE", "grantable"),
				),
			},
			{
				// The policy of roleName is dropped: all its privileges are revoked.
				Config: fmt.Sprintf(`
resource "postgresql_schema" "test_policy" {
  name     = "tf_policy"
  database = "%s"

  policy {
    usage = true
    role  = "%s"
  }
}
`, dbName, otherRole),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "USAGE", "absent"),
					testAccCheckSchemaPrivilege(dbName, "tf_policy", roleName, "CREATE", "absent"),
					testAccCheckSchemaPrivilege(dbName, "tf_policy", otherRole, "USAGE", "granted"),
				),
			},
		},
	})
}

func testAccPostgresqlSchemaPolicyConfig(database, roleName, policy string) string {
	return fmt.Sprintf(`
resource "postgresql_schema" "test_policy" {
  name     = "tf_policy"
  database = "%s"

  policy {
    %s
    role = "%s"
  }
}
`, database, policy, roleName)
}

// testAccCheckSchemaPrivilege checks in the catalog (nspacl) the state of a privilege
// of the role on the schema: "absent", "granted" or "grantable" (WITH GRANT OPTION).
func testAccCheckSchemaPrivilege(database, schemaName, role, privilege, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client).config.NewClient(database)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		query := `SELECT a.is_grantable
  FROM pg_catalog.pg_namespace n
  CROSS JOIN LATERAL pg_catalog.aclexplode(n.nspacl) a
  JOIN pg_catalog.pg_roles r ON r.oid = a.grantee
  WHERE n.nspname = $1 AND r.rolname = $2 AND a.privilege_type = $3`

		actual := "absent"
		var grantable bool
		switch err := db.QueryRow(query, schemaName, role, privilege).Scan(&grantable); {
		case err == sql.ErrNoRows:
		case err != nil:
			return fmt.Errorf("error reading privilege %s of schema %s: %w", privilege, schemaName, err)
		case grantable:
			actual = "grantable"
		default:
			actual = "granted"
		}

		if actual != expected {
			return fmt.Errorf("expected %s on schema %s for role %s to be %s; got %s", privilege, schemaName, role, expected, actual)
		}

		return nil
	}
}

// testAccCheckPostgresqlSchemaNotExists checks in the catalog that the schema is absent.
func testAccCheckPostgresqlSchemaNotExists(database, schemaName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSchemaExists(txn, schemaName)
		if err != nil {
			return fmt.Errorf("error checking schema %s", err)
		}

		if exists {
			return fmt.Errorf("Schema %s still exists", schemaName)
		}

		return nil
	}
}

func testAccPostgresqlSchemaNameConfig(database, schemaName string) string {
	return fmt.Sprintf(`
resource "postgresql_schema" "test" {
  name     = "%s"
  database = "%s"
}
`, schemaName, database)
}

func testAccPostgresqlSchemaOwnerConfig(database, owner string) string {
	return fmt.Sprintf(`
resource "postgresql_schema" "test" {
  name     = "tf_owner"
  database = "%s"
  owner    = "%s"
}
`, database, owner)
}

func testAccCheckPostgresqlSchemaDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_schema" {
			continue
		}

		database, ok := rs.Primary.Attributes[schemaDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}

		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSchemaExists(txn, getExtensionNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking schema %s", err)
		}

		if exists {
			return fmt.Errorf("Schema still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlSchemaExists(n string, schemaName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		database, ok := rs.Primary.Attributes[schemaDatabaseAttr]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}

		actualSchemaName := rs.Primary.Attributes["name"]
		if actualSchemaName != schemaName {
			return fmt.Errorf("Wrong value for schema name expected %s got %s", schemaName, actualSchemaName)
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSchemaExists(txn, schemaName)

		if err != nil {
			return fmt.Errorf("error checking schema %s", err)
		}

		if !exists {
			return fmt.Errorf("Schema not found")
		}

		return nil
	}
}

func checkSchemaExists(txn *sql.Tx, schemaName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE FROM pg_catalog.pg_namespace WHERE nspname=$1", schemaName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about schema: %w", err)
	}

	return true, nil
}

func testAccCreateSchemaTable(database, schemaName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {

		client := testAccProvider.Meta().(*Client).config.NewClient(database)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		if _, err = db.Exec(fmt.Sprintf("CREATE TABLE %s.test_table (id serial)", schemaName)); err != nil {
			return fmt.Errorf("could not create test table in schema %s: %s", schemaName, err)
		}

		return nil
	}
}

func testAccCheckSchemaOwner(database, schemaName, expectedOwner string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client).config.NewClient(database)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		var owner string

		query := "SELECT pg_catalog.pg_get_userbyid(n.nspowner)  FROM pg_catalog.pg_namespace n WHERE n.nspname=$1"
		switch err := db.QueryRow(query, schemaName).Scan(&owner); {
		case err == sql.ErrNoRows:
			return fmt.Errorf("could not find schema %s while checking owner", schemaName)
		case err != nil:
			return fmt.Errorf("error reading owner of schema %s: %w", schemaName, err)
		}

		if owner != expectedOwner {
			return fmt.Errorf("expected owner of schema %s to be %s; got %s", schemaName, expectedOwner, owner)
		}

		return nil
	}
}

const testAccPostgresqlSchemaConfig = `
resource "postgresql_role" "role_all_without_grant" {
  name = "role_all_without_grant"
  login = true
}

resource "postgresql_role" "role_all_with_grant" {
  name = "role_all_with_grant"
}

resource "postgresql_schema" "test1" {
  name = "foo"
}

resource "postgresql_schema" "test2" {
  name = "bar"
  owner = "${postgresql_role.role_all_without_grant.name}"
  if_not_exists = false

  policy {
    create = true
    usage = true
    role = "${postgresql_role.role_all_without_grant.name}"
  }
}

resource "postgresql_schema" "test3" {
  name = "baz"
  owner = "${postgresql_role.role_all_without_grant.name}"
  if_not_exists = true

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.role_all_with_grant.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.role_all_without_grant.name}"
  }
}
`

const testAccPostgresqlSchemaGrant1 = `
resource "postgresql_role" "all_without_grant_stay" {
  name = "all_without_grant_stay"
}

resource "postgresql_role" "all_without_grant_drop" {
  name = "all_without_grant_drop"
}

resource "postgresql_role" "policy_compose" {
  name = "policy_compose"
}

resource "postgresql_role" "policy_move" {
  name = "policy_move"
}

resource "postgresql_role" "all_with_grantstay" {
  name = "all_with_grantstay"
}

resource "postgresql_role" "all_with_grantdrop" {
  name = "all_with_grantdrop"
}

resource "postgresql_schema" "test4" {
  name = "test4"
  owner = "${postgresql_role.all_without_grant_stay.name}"

  policy {
    create = true
    usage = true
    role = "${postgresql_role.all_without_grant_stay.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.all_without_grant_drop.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.policy_compose.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.policy_move.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.all_with_grantstay.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.all_with_grantdrop.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.policy_compose.name}"
  }
}
`

const testAccPostgresqlSchemaGrant2 = `
resource "postgresql_role" "all_without_grant_stay" {
  name = "all_without_grant_stay"
}

resource "postgresql_role" "all_without_grant_drop" {
  name = "all_without_grant_drop"
}

resource "postgresql_role" "policy_compose" {
  name = "policy_compose"
}

resource "postgresql_role" "policy_move" {
  name = "policy_move"
}

resource "postgresql_role" "all_with_grantstay" {
  name = "all_with_grantstay"
}

resource "postgresql_role" "policy_new" {
  name = "policy_new"
}

resource "postgresql_schema" "test4" {
  name = "test4"
  owner = "${postgresql_role.all_without_grant_stay.name}"

  policy {
    create = true
    usage = true
    role = "${postgresql_role.all_without_grant_stay.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.policy_compose.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.all_with_grantstay.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.policy_compose.name}"
  }

  policy {
    create_with_grant = true
    usage_with_grant = true
    role = "${postgresql_role.policy_move.name}"
  }

  policy {
    create = true
    usage = true
    role = "${postgresql_role.policy_new.name}"
  }
}
`
