package postgresql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestCreateGrantQuery(t *testing.T) {
	var databaseName = "foo"
	var roleName = "bar"
	var tableObjects = []any{"o1", "o2"}
	var tableColumns = []any{"col1", "col2"}
	var fdwObjects = []any{"baz"}

	cases := []struct {
		resource   *schema.ResourceData
		privileges []string
		expected   string
	}{
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "table",
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"SELECT"},
			expected:   fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "sequence",
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"SELECT"},
			expected:   fmt.Sprintf("GRANT SELECT ON ALL SEQUENCES IN SCHEMA %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "function",
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"EXECUTE"},
			expected:   fmt.Sprintf("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "procedure",
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"EXECUTE"},
			expected:   fmt.Sprintf("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "routine",
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"EXECUTE"},
			expected:   fmt.Sprintf("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type":       "TABLE",
				"schema":            databaseName,
				"role":              roleName,
				"with_grant_option": true,
			}),
			privileges: []string{"SELECT", "INSERT", "UPDATE"},
			expected:   fmt.Sprintf("GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA %s TO %s WITH GRANT OPTION", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "database",
				"database":    databaseName,
				"role":        roleName,
			}),
			privileges: []string{"CREATE"},
			expected:   fmt.Sprintf("GRANT CREATE ON DATABASE %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "database",
				"database":    databaseName,
				"role":        roleName,
			}),
			privileges: []string{"CREATE", "CONNECT"},
			expected:   fmt.Sprintf("GRANT CREATE,CONNECT ON DATABASE %s TO %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type":       "DATABASE",
				"database":          databaseName,
				"role":              roleName,
				"with_grant_option": true,
			}),
			privileges: []string{"ALL PRIVILEGES"},
			expected:   fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s WITH GRANT OPTION", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "table",
				"objects":     tableObjects,
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"SELECT"},
			expected:   fmt.Sprintf(`GRANT SELECT ON TABLE %[1]s."o2",%[1]s."o1" TO %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "column",
				"objects":     []any{"o1"},
				"columns":     tableColumns,
				"schema":      databaseName,
				"role":        roleName,
			}),
			privileges: []string{"SELECT"},
			expected:   fmt.Sprintf(`GRANT SELECT (%[2]s,%[3]s) ON TABLE %[1]s."o1" TO %[4]s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier("col2"), pq.QuoteIdentifier("col1"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "foreign_data_wrapper",
				"objects":     fdwObjects,
				"role":        roleName,
			}),
			privileges: []string{"USAGE"},
			expected:   fmt.Sprintf(`GRANT USAGE ON FOREIGN DATA WRAPPER "baz" TO %s`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type":       "foreign_data_wrapper",
				"objects":           fdwObjects,
				"role":              roleName,
				"with_grant_option": true,
			}),
			privileges: []string{"ALL PRIVILEGES"},
			expected:   fmt.Sprintf(`GRANT ALL PRIVILEGES ON FOREIGN DATA WRAPPER "baz" TO %s WITH GRANT OPTION`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "foreign_server",
				"objects":     fdwObjects,
				"role":        roleName,
			}),
			privileges: []string{"USAGE"},
			expected:   fmt.Sprintf(`GRANT USAGE ON FOREIGN SERVER "baz" TO %s`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type":       "foreign_server",
				"objects":           fdwObjects,
				"role":              roleName,
				"with_grant_option": true,
			}),
			privileges: []string{"ALL PRIVILEGES"},
			expected:   fmt.Sprintf(`GRANT ALL PRIVILEGES ON FOREIGN SERVER "baz" TO %s WITH GRANT OPTION`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "type",
				"schema":      databaseName,
				"objects":     []any{"t1"},
				"role":        roleName,
			}),
			privileges: []string{"USAGE"},
			expected:   fmt.Sprintf(`GRANT USAGE ON TYPE %s.%s TO %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier("t1"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "domain",
				"schema":      databaseName,
				"objects":     []any{"d1"},
				"role":        roleName,
			}),
			privileges: []string{"ALL"},
			expected:   fmt.Sprintf(`GRANT ALL ON DOMAIN %s.%s TO %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier("d1"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "language",
				"objects":     []any{"plpgsql"},
				"role":        roleName,
			}),
			privileges: []string{"USAGE"},
			expected:   fmt.Sprintf(`GRANT USAGE ON LANGUAGE %s TO %s`, pq.QuoteIdentifier("plpgsql"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type":       "large_object",
				"objects":           []any{"16384"},
				"role":              roleName,
				"with_grant_option": true,
			}),
			privileges: []string{"SELECT", "UPDATE"},
			expected:   fmt.Sprintf(`GRANT SELECT,UPDATE ON LARGE OBJECT 16384 TO %s WITH GRANT OPTION`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "parameter",
				"objects":     []any{"pgaudit.log"},
				"role":        roleName,
			}),
			privileges: []string{"SET", "ALTER SYSTEM"},
			expected:   fmt.Sprintf(`GRANT SET,ALTER SYSTEM ON PARAMETER %s TO %s`, pq.QuoteIdentifier("pgaudit.log"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "tablespace",
				"objects":     []any{"ts1"},
				"role":        roleName,
			}),
			privileges: []string{"CREATE"},
			expected:   fmt.Sprintf(`GRANT CREATE ON TABLESPACE %s TO %s`, pq.QuoteIdentifier("ts1"), pq.QuoteIdentifier(roleName)),
		},
	}

	for _, c := range cases {
		out := createGrantQuery(c.resource, c.privileges)
		if out != c.expected {
			t.Fatalf("error matching output and expected: %#v vs %#v", out, c.expected)
		}
	}
}

func TestCreateRevokeQuery(t *testing.T) {
	var databaseName = "foo"
	var roleName = "bar"
	var tableObjects = []any{"o1", "o2"}
	var tableColumns = []any{"col1", "col2"}
	var fdwObjects = []any{"baz"}

	cases := []struct {
		resource *schema.ResourceData
		expected string
	}{
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "table",
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "sequence",
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "function",
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "procedure",
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL PROCEDURES IN SCHEMA %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "routine",
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON ALL ROUTINES IN SCHEMA %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "database",
				"database":    databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON DATABASE %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "DATABASE",
				"database":    databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf("REVOKE ALL PRIVILEGES ON DATABASE %s FROM %s", pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "table",
				"objects":     tableObjects,
				"schema":      databaseName,
				"role":        roleName,
			}),
			expected: fmt.Sprintf(`REVOKE ALL PRIVILEGES ON TABLE %[1]s."o2",%[1]s."o1" FROM %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "table",
				"objects":     tableObjects,
				"schema":      databaseName,
				"role":        roleName,
				"privileges":  []any{"INSERT", "UPDATE"},
			}),
			expected: fmt.Sprintf(`REVOKE UPDATE,INSERT ON TABLE %[1]s."o2",%[1]s."o1" FROM %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "column",
				"objects":     []any{"o1"},
				"schema":      databaseName,
				"columns":     tableColumns,
				"role":        roleName,
				"privileges":  []any{"SELECT"},
			}),
			expected: fmt.Sprintf(`REVOKE SELECT ("col2","col1") ON TABLE %[1]s."o1" FROM %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "foreign_data_wrapper",
				"objects":     fdwObjects,
				"role":        roleName,
			}),
			expected: fmt.Sprintf(`REVOKE ALL PRIVILEGES ON FOREIGN DATA WRAPPER "baz" FROM %s`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "foreign_server",
				"objects":     fdwObjects,
				"role":        roleName,
			}),
			expected: fmt.Sprintf(`REVOKE ALL PRIVILEGES ON FOREIGN SERVER "baz" FROM %s`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "type",
				"schema":      databaseName,
				"objects":     []any{"t1"},
				"role":        roleName,
				"privileges":  []any{"USAGE"},
			}),
			expected: fmt.Sprintf(`REVOKE USAGE ON TYPE %s.%s FROM %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier("t1"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "domain",
				"schema":      databaseName,
				"objects":     []any{"d1"},
				"role":        roleName,
			}),
			expected: fmt.Sprintf(`REVOKE ALL PRIVILEGES ON DOMAIN %s.%s FROM %s`, pq.QuoteIdentifier(databaseName), pq.QuoteIdentifier("d1"), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "large_object",
				"objects":     []any{"16384"},
				"role":        roleName,
				"privileges":  []any{"SELECT"},
			}),
			expected: fmt.Sprintf(`REVOKE SELECT ON LARGE OBJECT 16384 FROM %s`, pq.QuoteIdentifier(roleName)),
		},
		{
			resource: schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
				"object_type": "tablespace",
				"objects":     []any{"ts1"},
				"role":        roleName,
				"privileges":  []any{"CREATE"},
			}),
			expected: fmt.Sprintf(`REVOKE CREATE ON TABLESPACE %s FROM %s`, pq.QuoteIdentifier("ts1"), pq.QuoteIdentifier(roleName)),
		},
	}

	for _, c := range cases {
		out := createRevokeQuery(c.resource.Get)
		if out != c.expected {
			t.Fatalf("error matching output and expected: %#v vs %#v", out, c.expected)
		}
	}
}

func TestAccPostgresqlGrant(t *testing.T) {
	skipIfNotAcc(t)

	// We have to create the database outside of resource.Test
	// because we need to create tables to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table", "test_schema.test_table2"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	var testGrant = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "table"
		privileges   = %%s
	}
	`, dbName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testGrant, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"postgresql_grant.test", "id", fmt.Sprintf("%s_%s_test_schema_table", roleName, dbName),
					),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "SELECT"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{"SELECT"})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["SELECT", "INSERT", "UPDATE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "3"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "INSERT"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.1", "SELECT"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.2", "UPDATE"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{"SELECT", "INSERT", "UPDATE"})
					},
				),
			},
			// We reapply the first step to be sure that extra privileges are correctly granted.
			{
				Config: fmt.Sprintf(testGrant, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "SELECT"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{"SELECT"})
					},
				),
			},
			// We test to revoke everything
			{
				Config: fmt.Sprintf(testGrant, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{})
					},
				),
			},
		},
	})
}

func TestAccPostgresqlGrantColumns(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	var testGrant = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "column"
		objects     = ["test_table"]
		columns     = %%s
		privileges  = %%s
	}
	`, dbName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testGrant, `["test_column_one", "test_column_two"]`, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "columns.#", "2"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "SELECT"),
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"SELECT"}, []string{"test_column_one"})
					},
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"SELECT"}, []string{"test_column_one", "test_column_two"})
					},
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{""}, []string{"test_column_one", "var"})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["test_column_one", "test_column_two"]`, `["INSERT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "columns.#", "2"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "columns.0", "test_column_one"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "INSERT"),
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"INSERT"}, []string{`"test_column_one"`})
					},
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"INSERT"}, []string{`"test_column_one"`, `"test_column_two"`})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["test_column_one", "test_column_two"]`, `["UPDATE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "columns.#", "2"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "columns.0", "test_column_one"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "UPDATE"),

					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"UPDATE"}, []string{"test_column_one"})
					},
					func(*terraform.State) error {
						return testCheckColumnPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"UPDATE"}, []string{"test_column_one", "test_column_two"})
					},
				),
			},
		},
	})
}

func TestAccPostgresqlGrantObjects(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table", "test_schema.test_table2"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	var testGrant = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "table"
		objects     = %%s
		privileges  = ["SELECT"]
	}
	`, dbName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testGrant, `["test_table"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"postgresql_grant.test", "id", fmt.Sprintf("%s_%s_test_schema_table_test_table", roleName, dbName),
					),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"SELECT"})
					},
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{testTables[1]}, []string{})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["test_table", "test_table2"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "2"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.1", "test_table2"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{"SELECT"})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["test_table"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{testTables[0]}, []string{"SELECT"})
					},
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{testTables[1]}, []string{})
					},
				),
			},
			{
				// Empty list means that privileges will be applied on all tables.
				Config: fmt.Sprintf(testGrant, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "0"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{"SELECT"})
					},
				),
			},
			{
				Config:  fmt.Sprintf(testGrant, `[]`),
				Destroy: true,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "0"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{})
					},
				),
			},
		},
	})
}

func TestAccPostgresqlGrantObjectsError(t *testing.T) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					object_type = "database"
					objects     = ["o1", "o2"]
					privileges  = ["CONNECT"]
				}`,
				ExpectError: regexp.MustCompile("cannot specify `objects` when `object_type` is `database` or `schema`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					schema      = "test_schema"
					role        = "test_role"
					object_type = "schema"
					objects     = ["o1", "o2"]
					privileges  = ["CONNECT"]
				}`,
				ExpectError: regexp.MustCompile("cannot specify `objects` when `object_type` is `database` or `schema`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					object_type = "foreign_data_wrapper"
					objects     = ["o1", "o2"]
					privileges  = ["USAGE"]
				}`,
				ExpectError: regexp.MustCompile("one element must be specified in `objects` when `object_type` is `foreign_data_wrapper` or `foreign_server`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					object_type = "foreign_server"
					objects     = ["o1", "o2"]
					privileges  = ["USAGE"]
				}`,
				ExpectError: regexp.MustCompile("one element must be specified in `objects` when `object_type` is `foreign_data_wrapper` or `foreign_server`"),
			},
		},
	})
}

func TestAccPostgresqlGrantColumnsError(t *testing.T) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					schema      = "test_schema"
					object_type = "column"
					objects     = ["o1", "o2"]
					columns     = ["col1", "col2"]
					privileges  = ["SELECT"]
				}`,
				ExpectError: regexp.MustCompile("must specify exactly 1 table in the `objects` field when `object_type` is `column`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					schema      = "test_schema"
					object_type = "column"
					objects     = ["o1"]
					columns     = ["col1", "col2"]
					privileges  = ["SELECT", "INSERT"]
				}`,
				ExpectError: regexp.MustCompile("must specify exactly 1 `privileges` when `object_type` is `column`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					schema      = "test_schema"
					object_type = "column"
					objects     = ["o1"]
					privileges  = ["SELECT"]
				}`,
				ExpectError: regexp.MustCompile("must specify `columns` when `object_type` is `column`"),
			},
			{
				Config: `resource "postgresql_grant" "test" {
					database    = "test_db"
					role        = "test_role"
					schema      = "test_schema"
					object_type = "table"
					objects     = ["o1"]
					columns     = ["col1", "col2"]
					privileges  = ["SELECT"]
				}`,
				ExpectError: regexp.MustCompile("cannot specify `columns` when `object_type` is not `column`"),
			},
		},
	})
}

func TestAccPostgresqlGrantPublic(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	// We have to create the database outside of resource.Test
	// because we need to create tables to assert that grant are correctly applied
	// and we don't have this resource yet
	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	// create another role (first one is created in setupTestDatabase)
	// to assert that PUBLIC is applied to everyone
	role2 := fmt.Sprintf("tf_tests_role2_%s", dbSuffix)
	createTestRole(t, role2)
	dbExecute(t, config.connStr(dbName), fmt.Sprintf("GRANT usage ON SCHEMA test_schema to %s", role2))

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	var testGrant = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "public"
		schema      = "test_schema"
		object_type = "table"
		privileges   = %%s
	}
	`, dbName)

	// Wrapper to testCheckTablesPrivileges to test for both roles
	checkTablePrivileges := func(expectedPrivileges []string) error {
		if err := testCheckTablesPrivileges(t, dbName, roleName, testTables, expectedPrivileges); err != nil {
			return err
		}
		return testCheckTablesPrivileges(t, dbName, role2, testTables, expectedPrivileges)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testGrant, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"postgresql_grant.test", "id", fmt.Sprintf("public_%s_test_schema_table", dbName),
					),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					func(*terraform.State) error {
						return checkTablePrivileges([]string{"SELECT"})
					},
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["SELECT", "INSERT", "UPDATE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "3"),
					func(*terraform.State) error {
						return checkTablePrivileges([]string{"SELECT", "INSERT", "UPDATE"})
					},
				),
			},
			// We reapply the first step to be sure that extra privileges are correctly granted.
			{
				Config: fmt.Sprintf(testGrant, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					func(*terraform.State) error {
						return checkTablePrivileges([]string{"SELECT"})
					},
				),
			},
			// We test to revoke everything
			{
				Config: fmt.Sprintf(testGrant, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					func(*terraform.State) error {
						return checkTablePrivileges([]string{})
					},
				),
			},
		},
	})
}

func TestAccPostgresqlGrantEmptyPrivileges(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table", "test_schema.test_table2"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	// Grant some privileges on this table to our role to assert that they will be revoked
	dbExecute(t, config.connStr(dbName), fmt.Sprintf("GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA test_schema TO %s", roleName))

	var tfConfig = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "table"
		privileges   = []
	}
	`, dbName, roleName)

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
					resource.TestCheckResourceAttr(
						"postgresql_grant.test", "id", fmt.Sprintf("%s_%s_test_schema_table", roleName, dbName),
					),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, testTables, []string{})
					},
				),
			},
		},
	})
}

func TestAccPostgresqlGrantFunction(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test role and a schema as public has too wide open privileges
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_schema TO test_role")
	dbExecute(t, dsn, "ALTER DEFAULT PRIVILEGES REVOKE ALL ON FUNCTIONS FROM PUBLIC")

	// Create test function in this schema
	dbExecute(t, dsn, `
CREATE FUNCTION test_schema.test() RETURNS text
	AS $$ select 'foo'::text $$
    LANGUAGE SQL;
`)
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA test_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE test_role")
	}()

	// Test to grant directly to test_role and to public
	// in both case test_case should have the right
	for _, role := range []string{"test_role", "public"} {
		t.Run(role, func(t *testing.T) {

			tfConfig := fmt.Sprintf(`
resource postgresql_grant "test" {
  database    = "postgres"
  role        = "%s"
  schema      = "test_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
}
	`, role)

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
							resource.TestCheckResourceAttr("postgresql_grant.test", "id", fmt.Sprintf("%s_postgres_test_schema_function", role)),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "EXECUTE"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
							testCheckFunctionExecutable(t, "test_role", "test_schema.test"),
						),
					},
				},
			})
		})
	}
}

func TestAccPostgresqlGrantFunctionWithArgs(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test role and a schema as public has too wide open privileges
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_schema TO test_role")
	dbExecute(t, dsn, "ALTER DEFAULT PRIVILEGES REVOKE ALL ON FUNCTIONS FROM PUBLIC")

	// Create test function in this schema
	dbExecute(t, dsn, `
CREATE FUNCTION test_schema.test(arg1 text, arg2 character) RETURNS text
	AS $$ select 'foo'::text $$
    LANGUAGE SQL;
`)
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA test_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE test_role")
	}()

	// Test to grant directly to test_role and to public
	// in both case test_case should have the right
	for _, role := range []string{"test_role", "public"} {
		t.Run(role, func(t *testing.T) {

			tfConfig := fmt.Sprintf(`
resource postgresql_grant "test" {
  database    = "postgres"
  role        = "%s"
  schema      = "test_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects 	  = ["test(text, char)"]
}
	`, role)

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
							resource.TestCheckResourceAttr("postgresql_grant.test", "id", fmt.Sprintf("%s_postgres_test_schema_function_test(text, char)", role)),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "EXECUTE"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
							testCheckFunctionWithArgsExecutable(t, "test_role", "test_schema.test", []string{pq.QuoteLiteral("value 1"), pq.QuoteLiteral("value 2")}),
						),
					},
				},
			})
		})
	}
}

func TestAccPostgresqlGrantProcedure(t *testing.T) {
	skipIfNotAcc(t)
	testCheckCompatibleVersion(t, featureProcedure)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test role and a schema as public has too wide open privileges
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_schema TO test_role")
	dbExecute(t, dsn, "ALTER DEFAULT PRIVILEGES REVOKE ALL ON FUNCTIONS FROM PUBLIC")

	// Create test procedure in this schema
	dbExecute(t, dsn, `
CREATE PROCEDURE test_schema.test()
	AS $$ select 'foo'::text $$
    LANGUAGE SQL;
`)
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA test_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE test_role")
	}()

	// Test to grant directly to test_role and to public
	// in both case test_case should have the right
	for _, role := range []string{"test_role", "public"} {
		t.Run(role, func(t *testing.T) {

			tfConfig := fmt.Sprintf(`
resource postgresql_grant "test" {
  database    = "postgres"
  role        = "%s"
  schema      = "test_schema"
  object_type = "procedure"
  privileges  = ["EXECUTE"]
}
	`, role)

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
							resource.TestCheckResourceAttr("postgresql_grant.test", "id", fmt.Sprintf("%s_postgres_test_schema_procedure", role)),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "EXECUTE"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
							testCheckProcedureExecutable(t, "test_role", "test_schema.test"),
						),
					},
				},
			})
		})
	}
}

func TestAccPostgresqlGrantRoutine(t *testing.T) {
	skipIfNotAcc(t)
	testCheckCompatibleVersion(t, featureRoutine)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test role and a schema as public has too wide open privileges
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_schema TO test_role")
	dbExecute(t, dsn, "ALTER DEFAULT PRIVILEGES REVOKE ALL ON FUNCTIONS FROM PUBLIC")

	// Create test function in this schema
	dbExecute(t, dsn, `
CREATE FUNCTION test_schema.test_function() RETURNS text
	AS $$ select 'foo'::text $$
    LANGUAGE SQL;
`)
	// Create test procedure in this schema
	dbExecute(t, dsn, `
CREATE PROCEDURE test_schema.test_procedure()
	AS $$ select 'foo'::text $$
    LANGUAGE SQL;
`)
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA test_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE test_role")
	}()

	// Test to grant directly to test_role and to public
	// in both case test_case should have the right
	for _, role := range []string{"test_role", "public"} {
		t.Run(role, func(t *testing.T) {

			tfConfigRoutine := fmt.Sprintf(`
resource postgresql_grant "test" {
  database    = "postgres"
  role        = "%s"
  schema      = "test_schema"
  object_type = "routine"
  privileges  = ["EXECUTE"]
}
	`, role)

			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: tfConfigRoutine,
						Check: resource.ComposeTestCheckFunc(
							resource.TestCheckResourceAttr("postgresql_grant.test", "id", fmt.Sprintf("%s_postgres_test_schema_routine", role)),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.0", "EXECUTE"),
							resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
							testCheckFunctionExecutable(t, "test_role", "test_schema.test_function"),
							testCheckProcedureExecutable(t, "test_role", "test_schema.test_procedure"),
						),
					},
				},
			})
		})
	}
}

func TestAccPostgresqlGrantDatabase(t *testing.T) {
	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	config := fmt.Sprintf(`
resource "postgresql_role" "test" {
	name     = "test_grant_role"
	password = "%s"
	login    = true
}

resource "postgresql_database" "test_db" {
	depends_on = [postgresql_role.test]
	name = "test_grant_db"
}

resource "postgresql_grant" "test" {
	database    = postgresql_database.test_db.name
	role        = postgresql_role.test.name
	object_type = "database"
	privileges  = %%s
}
`, testRolePassword)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			// Not allowed to create
			{
				Config: fmt.Sprintf(config, `["CONNECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "id", "test_grant_role_test_grant_db_database"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
					testCheckDatabasesPrivileges(t, false),
				),
			},
			// Can create but not grant
			{
				Config: fmt.Sprintf(config, `["CONNECT", "CREATE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "2"),
					testCheckDatabasesPrivileges(t, true),
				),
			},
			// Revoke
			{
				Config: fmt.Sprintf(config, "[]"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					testCheckDatabasesPrivileges(t, false),
				),
			},
		},
	})
}

func TestAccPostgresqlImplicitGrants(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	var testGrant = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "table"
		objects     = ["test_table"]
		privileges  = %%s
	}
	`, dbName, roleName)

	var testCheckTableGrants = func(grants ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			return testCheckTablesPrivileges(t, dbName, roleName, []string{testTables[0]}, grants)
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
				Config: fmt.Sprintf(testGrant, `["ALL"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"postgresql_grant.test", "id", fmt.Sprintf("%s_%s_test_schema_table_test_table", roleName, dbName),
					),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					testCheckTableGrants("SELECT", "INSERT", "UPDATE", "DELETE"),
				),
			},
			{
				Config: fmt.Sprintf(testGrant, `["SELECT"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					testCheckTableGrants("SELECT"),
				),
			},
			{
				// Empty list means that privileges will be applied on all tables.
				Config: fmt.Sprintf(testGrant, `["SELECT", "INSERT", "UPDATE", "DELETE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					testCheckTableGrants("SELECT", "INSERT", "UPDATE", "DELETE"),
				),
			},
			{
				Config:  fmt.Sprintf(testGrant, `[]`),
				Destroy: true,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "objects.0", "test_table"),
					testCheckTableGrants(""),
				),
			},
		},
	})
}

func TestAccPostgresqlGrantSchema(t *testing.T) {
	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	config := fmt.Sprintf(`
resource "postgresql_role" "test" {
	name     = "test_grant_role"
	password = "%s"
	login    = true
}

resource "postgresql_schema" "test_schema" {
	depends_on   = [postgresql_role.test]
	name         = "test_schema"
	drop_cascade = true
}

resource "postgresql_grant" "test" {
	database    = "postgres"
	schema      = postgresql_schema.test_schema.name
	role        = postgresql_role.test.name
	object_type = "schema"
	privileges  = %%s
}
`, testRolePassword)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(config, `["USAGE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "id", "test_grant_role_postgres_test_schema_schema"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
					testCheckSchemaPrivileges(t, true, false),
				),
			},
			{
				Config: fmt.Sprintf(config, `["USAGE", "CREATE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "2"),
					testCheckSchemaPrivileges(t, true, true),
				),
			},
			{
				//Config: fmt.Sprintf(config, "[]"),
				Config: fmt.Sprintf(config, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					testCheckSchemaPrivileges(t, false, false),
				),
			},
		},
	})
}

func TestAccPostgresqlGrantForeignDataWrapper(t *testing.T) {
	skipIfNotAcc(t)
	skipIfNotSuperuser(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test foreign data wrapper
	dbExecute(t, dsn, "CREATE FOREIGN DATA WRAPPER test_fdw")

	defer func() {
		// Cleanup
		dbExecute(t, dsn, "DROP FOREIGN DATA WRAPPER test_fdw CASCADE")
	}()

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	tfConfig := fmt.Sprintf(`
resource "postgresql_role" "test" {
	name     = "test_role"
	password = "%s"
	login    = true
}

resource "postgresql_grant" "test" {
	depends_on           = [postgresql_role.test]
	database             = "postgres"
	role                 = postgresql_role.test.name
	object_type          = "foreign_data_wrapper"
	objects              = ["test_fdw"]
	privileges           = %%s
	with_grant_option    = %%s
}
`, testRolePassword)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			// Grant usage
			{
				Config: fmt.Sprintf(tfConfig, `["USAGE"]`, `true`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "id", "test_role_postgres_foreign_data_wrapper_test_fdw"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "true"),
					testCheckForeignDataWrapperPrivileges(t, true),
				),
			},
			// Revoke all privileges
			{
				Config: fmt.Sprintf(tfConfig, `[]`, `false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					testCheckForeignDataWrapperPrivileges(t, false),
				),
			},
		},
	})
}

func TestAccPostgresqlGrantForeignServer(t *testing.T) {
	skipIfNotAcc(t)
	skipIfNotSuperuser(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// Create a test foreign data wrapper
	dbExecute(t, dsn, "CREATE FOREIGN DATA WRAPPER test_fdw")
	dbExecute(t, dsn, "CREATE SERVER test_srv FOREIGN DATA WRAPPER test_fdw")

	defer func() {
		// Cleanup
		dbExecute(t, dsn, "DROP FOREIGN DATA WRAPPER test_fdw CASCADE")
	}()

	// create a TF config with placeholder for privileges
	// it will be filled in each step.
	tfConfig := fmt.Sprintf(`
resource "postgresql_role" "test" {
	name     = "test_role"
	password = "%s"
	login    = true
}

// From PostgreSQL 15, schema public is not wild open anymore
resource "postgresql_grant" "public_usage" {
	database          = "postgres"
	schema            = "public"
	role              = postgresql_role.test.name
	object_type       = "schema"
	privileges        = ["CREATE", "USAGE"]
}

resource "postgresql_grant" "test" {
	depends_on        = [postgresql_role.test]
	database          = "postgres"
	role              = postgresql_role.test.name
	object_type       = "foreign_server"
	objects           = ["test_srv"]
	privileges        = %%s
	with_grant_option = %%s
}
`, testRolePassword)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			// Grant usage
			{
				Config: fmt.Sprintf(tfConfig, `["USAGE"]`, `false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "id", "test_role_postgres_foreign_server_test_srv"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", "false"),
					testCheckForeignServerPrivileges(t, true),
				),
			},
			// Revoke all privileges
			{
				Config: fmt.Sprintf(tfConfig, `[]`, `false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					testCheckForeignServerPrivileges(t, false),
				),
			},
		},
	})
}

func TestAccPostgresqlGrantOwnerPG15(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	testTables := []string{"test_schema.test_table"}
	createTestTables(t, dbSuffix, testTables, "")

	dbName, roleName := getTestDBNames(dbSuffix)

	var tfConfig = fmt.Sprintf(`
	resource "postgresql_grant" "test" {
		database    = "%s"
		role        = "%s"
		schema      = "test_schema"
		object_type = "table"
		privileges  = ["SELECT"]
	}`, dbName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureDatabaseOwnerRole)

			// Change the owner to the new pg_database_owner role
			func() {
				config := getTestConfig(t)
				db, err := sql.Open(testDriverName, config.connStr(dbName))
				if err != nil {
					t.Fatalf("could not connect to database %s: %v", dbName, err)
				}
				defer closeDB(t, db)

				if _, err := db.Exec(`
					ALTER SCHEMA test_schema OWNER TO pg_database_owner;
					ALTER TABLE test_schema.test_table OWNER TO pg_database_owner;
				`); err != nil {
					t.Fatalf("could not alter owner of test_table (as %s): %v", config.Username, err)
				}

			}()
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: tfConfig,
				Check: resource.ComposeTestCheckFunc(
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{"test_schema.test_table"}, []string{"SELECT"})
					},
				),
			},
			{
				Config:  tfConfig,
				Destroy: true,
				Check: resource.ComposeTestCheckFunc(
					func(*terraform.State) error {
						return testCheckTablesPrivileges(t, dbName, roleName, []string{"test_schema.test_table"}, []string{})
					},
				),
			},
		},
	})
}

func testCheckDatabasesPrivileges(t *testing.T, canCreate bool) func(*terraform.State) error {
	return func(*terraform.State) error {
		db := connectAsTestRole(t, "test_grant_role", "test_grant_db")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, "CREATE SCHEMA plop", canCreate); err != nil {
			return err
		}
		return nil
	}
}

func testCheckFunctionExecutable(t *testing.T, role, function string) func(*terraform.State) error {
	return func(*terraform.State) error {
		db := connectAsTestRole(t, role, "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, fmt.Sprintf("SELECT %s()", function), true); err != nil {
			return err
		}
		return nil
	}
}

func testCheckFunctionWithArgsExecutable(t *testing.T, role, function string, args []string) func(*terraform.State) error {
	return func(*terraform.State) error {
		db := connectAsTestRole(t, role, "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, fmt.Sprintf("SELECT %s(%s)", function, strings.Join(args, ", ")), true); err != nil {
			return err
		}
		return nil
	}
}

func testCheckProcedureExecutable(t *testing.T, role, procedure string) func(*terraform.State) error {
	return func(*terraform.State) error {
		db := connectAsTestRole(t, role, "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, fmt.Sprintf("CALL %s()", procedure), true); err != nil {
			return err
		}
		return nil
	}
}

func testCheckSchemaPrivileges(t *testing.T, usage, create bool) func(*terraform.State) error {
	return func(*terraform.State) error {
		config := getTestConfig(t)
		dsn := config.connStr("postgres")

		// Create a table in the schema to check if user has usage privilege
		dbExecute(t, dsn, "CREATE TABLE IF NOT EXISTS test_schema.test_usage (id serial)")
		defer func() {
			dbExecute(t, dsn, "DROP TABLE IF EXISTS test_schema.test_create")
		}()
		dbExecute(t, dsn, "GRANT SELECT ON test_schema.test_usage TO test_grant_role")

		db := connectAsTestRole(t, "test_grant_role", "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, "SELECT 1 FROM test_schema.test_usage", usage); err != nil {
			return err
		}

		if err := testHasGrantForQuery(db, "CREATE TABLE test_schema.test_create (id serial)", create); err != nil {
			return err
		}

		return nil
	}
}

func testCheckForeignDataWrapperPrivileges(t *testing.T, usage bool) func(*terraform.State) error {
	return func(*terraform.State) error {
		config := getTestConfig(t)
		dsn := config.connStr("postgres")

		defer func() {
			dbExecute(t, dsn, "DROP SERVER IF EXISTS test_srv")
		}()
		db := connectAsTestRole(t, "test_role", "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, "CREATE SERVER test_srv FOREIGN DATA WRAPPER test_fdw", usage); err != nil {
			return err
		}

		return nil
	}
}

func testCheckForeignServerPrivileges(t *testing.T, usage bool) func(*terraform.State) error {
	return func(*terraform.State) error {
		config := getTestConfig(t)
		dsn := config.connStr("postgres")

		defer func() {
			dbExecute(t, dsn, "DROP FOREIGN TABLE IF EXISTS test_tbl")
		}()
		db := connectAsTestRole(t, "test_role", "postgres")
		defer closeDB(t, db)

		if err := testHasGrantForQuery(db, "CREATE FOREIGN TABLE test_tbl() SERVER test_srv", usage); err != nil {
			return err
		}

		return nil
	}
}

// Privileges granted on the same object to several roles at the same time:
// the grants are applied in parallel by Terraform and update the same row.
func TestAccPostgresqlGrant_ConcurrentOnSameObject(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	config := fmt.Sprintf(`
resource "postgresql_role" "concurrent" {
  count = 12
  name  = "concurrent_grant_${count.index}"
}

resource "postgresql_grant" "schema" {
  count       = 12
  database    = "%[1]s"
  role        = postgresql_role.concurrent[count.index].name
  schema      = "test_schema"
  object_type = "schema"
  privileges  = ["USAGE"]
}

resource "postgresql_grant" "database" {
  count       = 12
  database    = "%[1]s"
  role        = postgresql_role.concurrent[count.index].name
  object_type = "database"
  privileges  = ["CONNECT"]
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.schema.11", "privileges.#", "1"),
					resource.TestCheckResourceAttr("postgresql_grant.database.11", "privileges.#", "1"),
				),
			},
		},
	})
}

// grantedObjectCase describes a grant on an object for the matrix and named object tests.
type grantedObjectCase struct {
	// objectType is the object_type value of the resource
	objectType string
	// schema is the schema attribute ("" when not set)
	schema string
	// objects is the HCL value of the objects attribute
	objects string
	// columns is the HCL value of the columns attribute ("" when not set)
	columns string
	// database is the database of the grant (postgres by default)
	database string
	// sqlObject is the object as written after ON in a GRANT statement
	sqlObject string
	// revoke revokes all the privileges of the object outside of Terraform, %s is the grantee
	// (default: REVOKE ALL ON sqlObject)
	revoke string
	// drift grants a privilege outside of Terraform, %s is the grantee
	drift string
	// removeByDestroy is set when the privileges cannot be emptied: the removal is the destruction of the resource
	removeByDestroy bool
	// preCheck skips the test when the object cannot be created or granted in the environment
	preCheck func(t *testing.T)
	// setup and teardown are run as the test user around the test, teardown must be idempotent
	setup, teardown []string
	// each are the privileges granted one at a time, the first one is the reduction target
	each []string
	// full are the privileges of the extension (HCL list), fullACL are the privileges read back for it
	full    []string
	fullACL []string
	// privileges are all the privileges of the object which are not ALL
	privileges []string
	// aclQuery returns the privileges of the object for the grantee given by %[1]s, with * for the grant option
	aclQuery string
	// hasQuery returns the effective privilege of the test role, %[1]s is the role and %[2]s the privilege
	hasQuery string
}

// namedObjectGrantCase describes an object type which has no ALL ... IN SCHEMA form: the objects
// are named in `objects`.
type namedObjectGrantCase struct {
	grantedObjectCase
	// partial and partialACL are the privileges of the first steps and of the catalogs for them
	partial, partialACL []string
}

// testPostgresDSN returns the connection string of the postgres database of the tests.
func testPostgresDSN(t *testing.T) string {
	config := getTestConfig(t)
	return config.connStr("postgres")
}

// testExecSession runs the statements one after the other in the same session, which is needed
// for the settings which apply to the following statement (e.g.: allow_in_place_tablespaces).
func testExecSession(t *testing.T, dsn string, statements ...string) {
	db, err := sql.Open(testDriverName, dsn)
	if err != nil {
		t.Fatalf("could not create connection pool: %v", err)
	}
	defer closeDB(t, db)

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("could not get a connection: %v", err)
	}
	defer func() { _ = conn.Close() }()

	for _, statement := range statements {
		if _, err := conn.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("could not execute query %s: %v", statement, err)
		}
	}
}

// testRoleOnlyConfig returns the configuration of the role of the tests, without grant.
func testRoleOnlyConfig() string {
	return fmt.Sprintf(`
resource "postgresql_role" "test" {
  name     = "test_grant_role"
  password = "%s"
  login    = true
}
`, testRolePassword)
}

// grantedObjectConfig returns the configuration of a grant on the object for the role (or PUBLIC).
func grantedObjectConfig(c grantedObjectCase, role string, privileges []string, withOption bool, objects string) string {
	roleExpr := `postgresql_role.test.name`
	if role == publicRole {
		roleExpr = `"public"`
	}
	database := c.database
	if database == "" {
		database = "postgres"
	}
	optionalLines := ""
	if c.schema != "" {
		optionalLines += fmt.Sprintf("  schema            = %q\n", c.schema)
	}
	if c.columns != "" {
		optionalLines += fmt.Sprintf("  columns           = %s\n", c.columns)
	}
	return fmt.Sprintf(`
resource "postgresql_role" "test" {
  name     = "test_grant_role"
  password = "%s"
  login    = true
}

resource "postgresql_grant" "test" {
  depends_on        = [postgresql_role.test]
  database          = "%s"
  role              = %s
%s  object_type       = "%s"
  objects           = %s
  privileges        = %s
  with_grant_option = %t
}
`, testRolePassword, database, roleExpr, optionalLines, c.objectType, objects, hclStringList(privileges), withOption)
}

func hclStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// testCheckGrantedObject checks the privileges of the object in the catalogs, and the effective
// privileges of the test role, against the expected privileges. With the grant option, every
// privilege of the catalogs is expected with a *.
func testCheckGrantedObject(t *testing.T, c grantedObjectCase, role string, expected []string, withOption bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		db, err := sql.Open(testDriverName, testPostgresDSN(t))
		if err != nil {
			t.Fatalf("could not open connection pool: %v", err)
		}
		defer closeDB(t, db)

		grantee := "(SELECT oid FROM pg_roles WHERE rolname = 'test_grant_role')::oid"
		if role == publicRole {
			grantee = "0::oid"
		}
		var granted pq.StringArray
		if err := db.QueryRow(fmt.Sprintf(c.aclQuery, grantee)).Scan(&granted); err != nil {
			return fmt.Errorf("could not read the privileges of %s: %w", c.sqlObject, err)
		}

		want := []string{}
		for _, p := range expected {
			if withOption {
				p += "*"
			}
			want = append(want, p)
		}
		if !sameStrings(granted, want) {
			return fmt.Errorf("the catalogs have the privileges %v on %s for %s, expected %v", granted, c.sqlObject, role, want)
		}

		for _, p := range c.privileges {
			has, err := testEffectivePrivilege(t, c, p)
			if err != nil {
				return err
			}
			if has != sliceContainsStr(expected, p) {
				return fmt.Errorf("effective privilege %s on %s is %t, expected %t", p, c.sqlObject, has, !has)
			}
		}
		return nil
	}
}

// testEffectivePrivilege returns the effective privilege of the test role on the object.
func testEffectivePrivilege(t *testing.T, c grantedObjectCase, privilege string) (bool, error) {
	if c.objectType == "large_object" {
		// PostgreSQL has no has_largeobject_privilege function: the role has to use the object.
		db := connectAsTestRole(t, "test_grant_role", "postgres")
		defer closeDB(t, db)
		query := "SELECT lo_get(9001)"
		if privilege == "UPDATE" {
			query = "SELECT lo_put(9001, 0, ''::bytea)"
		}
		_, err := db.Exec(query)
		return err == nil, nil
	}

	db, err := sql.Open(testDriverName, testPostgresDSN(t))
	if err != nil {
		t.Fatalf("could not open connection pool: %v", err)
	}
	defer closeDB(t, db)

	var has bool
	if err := db.QueryRow(fmt.Sprintf(c.hasQuery, "test_grant_role", privilege)).Scan(&has); err != nil {
		return false, fmt.Errorf("could not check the privilege %s: %w", privilege, err)
	}
	return has, nil
}

func sameStrings(a, b []string) bool {
	x := append([]string{}, a...)
	y := append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	return fmt.Sprint(x) == fmt.Sprint(y)
}

// grantedObjectChecks checks the grant with the object in the catalogs and the effective privileges.
func grantedObjectChecks(t *testing.T, c grantedObjectCase, role string, expected []string, withOption bool) resource.TestCheckFunc {
	return resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttr("postgresql_grant.test", "with_grant_option", fmt.Sprint(withOption)),
		testCheckGrantedObject(t, c, role, expected, withOption),
	)
}

// testAccPostgresqlGrantRunSteps runs the resource with the steps, after the setup, and the teardown.
func testAccPostgresqlGrantRunSteps(t *testing.T, c grantedObjectCase, steps []resource.TestStep) {
	dsn := testPostgresDSN(t)
	defer testExecSession(t, dsn, c.teardown...)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			if c.preCheck != nil {
				c.preCheck(t)
			}
			testExecSession(t, dsn, c.setup...)
		},
		Providers: testAccProviders,
		Steps:     steps,
	})
}

// testAccPostgresqlGrantRevokeDrift returns the steps which revoke the privileges outside of
// Terraform, check that the plan detects it, and apply the grant again.
func testAccPostgresqlGrantRevokeDrift(t *testing.T, c grantedObjectCase, role string, config, expected []string, withOption bool) []resource.TestStep {
	revoke := c.revoke
	if revoke == "" {
		revoke = "REVOKE ALL ON " + c.sqlObject + " FROM %s"
	}
	return []resource.TestStep{
		{
			PreConfig: func() {
				dbExecute(t, testPostgresDSN(t), fmt.Sprintf(revoke, grantRoleSQL(role)))
			},
			Config:             grantedObjectConfig(c, role, config, withOption, c.objects),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		{
			Config: grantedObjectConfig(c, role, config, withOption, c.objects),
			Check:  grantedObjectChecks(t, c, role, expected, withOption),
		},
	}
}

// testAccPostgresqlGrantNamedObject runs the steps of the object types which are named in `objects`:
// creation, drift detected by the plan, extension, grant option in both directions, reduction and
// removal. The role is either a role or PUBLIC, which cannot have a grant option.
func testAccPostgresqlGrantNamedObject(t *testing.T, c namedObjectGrantCase, role string) {
	withOption := role != publicRole

	steps := []resource.TestStep{
		{
			// objects are mandatory: PostgreSQL has no ALL ... IN SCHEMA form for these object types
			Config:      grantedObjectConfig(c.grantedObjectCase, role, c.partial, false, `[]`),
			ExpectError: regexp.MustCompile("must specify `objects`"),
		},
		{
			Config: grantedObjectConfig(c.grantedObjectCase, role, c.partial, false, c.objects),
			Check:  grantedObjectChecks(t, c.grantedObjectCase, role, c.partialACL, false),
		},
	}
	steps = append(steps, testAccPostgresqlGrantRevokeDrift(t, c.grantedObjectCase, role, c.partial, c.partialACL, false)...)

	if c.drift != "" {
		steps = append(steps,
			resource.TestStep{
				// privileges granted outside of Terraform are detected by the plan
				PreConfig: func() {
					dbExecute(t, testPostgresDSN(t), fmt.Sprintf(c.drift, grantRoleSQL(role)))
				},
				Config:             grantedObjectConfig(c.grantedObjectCase, role, c.partial, false, c.objects),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			resource.TestStep{
				Config: grantedObjectConfig(c.grantedObjectCase, role, c.partial, false, c.objects),
				Check:  grantedObjectChecks(t, c.grantedObjectCase, role, c.partialACL, false),
			},
		)
	}

	if withOption {
		steps = append(steps, resource.TestStep{
			// extension of the privileges with the grant option (replaces the grant)
			Config: grantedObjectConfig(c.grantedObjectCase, role, c.full, true, c.objects),
			Check:  grantedObjectChecks(t, c.grantedObjectCase, role, c.fullACL, true),
		})
	}
	steps = append(steps, resource.TestStep{
		// removal of the grant option (replaces the grant) for a role, extension for PUBLIC
		Config: grantedObjectConfig(c.grantedObjectCase, role, c.full, false, c.objects),
		Check:  grantedObjectChecks(t, c.grantedObjectCase, role, c.fullACL, false),
	}, resource.TestStep{
		// reduction of the privileges
		Config: grantedObjectConfig(c.grantedObjectCase, role, c.partial, false, c.objects),
		Check:  grantedObjectChecks(t, c.grantedObjectCase, role, c.partialACL, false),
	}, resource.TestStep{
		// removal: everything is revoked
		Config: grantedObjectConfig(c.grantedObjectCase, role, []string{}, false, c.objects),
		Check:  grantedObjectChecks(t, c.grantedObjectCase, role, []string{}, false),
	})

	testAccPostgresqlGrantRunSteps(t, c.grantedObjectCase, steps)
}

// testAccPostgresqlGrantPrivileges grants each privilege of an object type alone, then all of them
// with and without the grant option, then reduces and removes them. The catalogs and the effective
// privileges are checked after each step.
func testAccPostgresqlGrantPrivileges(t *testing.T, c grantedObjectCase, role string) {
	withOption := role != publicRole

	var steps []resource.TestStep
	for i, privilege := range c.each {
		steps = append(steps, resource.TestStep{
			Config: grantedObjectConfig(c, role, []string{privilege}, false, c.objects),
			Check:  grantedObjectChecks(t, c, role, []string{privilege}, false),
		})
		if i == 0 {
			steps = append(steps, testAccPostgresqlGrantRevokeDrift(t, c, role, []string{privilege}, []string{privilege}, false)...)
		}
	}

	if withOption {
		steps = append(steps, resource.TestStep{
			Config: grantedObjectConfig(c, role, []string{"ALL"}, true, c.objects),
			Check:  grantedObjectChecks(t, c, role, c.fullACL, true),
		})
	}
	steps = append(steps, resource.TestStep{
		Config: grantedObjectConfig(c, role, []string{"ALL"}, false, c.objects),
		Check:  grantedObjectChecks(t, c, role, c.fullACL, false),
	}, resource.TestStep{
		Config: grantedObjectConfig(c, role, []string{c.each[0]}, false, c.objects),
		Check:  grantedObjectChecks(t, c, role, []string{c.each[0]}, false),
	})
	if c.removeByDestroy {
		// a column grant needs one privilege: the grant is removed with the resource
		steps = append(steps, resource.TestStep{
			Config: testRoleOnlyConfig(),
			Check:  testCheckGrantedObject(t, c, role, []string{}, false),
		})
	} else {
		steps = append(steps, resource.TestStep{
			Config: grantedObjectConfig(c, role, []string{}, false, c.objects),
			Check:  grantedObjectChecks(t, c, role, []string{}, false),
		})
	}

	testAccPostgresqlGrantRunSteps(t, c, steps)
}

// TestAccPostgresqlGrantNamedObjectTypes tests the object types which are named explicitly in the
// GRANT statement (there is no ALL ... IN SCHEMA form for them), for a role and for PUBLIC.
func TestAccPostgresqlGrantNamedObjectTypes(t *testing.T) {
	skipIfNotAcc(t)

	cases := []struct {
		name string
		c    namedObjectGrantCase
	}{
		{"type", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "type",
				schema:     "test_grant_objects",
				objects:    `["test_type"]`,
				sqlObject:  "TYPE test_grant_objects.test_type",
				setup: []string{
					"DROP SCHEMA IF EXISTS test_grant_objects CASCADE",
					"CREATE SCHEMA test_grant_objects",
					"CREATE TYPE test_grant_objects.test_type AS ENUM ('a')",
					// types are granted to PUBLIC by default
					"REVOKE USAGE ON TYPE test_grant_objects.test_type FROM PUBLIC",
				},
				teardown:   []string{"DROP SCHEMA IF EXISTS test_grant_objects CASCADE"},
				full:       []string{"ALL"},
				fullACL:    []string{"USAGE"},
				privileges: []string{"USAGE"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace, aclexplode(t.typacl) p
WHERE n.nspname = 'test_grant_objects' AND t.typname = 'test_type' AND p.grantee = %[1]s`,
				hasQuery: `SELECT has_type_privilege('%[1]s', 'test_grant_objects.test_type', '%[2]s')`,
			},
			partial:    []string{"USAGE"},
			partialACL: []string{"USAGE"},
		}},
		{"domain", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "domain",
				schema:     "test_grant_objects",
				objects:    `["test_domain"]`,
				sqlObject:  "DOMAIN test_grant_objects.test_domain",
				setup: []string{
					"DROP SCHEMA IF EXISTS test_grant_objects CASCADE",
					"CREATE SCHEMA test_grant_objects",
					"CREATE DOMAIN test_grant_objects.test_domain AS int",
					// domains are granted to PUBLIC by default
					"REVOKE USAGE ON DOMAIN test_grant_objects.test_domain FROM PUBLIC",
				},
				teardown:   []string{"DROP SCHEMA IF EXISTS test_grant_objects CASCADE"},
				full:       []string{"ALL"},
				fullACL:    []string{"USAGE"},
				privileges: []string{"USAGE"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace, aclexplode(t.typacl) p
WHERE n.nspname = 'test_grant_objects' AND t.typname = 'test_domain' AND p.grantee = %[1]s`,
				hasQuery: `SELECT has_type_privilege('%[1]s', 'test_grant_objects.test_domain', '%[2]s')`,
			},
			partial:    []string{"USAGE"},
			partialACL: []string{"USAGE"},
		}},
		{"language", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "language",
				objects:    `["plpgsql"]`,
				sqlObject:  "LANGUAGE plpgsql",
				preCheck: func(t *testing.T) {
					// Languages belong to the superuser: a role which is not one cannot grant them.
					skipIfNotSuperuser(t)
				},
				// languages are granted to PUBLIC by default, the public grant is removed for the test
				setup:      []string{"REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC"},
				teardown:   []string{"GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC"},
				full:       []string{"ALL"},
				fullACL:    []string{"USAGE"},
				privileges: []string{"USAGE"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_language l, aclexplode(l.lanacl) p
WHERE l.lanname = 'plpgsql' AND p.grantee = %[1]s`,
				hasQuery: `SELECT has_language_privilege('%[1]s', 'plpgsql', '%[2]s')`,
			},
			partial:    []string{"USAGE"},
			partialACL: []string{"USAGE"},
		}},
		{"large_object", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "large_object",
				objects:    `["9001"]`,
				sqlObject:  "LARGE OBJECT 9001",
				setup: []string{
					"DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid = 9001) THEN PERFORM lo_unlink(9001); END IF; END $$",
					"SELECT lo_create(9001)",
				},
				teardown: []string{"DO $$ BEGIN IF EXISTS (SELECT 1 FROM pg_largeobject_metadata WHERE oid = 9001) THEN PERFORM lo_unlink(9001); END IF; END $$"},
				full:     []string{"ALL"},
				fullACL:  []string{"SELECT", "UPDATE"},
				// PostgreSQL has no has_largeobject_privilege function: checked with lo_get and lo_put
				privileges: []string{"SELECT", "UPDATE"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_largeobject_metadata m, aclexplode(m.lomacl) p
WHERE m.oid = 9001 AND p.grantee = %[1]s`,
				drift: "GRANT UPDATE ON LARGE OBJECT 9001 TO %s",
			},
			partial:    []string{"SELECT"},
			partialACL: []string{"SELECT"},
		}},
		{"parameter", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "parameter",
				objects:    `["work_mem"]`,
				sqlObject:  "PARAMETER work_mem",
				preCheck: func(t *testing.T) {
					// Only a superuser can grant the parameters, the RDS-like role has no grant option on them.
					skipIfNotSuperuser(t)
					testCheckCompatibleVersion(t, featureParameterPrivilege)
				},
				full:       []string{"ALL"},
				fullACL:    []string{"ALTER SYSTEM", "SET"},
				privileges: []string{"SET", "ALTER SYSTEM"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_parameter_acl a, aclexplode(a.paracl) p
WHERE a.parname = 'work_mem' AND p.grantee = %[1]s`,
				hasQuery: `SELECT has_parameter_privilege('%[1]s', 'work_mem', '%[2]s')`,
				drift:    "GRANT ALTER SYSTEM ON PARAMETER work_mem TO %s",
			},
			partial:    []string{"SET"},
			partialACL: []string{"SET"},
		}},
		{"tablespace", namedObjectGrantCase{
			grantedObjectCase: grantedObjectCase{
				objectType: "tablespace",
				objects:    `["test_grant_ts"]`,
				sqlObject:  "TABLESPACE test_grant_ts",
				preCheck: func(t *testing.T) {
					// Only a superuser can create a tablespace
					skipIfNotSuperuser(t)
				},
				// in place tablespaces do not need a directory outside of the data directory
				setup: []string{
					"DROP TABLESPACE IF EXISTS test_grant_ts",
					"SET allow_in_place_tablespaces = true",
					"CREATE TABLESPACE test_grant_ts LOCATION ''",
				},
				teardown:   []string{"DROP TABLESPACE IF EXISTS test_grant_ts"},
				full:       []string{"ALL"},
				fullACL:    []string{"CREATE"},
				privileges: []string{"CREATE"},
				aclQuery: `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM pg_tablespace t, aclexplode(t.spcacl) p
WHERE t.spcname = 'test_grant_ts' AND p.grantee = %[1]s`,
				hasQuery: `SELECT has_tablespace_privilege('%[1]s', 'test_grant_ts', '%[2]s')`,
			},
			partial:    []string{"CREATE"},
			partialACL: []string{"CREATE"},
		}},
	}

	for _, tc := range cases {
		for _, role := range []string{"test_grant_role", publicRole} {
			t.Run(tc.name+"_"+role, func(t *testing.T) {
				testAccPostgresqlGrantNamedObject(t, tc.c, role)
			})
		}
	}
}

// grantRoleSQL returns the role as written in a GRANT statement: PUBLIC is a keyword.
func grantRoleSQL(role string) string {
	if role == publicRole {
		return "PUBLIC"
	}
	return pq.QuoteIdentifier(role)
}

// testCatalogACLQuery returns the query of the privileges of an object in the catalogs: the
// aclexplode of the ACL, with the grant option as *, for the grantee given by %[1]s.
func testCatalogACLQuery(from, where string) string {
	return `SELECT coalesce(array_agg(p.privilege_type || CASE WHEN p.is_grantable THEN '*' ELSE '' END ORDER BY p.privilege_type), '{}')
FROM ` + from + `, aclexplode(` + where + `) p
WHERE p.grantee = %[1]s AND `
}

// TestAccPostgresqlGrantPrivilegeMatrix tests each privilege of the object types, alone and then with
// ALL, for a role and for PUBLIC: the object types which have an ALL ... IN SCHEMA form are tested
// with the objects of the schema, too.
func TestAccPostgresqlGrantPrivilegeMatrix(t *testing.T) {
	skipIfNotAcc(t)

	// MAINTAIN is a privilege of the tables since PostgreSQL 17: ALL includes it
	tablePrivileges := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}
	if testServerVersionNum(t) >= 170000 {
		tablePrivileges = append(tablePrivileges, "MAINTAIN")
	}

	const dropSchema = "DROP SCHEMA IF EXISTS test_privs CASCADE"
	const createSchema = "CREATE SCHEMA test_privs"
	const createFunction = "CREATE FUNCTION test_privs.f1() RETURNS int LANGUAGE sql AS 'SELECT 1'"
	const revokeFunction = "REVOKE ALL ON FUNCTION test_privs.f1() FROM PUBLIC"
	const createProcedure = "CREATE PROCEDURE test_privs.p1() LANGUAGE sql AS 'SELECT 1'"
	const revokeProcedure = "REVOKE ALL ON PROCEDURE test_privs.p1() FROM PUBLIC"
	const dropFDW = "DROP FOREIGN DATA WRAPPER IF EXISTS test_privs_fdw CASCADE"

	// the functions and the procedures are granted to PUBLIC by default: the public grant is removed
	functionSetup := []string{"DROP SCHEMA IF EXISTS test_privs CASCADE", createSchema, createFunction, revokeFunction}
	procedureSetup := []string{"DROP SCHEMA IF EXISTS test_privs CASCADE", createSchema, createProcedure, revokeProcedure}
	procAclQuery := testCatalogACLQuery("pg_proc f JOIN pg_namespace n ON n.oid = f.pronamespace", "f.proacl") +
		"n.nspname = 'test_privs' AND f.proname = 'p1'"
	funcAclQuery := testCatalogACLQuery("pg_proc f JOIN pg_namespace n ON n.oid = f.pronamespace", "f.proacl") +
		"n.nspname = 'test_privs' AND f.proname = 'f1'"
	relAclQuery := func(name string) string {
		return testCatalogACLQuery("pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace", "c.relacl") +
			"n.nspname = 'test_privs' AND c.relname = '" + name + "'"
	}
	functionPrivileges := []string{"EXECUTE"}

	cases := []struct {
		name string
		c    grantedObjectCase
	}{
		{"table", grantedObjectCase{
			objectType: "table", schema: "test_privs", objects: `["t1"]`, sqlObject: "TABLE test_privs.t1",
			setup:    []string{dropSchema, createSchema, "CREATE TABLE test_privs.t1 (c1 int, c2 int)"},
			teardown: []string{dropSchema},
			each:     tablePrivileges, full: []string{"ALL"}, fullACL: tablePrivileges, privileges: tablePrivileges,
			aclQuery: relAclQuery("t1"),
			hasQuery: `SELECT has_table_privilege('%[1]s', 'test_privs.t1', '%[2]s')`,
		}},
		{"table_all_in_schema", grantedObjectCase{
			objectType: "table", schema: "test_privs", objects: `[]`, sqlObject: "TABLE test_privs.t1",
			setup:    []string{dropSchema, createSchema, "CREATE TABLE test_privs.t1 (c1 int, c2 int)"},
			teardown: []string{dropSchema},
			each:     tablePrivileges, full: []string{"ALL"}, fullACL: tablePrivileges, privileges: tablePrivileges,
			aclQuery: relAclQuery("t1"),
			hasQuery: `SELECT has_table_privilege('%[1]s', 'test_privs.t1', '%[2]s')`,
		}},
		{"column", grantedObjectCase{
			objectType: "column", schema: "test_privs", objects: `["t1"]`, columns: `["c1"]`, sqlObject: "TABLE test_privs.t1",
			removeByDestroy: true,
			revoke:          "REVOKE ALL (c1) ON TABLE test_privs.t1 FROM %s",
			setup:           []string{dropSchema, createSchema, "CREATE TABLE test_privs.t1 (c1 int, c2 int)"},
			teardown:        []string{dropSchema},
			each:            []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"}, full: []string{"ALL"},
			fullACL:    []string{"INSERT", "REFERENCES", "SELECT", "UPDATE"},
			privileges: []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"},
			aclQuery: testCatalogACLQuery("pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace", "a.attacl") +
				"n.nspname = 'test_privs' AND c.relname = 't1' AND a.attname = 'c1'",
			hasQuery: `SELECT has_column_privilege('%[1]s', 'test_privs.t1', 'c1', '%[2]s')`,
		}},
		{"sequence", grantedObjectCase{
			objectType: "sequence", schema: "test_privs", objects: `["s1"]`, sqlObject: "SEQUENCE test_privs.s1",
			setup:    []string{dropSchema, createSchema, "CREATE SEQUENCE test_privs.s1"},
			teardown: []string{dropSchema},
			each:     []string{"USAGE", "SELECT", "UPDATE"}, full: []string{"ALL"},
			fullACL: []string{"SELECT", "UPDATE", "USAGE"}, privileges: []string{"USAGE", "SELECT", "UPDATE"},
			aclQuery: relAclQuery("s1"),
			hasQuery: `SELECT has_sequence_privilege('%[1]s', 'test_privs.s1', '%[2]s')`,
		}},
		{"sequence_all_in_schema", grantedObjectCase{
			objectType: "sequence", schema: "test_privs", objects: `[]`, sqlObject: "SEQUENCE test_privs.s1",
			setup:    []string{dropSchema, createSchema, "CREATE SEQUENCE test_privs.s1"},
			teardown: []string{dropSchema},
			each:     []string{"USAGE", "SELECT", "UPDATE"}, full: []string{"ALL"},
			fullACL: []string{"SELECT", "UPDATE", "USAGE"}, privileges: []string{"USAGE", "SELECT", "UPDATE"},
			aclQuery: relAclQuery("s1"),
			hasQuery: `SELECT has_sequence_privilege('%[1]s', 'test_privs.s1', '%[2]s')`,
		}},
		{"database", grantedObjectCase{
			objectType: "database", objects: `[]`, database: "postgres", sqlObject: "DATABASE postgres",
			// the databases are granted to PUBLIC by default: the public grant is removed for the test
			setup:    []string{"REVOKE ALL ON DATABASE postgres FROM PUBLIC"},
			teardown: []string{"GRANT CONNECT, TEMPORARY ON DATABASE postgres TO PUBLIC"},
			each:     []string{"CREATE", "CONNECT", "TEMPORARY"}, full: []string{"ALL"},
			fullACL: []string{"CONNECT", "CREATE", "TEMPORARY"}, privileges: []string{"CREATE", "CONNECT", "TEMPORARY"},
			aclQuery: testCatalogACLQuery("pg_database d", "d.datacl") + "d.datname = 'postgres'",
			hasQuery: `SELECT has_database_privilege('%[1]s', 'postgres', '%[2]s')`,
		}},
		{"schema", grantedObjectCase{
			objectType: "schema", schema: "test_privs", objects: `[]`, sqlObject: "SCHEMA test_privs",
			setup:    []string{dropSchema, createSchema},
			teardown: []string{dropSchema},
			each:     []string{"CREATE", "USAGE"}, full: []string{"ALL"},
			fullACL: []string{"CREATE", "USAGE"}, privileges: []string{"CREATE", "USAGE"},
			aclQuery: testCatalogACLQuery("pg_namespace n", "n.nspacl") + "n.nspname = 'test_privs'",
			hasQuery: `SELECT has_schema_privilege('%[1]s', 'test_privs', '%[2]s')`,
		}},
		{"function", grantedObjectCase{
			objectType: "function", schema: "test_privs", objects: `["f1"]`, sqlObject: "FUNCTION test_privs.f1()",
			setup: functionSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: funcAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.f1()', '%[2]s')`,
		}},
		{"function_all_in_schema", grantedObjectCase{
			objectType: "function", schema: "test_privs", objects: `[]`, sqlObject: "FUNCTION test_privs.f1()",
			setup: functionSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: funcAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.f1()', '%[2]s')`,
		}},
		{"procedure", grantedObjectCase{
			objectType: "procedure", schema: "test_privs", objects: `["p1"]`, sqlObject: "PROCEDURE test_privs.p1()",
			setup: procedureSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: procAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.p1()', '%[2]s')`,
		}},
		{"procedure_all_in_schema", grantedObjectCase{
			objectType: "procedure", schema: "test_privs", objects: `[]`, sqlObject: "PROCEDURE test_privs.p1()",
			setup: procedureSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: procAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.p1()', '%[2]s')`,
		}},
		{"routine", grantedObjectCase{
			objectType: "routine", schema: "test_privs", objects: `["f1"]`, sqlObject: "ROUTINE test_privs.f1()",
			setup: functionSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: funcAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.f1()', '%[2]s')`,
		}},
		{"routine_all_in_schema", grantedObjectCase{
			objectType: "routine", schema: "test_privs", objects: `[]`, sqlObject: "ROUTINE test_privs.f1()",
			setup: functionSetup, teardown: []string{dropSchema},
			each: functionPrivileges, full: []string{"ALL"}, fullACL: functionPrivileges, privileges: functionPrivileges,
			aclQuery: funcAclQuery,
			hasQuery: `SELECT has_function_privilege('%[1]s', 'test_privs.f1()', '%[2]s')`,
		}},
		{"foreign_data_wrapper", grantedObjectCase{
			objectType: "foreign_data_wrapper", objects: `["test_privs_fdw"]`, sqlObject: "FOREIGN DATA WRAPPER test_privs_fdw",
			preCheck: skipIfNotSuperuser,
			setup:    []string{dropFDW, "CREATE FOREIGN DATA WRAPPER test_privs_fdw"},
			teardown: []string{dropFDW},
			each:     []string{"USAGE"}, full: []string{"ALL"}, fullACL: []string{"USAGE"}, privileges: []string{"USAGE"},
			aclQuery: testCatalogACLQuery("pg_foreign_data_wrapper w", "w.fdwacl") + "w.fdwname = 'test_privs_fdw'",
			hasQuery: `SELECT has_foreign_data_wrapper_privilege('%[1]s', 'test_privs_fdw', '%[2]s')`,
		}},
		{"foreign_server", grantedObjectCase{
			objectType: "foreign_server", objects: `["test_privs_srv"]`, sqlObject: "FOREIGN SERVER test_privs_srv",
			preCheck: skipIfNotSuperuser,
			setup:    []string{dropFDW, "CREATE FOREIGN DATA WRAPPER test_privs_fdw", "CREATE SERVER test_privs_srv FOREIGN DATA WRAPPER test_privs_fdw"},
			teardown: []string{dropFDW},
			each:     []string{"USAGE"}, full: []string{"ALL"}, fullACL: []string{"USAGE"}, privileges: []string{"USAGE"},
			aclQuery: testCatalogACLQuery("pg_foreign_server s", "s.srvacl") + "s.srvname = 'test_privs_srv'",
			hasQuery: `SELECT has_server_privilege('%[1]s', 'test_privs_srv', '%[2]s')`,
		}},
		{"parameter", grantedObjectCase{
			objectType: "parameter", objects: `["work_mem"]`, sqlObject: "PARAMETER work_mem",
			preCheck: func(t *testing.T) {
				skipIfNotSuperuser(t)
				testCheckCompatibleVersion(t, featureParameterPrivilege)
			},
			each: []string{"SET", "ALTER SYSTEM"}, full: []string{"ALL"},
			fullACL: []string{"ALTER SYSTEM", "SET"}, privileges: []string{"SET", "ALTER SYSTEM"},
			aclQuery: testCatalogACLQuery("pg_parameter_acl a", "a.paracl") + "a.parname = 'work_mem'",
			hasQuery: `SELECT has_parameter_privilege('%[1]s', 'work_mem', '%[2]s')`,
		}},
	}

	for _, tc := range cases {
		for _, role := range []string{"test_grant_role", publicRole} {
			t.Run(tc.name+"_"+role, func(t *testing.T) {
				testAccPostgresqlGrantPrivileges(t, tc.c, role)
			})
		}
	}
}

// TestAccPostgresqlGrantFunctionWithNamedArgs grants a function given with the names and the modes
// of its arguments, as PostgreSQL accepts them in the GRANT statement.
func TestAccPostgresqlGrantFunctionWithNamedArgs(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_named_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_named_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_named_schema TO test_named_role")
	dbExecute(t, dsn, "CREATE FUNCTION test_named_schema.test(IN arg1 text, arg2 character) RETURNS text AS $$ select 'foo'::text $$ LANGUAGE SQL")
	dbExecute(t, dsn, "REVOKE ALL ON FUNCTION test_named_schema.test(text, character) FROM PUBLIC")

	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_named_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_named_role")
	}()

	tfConfig := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_named_role"
  schema      = "test_named_schema"
  object_type = "function"
  privileges  = %s
  objects     = ["test(IN arg1 text, arg2 character)"]
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(tfConfig, `["EXECUTE"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1"),
					testCheckFunctionWithArgsExecutable(t, "test_named_role", "test_named_schema.test", []string{pq.QuoteLiteral("value 1"), pq.QuoteLiteral("value 2")}),
				),
			},
			{
				Config: fmt.Sprintf(tfConfig, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "0"),
					func(*terraform.State) error {
						db := connectAsTestRole(t, "test_named_role", "postgres")
						defer closeDB(t, db)
						return testHasGrantForQuery(db, "SELECT test_named_schema.test('value 1', 'value 2')", false)
					},
				),
			},
		},
	})
}

// TestAccPostgresqlGrantPublicWithGrantOption checks that a grant option is refused for PUBLIC,
// which PostgreSQL does not allow.
func TestAccPostgresqlGrantPublicWithGrantOption(t *testing.T) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_grant" "test" {
  database          = "postgres"
  role              = "public"
  schema            = "public"
  object_type       = "schema"
  privileges        = ["USAGE"]
  with_grant_option = true
}
`,
				ExpectError: regexp.MustCompile("with_grant_option cannot be used with role public"),
			},
		},
	})
}

func TestQuoteIdentifyIdent(t *testing.T) {
	for in, want := range map[string]string{
		"table1":                           `"table1"`,
		`we"ird`:                           `"we""ird"`,
		"f(text, char)":                    `"f"(text, char)`,
		"f(varchar(10), numeric(10,2))":    `"f"(varchar(10), numeric(10,2))`,
		"f(integer[], public.\"My Type\")": `"f"(integer[], public."My Type")`,
		"f()":                              `"f"()`,
	} {
		if got := quoteIdentifyIdent(in); got != want {
			t.Errorf("quoteIdentifyIdent(%q) = %s, want %s", in, got, want)
		}
	}
}

// testFunctionPrivilege checks, in the catalog, whether role can execute the function with this exact signature.
func testFunctionPrivilege(t *testing.T, role, signature string, want bool) error {
	var got bool
	db, err := sql.Open(testDriverName, testPostgresDSN(t))
	if err != nil {
		return err
	}
	defer closeDB(t, db)
	if err := db.QueryRow("SELECT has_function_privilege($1, $2::regprocedure, 'EXECUTE')", role, signature).Scan(&got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("role %s can execute %s: %v, expected %v", role, signature, got, want)
	}
	return nil
}

// TestAccPostgresqlGrantFunctionSignatures grants EXECUTE on functions given with every kind of argument
// list: type modifiers (with commas), argument names and modes, a schema-qualified type, an array type, a type
// whose name needs quoting, and one overload among two with the same name.
func TestAccPostgresqlGrantFunctionSignatures(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_sig_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_sig_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_sig_schema TO test_sig_role")
	dbExecute(t, dsn, `CREATE TYPE test_sig_schema.mytype AS ENUM ('a')`)
	dbExecute(t, dsn, `CREATE TYPE test_sig_schema."My Type" AS ENUM ('a')`)
	for _, f := range []string{
		"f_typmod(a varchar(10), b numeric(10,2))",
		"f_named(IN a text, b double precision)",
		"f_qualified(a test_sig_schema.mytype)",
		"f_array(a integer[])",
		"f_twoword(a double precision)",
		`f_quoted(a test_sig_schema."My Type")`,
		"f_over(a integer)",
		"f_over(a text)",
	} {
		dbExecute(t, dsn, fmt.Sprintf("CREATE FUNCTION test_sig_schema.%s RETURNS int AS $$ select 1 $$ LANGUAGE SQL", f))
	}
	dbExecute(t, dsn, "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA test_sig_schema FROM PUBLIC")
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_sig_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_sig_role")
	}()

	// each function is granted by its own resource: Read keeps the privileges of the first object which differs
	cases := []struct{ object, canonical string }{
		{"f_typmod(varchar(10), numeric(10,2))", "test_sig_schema.f_typmod(character varying,numeric)"},
		{"f_named(IN a text, b double precision)", "test_sig_schema.f_named(text,double precision)"},
		{"f_qualified(test_sig_schema.mytype)", "test_sig_schema.f_qualified(test_sig_schema.mytype)"},
		{"f_array(integer[])", "test_sig_schema.f_array(integer[])"},
		{"f_twoword(double precision)", "test_sig_schema.f_twoword(double precision)"},
		{`f_quoted(test_sig_schema.\"My Type\")`, `test_sig_schema.f_quoted(test_sig_schema."My Type")`},
		{"f_over(integer)", "test_sig_schema.f_over(integer)"},
	}
	tfConfig := ""
	for i, c := range cases {
		tfConfig += fmt.Sprintf(`
resource "postgresql_grant" "test%d" {
  database    = "postgres"
  role        = "test_sig_role"
  schema      = "test_sig_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["%s"]
}
`, i, c.object)
	}
	checkAll := func(*terraform.State) error {
		for _, c := range cases {
			if err := testFunctionPrivilege(t, "test_sig_role", c.canonical, true); err != nil {
				return err
			}
		}
		// the other overload of f_over
		return testFunctionPrivilege(t, "test_sig_role", "test_sig_schema.f_over(text)", false)
	}

	steps := []resource.TestStep{{Config: tfConfig, Check: checkAll}}
	for _, c := range cases {
		canonical := c.canonical
		steps = append(steps,
			resource.TestStep{
				// a privilege revoked outside of Terraform is detected, whatever the form of the signature
				PreConfig: func() {
					dbExecute(t, dsn, "REVOKE EXECUTE ON FUNCTION "+canonical+" FROM test_sig_role")
				},
				Config:             tfConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			resource.TestStep{Config: tfConfig, Check: checkAll},
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps:     steps,
	})
}

// testSetupOverloads creates test_ov_role and the two overloads test_ov_schema.f_ov(integer) and
// test_ov_schema.f_ov(text), which only the owner can execute. The returned function drops all of it.
func testSetupOverloads(t *testing.T) func() {
	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_ov_role LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_ov_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_ov_schema TO test_ov_role")
	dbExecute(t, dsn, "CREATE FUNCTION test_ov_schema.f_ov(a integer) RETURNS int AS $$ select 1 $$ LANGUAGE SQL")
	dbExecute(t, dsn, "CREATE FUNCTION test_ov_schema.f_ov(a text) RETURNS int AS $$ select 1 $$ LANGUAGE SQL")
	dbExecute(t, dsn, "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA test_ov_schema FROM PUBLIC")
	return func() {
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_ov_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_ov_role")
	}
}

func testCheckOverloads(t *testing.T, wantInt, wantText bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if err := testFunctionPrivilege(t, "test_ov_role", "test_ov_schema.f_ov(integer)", wantInt); err != nil {
			return err
		}
		return testFunctionPrivilege(t, "test_ov_role", "test_ov_schema.f_ov(text)", wantText)
	}
}

// TestAccPostgresqlGrantFunctionSignatureDrift revokes, outside of Terraform, the privilege on one overload of a
// function which the configuration grants with its signature: the plan must see it, for this overload only.
func TestAccPostgresqlGrantFunctionSignatureDrift(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()

	grant := func(name, signature string) string {
		return fmt.Sprintf(`
resource "postgresql_grant" "%s" {
  database    = "postgres"
  role        = "test_ov_role"
  schema      = "test_ov_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["%s"]
}
`, name, signature)
	}
	both := grant("int", "f_ov(a integer)") + grant("text", "f_ov(text)")
	revoke := func(signature string) func() {
		return func() {
			dbExecute(t, testPostgresDSN(t), "REVOKE EXECUTE ON FUNCTION test_ov_schema."+signature+" FROM test_ov_role")
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
				Config: both,
				Check:  testCheckOverloads(t, true, true),
			},
			{
				// one overload loses its privilege: the plan shows a change
				PreConfig:          revoke("f_ov(text)"),
				Config:             both,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// the apply restores it, and only it
				Config: both,
				Check:  testCheckOverloads(t, true, true),
			},
			{
				PreConfig:          revoke("f_ov(integer)"),
				Config:             both,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: both,
				Check:  testCheckOverloads(t, true, true),
			},
			{
				// the other overload is not in the configuration of this resource: no change
				Config: grant("int", "f_ov(integer)"),
				Check:  testCheckOverloads(t, true, false),
			},
			{
				PreConfig:          revoke("f_ov(text)"),
				Config:             grant("int", "f_ov(integer)"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// TestAccPostgresqlGrantFunctionBareName checks that a function given by its name only keeps being read by name
// (PostgreSQL refuses a bare name which is ambiguous, so the function is not overloaded).
func TestAccPostgresqlGrantFunctionBareName(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()
	dbExecute(t, testPostgresDSN(t), "CREATE FUNCTION test_ov_schema.f_one() RETURNS int AS $$ select 1 $$ LANGUAGE SQL")
	dbExecute(t, testPostgresDSN(t), "REVOKE ALL ON FUNCTION test_ov_schema.f_one() FROM PUBLIC")

	config := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_ov_role"
  schema      = "test_ov_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["f_one"]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(*terraform.State) error {
					return testFunctionPrivilege(t, "test_ov_role", "test_ov_schema.f_one()", true)
				},
			},
			{
				PreConfig: func() {
					dbExecute(t, testPostgresDSN(t), "REVOKE EXECUTE ON FUNCTION test_ov_schema.f_one() FROM test_ov_role")
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// testGrantorsQuery returns the grantor and privilege of the ACL entries of an object for a role, as
// "grantor:PRIVILEGE" sorted, e.g. {"test_own_owner:SELECT"}.
func testCheckGrantors(t *testing.T, aclQuery string, want []string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		db, err := sql.Open(testDriverName, testPostgresDSN(t))
		if err != nil {
			return err
		}
		defer closeDB(t, db)
		var got pq.StringArray
		if err := db.QueryRow(aclQuery).Scan(&got); err != nil {
			return fmt.Errorf("could not read the ACL: %w", err)
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			return fmt.Errorf("ACL entries (grantor:privilege) are %v, expected %v", got, want)
		}
		return nil
	}
}

// TestAccPostgresqlGrantObjectsOfAnotherOwner grants privileges on named objects which belong to a role which
// is neither the connected user nor a superuser. A superuser grants them in the name of the owner; a user who
// is not a superuser has to be made a member of the owner by the provider. In both cases the grantor
// recorded in the ACL is the owner of the object.
func TestAccPostgresqlGrantObjectsOfAnotherOwner(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, "CREATE ROLE test_own_owner NOLOGIN")
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_own_grantee LOGIN PASSWORD '%s'", testRolePassword))
	dbExecute(t, dsn, "CREATE SCHEMA test_own_schema")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_own_schema TO test_own_grantee")
	// A second schema without any table: the owners of its objects cannot be found from its tables.
	dbExecute(t, dsn, "CREATE SCHEMA test_own_schema2")
	dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_own_schema2 TO test_own_grantee")
	defer func() {
		dbExecute(t, dsn, "GRANT test_own_owner TO CURRENT_USER")
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_own_schema CASCADE")
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_own_schema2 CASCADE")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_own_grantee")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_own_owner")
	}()

	// The objects are created by their owner. A user who is not a superuser becomes a member of the owner
	// for this, and leaves the role afterwards.
	script := `
SET ROLE test_own_owner;
CREATE TABLE test_own_schema.t (a int);
CREATE SEQUENCE test_own_schema.s;
CREATE FUNCTION test_own_schema.f() RETURNS int AS $$ select 1 $$ LANGUAGE SQL;
CREATE FUNCTION test_own_schema.g(a integer) RETURNS int AS $$ select 1 $$ LANGUAGE SQL;
REVOKE ALL ON FUNCTION test_own_schema.f() FROM PUBLIC;
REVOKE ALL ON FUNCTION test_own_schema.g(integer) FROM PUBLIC;
CREATE TYPE test_own_schema2.ty AS ENUM ('a');
CREATE DOMAIN test_own_schema2.dom AS integer;
REVOKE ALL ON TYPE test_own_schema2.ty FROM PUBLIC;
REVOKE ALL ON DOMAIN test_own_schema2.dom FROM PUBLIC;
CREATE SEQUENCE test_own_schema2.s;
CREATE FUNCTION test_own_schema2.f() RETURNS int AS $$ select 1 $$ LANGUAGE SQL;
CREATE FUNCTION test_own_schema2.g(a integer) RETURNS int AS $$ select 1 $$ LANGUAGE SQL;
REVOKE ALL ON FUNCTION test_own_schema2.f() FROM PUBLIC;
REVOKE ALL ON FUNCTION test_own_schema2.g(integer) FROM PUBLIC;
RESET ROLE;
`
	dbExecute(t, dsn, "GRANT test_own_owner TO CURRENT_USER")
	dbExecute(t, dsn, "GRANT USAGE, CREATE ON SCHEMA test_own_schema, test_own_schema2 TO test_own_owner")
	dbExecute(t, dsn, script+"REVOKE test_own_owner FROM CURRENT_USER;")

	grantors := func(schemaName, catalog, nameColumn, aclColumn, name string) string {
		return fmt.Sprintf(`SELECT coalesce(array_agg(pg_get_userbyid(p.grantor) || ':' || p.privilege_type), '{}')
FROM %[1]s o JOIN pg_namespace n ON n.oid = o.%[5]s, aclexplode(o.%[3]s) p
WHERE n.nspname = '%[6]s' AND o.%[2]s = '%[4]s' AND p.grantee = 'test_own_grantee'::regrole`,
			catalog, nameColumn, aclColumn, name, map[string]string{"pg_class": "relnamespace", "pg_proc": "pronamespace", "pg_type": "typnamespace"}[catalog], schemaName)
	}

	cases := []struct {
		name       string
		schema     string
		objectType string
		objects    string
		privileges string
		acl        string
		want       []string
	}{
		{"table", "test_own_schema", "table", `["t"]`, `["SELECT", "INSERT"]`, grantors("test_own_schema", "pg_class", "relname", "relacl", "t"), []string{"test_own_owner:SELECT", "test_own_owner:INSERT"}},
		{"sequence", "test_own_schema", "sequence", `["s"]`, `["USAGE"]`, grantors("test_own_schema", "pg_class", "relname", "relacl", "s"), []string{"test_own_owner:USAGE"}},
		{"function", "test_own_schema", "function", `["f"]`, `["EXECUTE"]`, grantors("test_own_schema", "pg_proc", "proname", "proacl", "f"), []string{"test_own_owner:EXECUTE"}},
		{"function with arguments", "test_own_schema", "function", `["g(integer)"]`, `["EXECUTE"]`, grantors("test_own_schema", "pg_proc", "proname", "proacl", "g"), []string{"test_own_owner:EXECUTE"}},
		{"type", "test_own_schema2", "type", `["ty"]`, `["USAGE"]`, grantors("test_own_schema2", "pg_type", "typname", "typacl", "ty"), []string{"test_own_owner:USAGE"}},
		{"domain", "test_own_schema2", "domain", `["dom"]`, `["USAGE"]`, grantors("test_own_schema2", "pg_type", "typname", "typacl", "dom"), []string{"test_own_owner:USAGE"}},
		{"sequence in a schema without table", "test_own_schema2", "sequence", `["s"]`, `["USAGE"]`, grantors("test_own_schema2", "pg_class", "relname", "relacl", "s"), []string{"test_own_owner:USAGE"}},
		{"function in a schema without table", "test_own_schema2", "function", `["f"]`, `["EXECUTE"]`, grantors("test_own_schema2", "pg_proc", "proname", "proacl", "f"), []string{"test_own_owner:EXECUTE"}},
		{"function with arguments in a schema without table", "test_own_schema2", "function", `["g(integer)"]`, `["EXECUTE"]`, grantors("test_own_schema2", "pg_proc", "proname", "proacl", "g"), []string{"test_own_owner:EXECUTE"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			tfConfig := fmt.Sprintf(`
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_own_grantee"
  schema      = "%s"
  object_type = "%s"
  objects     = %s
  privileges  = %s
}
`, c.schema, c.objectType, c.objects, c.privileges)
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{
						Config: tfConfig,
						Check:  testCheckGrantors(t, c.acl, c.want),
					},
					{
						Config:  tfConfig,
						Destroy: true,
						Check:   testCheckGrantors(t, c.acl, []string{}),
					},
				},
			})
		})
	}
}

// TestAccPostgresqlGrantMaintainBefore17 checks that the privilege MAINTAIN, which tables have since PostgreSQL 17,
// is refused with a clear error by the older versions (the test is skipped from version 17, where
// TestAccPostgresqlGrantPrivilegeMatrix grants it).
func TestAccPostgresqlGrantMaintainBefore17(t *testing.T) {
	skipIfNotAcc(t)

	for _, objects := range []string{"", `objects = ["t"]`} {
		resource.Test(t, resource.TestCase{
			PreCheck: func() {
				testAccPreCheck(t)
				db, err := testAccProvider.Meta().(*Client).Connect()
				if err != nil {
					t.Fatalf("could not connect to database: %v", err)
				}
				if db.featureSupported(featureMaintainPrivilege) {
					t.Skipf("MAINTAIN exists in PostgreSQL %s", db.version)
				}
			},
			Providers: testAccProviders,
			Steps: []resource.TestStep{
				{
					Config: fmt.Sprintf(`
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "public"
  schema      = "public"
  object_type = "table"
  privileges  = ["SELECT", "MAINTAIN"]
  %s
}
`, objects),
					ExpectError: regexp.MustCompile("MAINTAIN is not an allowed privilege for object type table"),
				},
			},
		})
	}
}

func TestGrantedByQueries(t *testing.T) {
	d := schema.TestResourceDataRaw(t, resourcePostgreSQLGrant().Schema, map[string]any{
		"object_type":       "table",
		"schema":            "public",
		"objects":           []any{"t"},
		"role":              "r",
		"granted_by":        "g",
		"privileges":        []any{"SELECT"},
		"with_grant_option": true,
	})
	if got, want := createGrantQuery(d, []string{"SELECT"}), `GRANT SELECT ON TABLE "public"."t" TO "r" WITH GRANT OPTION GRANTED BY "g"`; got != want {
		t.Errorf("grant query is %s, expected %s", got, want)
	}
	if got, want := createRevokeQuery(d.Get), `REVOKE SELECT ON TABLE "public"."t" FROM "r" GRANTED BY "g"`; got != want {
		t.Errorf("revoke query is %s, expected %s", got, want)
	}
}

// testSkipUnlessGrantedBy skips the test before PostgreSQL 19, where GRANTED BY is only accepted for the current user.
func testSkipUnlessGrantedBy(t *testing.T) {
	db, err := testAccProvider.Meta().(*Client).Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}
	if !db.featureSupported(featureGrantedBy) {
		t.Skipf("GRANTED BY is a clause of the object privileges since PostgreSQL 19, not of %s", db.version)
	}
}

// testSetupGrantedBy creates test_gb_owner, test_gb_grantor and test_gb_grantee, and the table
// test_gb_schema.t, owned by test_gb_owner. As the connected user is perhaps not a superuser, it joins the owner
// for the time of the setup, as the provider does.
func testSetupGrantedBy(t *testing.T) func() {
	dsn := testPostgresDSN(t)
	for _, role := range []string{"test_gb_owner", "test_gb_grantor", "test_gb_grantee"} {
		dbExecute(t, dsn, "CREATE ROLE "+role+" NOLOGIN")
	}
	dbExecute(t, dsn, "CREATE SCHEMA test_gb_schema")
	dbExecute(t, dsn, "GRANT USAGE, CREATE ON SCHEMA test_gb_schema TO test_gb_owner")
	dbExecute(t, dsn, "GRANT test_gb_owner TO CURRENT_USER")
	dbExecute(t, dsn, `
SET ROLE test_gb_owner;
CREATE TABLE test_gb_schema.t (a int);
RESET ROLE;
REVOKE test_gb_owner FROM CURRENT_USER;`)
	return func() {
		dbExecute(t, dsn, "GRANT test_gb_owner TO CURRENT_USER")
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_gb_schema CASCADE")
		for _, role := range []string{"test_gb_grantee", "test_gb_grantor", "test_gb_owner"} {
			dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
		}
	}
}

// testGrantedByACL checks the ACL entries "grantor:PRIVILEGE" of test_gb_grantee on test_gb_schema.t.
func testGrantedByACL(t *testing.T, want ...string) resource.TestCheckFunc {
	return testCheckGrantors(t, `SELECT coalesce(array_agg(pg_get_userbyid(p.grantor) || ':' || p.privilege_type), '{}')
FROM pg_class c, aclexplode(c.relacl) p
WHERE c.oid = 'test_gb_schema.t'::regclass AND p.grantee = 'test_gb_grantee'::regrole`, want)
}

func testGrantedByConfig(grantedBy string, privileges string) string {
	return fmt.Sprintf(`
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_gb_grantee"
  schema      = "test_gb_schema"
  object_type = "table"
  objects     = ["t"]
  privileges  = %s
  granted_by  = "%s"
}
`, privileges, grantedBy)
}

// TestAccPostgresqlGrantGrantedByUnsupported checks the error of the versions where GRANTED BY is not a real clause.
func TestAccPostgresqlGrantGrantedByUnsupported(t *testing.T) {
	skipIfNotAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			db, err := testAccProvider.Meta().(*Client).Connect()
			if err != nil {
				t.Fatalf("could not connect to database: %v", err)
			}
			if db.featureSupported(featureGrantedBy) {
				t.Skipf("GRANTED BY is supported by PostgreSQL %s", db.version)
			}
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      testGrantedByConfig("postgres", `["SELECT"]`),
				ExpectError: regexp.MustCompile("granted_by is not supported for this Postgres version"),
			},
		},
	})
}

// TestAccPostgresqlGrantGrantedByOwner grants as the owner of the table, in both modes (a user who is not a
// superuser joins the owner during the change), and removes the grant with REVOKE ... GRANTED BY.
func TestAccPostgresqlGrantGrantedByOwner(t *testing.T) {
	skipIfNotAcc(t)

	revokeOutside := func() {
		dsn := testPostgresDSN(t)
		dbExecute(t, dsn, "GRANT test_gb_owner TO CURRENT_USER")
		dbExecute(t, dsn, "REVOKE SELECT ON test_gb_schema.t FROM test_gb_grantee GRANTED BY test_gb_owner")
		dbExecute(t, dsn, "REVOKE test_gb_owner FROM CURRENT_USER")
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSkipUnlessGrantedBy(t)
			t.Cleanup(testSetupGrantedBy(t))
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testGrantedByConfig("test_gb_owner", `["SELECT"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:SELECT"),
			},
			{
				Config: testGrantedByConfig("test_gb_owner", `["SELECT", "INSERT"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:SELECT", "test_gb_owner:INSERT"),
			},
			{
				PreConfig:          revokeOutside,
				Config:             testGrantedByConfig("test_gb_owner", `["SELECT", "INSERT"]`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: testGrantedByConfig("test_gb_owner", `["SELECT", "INSERT"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:SELECT", "test_gb_owner:INSERT"),
			},
			{
				Config:  testGrantedByConfig("test_gb_owner", `["SELECT", "INSERT"]`),
				Destroy: true,
				Check:   testGrantedByACL(t),
			},
		},
	})
}

// TestAccPostgresqlGrantGrantedByOtherRole uses a grantor which is not the owner: a role which holds the grant
// option. Only the entries of this grantor are read, changed and removed; the privileges which the owner
// granted to the same role are left alone.
func TestAccPostgresqlGrantGrantedByOtherRole(t *testing.T) {
	skipIfNotAcc(t)
	skipIfNotSuperuser(t)

	dsn := testPostgresDSN(t)
	config := func(privileges string) string { return testGrantedByConfig("test_gb_grantor", privileges) }

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSkipUnlessGrantedBy(t)
			t.Cleanup(testSetupGrantedBy(t))
			dbExecute(t, dsn, "GRANT SELECT, INSERT, UPDATE ON test_gb_schema.t TO test_gb_grantor WITH GRANT OPTION")
			// granted by the owner, not by the grantor of the resource
			dbExecute(t, dsn, "GRANT INSERT ON test_gb_schema.t TO test_gb_grantee")
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config(`["SELECT"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:INSERT", "test_gb_grantor:SELECT"),
			},
			{
				Config: config(`["SELECT", "UPDATE"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:INSERT", "test_gb_grantor:SELECT", "test_gb_grantor:UPDATE"),
			},
			{
				// the owner grants the same privilege: it is not the one of the resource, the revoke is seen
				PreConfig: func() {
					dbExecute(t, dsn, "GRANT SELECT ON test_gb_schema.t TO test_gb_grantee")
					dbExecute(t, dsn, "REVOKE SELECT ON test_gb_schema.t FROM test_gb_grantee GRANTED BY test_gb_grantor")
				},
				Config:             config(`["SELECT", "UPDATE"]`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`["SELECT", "UPDATE"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:INSERT", "test_gb_owner:SELECT", "test_gb_grantor:SELECT", "test_gb_grantor:UPDATE"),
			},
			{
				// what the owner grants is not managed: no change
				PreConfig: func() {
					dbExecute(t, dsn, "GRANT UPDATE ON test_gb_schema.t TO test_gb_grantee GRANTED BY test_gb_owner")
				},
				Config:   config(`["SELECT", "UPDATE"]`),
				PlanOnly: true,
			},
			{
				Config:  config(`["SELECT", "UPDATE"]`),
				Destroy: true,
				Check:   testGrantedByACL(t, "test_gb_owner:INSERT", "test_gb_owner:SELECT", "test_gb_owner:UPDATE"),
			},
		},
	})
}

// TestAccPostgresqlGrantFunctionSignatureMixed grants a function given with its signature and one given by its
// name: the revoke of the privilege of the first one, outside of Terraform, must be seen, and undone by the
// apply, although the second one still has the privilege.
func TestAccPostgresqlGrantFunctionSignatureMixed(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()
	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, "CREATE FUNCTION test_ov_schema.f_one() RETURNS int AS $$ select 1 $$ LANGUAGE SQL")
	dbExecute(t, dsn, "REVOKE ALL ON FUNCTION test_ov_schema.f_one() FROM PUBLIC")

	config := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_ov_role"
  schema      = "test_ov_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["f_ov(integer)", "f_one"]
}
`
	checkBoth := func(*terraform.State) error {
		if err := testFunctionPrivilege(t, "test_ov_role", "test_ov_schema.f_ov(integer)", true); err != nil {
			return err
		}
		return testFunctionPrivilege(t, "test_ov_role", "test_ov_schema.f_one()", true)
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{Config: config, Check: checkBoth},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "REVOKE EXECUTE ON FUNCTION test_ov_schema.f_ov(integer) FROM test_ov_role")
				},
				// one refresh only (a plan and its refreshes would read the state which the first one changed)
				Config: config,
				Check:  checkBoth,
			},
		},
	})
}

// TestAccPostgresqlGrantFunctionOutArguments grants a function and a procedure given with an OUT argument, next to
// another one whose input arguments are the arguments with their OUT: PostgreSQL ignores the OUT arguments, so
// the grant (and the read of its privileges) is on the one which has the input arguments only.
// GRANT ON PROCEDURE p(integer, integer) designates p(a integer, OUT b integer) too: a list of types
// without modes is matched with the arguments of every mode (PostgreSQL 14 and above). The privileges
// are read from that procedure, a change made outside is planned.
func TestAccPostgresqlGrantRoutineAllArgumentTypes(t *testing.T) {
	skipIfNotAcc(t)

	for _, test := range []struct{ name, objectType, create, revoke string }{
		{
			"procedure", "procedure",
			"CREATE PROCEDURE test_allargs_schema.r(a integer, OUT b numeric(10,2), VARIADIC c double precision[]) AS $$ begin b := 1; end $$ LANGUAGE plpgsql",
			"REVOKE EXECUTE ON PROCEDURE test_allargs_schema.r(integer, double precision[]) FROM test_allargs_role",
		},
		{
			"routine_procedure", "routine",
			"CREATE PROCEDURE test_allargs_schema.r(a integer, OUT b numeric(10,2), VARIADIC c double precision[]) AS $$ begin b := 1; end $$ LANGUAGE plpgsql",
			"REVOKE EXECUTE ON PROCEDURE test_allargs_schema.r(integer, double precision[]) FROM test_allargs_role",
		},
		{
			"routine_function", "routine",
			"CREATE FUNCTION test_allargs_schema.r(a integer, OUT b numeric(10,2), VARIADIC c double precision[]) AS $$ select 1.0 $$ LANGUAGE SQL",
			"REVOKE EXECUTE ON FUNCTION test_allargs_schema.r(integer, double precision[]) FROM test_allargs_role",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := testPostgresDSN(t)
			// before the setup, which creates a procedure with an OUT argument
			var version int
			conn, err := sql.Open(testDriverName, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer closeDB(t, conn)
			if err := conn.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version < 140000 {
				t.Skipf("the types of the OUT arguments designate a routine since PostgreSQL 14, not in %d", version)
			}
			dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_allargs_role LOGIN PASSWORD '%s'", testRolePassword))
			dbExecute(t, dsn, "CREATE SCHEMA test_allargs_schema")
			dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_allargs_schema TO test_allargs_role")
			dbExecute(t, dsn, test.create)
			dbExecute(t, dsn, "REVOKE ALL ON ALL ROUTINES IN SCHEMA test_allargs_schema FROM PUBLIC")
			defer func() {
				dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_allargs_schema CASCADE")
				dbExecute(t, dsn, "DROP ROLE IF EXISTS test_allargs_role")
			}()

			config := fmt.Sprintf(`
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_allargs_role"
  schema      = "test_allargs_schema"
  object_type = "%s"
  privileges  = ["EXECUTE"]
  objects     = ["r(integer, numeric(10,2), double precision[])"]
}
`, test.objectType)
			check := func(*terraform.State) error {
				return testFunctionPrivilege(t, "test_allargs_role", "test_allargs_schema.r(integer,double precision[])", true)
			}
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
					testCheckCompatibleVersion(t, featureProcedure)
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					{Config: config, Check: check},
					{
						PreConfig:          func() { dbExecute(t, dsn, test.revoke) },
						Config:             config,
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					},
					{Config: config, Check: check},
				},
			})
		})
	}
}

// The types of all the arguments designate a procedure or a routine, never a function, and only when
// the signature has no OUT argument: with one, PostgreSQL looks for the input arguments only.
func TestAccPostgresqlResolveRoutineAllArgumentTypes(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	db, err := sql.Open(testDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(t, db)
	var version int
	if err := db.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 140000 {
		t.Skipf("a procedure has OUT arguments since PostgreSQL 14, not in %d", version)
	}
	dbExecute(t, dsn, "CREATE SCHEMA test_allres_schema")
	defer dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_allres_schema CASCADE")
	dbExecute(t, dsn, "CREATE PROCEDURE test_allres_schema.p(a integer, OUT b numeric) AS $$ begin b := 1; end $$ LANGUAGE plpgsql")
	dbExecute(t, dsn, "CREATE FUNCTION test_allres_schema.f(a integer, OUT b numeric) AS $$ select 1.0 $$ LANGUAGE SQL")

	for _, test := range []struct{ objectType, signature, want string }{
		{"procedure", "p(integer)", "test_allres_schema.p(integer)"},
		{"procedure", "p(integer, numeric)", "test_allres_schema.p(integer)"},
		{"routine", "p(integer, numeric)", "test_allres_schema.p(integer)"},
		{"routine", "f(integer, numeric)", "test_allres_schema.f(integer)"},
		{"procedure", "p(IN a integer, OUT b numeric)", "test_allres_schema.p(integer)"},
		// not the types of all the arguments
		{"procedure", "p(integer, text)", ""},
		{"procedure", "p(integer, numeric, numeric)", ""},
		// a function is not designated by its OUT arguments, and it is not a procedure
		{"function", "f(integer, numeric)", ""},
		{"function", "p(integer, numeric)", ""},
		{"procedure", "f(integer, numeric)", ""},
		// with an OUT argument the other ones are the input arguments, there is no procedure p(integer, numeric)
		{"procedure", "p(IN integer, IN numeric, OUT text)", ""},
	} {
		txn, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		oid, err := resolveFunctionSignature(txn, "test_allres_schema", test.objectType, test.signature)
		if err != nil {
			t.Errorf("%s %s: %v", test.objectType, test.signature, err)
		}
		got := ""
		if oid != "" {
			if err := txn.QueryRow("SELECT $1::oid::regprocedure::text", oid).Scan(&got); err != nil {
				t.Errorf("%s %s returned %q: %v", test.objectType, test.signature, oid, err)
			}
		}
		if got != test.want {
			t.Errorf("%s %s = %q, want %q", test.objectType, test.signature, got, test.want)
		}
		_ = txn.Rollback()
	}
}

func TestAccPostgresqlGrantFunctionOutArguments(t *testing.T) {
	skipIfNotAcc(t)

	for _, kind := range []string{"function", "procedure"} {
		t.Run(kind, func(t *testing.T) {
			dsn := testPostgresDSN(t)
			if kind == "procedure" {
				// before the setup, which creates a procedure with an OUT argument
				var version int
				conn, err := sql.Open(testDriverName, dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer closeDB(t, conn)
				if err := conn.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
					t.Fatal(err)
				}
				if version < 140000 {
					t.Skipf("a procedure has OUT arguments since PostgreSQL 14, not in %d", version)
				}
			}
			dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_out_role LOGIN PASSWORD '%s'", testRolePassword))
			dbExecute(t, dsn, "CREATE SCHEMA test_out_schema")
			dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_out_schema TO test_out_role")
			if kind == "function" {
				dbExecute(t, dsn, "CREATE FUNCTION test_out_schema.f(a integer, OUT b integer) AS $$ select 1 $$ LANGUAGE SQL")
				dbExecute(t, dsn, "CREATE FUNCTION test_out_schema.f(a integer, b integer) RETURNS integer AS $$ select 1 $$ LANGUAGE SQL")
			} else {
				dbExecute(t, dsn, "CREATE PROCEDURE test_out_schema.f(a integer, OUT b integer) AS $$ begin b := 1; end $$ LANGUAGE plpgsql")
				dbExecute(t, dsn, "CREATE PROCEDURE test_out_schema.f(a integer, b integer) AS $$ select 1 $$ LANGUAGE SQL")
			}
			dbExecute(t, dsn, "REVOKE ALL ON ALL ROUTINES IN SCHEMA test_out_schema FROM PUBLIC")
			defer func() {
				dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_out_schema CASCADE")
				dbExecute(t, dsn, "DROP ROLE IF EXISTS test_out_role")
			}()

			config := fmt.Sprintf(`
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_out_role"
  schema      = "test_out_schema"
  object_type = "%s"
  privileges  = ["EXECUTE"]
  objects     = ["f(IN a integer, OUT b integer)"]
}
`, kind)
			check := func(*terraform.State) error {
				if err := testFunctionPrivilege(t, "test_out_role", "test_out_schema.f(integer)", true); err != nil {
					return err
				}
				return testFunctionPrivilege(t, "test_out_role", "test_out_schema.f(integer,integer)", false)
			}
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testCheckCompatibleVersion(t, featurePrivileges)
					if kind == "procedure" {
						testCheckCompatibleVersion(t, featureProcedure)
					}
				},
				Providers: testAccProviders,
				Steps: []resource.TestStep{
					// the apply leaves nothing to change: the privileges are read from the right object
					{Config: config, Check: check},
					{
						PreConfig: func() {
							dbExecute(t, dsn, fmt.Sprintf("REVOKE EXECUTE ON %s test_out_schema.f(integer) FROM test_out_role", strings.ToUpper(kind)))
						},
						Config:             config,
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					},
					{Config: config, Check: check},
				},
			})
		})
	}
}

// TestAccPostgresqlGrantFunctionUnknownSignature checks the error of a function which does not exist, given
// with its argument types: the one of PostgreSQL, not one about an OID.
func TestAccPostgresqlGrantFunctionUnknownSignature(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_ov_role"
  schema      = "test_ov_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["f_ov(boolean)"]
}
`,
				ExpectError: regexp.MustCompile(`function .*f_ov\(boolean\) does not exist`),
			},
		},
	})
}

// TestAccPostgresqlResolveFunctionSignature resolves signatures against the catalogs of the server.
func TestAccPostgresqlResolveFunctionSignature(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()
	dbExecute(t, testPostgresDSN(t), "CREATE FUNCTION test_ov_schema.f_one() RETURNS int AS $$ select 1 $$ LANGUAGE SQL")

	db, err := sql.Open(testDriverName, testPostgresDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(t, db)
	for signature, want := range map[string]string{
		"f_ov(integer)":    "test_ov_schema.f_ov(integer)",
		" f_ov(a text) ":   "test_ov_schema.f_ov(text)",
		"f_ov(integer, )":  "test_ov_schema.f_ov(integer)", // an empty argument is ignored
		"f_one()":          "test_ov_schema.f_one()",
		"f_one( )":         "test_ov_schema.f_one()",
		"f_ov(boolean)":    "",
		"f_one(IN)":        "", // IN is the name of a type here, not a mode
		"f_one":            "",
		"f_ov(integer":     "",
		"f_ov integer)":    "",
		"f_ov(integer))":   "",
		"f_ov(no_such_t)":  "",
		"f_nothing(text)":  "",
		"(integer)":        "", // no name
		"f_ov(text, text)": "",
	} {
		txn, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		oid, err := resolveFunctionSignature(txn, "test_ov_schema", "function", signature)
		if err != nil {
			t.Errorf("resolveFunctionSignature(%q): %v", signature, err)
		}
		got := ""
		if oid != "" {
			if err := txn.QueryRow("SELECT $1::oid::regprocedure::text", oid).Scan(&got); err != nil {
				t.Errorf("resolveFunctionSignature(%q) returned %q: %v", signature, oid, err)
			}
		}
		if got != want {
			t.Errorf("resolveFunctionSignature(%q) = %q, want %q", signature, got, want)
		}
		_ = txn.Rollback()
	}
}

// A malformed signature is refused before any query: there is no transaction to use.
func TestResolveFunctionSignatureMalformed(t *testing.T) {
	for _, signature := range []string{"f", "f(integer", "f integer)", "abc)", ""} {
		if oid, err := resolveFunctionSignature(nil, "s", "function", signature); oid != "" || err != nil {
			t.Errorf("resolveFunctionSignature(%q) = %q, %v, want no function and no error", signature, oid, err)
		}
	}
}

func TestSplitTopLevel(t *testing.T) {
	for in, want := range map[string][]string{
		"":                                  nil,
		"  ":                                nil,
		"integer":                           {"integer"},
		"a, b":                              {"a", " b"},
		"a,":                                {"a", ""},
		"varchar(10), numeric(10,2)":        {"varchar(10)", " numeric(10,2)"},
		"integer[], int4range[2,3]":         {"integer[]", " int4range[2,3]"},
		`"a,b" integer, c`:                  {`"a,b" integer`, " c"},
		`"a""," integer, "(" text, d text)`: {`"a""," integer`, ` "(" text`, " d text)"},
	} {
		got := splitTopLevel(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("splitTopLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstTokenEnd(t *testing.T) {
	for in, want := range map[string]int{
		"":                       0,
		"integer":                7,
		"a integer":              1,
		"a\tinteger":             1,
		"a\ninteger":             1,
		"varchar(10) x":          11,
		"numeric(10, 2) x":       14,
		`"my arg" integer`:       8,
		`"my ""x"" arg" integer`: 14,
		`"(" integer`:            3,
	} {
		if got := firstTokenEnd(in); got != want {
			t.Errorf("firstTokenEnd(%q) = %d, want %d", in, got, want)
		}
	}
}

// A word which is a mode is a mode only when a type follows it. No query is needed for these.
func TestParseFunctionArgumentWithoutQuery(t *testing.T) {
	for in, want := range map[string][2]string{
		"integer":            {"", "integer"},
		"IN":                 {"", "IN"},
		"out":                {"", "out"},
		"OUT integer":        {"OUT", "integer"},
		"inout  integer[]":   {"INOUT", "integer[]"},
		"VARIADIC integer[]": {"VARIADIC", "integer[]"},
		`IN "a b" integer`:   {"IN", `integer`},
		"":                   {"", ""},
	} {
		if in == `IN "a b" integer` {
			continue // a name and a type need a query to be told from a type of two words
		}
		mode, argType, err := parseFunctionArgument(nil, in)
		if err != nil || mode != want[0] || argType != want[1] {
			t.Errorf("parseFunctionArgument(%q) = %q, %q, %v, want %q, %q", in, mode, argType, err, want[0], want[1])
		}
	}
}

// fakeDriver is a database/sql driver which fails the statements named by failOn (a prefix of the query).
type fakeDriver struct{ failOn []string }

type fakeConn struct{ d *fakeDriver }
type fakeStmt struct {
	c     *fakeConn
	query string
}
type fakeRows struct{ done bool }
type fakeTx struct{}

func (d *fakeDriver) Open(string) (driver.Conn, error) { return &fakeConn{d}, nil }
func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return &fakeStmt{c, query}, nil
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit() error                  { return nil }
func (fakeTx) Rollback() error                { return nil }
func (s *fakeStmt) Close() error              { return nil }
func (s *fakeStmt) NumInput() int             { return -1 }
func (s *fakeStmt) fail() error {
	for _, prefix := range s.c.d.failOn {
		if strings.HasPrefix(s.query, prefix) {
			return errors.New("fake failure of " + prefix)
		}
	}
	return nil
}
func (s *fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.ResultNoRows, s.fail()
}
func (s *fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	if err := s.fail(); err != nil {
		return nil, err
	}
	return &fakeRows{}, nil
}
func (r *fakeRows) Columns() []string { return []string{"v"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = "42"
	return nil
}

// TestSoftQueryStringErrors checks which failures softQueryString reports: a failing statement is not one,
// as the savepoint is rolled back, but a savepoint which cannot be rolled back is.
func TestSoftQueryStringErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		failOn  []string
		wantOK  bool
		wantErr bool
	}{
		"no failure":                   {nil, true, false},
		"failing statement":            {[]string{"SELECT"}, false, false},
		"failing savepoint":            {[]string{"SAVEPOINT"}, false, true},
		"failing release":              {[]string{"RELEASE"}, false, true},
		"failing statement + rollback": {[]string{"SELECT", "ROLLBACK"}, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			driverName := "tfpg-fake-" + strings.ReplaceAll(name, " ", "-")
			sql.Register(driverName, &fakeDriver{failOn: tc.failOn})
			db, err := sql.Open(driverName, "")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			txn, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			value, ok, err := softQueryString(txn, "SELECT 1", "x")
			if (err != nil) != tc.wantErr || ok != tc.wantOK {
				t.Errorf("softQueryString = %q, %v, %v, want ok %v, error %v", value, ok, err, tc.wantOK, tc.wantErr)
			}
			if tc.wantOK && value != "42" {
				t.Errorf("softQueryString returned %q, want 42", value)
			}
		})
	}
}

// TestAccPostgresqlGrantSchemaDroppedOutside drops, outside of Terraform, the schema of a grant on its tables: the
// grant is not there any more, the plan creates it again.
func TestAccPostgresqlGrantSchemaDroppedOutside(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_sd_role LOGIN PASSWORD '%s'", testRolePassword))
	createSchema := func() {
		dbExecute(t, dsn, "CREATE SCHEMA test_sd_schema")
		dbExecute(t, dsn, "GRANT USAGE ON SCHEMA test_sd_schema TO test_sd_role")
		dbExecute(t, dsn, "CREATE TABLE test_sd_schema.t (a int)")
	}
	createSchema()
	defer func() {
		dbExecute(t, dsn, "DROP SCHEMA IF EXISTS test_sd_schema CASCADE")
		dbExecute(t, dsn, "DROP ROLE IF EXISTS test_sd_role")
	}()

	config := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_sd_role"
  schema      = "test_sd_schema"
  object_type = "table"
  privileges  = ["SELECT"]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          func() { dbExecute(t, dsn, "DROP SCHEMA test_sd_schema CASCADE") },
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: createSchema,
				Config:    config,
				Check: func(*terraform.State) error {
					db := connectAsTestRole(t, "test_sd_role", "postgres")
					defer closeDB(t, db)
					return testHasGrantForQuery(db, "SELECT 1 FROM test_sd_schema.t", true)
				},
			},
		},
	})
}

// TestAccPostgresqlGrantSchemaOfObjectTypeWithoutSchema gives a schema which does not exist to a grant on an
// object which does not belong to a schema: the schema is not looked for.
func TestAccPostgresqlGrantSchemaOfObjectTypeWithoutSchema(t *testing.T) {
	skipIfNotAcc(t)

	dsn := testPostgresDSN(t)
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE test_ns_role LOGIN PASSWORD '%s'", testRolePassword))
	defer dbExecute(t, dsn, "DROP ROLE IF EXISTS test_ns_role")

	config := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_ns_role"
  schema      = "test_ns_no_such_schema"
  object_type = "database"
  privileges  = ["CONNECT"]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.TestCheckResourceAttr("postgresql_grant.test", "privileges.#", "1")},
			{Config: config, PlanOnly: true},
		},
	})
}

// TestAccPostgresqlGrantGrantedByReplace changes the grantor: the privileges of the first one are revoked, the
// resource is replaced and does not leave the grant of the former grantor.
func TestAccPostgresqlGrantGrantedByReplace(t *testing.T) {
	skipIfNotAcc(t)
	skipIfNotSuperuser(t)

	dsn := testPostgresDSN(t)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSkipUnlessGrantedBy(t)
			t.Cleanup(testSetupGrantedBy(t))
			dbExecute(t, dsn, "GRANT SELECT ON test_gb_schema.t TO test_gb_grantor WITH GRANT OPTION")
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testGrantedByConfig("test_gb_owner", `["SELECT"]`),
				Check:  testGrantedByACL(t, "test_gb_owner:SELECT"),
			},
			{
				Config: testGrantedByConfig("test_gb_grantor", `["SELECT"]`),
				Check:  testGrantedByACL(t, "test_gb_grantor:SELECT"),
			},
		},
	})
}

// TestGrantedByRequiresNew checks that the plan replaces the grant when its grantor changes: the grantor is the
// one whose privileges are revoked by the destruction.
func TestGrantedByRequiresNew(t *testing.T) {
	r := resourcePostgreSQLGrant()
	state := &terraform.InstanceState{
		ID: "r_d_s_table",
		Attributes: map[string]string{
			"id":                "r_d_s_table",
			"database":          "d",
			"role":              "r",
			"schema":            "s",
			"object_type":       "table",
			"granted_by":        "first",
			"with_grant_option": "false",
			"privileges.#":      "1",
			fmt.Sprintf("privileges.%d", schema.HashString("SELECT")): "SELECT",
		},
	}
	for grantedBy, wantReplace := range map[string]bool{"first": false, "second": true} {
		config := terraform.NewResourceConfigRaw(map[string]any{
			"database":    "d",
			"role":        "r",
			"schema":      "s",
			"object_type": "table",
			"granted_by":  grantedBy,
			"privileges":  []any{"SELECT"},
		})
		diff, err := r.Diff(context.Background(), state, config, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := diff != nil && diff.RequiresNew(); got != wantReplace {
			t.Errorf("granted_by %q: the plan replaces the grant: %v, expected %v", grantedBy, got, wantReplace)
		}
	}
}

// TestAccPostgresqlGrantFunctionDroppedOutside drops, outside of Terraform, a function which the configuration
// grants with its signature: reading it is not an error (there is nothing to read), and the apply which follows
// the creation of a new function grants it again.
func TestAccPostgresqlGrantFunctionDroppedOutside(t *testing.T) {
	skipIfNotAcc(t)
	defer testSetupOverloads(t)()
	dsn := testPostgresDSN(t)

	config := `
resource "postgresql_grant" "test" {
  database    = "postgres"
  role        = "test_ov_role"
  schema      = "test_ov_schema"
  object_type = "function"
  privileges  = ["EXECUTE"]
  objects     = ["f_ov(integer)"]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{Config: config, Check: testCheckOverloads(t, true, false)},
			{
				PreConfig: func() { dbExecute(t, dsn, "DROP FUNCTION test_ov_schema.f_ov(integer)") },
				Config:    config,
				PlanOnly:  true,
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "CREATE FUNCTION test_ov_schema.f_ov(a integer) RETURNS int AS $$ select 1 $$ LANGUAGE SQL")
					dbExecute(t, dsn, "REVOKE ALL ON FUNCTION test_ov_schema.f_ov(integer) FROM PUBLIC")
				},
				Config: config,
				Check:  testCheckOverloads(t, true, false),
			},
		},
	})
}
