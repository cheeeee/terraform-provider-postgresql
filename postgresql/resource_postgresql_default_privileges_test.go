package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestAccPostgresqlDefaultPrivileges(t *testing.T) {
	skipIfNotAcc(t)

	// We have to create the database outside of resource.Test
	// because we need to create a table to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	// Set default privileges to the test role then to public (i.e.: everyone)
	for _, role := range []string{roleName, "public"} {
		t.Run(role, func(t *testing.T) {
			withGrant := (role != "public")

			// We set PGUSER as owner as he will create the test table
			var tfConfig = fmt.Sprintf(`
resource "postgresql_default_privileges" "test_ro" {
	database    = "%s"
	owner       = "%s"
	role        = "%s"
	schema      = "test_schema"
	object_type = "table"
	with_grant_option = %t
	privileges   = %%s
}
	`, dbName, config.Username, role, withGrant)

			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(tfConfig, `[]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table"}
								// To test default privileges, we need to create a table
								// after having apply the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "table"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "with_grant_option", fmt.Sprintf("%t", withGrant)),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "0"),
						),
					},
					{
						Config: fmt.Sprintf(tfConfig, `["SELECT"]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table"}
								// To test default privileges, we need to create a table
								// after having apply the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{"SELECT"})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "table"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "with_grant_option", fmt.Sprintf("%t", withGrant)),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "SELECT"),
						),
					},
					{
						Config: fmt.Sprintf(tfConfig, `["SELECT", "UPDATE"]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table"}
								// To test default privileges, we need to create a table
								// after having apply the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{"SELECT", "UPDATE"})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "2"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "SELECT"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.1", "UPDATE"),
						),
					},
					{
						Config: fmt.Sprintf(tfConfig, `[]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table"}
								// To test default privileges, we need to create a table
								// after having apply the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "table"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "with_grant_option", fmt.Sprintf("%t", withGrant)),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "0"),
						),
					},
				},
			})
		})
	}
}

// Test the case where we need to grant the owner to the connected user.
// The owner should be revoked
func TestAccPostgresqlDefaultPrivileges_GrantOwner(t *testing.T) {
	skipIfNotAcc(t)

	// We have to create the database outside of resource.Test
	// because we need to create a table to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	// We set PGUSER as owner as he will create the test table
	var stateConfig = fmt.Sprintf(`

resource postgresql_role "test_owner" {
    name = "test_owner"
}

// From PostgreSQL 15, schema public is not wild open anymore
resource "postgresql_grant" "public_usage" {
	database          = "%s"
	schema            = "public"
	role              = postgresql_role.test_owner.name
	object_type       = "schema"
	privileges        = ["CREATE", "USAGE"]
}

resource "postgresql_default_privileges" "test_ro" {
	database    = "%s"
	owner       = postgresql_role.test_owner.name
	role        = "%s"
	schema      = "public"
	object_type = "table"
	privileges  = ["SELECT"]
}
	`, dbName, dbName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: stateConfig,
				Check: resource.ComposeTestCheckFunc(
					func(*terraform.State) error {
						tables := []string{"public.test_table"}
						// To test default privileges, we need to create a table
						// after having apply the state.
						dropFunc := createTestTables(t, dbSuffix, tables, "test_owner")
						defer dropFunc()

						return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{"SELECT"})
					},
					resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "table"),
					resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "SELECT"),

					// check if connected user does not have test_owner granted anymore.
					checkUserMembership(t, config.Username, "test_owner", false),
				),
			},
		},
	})
}

// Test the case where we define default privileges without specifying a schema. These
// privileges should apply to newly created resources for the named role in all schema.
func TestAccPostgresqlDefaultPrivileges_NoSchema(t *testing.T) {
	skipIfNotAcc(t)

	// We have to create the database outside of resource.Test
	// because we need to create a table to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	// Set default privileges to the test role then to public (i.e.: everyone)
	for _, role := range []string{roleName, "public"} {
		t.Run(role, func(t *testing.T) {

			hclText := `
resource "postgresql_default_privileges" "test_ro" {
	database    = "%s"
	owner       = "%s"
	role        = "%s"
	object_type = "table"
	privileges  = %%s
}
`
			// We set PGUSER as owner as he will create the test table
			var tfConfig = fmt.Sprintf(hclText, dbName, config.Username, role)

			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(tfConfig, `["SELECT"]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table", "dev_schema.test_table"}
								// To test default privileges, we need to create tables
								// in both dev and test schema after having applied the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{"SELECT"})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "table"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "SELECT"),
						),
					},
					{
						Config: fmt.Sprintf(tfConfig, `["SELECT", "UPDATE"]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								tables := []string{"test_schema.test_table", "dev_schema.test_table"}
								// To test default privileges, we need to create tables
								// in both dev and test schema after having applied the state.
								dropFunc := createTestTables(t, dbSuffix, tables, "")
								defer dropFunc()

								return testCheckTablesPrivileges(t, dbName, roleName, tables, []string{"SELECT", "UPDATE"})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "2"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "SELECT"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.1", "UPDATE"),
						),
					},
				},
			})
		})
	}
}

// Test defaults privileges on schemas
func TestAccPostgresqlDefaultPrivilegesOnSchemas(t *testing.T) {
	skipIfNotAcc(t)

	// We have to create the database outside of resource.Test
	// because we need to create schemas to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	// Set default privileges to the test role then to public (i.e.: everyone)
	for _, role := range []string{roleName, "public"} {
		t.Run(role, func(t *testing.T) {

			hclText := `
resource "postgresql_default_privileges" "test_ro" {
	database    = "%s"
	owner       = "%s"
	role        = "%s"
	object_type = "schema"
	privileges  = %%s
}
`
			// We set PGUSER as owner as he will create the test schemas
			var tfConfig = fmt.Sprintf(hclText, dbName, config.Username, role)

			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
					testCheckCompatibleVersion(t, featurePrivilegesOnSchemas)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(tfConfig, `[]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								schemas := []string{"test_schema2", "dev_schema2"}
								// To test default privileges, we need to create a schema
								// after having apply the state.
								dropFunc := createTestSchemas(t, dbSuffix, schemas, "")
								defer dropFunc()

								return testCheckSchemasPrivileges(t, dbName, roleName, schemas, []string{})
							},
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "object_type", "schema"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "0"),
						),
					},
					{
						Config: fmt.Sprintf(tfConfig, `["CREATE", "USAGE"]`),
						Check: resource.ComposeTestCheckFunc(
							func(*terraform.State) error {
								schemas := []string{"test_schema2", "dev_schema2"}
								// To test default privileges, we need to create a schema
								// after having apply the state.
								dropFunc := createTestSchemas(t, dbSuffix, schemas, "")
								defer dropFunc()

								return testCheckSchemasPrivileges(t, dbName, roleName, schemas, []string{"CREATE", "USAGE"})
							},
							resource.TestCheckResourceAttr(
								"postgresql_default_privileges.test_ro", "id", fmt.Sprintf("%s_%s_noschema_%s_schema", role, dbName, config.Username),
							),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.#", "2"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.0", "CREATE"),
							resource.TestCheckResourceAttr("postgresql_default_privileges.test_ro", "privileges.1", "USAGE"),
						),
					},
				},
			})
		})
	}
}

func TestAccPostgresqlDefaultPrivileges_Routines(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	resourceConfig := fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database          = "%s"
	schema            = "test_schema"
	owner             = "%s"
	role              = "%s"
	object_type       = "routine"
	privileges        = ["EXECUTE"]
}
`, dbName, config.Username, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: resourceConfig,
				Check: resource.ComposeTestCheckFunc(
					// Create a test function to check if default privileges are applied
					func(*terraform.State) error {
						dbExecute(
							t, config.connStr(dbName),
							"CREATE FUNCTION test_schema.test_function() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql;",
						)

						db := connectAsTestRole(t, roleName, dbName)
						defer closeDB(t, db)

						if _, err := db.Exec("SELECT test_schema.test_function();"); err != nil {
							t.Fatalf("Expected test role to be able to execute function, got error: %s", err)
						}

						return nil
					},
				),
			},
		},
	})
}

// defaultPrivilegesObjectClass describes one object class for the table-driven test.
type defaultPrivilegesObjectClass struct {
	objectType string
	// sqlClass is the class keyword of ALTER DEFAULT PRIVILEGES
	sqlClass string
	// create and drop run as the owner (the connected user) and create the object in test_schema
	create, drop string
	// object is the SQL literal which names the object in hasPrivilege
	object string
	// hasPrivilege is the has_*_privilege function of the object class
	hasPrivilege string
	// privileges are the individual privileges of the class (ALL excluded)
	privileges []string
	// schemaScoped is true when the class accepts the schema attribute
	schemaScoped bool
}

var testDefaultPrivilegesClasses = []defaultPrivilegesObjectClass{
	{
		objectType: "table", sqlClass: "TABLES",
		create: "CREATE TABLE test_schema.dp_table (val text)", drop: "DROP TABLE test_schema.dp_table",
		object: "'test_schema.dp_table'", hasPrivilege: "has_table_privilege",
		privileges:   []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"},
		schemaScoped: true,
	},
	{
		objectType: "sequence", sqlClass: "SEQUENCES",
		create: "CREATE SEQUENCE test_schema.dp_sequence", drop: "DROP SEQUENCE test_schema.dp_sequence",
		object: "'test_schema.dp_sequence'", hasPrivilege: "has_sequence_privilege",
		privileges:   []string{"USAGE", "SELECT", "UPDATE"},
		schemaScoped: true,
	},
	{
		objectType: "function", sqlClass: "FUNCTIONS",
		create: "CREATE FUNCTION test_schema.dp_function() RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql",
		drop:   "DROP FUNCTION test_schema.dp_function()",
		object: "'test_schema.dp_function()'", hasPrivilege: "has_function_privilege",
		privileges:   []string{"EXECUTE"},
		schemaScoped: true,
	},
	{
		objectType: "routine", sqlClass: "ROUTINES",
		create: "CREATE FUNCTION test_schema.dp_routine() RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql",
		drop:   "DROP FUNCTION test_schema.dp_routine()",
		object: "'test_schema.dp_routine()'", hasPrivilege: "has_function_privilege",
		privileges:   []string{"EXECUTE"},
		schemaScoped: true,
	},
	{
		objectType: "type", sqlClass: "TYPES",
		create: "CREATE TYPE test_schema.dp_type AS ENUM ('a')", drop: "DROP TYPE test_schema.dp_type",
		object: "'test_schema.dp_type'", hasPrivilege: "has_type_privilege",
		privileges:   []string{"USAGE"},
		schemaScoped: true,
	},
	{
		objectType: "schema", sqlClass: "SCHEMAS",
		create: "CREATE SCHEMA dp_schema", drop: "DROP SCHEMA dp_schema",
		object: "'dp_schema'", hasPrivilege: "has_schema_privilege",
		privileges: []string{"CREATE", "USAGE"},
	},
	{
		objectType: "large_object", sqlClass: "LARGE OBJECTS",
		create: "SELECT lo_create(4242)", drop: "SELECT lo_unlink(4242)",
		object: "4242", hasPrivilege: "has_largeobject_privilege",
		privileges: []string{"SELECT", "UPDATE"},
	},
}

// testServerVersionNum returns the server_version_num of the test database.
func testServerVersionNum(t *testing.T) int {
	config := getTestConfig(t)
	db, err := sql.Open(testDriverName, config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open connection pool: %v", err)
	}
	defer closeDB(t, db)

	var version int
	if err := db.QueryRow("SHOW server_version_num").Scan(&version); err != nil {
		t.Fatalf("could not read server version: %v", err)
	}
	return version
}

// testCheckDefaultPrivilegesACL checks in pg_default_acl the privileges granted to roleName
// for the owner, the schema (empty for global defaults) and the object class.
func testCheckDefaultPrivilegesACL(t *testing.T, dbName string, class defaultPrivilegesObjectClass, owner, roleName, schema string, expected []string, grantOption bool) error {
	config := getTestConfig(t)
	db, err := sql.Open(testDriverName, config.connStr(dbName))
	if err != nil {
		t.Fatalf("could not open connection pool for db %s: %v", dbName, err)
	}
	defer closeDB(t, db)

	// Per-schema default privileges are added to the global ones: a schema row starts empty and
	// is read alone. The global row starts from the built-in default, which PostgreSQL stores
	// as no row at all (it deletes a default ACL which equals acldefault).
	query := `SELECT array_agg(a.privilege_type ORDER BY a.privilege_type), bool_and(a.is_grantable)
	FROM (SELECT CASE WHEN $2 <> '' THEN
		(SELECT defaclacl FROM pg_default_acl WHERE defaclobjtype = $1 AND defaclrole = (SELECT oid FROM pg_roles WHERE rolname = $4)
			AND defaclnamespace = (SELECT oid FROM pg_namespace WHERE nspname = $2))
	ELSE COALESCE(
		(SELECT defaclacl FROM pg_default_acl WHERE defaclobjtype = $1 AND defaclrole = (SELECT oid FROM pg_roles WHERE rolname = $4)
			AND defaclnamespace = 0),
		acldefault($1::"char", (SELECT oid FROM pg_roles WHERE rolname = $4))
	) END AS acl) AS d CROSS JOIN LATERAL aclexplode(d.acl) AS a
	WHERE a.grantee = CASE WHEN $3 = 'public' THEN 0 ELSE (SELECT oid FROM pg_roles WHERE rolname = $3) END`

	defacl := map[string]string{"TABLES": "r", "SEQUENCES": "S", "FUNCTIONS": "f", "ROUTINES": "f", "TYPES": "T", "SCHEMAS": "n", "LARGE OBJECTS": "L"}

	var privileges pq.StringArray
	var grantable sql.NullBool
	if err := db.QueryRow(query, defacl[class.sqlClass], schema, roleName, owner).Scan(&privileges, &grantable); err != nil {
		return fmt.Errorf("could not read pg_default_acl: %w", err)
	}

	want := append([]string{}, expected...)
	sort.Strings(want)
	got := append([]string{}, privileges...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("pg_default_acl for %s on %s (schema %q): expected privileges %v, got %v", roleName, class.sqlClass, schema, want, got)
	}
	if len(want) > 0 && grantable.Bool != grantOption {
		return fmt.Errorf("pg_default_acl for %s on %s: expected grant option %t, got %t", roleName, class.sqlClass, grantOption, grantable.Bool)
	}
	return nil
}

// testCheckDefaultPrivilegesEffect checks, with the has_*_privilege catalog functions, which
// privileges roleName has on an object created by the owner after the default privileges were set.
func testCheckDefaultPrivilegesEffect(t *testing.T, dbName string, class defaultPrivilegesObjectClass, roleName string, privileges, expected []string) error {
	config := getTestConfig(t)
	db, err := sql.Open(testDriverName, config.connStr(dbName))
	if err != nil {
		t.Fatalf("could not open connection pool for db %s: %v", dbName, err)
	}
	defer closeDB(t, db)

	for _, privilege := range privileges {
		var has bool
		query := fmt.Sprintf("SELECT %s('%s', %s, '%s')", class.hasPrivilege, roleName, class.object, privilege)
		if err := db.QueryRow(query).Scan(&has); err != nil {
			return fmt.Errorf("could not run %s: %w", query, err)
		}
		if want := sliceContainsStr(expected, privilege); has != want {
			return fmt.Errorf("%s on new %s: expected %t, got %t", privilege, class.objectType, want, has)
		}
	}
	return nil
}

// testResetDefaultPrivileges brings the default privileges of the owner for one object class
// back to the built-in default, globally and in test_schema, for roleName and PUBLIC, so that
// the subtests of a table-driven test do not depend on the rows left by an earlier one.
func testResetDefaultPrivileges(t *testing.T, dbName string, class defaultPrivilegesObjectClass, owner, roleName string) {
	config := getTestConfig(t)
	base := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s ", owner)
	for _, grantee := range []string{roleName, "PUBLIC"} {
		dbExecute(t, config.connStr(dbName), fmt.Sprintf("%sREVOKE ALL ON %s FROM %s", base, class.sqlClass, grantee))
		if class.schemaScoped {
			dbExecute(t, config.connStr(dbName), fmt.Sprintf("%sIN SCHEMA test_schema REVOKE ALL ON %s FROM %s", base, class.sqlClass, grantee))
		}
	}
	// The built-in default which PUBLIC has on new objects
	switch class.sqlClass {
	case "FUNCTIONS", "ROUTINES":
		dbExecute(t, config.connStr(dbName), base+"GRANT EXECUTE ON FUNCTIONS TO PUBLIC")
	case "TYPES":
		dbExecute(t, config.connStr(dbName), base+"GRANT USAGE ON TYPES TO PUBLIC")
	}
}

// testDefaultPrivilegesList returns the HCL privileges list of a step.
func testDefaultPrivilegesList(privileges []string) string {
	quoted := []string{}
	for _, p := range privileges {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// Table-driven test of every object class and of every privilege of the class:
// each step sets privileges, then checks pg_default_acl and the effect on an object
// created afterwards by the owner. Steps reduce and extend the privileges, use ALL and remove them.
func TestAccPostgresqlDefaultPrivilegesObjectClasses(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	maintain := testServerVersionNum(t) >= 170000

	for _, class := range testDefaultPrivilegesClasses {
		class := class
		privileges := []string{}
		for _, p := range class.privileges {
			if p != "MAINTAIN" || maintain {
				privileges = append(privileges, p)
			}
		}
		// Steps: nothing, first privilege, all privileges, ALL, back to the first privilege, nothing
		steps := [][]string{{}, {privileges[0]}, privileges, {"ALL"}, {privileges[0]}, {}}

		scopes := []string{""}
		if class.schemaScoped {
			scopes = append(scopes, "test_schema")
		}

		for _, role := range []string{roleName, "public"} {
			for _, schema := range scopes {
				t.Run(fmt.Sprintf("%s/%s/schema=%s", class.objectType, role, schema), func(t *testing.T) {
					withGrant := role != "public"
					schemaLine := ""
					if schema != "" {
						schemaLine = fmt.Sprintf(`schema = "%s"`, schema)
					}
					tfConfig := fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database          = "%s"
	owner             = "%s"
	role              = "%s"
	%s
	object_type       = "%s"
	with_grant_option = %t
	privileges        = %%s
}
`, dbName, config.Username, role, schemaLine, class.objectType, withGrant)

					// Every subtest starts from the built-in default privileges of the owner, then
					// removes what PUBLIC has on new objects by default, which would hide what the
					// role gets. It is global, because a schema-scoped revoke cannot remove it.
					reset := func() { testResetDefaultPrivileges(t, dbName, class, config.Username, roleName) }
					revokePublic := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s REVOKE ALL ON %s FROM PUBLIC", config.Username, class.sqlClass)

					var resourceSteps []resource.TestStep
					for i, step := range steps {
						step := step
						expected := step
						if len(step) == 1 && step[0] == "ALL" {
							expected = privileges
						}
						tfStep := resource.TestStep{
							Config: fmt.Sprintf(tfConfig, testDefaultPrivilegesList(step)),
							Check: func(*terraform.State) error {
								dbExecute(t, config.connStr(dbName), class.create)
								defer dbExecute(t, config.connStr(dbName), class.drop)

								if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, role, schema, expected, withGrant); err != nil {
									return err
								}
								return testCheckDefaultPrivilegesEffect(t, dbName, class, roleName, privileges, expected)
							},
						}
						if i == 0 {
							tfStep.PreConfig = func() {
								// Registered here: a subtest skipped by its PreCheck must not reset
								t.Cleanup(reset)
								reset()
								dbExecute(t, config.connStr(dbName), revokePublic)
							}
						}
						resourceSteps = append(resourceSteps, tfStep)
					}

					resource.Test(t, resource.TestCase{
						PreCheck: func() {
							testAccPreCheck(t)
							testCheckCompatibleVersion(t, featurePrivileges)
							if class.objectType == "large_object" {
								testCheckCompatibleVersion(t, featureDefaultPrivilegesLargeObjects)
							}
							if class.objectType == "schema" {
								testCheckCompatibleVersion(t, featurePrivilegesOnSchemas)
							}
						},
						Providers: testAccProviders,
						Steps:     resourceSteps,
					})
				})
			}
		}
	}
}

// testDefaultPrivilegesClassByType returns the test description of an object_type.
func testDefaultPrivilegesClassByType(t *testing.T, objectType string) defaultPrivilegesObjectClass {
	for _, class := range testDefaultPrivilegesClasses {
		if class.objectType == objectType {
			return class
		}
	}
	t.Fatalf("unknown object type %s in test", objectType)
	return defaultPrivilegesObjectClass{}
}

// The grant option is removed by a change from true to false, and added by a change from false to true.
// Each change recreates the resource, which must leave the grant option as configured.
func TestAccPostgresqlDefaultPrivilegesGrantOption(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "table")

	tfConfig := func(withGrant bool) string {
		return fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database          = "%s"
	owner             = "%s"
	role              = "%s"
	schema            = "test_schema"
	object_type       = "table"
	with_grant_option = %t
	privileges        = ["SELECT"]
}
`, dbName, config.Username, roleName, withGrant)
	}

	// checkGrantOption checks pg_default_acl and the effect on a new table
	checkGrantOption := func(withGrant bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"SELECT"}, withGrant); err != nil {
				return err
			}
			dbExecute(t, config.connStr(dbName), class.create)
			defer dbExecute(t, config.connStr(dbName), class.drop)
			return testCheckDefaultPrivilegesEffect(t, dbName, class, roleName, []string{"SELECT", "INSERT"}, []string{"SELECT"})
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA test_schema REVOKE ALL ON TABLES FROM PUBLIC", config.Username))
				},
				Config: tfConfig(true),
				Check:  checkGrantOption(true),
			},
			{
				// true to false: the grant option must be removed
				Config: tfConfig(false),
				Check:  checkGrantOption(false),
			},
			{
				// false to true: the grant option must be added
				Config: tfConfig(true),
				Check:  checkGrantOption(true),
			},
			{
				// The grant option is removed outside of Terraform: the plan must show a change
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA test_schema REVOKE GRANT OPTION FOR SELECT ON TABLES FROM %s", config.Username, roleName))
				},
				Config:             tfConfig(true),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// A change made outside of Terraform is detected by the plan, and the next apply restores the configuration.
func TestAccPostgresqlDefaultPrivilegesOutsideChange(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "table")

	tfConfig := fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database    = "%s"
	owner       = "%s"
	role        = "%s"
	schema      = "test_schema"
	object_type = "table"
	privileges  = ["SELECT"]
}
`, dbName, config.Username, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: tfConfig,
				Check: func(*terraform.State) error {
					return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"SELECT"}, false)
				},
			},
			{
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA test_schema GRANT INSERT ON TABLES TO %s", config.Username, roleName))
				},
				Config:             tfConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// The apply restores the privileges of the configuration
				Config: tfConfig,
				Check: func(*terraform.State) error {
					if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"SELECT"}, false); err != nil {
						return err
					}
					dbExecute(t, config.connStr(dbName), class.create)
					defer dbExecute(t, config.connStr(dbName), class.drop)
					return testCheckDefaultPrivilegesEffect(t, dbName, class, roleName, []string{"SELECT", "INSERT"}, []string{"SELECT"})
				},
			},
		},
	})
}

// Removing the resource revokes the default privileges of the role.
func TestAccPostgresqlDefaultPrivilegesRemoval(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "table")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database          = "%s"
	owner             = "%s"
	role              = "%s"
	schema            = "test_schema"
	object_type       = "table"
	with_grant_option = true
	privileges        = ["SELECT", "UPDATE"]
}
`, dbName, config.Username, roleName),
				Check: func(*terraform.State) error {
					return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"SELECT", "UPDATE"}, true)
				},
			},
		},
		// After the destroy of the resource, nothing is granted to the role by default
		CheckDestroy: func(*terraform.State) error {
			if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{}, false); err != nil {
				return err
			}
			dbExecute(t, config.connStr(dbName), class.create)
			defer dbExecute(t, config.connStr(dbName), class.drop)
			return testCheckDefaultPrivilegesEffect(t, dbName, class, roleName, []string{"SELECT", "UPDATE"}, []string{})
		},
	})
}

// PostgreSQL has no default privileges for schemas inside a schema, the provider refuses them.
func TestAccPostgresqlDefaultPrivilegesSchemaInSchema(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivilegesOnSchemas)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database    = "%s"
	owner       = "%s"
	role        = "public"
	schema      = "test_schema"
	object_type = "schema"
	privileges  = ["USAGE"]
}
`, "postgres", config.Username),
				ExpectError: regexp.MustCompile("cannot specify `schema` when `object_type` is `schema`"),
			},
		},
	})
}

// Default privileges on large objects exist from PostgreSQL 18 and cannot be scoped to a schema.
func TestAccPostgresqlDefaultPrivilegesLargeObjects(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	version := testServerVersionNum(t)

	if version < 180000 {
		// The provider refuses the object type with a clear error on older versions
		resource.Test(t, resource.TestCase{
			PreCheck: func() {
				testAccPreCheck(t)
			},
			Providers: testAccProviders,
			Steps: []resource.TestStep{
				{
					Config: fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database    = "postgres"
	owner       = "%s"
	role        = "public"
	object_type = "large_object"
	privileges  = ["SELECT"]
}
`, config.Username),
					ExpectError: regexp.MustCompile("object type LARGE OBJECTS is not supported for this Postgres version"),
				},
			},
		})
		return
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureDefaultPrivilegesLargeObjects)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database    = "postgres"
	owner       = "%s"
	role        = "public"
	schema      = "test_schema"
	object_type = "large_object"
	privileges  = ["SELECT"]
}
`, config.Username),
				ExpectError: regexp.MustCompile("cannot specify `schema` when `object_type` is `large_object`"),
			},
		},
	})
}

// PUBLIC cannot receive privileges WITH GRANT OPTION, the provider refuses it.
func TestAccPostgresqlDefaultPrivilegesPublicWithGrantOption(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_default_privileges" "test" {
	database          = "postgres"
	owner             = "%s"
	role              = "public"
	object_type       = "table"
	with_grant_option = true
	privileges        = ["SELECT"]
}
`, config.Username),
				ExpectError: regexp.MustCompile("with_grant_option cannot be true for role 'public'"),
			},
		},
	})
}

func testDefaultPrivilegesResource(name, dbName, owner, role, schema, objectType, privileges string) string {
	schemaLine := ""
	if schema != "" {
		schemaLine = fmt.Sprintf(`schema = "%s"`, schema)
	}
	return fmt.Sprintf(`
resource "postgresql_default_privileges" "%s" {
	database    = "%s"
	owner       = "%s"
	role        = "%s"
	%s
	object_type = "%s"
	privileges  = %s
}
`, name, dbName, owner, role, schemaLine, objectType, privileges)
}

// Per-schema default privileges are added to the global ones: a schema resource reads only its
// own schema row, whatever the global default privileges of the role are.
func TestAccPostgresqlDefaultPrivilegesSchemaIgnoresGlobal(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "table")
	global := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT SELECT ON TABLES TO %s", config.Username, roleName)
	defer testResetDefaultPrivileges(t, dbName, class, config.Username, roleName)

	empty := testDefaultPrivilegesResource("test", dbName, config.Username, roleName, "test_schema", "table", "[]")
	insert := testDefaultPrivilegesResource("test", dbName, config.Username, roleName, "test_schema", "table", `["INSERT"]`)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: empty,
				Check:  resource.TestCheckResourceAttr("postgresql_default_privileges.test", "privileges.#", "0"),
			},
			{
				// SELECT granted globally (outside of Terraform) is not a privilege of the schema resource
				PreConfig: func() { dbExecute(t, config.connStr(dbName), global) },
				Config:    empty,
				PlanOnly:  true,
			},
			{
				Config: insert,
				Check: func(*terraform.State) error {
					if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"INSERT"}, false); err != nil {
						return err
					}
					return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "", []string{"SELECT"}, false)
				},
			},
		},
	})
}

// A global and a schema resource of the same owner, role and object type each read their own row,
// and a new object of the schema gets the union of both.
func TestAccPostgresqlDefaultPrivilegesGlobalAndSchema(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "table")
	defer testResetDefaultPrivileges(t, dbName, class, config.Username, roleName)

	tfConfig := testDefaultPrivilegesResource("global", dbName, config.Username, roleName, "", "table", `["SELECT", "UPDATE"]`) +
		testDefaultPrivilegesResource("in_schema", dbName, config.Username, roleName, "test_schema", "table", `["INSERT"]`)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: tfConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_default_privileges.global", "privileges.#", "2"),
					resource.TestCheckResourceAttr("postgresql_default_privileges.in_schema", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_default_privileges.in_schema", "privileges.0", "INSERT"),
					func(*terraform.State) error {
						if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "", []string{"SELECT", "UPDATE"}, false); err != nil {
							return err
						}
						if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, roleName, "test_schema", []string{"INSERT"}, false); err != nil {
							return err
						}

						dbExecute(t, config.connStr(dbName), "CREATE SCHEMA dp_other")
						defer dbExecute(t, config.connStr(dbName), "DROP SCHEMA dp_other CASCADE")
						dbExecute(t, config.connStr(dbName), class.create)
						defer dbExecute(t, config.connStr(dbName), class.drop)
						dbExecute(t, config.connStr(dbName), "CREATE TABLE dp_other.dp_table (val text)")

						all := []string{"SELECT", "INSERT", "UPDATE", "DELETE"}
						// In the schema: the union of the global and of the schema privileges
						if err := testCheckDefaultPrivilegesEffect(t, dbName, class, roleName, all, []string{"SELECT", "INSERT", "UPDATE"}); err != nil {
							return err
						}
						// Elsewhere: the global privileges only
						other := class
						other.object = "'dp_other.dp_table'"
						return testCheckDefaultPrivilegesEffect(t, dbName, other, roleName, all, []string{"SELECT", "UPDATE"})
					},
				),
			},
		},
	})
}

// A schema resource does not report the built-in default privileges (EXECUTE of PUBLIC on
// functions) as if its schema row granted them: without a row it has no privileges.
func TestAccPostgresqlDefaultPrivilegesSchemaNoBuiltInDefault(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, _ := getTestDBNames(dbSuffix)
	class := testDefaultPrivilegesClassByType(t, "function")
	defer testResetDefaultPrivileges(t, dbName, class, config.Username, "public")

	tfConfig := testDefaultPrivilegesResource("test", dbName, config.Username, "public", "test_schema", "function", `["EXECUTE"]`)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: tfConfig,
				Check: func(*terraform.State) error {
					return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, "public", "test_schema", []string{"EXECUTE"}, false)
				},
			},
			{
				// The schema row disappears: the built-in default of PUBLIC still applies to new
				// functions, but it is not a privilege of the schema resource, which must be recreated.
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA test_schema REVOKE ALL ON FUNCTIONS FROM PUBLIC", config.Username))
				},
				Config:             tfConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: tfConfig,
				Check: func(*terraform.State) error {
					return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, "public", "test_schema", []string{"EXECUTE"}, false)
				},
			},
		},
	})
}

// IN SCHEMA ... REVOKE of a privilege which only exists globally or by built-in default is a
// silent no-op in PostgreSQL: the resource of the schema is valid (nothing is granted in the
// schema) and the role keeps the privileges it has globally.
func TestAccPostgresqlDefaultPrivilegesSchemaRevokeNoEffect(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)
	tableClass := testDefaultPrivilegesClassByType(t, "table")
	functionClass := testDefaultPrivilegesClassByType(t, "function")
	defer testResetDefaultPrivileges(t, dbName, tableClass, config.Username, roleName)
	defer testResetDefaultPrivileges(t, dbName, functionClass, config.Username, roleName)

	// The resource of the schema is applied and its plan is empty afterwards (checked by
	// every step); nothing is stored for the schema and the global privileges are untouched.
	check := func(class defaultPrivilegesObjectClass, role string, global []string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if err := testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, role, "test_schema", []string{}, false); err != nil {
				return err
			}
			return testCheckDefaultPrivilegesACL(t, dbName, class, config.Username, role, "", global, false)
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				// EXECUTE of PUBLIC on functions is the built-in default: PUBLIC keeps it
				Config: testDefaultPrivilegesResource("test", dbName, config.Username, "public", "test_schema", "function", "[]"),
				Check:  check(functionClass, "public", []string{"EXECUTE"}),
			},
			{
				// SELECT granted globally: the role keeps it
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s GRANT SELECT ON TABLES TO %s", config.Username, roleName))
				},
				Config: testDefaultPrivilegesResource("test", dbName, config.Username, roleName, "test_schema", "table", "[]"),
				Check:  check(tableClass, roleName, []string{"SELECT"}),
			},
			{
				// Nothing is granted to the role globally
				PreConfig: func() {
					dbExecute(t, config.connStr(dbName), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s REVOKE ALL ON TABLES FROM %s", config.Username, roleName))
				},
				Config: testDefaultPrivilegesResource("test", dbName, config.Username, roleName, "test_schema", "table", "[]"),
				Check:  check(tableClass, roleName, []string{}),
			},
		},
	})
}

// The look at the privileges of a schema resource with `privileges = []` (the warning of
// warnDefaultPrivilegesRevokeNoEffect) only runs for that case, and fails on a role which does
// not exist before anything is revoked: the error tells which case ran.
func TestAccPostgresqlDefaultPrivilegesSchemaRevokeNoEffectUnknownRole(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	config := getTestConfig(t)
	dbName, roleName := getTestDBNames(dbSuffix)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				// schema + no privileges: the warning looks for the role first
				Config:      testDefaultPrivilegesResource("test", dbName, config.Username, "no_such_role", "test_schema", "table", "[]"),
				ExpectError: regexp.MustCompile("could not find oid for role no_such_role"),
			},
			{
				// ... then for the owner (a role which is not a superuser fails earlier, when it
				// checks whether the owner is one)
				Config:      testDefaultPrivilegesResource("test", dbName, "no_such_owner", roleName, "test_schema", "table", "[]"),
				ExpectError: regexp.MustCompile("could not (find oid for|check if) role no_such_owner"),
			},
			{
				// privileges given: no look at the privileges, the REVOKE fails first
				Config:      testDefaultPrivilegesResource("test", dbName, config.Username, "no_such_role", "test_schema", "table", `["SELECT"]`),
				ExpectError: regexp.MustCompile(`role "no_such_role" does not exist`),
			},
			{
				// no schema: no look at the privileges either
				Config:      testDefaultPrivilegesResource("test", dbName, config.Username, "no_such_role", "", "table", "[]"),
				ExpectError: regexp.MustCompile(`role "no_such_role" does not exist`),
			},
		},
	})
}
