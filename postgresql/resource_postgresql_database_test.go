package postgresql

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlDatabase_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgreSQLDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.mydb"),
					resource.TestCheckResourceAttr(
						"postgresql_database.mydb", "name", "mydb"),
					resource.TestCheckResourceAttr(
						"postgresql_database.mydb", "owner", "myrole"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "owner", "myrole"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "name", "default_opts_name"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "template", "template0"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "encoding", "UTF8"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "lc_collate", "C"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "lc_ctype", "C"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "tablespace_name", "pg_default"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "connection_limit", "-1"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "is_template", "false"),
					resource.TestCheckResourceAttr(
						"postgresql_database.default_opts", "alter_object_ownership", "false"),

					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "owner", "myrole"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "name", "custom_template_db"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "template", "template0"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "encoding", "UTF8"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "lc_collate", "en_US.UTF-8"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "lc_ctype", "en_US.UTF-8"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "tablespace_name", "pg_default"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "connection_limit", "10"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "is_template", "true"),
					resource.TestCheckResourceAttr(
						"postgresql_database.modified_opts", "alter_object_ownership", "true"),

					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "owner", "myrole"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "name", "bad_template_db"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "template", "template0"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "encoding", "LATIN1"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "lc_collate", "C"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "lc_ctype", "C"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "tablespace_name", "pg_default"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "connection_limit", "0"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pathological_opts", "is_template", "true"),

					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "owner", "myrole"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "name", "pg_defaults_db"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "template", "DEFAULT"),
					// resource.TestCheckResourceAttr(
					// 	"postgresql_database.pg_default_opts", "encoding", "DEFAULT"),
					// resource.TestCheckResourceAttr(
					// 	"postgresql_database.pg_default_opts", "lc_collate", "DEFAULT"),
					// resource.TestCheckResourceAttr(
					//  "postgresql_database.pg_default_opts", "lc_ctype", "DEFAULT"),
					// resource.TestCheckResourceAttr(
					// 	"postgresql_database.pg_default_opts", "tablespace_name", "DEFAULT"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "connection_limit", "0"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "is_template", "true"),
				),
			},
		},
	})
}

func TestAccPostgresqlDatabase_DefaultOwner(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgreSQLDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.mydb_default_owner"),
					resource.TestCheckResourceAttr(
						"postgresql_database.mydb_default_owner", "name", "mydb_default_owner"),
					resource.TestCheckResourceAttrSet(
						"postgresql_database.mydb_default_owner", "owner"),
				),
			},
		},
	})
}

func TestAccPostgresqlDatabase_Update(t *testing.T) {

	// Version dependent features values will be set in PreCheck
	// because we need to access database to check Postgres version.

	// Allow connection depends on Postgres version (needs pg >= 9.5)
	var allowConnections bool

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)

			client := testAccProvider.Meta().(*Client)
			db, err := client.Connect()
			if err != nil {
				t.Fatalf("could not connect to database: %v", err)
			}
			allowConnections = db.featureSupported(featureDBAllowConnections)

		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource postgresql_database test_db {
    name = "test_db"
	allow_connections = "%t"
}
`, allowConnections),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "connection_limit", "-1"),
					resource.TestCheckResourceAttr(
						"postgresql_database.test_db", "allow_connections",
						strconv.FormatBool(allowConnections),
					),
				),
			},
			{
				Config: `
resource postgresql_database test_db {
	name = "test_db"
	connection_limit = 2
	allow_connections = false
}
	`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "connection_limit", "2"),
					resource.TestCheckResourceAttr(
						"postgresql_database.test_db", "allow_connections", "false",
					),
				),
			},
		},
	})
}

// Test the case where we need to grant the owner to the connected user.
// The owner should be revoked
func TestAccPostgresqlDatabase_GrantOwner(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	var stateConfig = `
resource postgresql_role "test_owner" {
       name = "test_owner"
}
resource postgresql_database "test_db" {
       name  = "test_db"
       owner = "${postgresql_role.test_owner.name}"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: stateConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "owner", "test_owner"),

					// check if connected user does not have test_owner granted anymore.
					checkUserMembership(t, config.Username, "test_owner", false),
				),
			},
		},
	})
}

// Test the case where the connected user is already a member of the owner.
// There were a bug which was revoking the owner anyway.
func TestAccPostgresqlDatabase_GrantOwnerNotNeeded(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	dbExecute(
		t, dsn,
		fmt.Sprintf("CREATE ROLE test_owner; GRANT test_owner TO %s", config.Username),
	)
	defer func() {
		dbExecute(t, dsn, "DROP ROLE test_owner")
	}()

	var stateConfig = `
resource postgresql_database "test_db" {
       name  = "test_db"
       owner = "test_owner"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: stateConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "owner", "test_owner"),

					// check if connected user still have test_owner granted.
					checkUserMembership(t, config.Username, "test_owner", true),
				),
			},
		},
	})
}

// Test the case where the owned objects by the previous database owner are altered.
func TestAccPostgresqlDatabase_AlterObjectOwnership(t *testing.T) {
	skipIfNotAcc(t)

	const (
		databaseSuffix = "ownership"
		tableName      = "testtable1"
		previous_owner = "previous_owner"
		new_owner      = "new_owner"
	)

	databaseName := fmt.Sprintf("%s_%s", dbNamePrefix, databaseSuffix)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	for _, role := range []string{previous_owner, new_owner} {
		dbExecute(
			t, dsn,
			fmt.Sprintf("CREATE ROLE %s;", role),
		)
		defer func(role string) {
			dbExecute(t, dsn, fmt.Sprintf("DROP ROLE %s", role))
		}(role)

	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource postgresql_database "test_db" {
       name  = "tf_tests_db_ownership"
       owner = "previous_owner"
	   alter_object_ownership = true
}
`,
				Check: func(*terraform.State) error {
					// To test default privileges, we need to create a table
					// after having apply the state.
					_ = createTestTables(t, databaseSuffix, []string{tableName}, previous_owner)
					return nil
				},
			},
			{
				Config: `
resource postgresql_database "test_db" {
       name  = "tf_tests_db_ownership"
       owner = "new_owner"
	   alter_object_ownership = true
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "name", databaseName),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "owner", new_owner),
					resource.TestCheckResourceAttr("postgresql_database.test_db", "alter_object_ownership", "true"),

					checkTableOwnership(t, config.connStr(databaseName), new_owner, tableName),
				),
			},
		},
	})

}

func checkUserMembership(
	t *testing.T, member, role string, shouldHaveRole bool,
) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			t.Fatalf("could to create connection pool: %v", err)
		}

		var _rez int
		query := "SELECT 1 FROM pg_auth_members WHERE pg_get_userbyid(roleid) = $1 AND pg_get_userbyid(member) = $2"
		if db.featureSupported(featureCreateRoleSelfGrant) {
			query += " AND (set_option OR inherit_option)"
		}
		err = db.QueryRow(query, role, member).Scan(&_rez)

		switch {
		case err == sql.ErrNoRows:
			if shouldHaveRole {
				return fmt.Errorf(
					"User %s is not a member of %s",
					member, role,
				)
			}
			return nil

		case err != nil:
			t.Fatalf("could not check granted role: %v", err)
		}

		if !shouldHaveRole {
			return fmt.Errorf(
				"User (%s) should not be a member of %s",
				member, role,
			)
		}
		return nil
	}
}

func checkTableOwnership(
	t *testing.T, dsn, owner, tableName string,
) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatalf("could not create connection pool: %v", err)
		}
		defer closeDB(t, db)

		var _rez int

		err = db.QueryRow(`
			SELECT 1 FROM pg_tables
			WHERE tablename = $1 AND tableowner = $2
		`, tableName, owner).Scan(&_rez)

		switch {
		case err == sql.ErrNoRows:
			return fmt.Errorf(
				"User %s should be owner of %s but is not", owner, tableName,
			)
		case err != nil:
			t.Fatalf("error checking table ownership. %v", err)

		}

		return nil

	}
}

func testAccCheckPostgresqlDatabaseDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_database" {
			continue
		}

		exists, err := checkDatabaseExists(client, rs.Primary.ID)

		if err != nil {
			return fmt.Errorf("error checking db %s", err)
		}

		if exists {
			return errors.New("Db still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlDatabaseExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return errors.New("No ID is set")
		}

		client := testAccProvider.Meta().(*Client)
		exists, err := checkDatabaseExists(client, rs.Primary.ID)

		if err != nil {
			return fmt.Errorf("error checking db %s", err)
		}

		if !exists {
			return errors.New("Db not found")
		}

		return nil
	}
}

func checkDatabaseExists(client *Client, dbName string) (bool, error) {
	db, err := client.Connect()
	if err != nil {
		return false, err
	}
	var _rez int
	err = db.QueryRow("SELECT 1 from pg_database d WHERE datname=$1", dbName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about database: %s", err)
	}

	return true, nil
}

var testAccPostgreSQLDatabaseConfig = `
resource "postgresql_role" "myrole" {
  name = "myrole"
  login = true
}

resource "postgresql_database" "mydb" {
   name = "mydb"
   owner = "${postgresql_role.myrole.name}"
}

resource "postgresql_database" "mydb2" {
   name = "mydb2"
   owner = "${postgresql_role.myrole.name}"
}

resource "postgresql_database" "default_opts" {
   name = "default_opts_name"
   owner = "${postgresql_role.myrole.name}"
   template = "template0"
   encoding = "UTF8"
   lc_collate = "C"
   lc_ctype = "C"
   connection_limit = -1
   is_template = false
   alter_object_ownership = false
}

resource "postgresql_database" "modified_opts" {
   name = "custom_template_db"
   owner = "${postgresql_role.myrole.name}"
   template = "template0"
   encoding = "UTF8"
   lc_collate = "en_US.UTF-8"
   lc_ctype = "en_US.UTF-8"
   connection_limit = 10
   is_template = true
   alter_object_ownership = true
}

resource "postgresql_database" "pathological_opts" {
   name = "bad_template_db"
   owner = "${postgresql_role.myrole.name}"
   template = "template0"
   encoding = "LATIN1"
   lc_collate = "C"
   lc_ctype = "C"
   connection_limit = 0
   is_template = true
}

resource "postgresql_database" "pg_default_opts" {
  lifecycle {
    ignore_changes = [
      "template",
      "encoding",
      "lc_collate",
      "lc_ctype",
      "tablespace_name",
    ]
  }

  name = "pg_defaults_db"
  owner = "${postgresql_role.myrole.name}"
  template = "DEFAULT"
  encoding = "DEFAULT"
  lc_collate = "DEFAULT"
  lc_ctype = "DEFAULT"
  tablespace_name = "DEFAULT"
  connection_limit = 0
  is_template = true
}

resource "postgresql_database" "mydb_default_owner" {
   name = "mydb_default_owner"
}

`

func TestAccPostgresqlDatabase_Parameters(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	dbConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "params_db" {
  name = "params_db"
  %s
}
`, parameters)
	}

	configCreate := dbConfig(`parameters = {
    work_mem = "64MB"
    timezone = "UTC"
  }`)
	configUpdate := dbConfig(`parameters = {
    work_mem     = "128MB"
    lock_timeout = "5s"
  }`)
	configNoParameters := dbConfig("")
	configEmptyParameters := dbConfig("parameters = {}")

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.params_db"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "2"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.work_mem", "64MB"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.timezone", "UTC"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=64MB", "TimeZone=UTC"}),
				),
			},
			// Parameters are changed, removed and added.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "2"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.work_mem", "128MB"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.lock_timeout", "5s"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=128MB", "lock_timeout=5s"}),
				),
			},
			// Changes made outside of Terraform are detected...
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER DATABASE params_db RESET work_mem")
					dbExecute(t, dsn, "ALTER DATABASE params_db SET timezone TO 'UTC'")
				},
				Config:             configUpdate,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// ...and reverted.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "2"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=128MB", "lock_timeout=5s"}),
				),
			},
			// The existing parameters are read by the import.
			{
				ResourceName:            "postgresql_database.params_db",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
			},
			// An empty map resets all the parameters.
			{
				Config: configEmptyParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "0"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{}),
				),
			},
			// Without parameters in the resource, the ones set outside of Terraform
			// are read but left untouched.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER DATABASE params_db SET work_mem TO '32MB'")
				},
				Config: configNoParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "1"),
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.work_mem", "32MB"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=32MB"}),
				),
			},
			// As soon as parameters is defined, the ones which are not in the resource are reset.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "2"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=128MB", "lock_timeout=5s"}),
				),
			},
			// Removing parameters from the resource keeps the existing ones.
			{
				Config: configNoParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.params_db", "parameters.%", "2"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_db", []string{"work_mem=128MB", "lock_timeout=5s"}),
				),
			},
		},
	})
}

// Test every parameter PostgreSQL allows to set for a database.
func TestAccPostgresqlDatabase_ParametersAllSettable(t *testing.T) {
	skipIfNotAcc(t)

	parameters := testSettableParameters(t, nil)

	dbConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "all_params_db" {
  name       = "all_params_db"
  parameters = %s
}
`, parameters)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig(testParametersHCL(parameters)),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.all_params_db", "parameters.%", strconv.Itoa(len(parameters))),
					testAccCheckAllConfigParameters(testDatabaseConfigQuery, "all_params_db", nil, parameters),
				),
			},
			{
				ResourceName:            "postgresql_database.all_params_db",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
			},
			{
				Config: dbConfig("{}"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.all_params_db", "parameters.%", "0"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "all_params_db", []string{}),
				),
			},
		},
	})
}

// Test that the parameters can be set on a database owned by another role
// when the connected user is not a superuser.
func TestAccPostgresqlDatabase_ParametersGrantOwner(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	dbConfig := func(workMem string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "params_owner" {
  name = "params_owner"
}
resource "postgresql_database" "params_db" {
  name  = "params_owned_db"
  owner = postgresql_role.params_owner.name
  parameters = {
    work_mem = "%s"
  }
}
`, workMem)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("64MB"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_owned_db", []string{"work_mem=64MB"}),
					checkUserMembership(t, config.Username, "params_owner", false),
				),
			},
			{
				Config: dbConfig("128MB"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "params_owned_db", []string{"work_mem=128MB"}),
					checkUserMembership(t, config.Username, "params_owner", false),
				),
			},
		},
	})
}

// Test the pgaudit settings of a database, this is the way to enable / disable
// the audit of a specific database (e.g.: on AWS RDS).
func TestAccPostgresqlDatabase_ParametersPgaudit(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)

	dbConfig := func(pgauditLog string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "audited_db" {
  name = "audited_db"
  parameters = {
    "pgaudit.log" = "%s"
  }
}
`, pgauditLog)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSuperuserPreCheck(t)
			testCheckPgauditLoaded(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("all"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.audited_db", "parameters.pgaudit.log", "all"),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "audited_db", []string{"pgaudit.log=all"}),
					testAccCheckSessionParameter(t, config.Username, config.Password, "audited_db", "pgaudit.log", "all"),
				),
			},
			{
				Config: dbConfig("none"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckSessionParameter(t, config.Username, config.Password, "audited_db", "pgaudit.log", "none"),
				),
			},
		},
	})
}
