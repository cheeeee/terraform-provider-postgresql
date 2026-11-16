package postgresql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"syscall"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
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
					// The state holds what the server stored, never the DEFAULT keyword: DEFAULT
					// copies the encoding, the collation, the character classification and the
					// tablespace of the template (template1), which only the catalogs can prove.
					testCheckDatabaseValue(t, "true", `SELECT (d.encoding = t.encoding AND d.datcollate = t.datcollate
						AND d.datctype = t.datctype AND d.dattablespace = t.dattablespace)::text
						FROM pg_database d, pg_database t WHERE d.datname = $1 AND t.datname = 'template1'`, "pg_defaults_db"),
					resource.TestCheckResourceAttr(
						"postgresql_database.pg_default_opts", "tablespace_name", "pg_default"),
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
	skipIfNotAcc(t)

	// Both values of allow_connections are tested as the starting point of the update.
	for _, allowConnections := range []bool{true, false} {
		t.Run(fmt.Sprintf("allow_connections_%t", allowConnections), func(t *testing.T) {
			const allowQuery = "SELECT datallowconn::text FROM pg_database WHERE datname = $1"
			dbConfig := func(allow bool, limit int) string {
				return fmt.Sprintf(`
resource postgresql_database test_db {
	name = "test_db"
	connection_limit = %d
	allow_connections = %t
}
`, limit, allow)
			}

			resource.Test(t, resource.TestCase{
				PreCheck:     func() { testAccPreCheck(t) },
				Providers:    testAccProviders,
				CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
				Steps: []resource.TestStep{
					{
						Config: dbConfig(allowConnections, -1),
						Check: resource.ComposeTestCheckFunc(
							testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "connection_limit", "-1"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "allow_connections", strconv.FormatBool(allowConnections)),
							testCheckDatabaseValue(t, strconv.FormatBool(allowConnections), allowQuery, "test_db"),
						),
					},
					{
						Config: dbConfig(!allowConnections, 2),
						Check: resource.ComposeTestCheckFunc(
							testAccCheckPostgresqlDatabaseExists("postgresql_database.test_db"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "name", "test_db"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "connection_limit", "2"),
							resource.TestCheckResourceAttr("postgresql_database.test_db", "allow_connections", strconv.FormatBool(!allowConnections)),
							testCheckDatabaseValue(t, strconv.FormatBool(!allowConnections), allowQuery, "test_db"),
							testCheckDatabaseValue(t, "2", "SELECT datconnlimit::text FROM pg_database WHERE datname = $1", "test_db"),
						),
					},
				},
			})
		})
	}
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

// On AWS RDS, a member of rds_iam can only log in with an IAM token: the owner of
// a database must not be granted to a user which is connected with a password.
func TestAccPostgresqlDatabase_OwnerMemberOfRDSIAM(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	// rds_iam only exists on AWS RDS, where the test is skipped: it would lock the connected user out.
	var exists bool
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rds_iam')").Scan(&exists); err != nil {
		t.Fatalf("could not check if rds_iam exists: %v", err)
	}
	closeDB(t, db)
	if exists {
		t.Skip("Skip test: the role rds_iam already exists")
	}

	dbExecute(t, dsn, "CREATE ROLE rds_iam")
	defer func() {
		dbExecute(t, dsn, "DROP ROLE rds_iam")
	}()

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "iam_owner" {
  name  = "iam_owner"
  roles = ["rds_iam"]
}
resource "postgresql_database" "iam_db" {
  name  = "iam_db"
  owner = postgresql_role.iam_owner.name
}
`,
				ExpectError: regexp.MustCompile(`role iam_owner is a member of rds_iam: it cannot be granted to`),
			},
			// rds_iam itself cannot be the owner either
			{
				Config: `
resource "postgresql_database" "iam_db" {
  name  = "iam_db"
  owner = "rds_iam"
}
`,
				ExpectError: regexp.MustCompile(`role rds_iam is a member of rds_iam: it cannot be granted to`),
			},
			// The owner is a group which is not a member of rds_iam, the role which logs in is a member of both.
			{
				Config: `
resource "postgresql_role" "iam_owner" {
  name = "iam_owner"
}
resource "postgresql_role" "iam_login" {
  name        = "iam_login"
  login       = true
  roles       = ["rds_iam", postgresql_role.iam_owner.name]
  assume_role = postgresql_role.iam_owner.name
}
resource "postgresql_database" "iam_db" {
  name  = "iam_db"
  owner = postgresql_role.iam_owner.name
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.iam_db"),
					resource.TestCheckResourceAttr("postgresql_database.iam_db", "owner", "iam_owner"),
					checkUserMembership(t, config.Username, "rds_iam", false),
				),
			},
		},
	})
}

// A user which logs in with an IAM token is already a member of rds_iam:
// it can create a database owned by another member of rds_iam.
func TestAccPostgresqlDatabase_OwnerMemberOfRDSIAMWithIAMUser(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	var exists bool
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rds_iam')").Scan(&exists); err != nil {
		t.Fatalf("could not check if rds_iam exists: %v", err)
	}
	closeDB(t, db)
	if exists {
		t.Skip("Skip test: the role rds_iam already exists")
	}

	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE rds_iam; GRANT rds_iam TO %s", config.Username))
	defer func() {
		dbExecute(t, dsn, "DROP ROLE rds_iam")
	}()

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "iam_owner" {
  name  = "iam_owner"
  roles = ["rds_iam"]
}
resource "postgresql_database" "iam_db" {
  name  = "iam_db"
  owner = postgresql_role.iam_owner.name
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.iam_db"),
					resource.TestCheckResourceAttr("postgresql_database.iam_db", "owner", "iam_owner"),
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

	rounds := testParametersRounds(testSettableParameters(t, nil))

	dbConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "all_params_db" {
  name       = "all_params_db"
  parameters = %s
}
`, parameters)
	}

	// All the parameters are set at once, with other values at each step
	var steps []resource.TestStep
	for _, parameters := range rounds {
		steps = append(steps, resource.TestStep{
			Config: dbConfig(testParametersHCL(parameters)),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.all_params_db", "parameters.%", strconv.Itoa(len(parameters))),
				testAccCheckAllConfigParameters(testDatabaseConfigQuery, "all_params_db", nil, parameters),
			),
		})
	}
	steps = append(steps,
		resource.TestStep{
			ResourceName:            "postgresql_database.all_params_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		resource.TestStep{
			Config: dbConfig("{}"),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.all_params_db", "parameters.%", "0"),
				testAccCheckConfigParameters(testDatabaseConfigQuery, "all_params_db", []string{}),
			),
		},
	)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        steps,
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

// Test that the error of PostgreSQL is reported when a parameter cannot be set
// for a database (e.g.: the ones which need a restart of the server).
func TestAccPostgresqlDatabase_ParametersRejected(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(name, parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "rejected_params_db" {
  name = "%s"
  %s
}
`, name, parameters)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			// at the creation
			{
				Config:      dbConfig("rejected_params_db", `parameters = { shared_buffers = "1GB" }`),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for database rejected_params_db`),
			},
			{
				Config: dbConfig("rejected_params_db", `parameters = { work_mem = "64MB" }`),
				Check:  testAccCheckConfigParameters(testDatabaseConfigQuery, "rejected_params_db", []string{"work_mem=64MB"}),
			},
			// on update, the other parameters are not changed
			{
				Config:      dbConfig("rejected_params_db", `parameters = { work_mem = "128MB", shared_buffers = "1GB" }`),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for database rejected_params_db`),
			},
			{
				Config: dbConfig("rejected_params_db", `parameters = { work_mem = "64MB" }`),
				Check:  testAccCheckConfigParameters(testDatabaseConfigQuery, "rejected_params_db", []string{"work_mem=64MB"}),
			},
		},
	})
}

// Test the parameters which are a list of identifiers: PostgreSQL stores them in
// a normalized form, the differences of formatting must not be seen as a change.
func TestAccPostgresqlDatabase_ParametersList(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(searchPath, otherSearchPath, parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "list_params_db" {
  name = "list_params_db"
  parameters = {
    search_path = %s
    %s
  }
}

resource "postgresql_database" "list_params_db2" {
  name = "list_params_db2"
  parameters = {
    search_path = %s
  }
}
`, strconv.Quote(searchPath), parameters, strconv.Quote(otherSearchPath))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			// The lists are not written as PostgreSQL stores them: no quote, no space,
			// an empty list, a name which needs to be quoted.
			{
				Config: dbConfig(`$user,public`, `Foo`, `temp_tablespaces = ""`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db", []string{
						`search_path="$user", public`, `temp_tablespaces=""`,
					}),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db2", []string{`search_path="Foo"`}),
				),
			},
			// An element is changed, the number of elements is the same.
			{
				Config: dbConfig(`$user,other`, `bar`, `temp_tablespaces = ""`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db", []string{
						`search_path="$user", other`, `temp_tablespaces=""`,
					}),
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db2", []string{`search_path=bar`}),
				),
			},
			// The empty list is removed.
			{
				Config: dbConfig(`$user,other`, `bar`, ``),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db", []string{`search_path="$user", other`}),
				),
			},
			// An empty list is not the same as no parameter.
			{
				Config: dbConfig(`$user,other`, `bar`, `temp_tablespaces = ""`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckConfigParameters(testDatabaseConfigQuery, "list_params_db", []string{
						`search_path="$user", other`, `temp_tablespaces=""`,
					}),
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
			testCheckPgauditLoaded(t)
			testCheckCanSetParameter(t, "pgaudit.log", "none")
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

// testSkipIfOlderThan skips the test if the server is older than minVersion (server_version_num).
func testSkipIfOlderThan(t *testing.T, minVersion int, feature string) {
	if version := testServerVersionNum(t); version < minVersion {
		t.Skipf("Skip test: %s needs a PostgreSQL server of version %d or later", feature, minVersion)
	}
}

// testDatabaseOptionSteps returns the steps if the server accepts the CREATE DATABASE option,
// otherwise a single step which checks that the provider refuses it with a clear error.
func testDatabaseOptionSteps(t *testing.T, minVersion int, clause, config string, steps []resource.TestStep) []resource.TestStep {
	if testServerVersionNum(t) >= minVersion {
		return steps
	}
	return []resource.TestStep{{
		Config:      config,
		ExpectError: regexp.MustCompile(`does not support database ` + clause),
	}}
}

// testQueryDatabase returns the first column of the first row returned by a query on the server.
func testQueryDatabase(t *testing.T, query string, args ...any) string {
	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	defer closeDB(t, db)

	var value sql.NullString
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("could not run the query %q: %v", query, err)
	}
	return value.String
}

// testCheckDatabaseValue checks the value returned by a query on the server (e.g.: a catalog query).
func testCheckDatabaseValue(t *testing.T, expected, query string, args ...any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := testQueryDatabase(t, query, args...); got != expected {
			return fmt.Errorf("query %q: expected %q, got %q", query, expected, got)
		}
		return nil
	}
}

// testICULocaleColumn returns the column of pg_database which stores the ICU locale.
func testICULocaleColumn(t *testing.T) string {
	if testServerVersionNum(t) >= 170000 {
		return "datlocale"
	}
	return "daticulocale"
}

// Renaming a database changes its name in place: the database is not replaced.
func TestAccPostgresqlDatabase_Rename(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(name string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "rename_db" {
  name = %q
}
`, name)
	}
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	var oid string
	recordOID := func(name string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			oid = testQueryDatabase(t, oidQuery, name)
			return nil
		}
	}
	checkSameOID := func(name string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := testQueryDatabase(t, oidQuery, name); got != oid {
				return fmt.Errorf("database %s was replaced: its OID changed from %s to %s", name, oid, got)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("rename_db_1"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.rename_db"),
					recordOID("rename_db_1"),
				),
			},
			{
				Config: dbConfig("rename_db_2"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.rename_db", "name", "rename_db_2"),
					testCheckDatabaseValue(t, "0", "SELECT count(*)::text FROM pg_database WHERE datname = $1", "rename_db_1"),
					checkSameOID("rename_db_2"),
				),
			},
			{
				// A rename made outside of Terraform is seen as a missing database: it is planned again.
				PreConfig: func() {
					config := getTestConfig(t)
					dbExecute(t, config.connStr("postgres"), `ALTER DATABASE rename_db_2 RENAME TO rename_db_3`)
				},
				Config:             dbConfig("rename_db_2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// The database moved away is dropped, the configuration creates it again.
				PreConfig: func() {
					config := getTestConfig(t)
					dbExecute(t, config.connStr("postgres"), `DROP DATABASE rename_db_3`)
				},
				Config: dbConfig("rename_db_2"),
				Check:  testAccCheckPostgresqlDatabaseExists("postgresql_database.rename_db"),
			},
		},
	})
}

// is_template can be set to true and back to false, the change made outside of Terraform is reverted.
func TestAccPostgresqlDatabase_IsTemplate(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	dbConfig := func(isTemplate string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "template_db" {
  name        = "template_db"
  is_template = %s
}
`, isTemplate)
	}
	const isTemplateQuery = "SELECT datistemplate::text FROM pg_database WHERE datname = $1"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("false"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.template_db", "is_template", "false"),
					testCheckDatabaseValue(t, "false", isTemplateQuery, "template_db"),
				),
			},
			{
				Config: dbConfig("true"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.template_db", "is_template", "true"),
					testCheckDatabaseValue(t, "true", isTemplateQuery, "template_db"),
				),
			},
			{
				Config: dbConfig("false"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.template_db", "is_template", "false"),
					testCheckDatabaseValue(t, "false", isTemplateQuery, "template_db"),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER DATABASE template_db IS_TEMPLATE true")
				},
				Config:             dbConfig("false"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: dbConfig("false"),
				Check:  testCheckDatabaseValue(t, "false", isTemplateQuery, "template_db"),
			},
		},
	})
}

// allow_connections can be false at the creation and changed in both directions.
func TestAccPostgresqlDatabase_AllowConnections(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	dbConfig := func(allowConnections string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "allow_conn_db" {
  name              = "allow_conn_db"
  allow_connections = %s
}
`, allowConnections)
	}
	const allowConnQuery = "SELECT datallowconn::text FROM pg_database WHERE datname = $1"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("false"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.allow_conn_db", "allow_connections", "false"),
					testCheckDatabaseValue(t, "false", allowConnQuery, "allow_conn_db"),
				),
			},
			{
				Config: dbConfig("true"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.allow_conn_db", "allow_connections", "true"),
					testCheckDatabaseValue(t, "true", allowConnQuery, "allow_conn_db"),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER DATABASE allow_conn_db ALLOW_CONNECTIONS false")
				},
				Config:             dbConfig("true"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: dbConfig("true"),
				Check:  testCheckDatabaseValue(t, "true", allowConnQuery, "allow_conn_db"),
			},
		},
	})
}

// connection_limit is changed in place, the change made outside of Terraform is reverted.
func TestAccPostgresqlDatabase_ConnectionLimit(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	dbConfig := func(connLimit string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "conn_limit_db" {
  name             = "conn_limit_db"
  connection_limit = %s
}
`, connLimit)
	}
	const connLimitQuery = "SELECT datconnlimit::text FROM pg_database WHERE datname = $1"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig("5"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.conn_limit_db", "connection_limit", "5"),
					testCheckDatabaseValue(t, "5", connLimitQuery, "conn_limit_db"),
				),
			},
			{
				Config: dbConfig("2"),
				Check:  testCheckDatabaseValue(t, "2", connLimitQuery, "conn_limit_db"),
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER DATABASE conn_limit_db CONNECTION LIMIT = 7")
				},
				Config:             dbConfig("2"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: dbConfig("-1"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.conn_limit_db", "connection_limit", "-1"),
					testCheckDatabaseValue(t, "-1", connLimitQuery, "conn_limit_db"),
				),
			},
		},
	})
}

// Changing the owner of a database changes the owner of the database only, the owner of its
// objects is changed too when alter_object_ownership is set.
func TestAccPostgresqlDatabase_OwnerChange(t *testing.T) {
	testAccPostgresqlDatabaseOwnerChange(t, false)
}

func TestAccPostgresqlDatabase_OwnerChangeAlterObjectOwnership(t *testing.T) {
	testAccPostgresqlDatabaseOwnerChange(t, true)
}

func testAccPostgresqlDatabaseOwnerChange(t *testing.T, alterObjectOwnership bool) {
	skipIfNotAcc(t)

	const (
		dbSuffix  = "owner_change"
		tableName = "owner_change_table"
		newOwner  = "owner_change_new"
	)
	dbName := fmt.Sprintf("%s_%s", dbNamePrefix, dbSuffix)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	// PostgreSQL only lets the owner of a database change its owner: without
	// alter_object_ownership, the database is first owned by the connected user.
	// With it, the objects of the initial owner are reassigned: it is a role of its own,
	// as the connected superuser owns objects which cannot be reassigned.
	initialOwner := config.Username
	tableCreator := ""
	roles := []string{newOwner}
	if alterObjectOwnership {
		initialOwner = "owner_change_initial"
		tableCreator = initialOwner
		roles = append(roles, initialOwner)
	}
	for _, role := range roles {
		dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE %s", role))
		defer dbExecute(t, dsn, fmt.Sprintf("DROP ROLE %s", role))
	}
	dbConfig := func(owner string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "owner_change_db" {
  name                   = %q
  owner                  = %q
  alter_object_ownership = %t
}
`, dbName, owner, alterObjectOwnership)
	}
	const ownerQuery = "SELECT pg_get_userbyid(datdba)::text FROM pg_database WHERE datname = $1"

	// The owner of the table is the one of the objects which are reassigned with the database.
	tableOwnerAfterChange := initialOwner
	if alterObjectOwnership {
		tableOwnerAfterChange = newOwner
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			// PostgreSQL denies REASSIGN OWNED to a role which is not a superuser.
			if alterObjectOwnership {
				testSuperuserPreCheck(t)
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig(initialOwner),
				Check: resource.ComposeTestCheckFunc(
					testCheckDatabaseValue(t, initialOwner, ownerQuery, dbName),
					func(*terraform.State) error {
						// The table is created after the database, by its owner.
						_ = createTestTables(t, dbSuffix, []string{tableName}, tableCreator)
						return nil
					},
				),
			},
			{
				Config: dbConfig(newOwner),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.owner_change_db", "owner", newOwner),
					testCheckDatabaseValue(t, newOwner, ownerQuery, dbName),
					checkTableOwnership(t, config.connStr(dbName), tableOwnerAfterChange, tableName),
				),
			},
		},
	})
}

// Dropping a database terminates the sessions which are connected to it.
func TestAccPostgresqlDatabase_DropWithOpenConnection(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	const dbName = "drop_open_db"

	var pool *sql.DB
	var conn *sql.Conn
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_database" "drop_open_db" {
  name = "drop_open_db"
}
`,
				Check: func(*terraform.State) error {
					var err error
					pool, err = sql.Open("postgres", config.connStr(dbName))
					if err != nil {
						return fmt.Errorf("could not open SQL connection: %w", err)
					}
					conn, err = pool.Conn(context.Background())
					if err != nil {
						return fmt.Errorf("could not connect to the database: %w", err)
					}
					var one int
					return conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&one)
				},
			},
			{
				Config: "# the database is removed while a session is connected to it\n",
				Check: func(*terraform.State) error {
					defer func() { _ = pool.Close() }()
					return testCheckSessionTerminated(conn)
				},
			},
		},
	})
}

// LOCALE sets LC_COLLATE and LC_CTYPE at once, it cannot be used with them.
func TestAccPostgresqlDatabase_Locale(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "locale_db" {
  name = "locale_db"
  %s
}
`, options)
	}
	const collateQuery = "SELECT datcollate || ' ' || datctype FROM pg_database WHERE datname = $1"
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	var oid string
	recordOID := func(*terraform.State) error {
		oid = testQueryDatabase(t, oidQuery, "locale_db")
		return nil
	}
	checkOIDChanged := func(*terraform.State) error {
		if got := testQueryDatabase(t, oidQuery, "locale_db"); got == oid {
			return errors.New("the database was not replaced by the change of locale")
		}
		return nil
	}
	checkSameOID := func(*terraform.State) error {
		if got := testQueryDatabase(t, oidQuery, "locale_db"); got != oid {
			return fmt.Errorf("the database was replaced: its OID changed from %s to %s", oid, got)
		}
		return nil
	}

	steps := []resource.TestStep{
		{
			Config: dbConfig(`locale = "C"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.locale_db", "locale", "C"),
				resource.TestCheckResourceAttr("postgresql_database.locale_db", "lc_collate", "C"),
				resource.TestCheckResourceAttr("postgresql_database.locale_db", "lc_ctype", "C"),
				testCheckDatabaseValue(t, "C C", collateQuery, "locale_db"),
				recordOID,
			),
		},
		{
			ResourceName:            "postgresql_database.locale_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		// LOCALE is set at the creation: the database is replaced.
		{
			Config: dbConfig(`locale = "en_US.UTF-8"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.locale_db", "locale", "en_US.UTF-8"),
				testCheckDatabaseValue(t, "en_US.UTF-8 en_US.UTF-8", collateQuery, "locale_db"),
				checkOIDChanged,
				recordOID,
			),
		},
		{
			Config:      "resource \"postgresql_database\" \"locale_db\" {\n  name   = \"locale_db\"\n  locale = \"C\"\n  lc_collate = \"C\"\n}\n",
			ExpectError: regexp.MustCompile(`conflicts with`),
		},
		// Without locale, the database is left as it is.
		{
			Config: dbConfig(""),
			Check: resource.ComposeTestCheckFunc(
				testCheckDatabaseValue(t, "en_US.UTF-8 en_US.UTF-8", collateQuery, "locale_db"),
				checkSameOID,
			),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 130000, "LOCALE", dbConfig(`locale = "C"`), steps),
	})
}

// tablespace_name is changed in place: DEFAULT moves the database back to pg_default.
// The tablespace needs a directory on the server, so it is created with
// allow_in_place_tablespaces (PostgreSQL 15 and above, superuser only).
func TestAccPostgresqlDatabase_Tablespace(t *testing.T) {
	skipIfNotAcc(t)
	testSkipIfOlderThan(t, 150000, "allow_in_place_tablespaces")

	if testQueryDatabase(t, "SELECT rolsuper::text FROM pg_roles WHERE rolname = current_user") != "true" {
		t.Skip("Skip test: CREATE TABLESPACE can be run only by a superuser")
	}
	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	const tablespace = "tf_tests_tablespace"

	dbExecute(t, dsn, fmt.Sprintf("DROP TABLESPACE IF EXISTS %s", tablespace))
	func() {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatalf("could not open SQL connection: %v", err)
		}
		defer closeDB(t, db)

		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatalf("could not connect to the database: %v", err)
		}
		defer func() { _ = conn.Close() }()

		// The option is set for this session only, the tablespace has no directory.
		for _, statement := range []string{
			"SET allow_in_place_tablespaces = on",
			fmt.Sprintf("CREATE TABLESPACE %s LOCATION ''", tablespace),
		} {
			if _, err := conn.ExecContext(context.Background(), statement); err != nil {
				t.Fatalf("could not run %q: %v", statement, err)
			}
		}
	}()
	defer dbExecute(t, dsn, fmt.Sprintf("DROP TABLESPACE %s", tablespace))

	const tablespaceQuery = "SELECT t.spcname FROM pg_database d JOIN pg_tablespace t ON t.oid = d.dattablespace WHERE d.datname = $1"
	dbConfig := func(tablespaceName string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "tablespace_db" {
  name            = "tablespace_db"
  tablespace_name = %q
}
`, tablespaceName)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: dbConfig(tablespace),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.tablespace_db", "tablespace_name", tablespace),
					testCheckDatabaseValue(t, tablespace, tablespaceQuery, "tablespace_db"),
				),
			},
			{
				Config: dbConfig("DEFAULT"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_database.tablespace_db", "tablespace_name", "pg_default"),
					testCheckDatabaseValue(t, "pg_default", tablespaceQuery, "tablespace_db"),
				),
			},
			// A move made outside of Terraform is reverted.
			{
				PreConfig: func() {
					dbExecute(t, dsn, fmt.Sprintf("ALTER DATABASE tablespace_db SET TABLESPACE %s", tablespace))
				},
				Config:             dbConfig("DEFAULT"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: dbConfig(tablespace),
				Check:  testCheckDatabaseValue(t, tablespace, tablespaceQuery, "tablespace_db"),
			},
			{
				Config: dbConfig("DEFAULT"),
				Check:  testCheckDatabaseValue(t, "pg_default", tablespaceQuery, "tablespace_db"),
			},
		},
	})
}

// strategy is only used at the creation: it is not read back, its changes are ignored.
func TestAccPostgresqlDatabase_Strategy(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(strategy string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "strategy_db" {
  name     = "strategy_db"
  strategy = %q
}
`, strategy)
	}
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	var oid string

	steps := []resource.TestStep{
		{
			// the values are case sensitive
			Config:      dbConfig("WAL_LOG"),
			ExpectError: regexp.MustCompile(`expected strategy to be one of`),
		},
		{
			Config: dbConfig("wal_log"),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.strategy_db", "strategy", "wal_log"),
				testCheckDatabaseValue(t, "strategy_db", "SELECT datname FROM pg_database WHERE datname = $1", "strategy_db"),
				func(*terraform.State) error {
					oid = testQueryDatabase(t, oidQuery, "strategy_db")
					return nil
				},
			),
		},
		{
			ResourceName:            "postgresql_database.strategy_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership", "strategy"},
		},
		// No diff, the database is not replaced.
		{
			Config: dbConfig("file_copy"),
			Check: func(*terraform.State) error {
				if got := testQueryDatabase(t, oidQuery, "strategy_db"); got != oid {
					return fmt.Errorf("the database was replaced: its OID changed from %s to %s", oid, got)
				}
				return nil
			},
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 150000, "STRATEGY", dbConfig("wal_log"), steps),
	})
}

// oid is set at the creation, the database is replaced when it changes.
func TestAccPostgresqlDatabase_OID(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(oid string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "oid_db" {
  name = "oid_db"
  %s
}
`, oid)
	}
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"

	steps := []resource.TestStep{
		{
			Config: dbConfig("oid = 3456789"),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.oid_db", "oid", "3456789"),
				testCheckDatabaseValue(t, "3456789", oidQuery, "oid_db"),
			),
		},
		{
			ResourceName:            "postgresql_database.oid_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		{
			Config: dbConfig("oid = 3456790"),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.oid_db", "oid", "3456790"),
				testCheckDatabaseValue(t, "3456790", oidQuery, "oid_db"),
			),
		},
		// Without oid, the database is left as it is.
		{
			Config: dbConfig(""),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.oid_db", "oid", "3456790"),
				testCheckDatabaseValue(t, "3456790", oidQuery, "oid_db"),
			),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 150000, "OID", dbConfig("oid = 3456789"), steps),
	})
}

// locale_provider, icu_locale: the ICU locale is stored in daticulocale up to PostgreSQL 16, in datlocale after.
func TestAccPostgresqlDatabase_LocaleProvider(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "icu_db" {
  name = "icu_db"
  %s
}
`, options)
	}
	icuLocaleQuery := fmt.Sprintf("SELECT COALESCE(%s, '') FROM pg_database WHERE datname = $1", testICULocaleColumn(t))
	const providerQuery = "SELECT datlocprovider::text FROM pg_database WHERE datname = $1"

	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	var oid string

	steps := []resource.TestStep{
		{
			Config:      dbConfig(`icu_locale = "en-US"`),
			ExpectError: regexp.MustCompile(`cannot be specified unless locale provider is ICU`),
		},
		{
			// the values are case sensitive
			Config:      dbConfig(`locale_provider = "ICU"`),
			ExpectError: regexp.MustCompile(`expected locale_provider to be one of`),
		},
		{
			Config: dbConfig(`locale_provider = "icu"
  icu_locale      = "en-US"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.icu_db", "locale_provider", "icu"),
				resource.TestCheckResourceAttr("postgresql_database.icu_db", "icu_locale", "en-US"),
				testCheckDatabaseValue(t, "i", providerQuery, "icu_db"),
				testCheckDatabaseValue(t, "en-US", icuLocaleQuery, "icu_db"),
			),
		},
		{
			ResourceName:            "postgresql_database.icu_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		// The provider and the ICU locale are set at the creation: the database is replaced.
		{
			Config: dbConfig(`locale_provider = "icu"
  icu_locale      = "fr-FR"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.icu_db", "icu_locale", "fr-FR"),
				testCheckDatabaseValue(t, "fr-FR", icuLocaleQuery, "icu_db"),
			),
		},
		// Without these options, the database is left as it is.
		{
			Config: dbConfig(""),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.icu_db", "icu_locale", "fr-FR"),
				testCheckDatabaseValue(t, "fr-FR", icuLocaleQuery, "icu_db"),
				testCheckDatabaseValue(t, "i", providerQuery, "icu_db"),
				func(*terraform.State) error {
					oid = testQueryDatabase(t, oidQuery, "icu_db")
					return nil
				},
			),
		},
		// The provider alone is set at the creation: the database is replaced.
		{
			Config: dbConfig(`locale_provider = "libc"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.icu_db", "locale_provider", "libc"),
				testCheckDatabaseValue(t, "c", providerQuery, "icu_db"),
				func(*terraform.State) error {
					if got := testQueryDatabase(t, oidQuery, "icu_db"); got == oid {
						return errors.New("the database was not replaced by the change of locale_provider")
					}
					return nil
				},
			),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 150000, "LOCALE_PROVIDER", dbConfig(`locale_provider = "icu"`), steps),
	})
}

// icu_rules is stored in daticurules (PostgreSQL 16 and above).
func TestAccPostgresqlDatabase_ICURules(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "icu_rules_db" {
  name = "icu_rules_db"
  %s
}
`, options)
	}
	const rulesQuery = "SELECT COALESCE(daticurules, '') FROM pg_database WHERE datname = $1"
	const rules = "&a<b"

	steps := []resource.TestStep{
		{
			Config: dbConfig(fmt.Sprintf(`locale_provider = "icu"
  icu_locale = "en-US"
  icu_rules  = %q`, rules)),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.icu_rules_db", "icu_rules", rules),
				testCheckDatabaseValue(t, rules, rulesQuery, "icu_rules_db"),
			),
		},
		{
			ResourceName:            "postgresql_database.icu_rules_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		{
			Config: dbConfig(`locale_provider = "icu"
  icu_locale = "en-US"`),
			Check: testCheckDatabaseValue(t, rules, rulesQuery, "icu_rules_db"),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 160000, "ICU_RULES", dbConfig(fmt.Sprintf("icu_rules = %q", rules)), steps),
	})
}

// collation_version is stored in datcollversion (PostgreSQL 15 and above). It is only sent when the
// database is created: a difference with an existing database (REFRESH COLLATION VERSION on the server,
// or an edit of the configuration) never plans anything.
func TestAccPostgresqlDatabase_CollationVersion(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "collation_version_db" {
  name = "collation_version_db"
  %s
}
`, options)
	}
	// The ICU provider has a real collation version, so that REFRESH COLLATION VERSION changes it.
	const icuEN = `locale_provider = "icu"
  icu_locale      = "en-US"`
	const icuFR = `locale_provider = "icu"
  icu_locale      = "fr-FR"`
	withVersion := func(base, version string) string {
		return base + "\n  collation_version = " + strconv.Quote(version)
	}
	const versionQuery = "SELECT datcollversion FROM pg_database WHERE datname = $1"
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	const resourceName = "postgresql_database.collation_version_db"
	var oid string
	recordOID := func(*terraform.State) error {
		oid = testQueryDatabase(t, oidQuery, "collation_version_db")
		return nil
	}
	checkSameOID := func(*terraform.State) error {
		if got := testQueryDatabase(t, oidQuery, "collation_version_db"); got != oid {
			return fmt.Errorf("the database was replaced: its OID changed from %s to %s", oid, got)
		}
		return nil
	}

	steps := []resource.TestStep{
		{
			Config: dbConfig(withVersion(icuEN, "1.0")),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(resourceName, "collation_version", "1.0"),
				testCheckDatabaseValue(t, "1.0", versionQuery, "collation_version_db"),
				recordOID,
			),
		},
		{
			ResourceName:            resourceName,
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		{
			// The version is removed from the configuration: nothing changes.
			Config: dbConfig(icuEN),
			Check: resource.ComposeTestCheckFunc(
				testCheckDatabaseValue(t, "1.0", versionQuery, "collation_version_db"),
				checkSameOID,
			),
		},
		{
			// The server changes the version: the plan stays empty (no replacement, no update).
			PreConfig: func() {
				config := getTestConfig(t)
				dbExecute(t, config.connStr("postgres"), `ALTER DATABASE collation_version_db REFRESH COLLATION VERSION`)
				if got := testQueryDatabase(t, versionQuery, "collation_version_db"); got == "1.0" {
					t.Fatalf("REFRESH COLLATION VERSION did not change datcollversion: %q", got)
				}
			},
			Config:   dbConfig(icuEN),
			PlanOnly: true,
		},
		{
			// The configured version differs from the stored one: the plan is still empty.
			Config:   dbConfig(withVersion(icuEN, "1.0")),
			PlanOnly: true,
		},
		{
			// Same, applied: the database is neither replaced nor altered.
			Config: dbConfig(withVersion(icuEN, "2.0")),
			Check: resource.ComposeTestCheckFunc(
				checkSameOID,
				func(*terraform.State) error {
					if got := testQueryDatabase(t, versionQuery, "collation_version_db"); got == "2.0" {
						return errors.New("collation_version was applied to an existing database")
					}
					return nil
				},
			),
		},
		{
			// Another ForceNew attribute replaces the database, the new one uses the configured version.
			Config: dbConfig(withVersion(icuFR, "3.0")),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(resourceName, "collation_version", "3.0"),
				testCheckDatabaseValue(t, "3.0", versionQuery, "collation_version_db"),
				testCheckDatabaseValue(t, "fr-FR", fmt.Sprintf("SELECT %s FROM pg_database WHERE datname = $1", testICULocaleColumn(t)), "collation_version_db"),
				func(*terraform.State) error {
					if got := testQueryDatabase(t, oidQuery, "collation_version_db"); got == oid {
						return errors.New("the database was not replaced")
					}
					return nil
				},
			),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 150000, "COLLATION_VERSION", dbConfig(`collation_version = "1.0"`), steps),
	})
}

// builtin_locale is stored in datlocale (PostgreSQL 17 and above).
func TestAccPostgresqlDatabase_BuiltinLocale(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "builtin_db" {
  name = "builtin_db"
  %s
}
`, options)
	}
	const providerQuery = "SELECT datlocprovider::text FROM pg_database WHERE datname = $1"
	const localeQuery = "SELECT COALESCE(datlocale, '') FROM pg_database WHERE datname = $1"
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	var oid string

	steps := []resource.TestStep{
		{
			Config: dbConfig(`locale_provider = "builtin"
  builtin_locale  = "C.UTF-8"`),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_database.builtin_db", "builtin_locale", "C.UTF-8"),
				testCheckDatabaseValue(t, "b", providerQuery, "builtin_db"),
				testCheckDatabaseValue(t, "C.UTF-8", localeQuery, "builtin_db"),
			),
		},
		{
			ResourceName:            "postgresql_database.builtin_db",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"template", "alter_object_ownership"},
		},
		{
			Config: dbConfig(""),
			Check: resource.ComposeTestCheckFunc(
				testCheckDatabaseValue(t, "C.UTF-8", localeQuery, "builtin_db"),
				func(*terraform.State) error {
					oid = testQueryDatabase(t, oidQuery, "builtin_db")
					return nil
				},
			),
		},
		// builtin_locale is set at the creation: the database is replaced.
		{
			Config: dbConfig(`locale_provider = "builtin"
  builtin_locale  = "C"`),
			Check: resource.ComposeTestCheckFunc(
				testCheckDatabaseValue(t, "C", localeQuery, "builtin_db"),
				func(*terraform.State) error {
					if got := testQueryDatabase(t, oidQuery, "builtin_db"); got == oid {
						return errors.New("the database was not replaced by the change of builtin_locale")
					}
					return nil
				},
			),
		},
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps:        testDatabaseOptionSteps(t, 170000, "BUILTIN_LOCALE", dbConfig(`builtin_locale = "C.UTF-8"`), steps),
	})
}

// testCheckSessionTerminated checks that the session, which worked when it was opened (testOpenSession),
// was ended by the server (pg_terminate_backend or DROP DATABASE ... WITH (FORCE) both end it with
// SQLSTATE 57P01), not by any other failure.
func testCheckSessionTerminated(conn *sql.Conn) error {
	var one int
	err := conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&one)
	if err == nil {
		return errors.New("the session opened on the database is still connected")
	}
	var pqErr *pq.Error
	// The server closed the socket before the error message was read: depending on the timing
	// and on what is between the client and the server (e.g. the port forwarding of Docker)
	// it is seen as a bad connection, an end of file or a reset of the connection.
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return nil
	}
	if !errors.As(err, &pqErr) || pqErr.Code != "57P01" {
		return fmt.Errorf("the session was not terminated by the server (expected SQLSTATE 57P01): %w", err)
	}
	return nil
}

// testOpenSession opens a session on the database and checks it works.
func testOpenSession(t *testing.T, dbName string) (*sql.DB, *sql.Conn) {
	config := getTestConfig(t)
	pool, err := sql.Open("postgres", config.connStr(dbName))
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	conn, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatalf("could not connect to the database %s: %v", dbName, err)
	}
	var one int
	if err := conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("could not query the database %s: %v", dbName, err)
	}
	return pool, conn
}

// The provider ends the sessions itself (terminateBConnections, before DROP DATABASE) on every version, and
// DROP DATABASE ... WITH (FORCE) is added on 13 and above: both run on every delete, so the end to end test
// (_DropWithOpenConnection) cannot tell them apart. Each mechanism is proved on its own here: the provider
// function alone ends the sessions and blocks new ones, and on 13 and above the server clause alone drops
// a database which has an open session.
func TestAccPostgresqlDatabase_DropMechanisms(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)

	config := getTestConfig(t)
	admin := config.connStr("postgres")
	db, err := testAccProvider.Meta().(*Client).Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}

	const dbName = "tf_drop_mechanisms"
	dbExecute(t, admin, "DROP DATABASE IF EXISTS "+dbName)
	dbExecute(t, admin, "CREATE DATABASE "+dbName)
	t.Cleanup(func() {
		if err := terminateBConnections(db, dbName); err == nil {
			dbExecute(t, admin, "DROP DATABASE IF EXISTS "+dbName)
		}
	})

	t.Run("terminateBConnections", func(t *testing.T) {
		pool, conn := testOpenSession(t, dbName)
		defer func() { _ = pool.Close() }()
		// (also when the subtest fails: the next one connects to the database)
		defer dbExecute(t, admin, "ALTER DATABASE "+dbName+" ALLOW_CONNECTIONS true")

		if err := terminateBConnections(db, dbName); err != nil {
			t.Fatalf("terminateBConnections: %v", err)
		}
		if err := testCheckSessionTerminated(conn); err != nil {
			t.Fatal(err)
		}
		if got := testQueryDatabase(t, "SELECT datallowconn::text FROM pg_database WHERE datname = $1", dbName); got != "false" {
			t.Fatalf("new connections are not blocked: datallowconn = %s", got)
		}
		if got := testQueryDatabase(t, "SELECT count(*)::text FROM pg_database WHERE datname = $1", dbName); got != "1" {
			t.Fatalf("the database must still exist after terminateBConnections, count = %s", got)
		}
	})

	t.Run("with_force", func(t *testing.T) {
		testSkipIfOlderThan(t, 130000, "DROP DATABASE ... WITH (FORCE)")
		pool, conn := testOpenSession(t, dbName)
		defer func() { _ = pool.Close() }()

		dbExecute(t, admin, "DROP DATABASE "+dbName+" WITH (FORCE)")
		if err := testCheckSessionTerminated(conn); err != nil {
			t.Fatal(err)
		}
		if got := testQueryDatabase(t, "SELECT count(*)::text FROM pg_database WHERE datname = $1", dbName); got != "0" {
			t.Fatalf("the database was not dropped, count = %s", got)
		}
	})
}

// A database name with quotes is escaped everywhere: the sessions of the database are terminated with a
// query parameter, and the identifier is quoted.
func TestAccPostgresqlDatabase_QuotesInName(t *testing.T) {
	skipIfNotAcc(t)

	const dbName = `tf_it's "quoted"`
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "postgresql_database" "quoted" {
  name = %q
}
`, dbName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlDatabaseExists("postgresql_database.quoted"),
					testCheckDatabaseValue(t, "1", "SELECT count(*)::text FROM pg_database WHERE datname = $1", dbName),
				),
			},
			{
				Config: "# the database is removed\n",
				Check:  testCheckDatabaseValue(t, "0", "SELECT count(*)::text FROM pg_database WHERE datname = $1", dbName),
			},
		},
	})
}

// A connection failure when the provider locks the owner is returned as an error: the transaction used to
// be nil, and the provider panicked. A closed connection pool makes startTransaction fail without a server.
func TestDatabaseStartTransactionError(t *testing.T) {
	config := getTestConfig(t)
	client := config.NewClient("postgres")

	closed, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("could not close the pool: %v", err)
	}
	broken := &DBConnection{DB: closed, client: client}

	dsn := config.connStr("postgres")
	dbRegistryLock.Lock()
	previous, hadPrevious := dbRegistry[dsn]
	dbRegistry[dsn] = broken
	dbRegistryLock.Unlock()
	defer func() {
		dbRegistryLock.Lock()
		defer dbRegistryLock.Unlock()
		if hadPrevious {
			dbRegistry[dsn] = previous
		} else {
			delete(dbRegistry, dsn)
		}
	}()

	d := schema.TestResourceDataRaw(t, resourcePostgreSQLDatabase().Schema, map[string]any{
		dbNameAttr:  "some_db",
		dbOwnerAttr: "some_owner",
	})

	if err := resourcePostgreSQLDatabaseDelete(broken, d); err == nil {
		t.Error("delete: expected the error of the failed transaction")
	}
	if err := setDBOwner(broken, d); err == nil {
		t.Error("setDBOwner: expected the error of the failed transaction")
	}
}

// lc_collate and lc_ctype are set at the creation: a change of either one replaces the database.
func TestAccPostgresqlDatabase_CollateCTypeReplace(t *testing.T) {
	skipIfNotAcc(t)

	dbConfig := func(collate, ctype string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "collate_db" {
  name       = "collate_db"
  template   = "template0"
  encoding   = "UTF8"
  lc_collate = %q
  lc_ctype   = %q
}
`, collate, ctype)
	}
	const oidQuery = "SELECT oid::text FROM pg_database WHERE datname = $1"
	const collateQuery = "SELECT datcollate || ' ' || datctype FROM pg_database WHERE datname = $1"
	var oid string
	step := func(collate, ctype string, replaced bool) resource.TestStep {
		return resource.TestStep{
			Config: dbConfig(collate, ctype),
			Check: resource.ComposeTestCheckFunc(
				testCheckDatabaseValue(t, collate+" "+ctype, collateQuery, "collate_db"),
				func(*terraform.State) error {
					got := testQueryDatabase(t, oidQuery, "collate_db")
					if replaced && got == oid {
						return errors.New("the database was not replaced")
					}
					oid = got
					return nil
				},
			),
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			step("C", "C", false),
			step("en_US.UTF-8", "C", true),
			step("en_US.UTF-8", "en_US.UTF-8", true),
		},
	})
}

// collation_version is Computed: left out, it is read from the server and the plan stays empty.
func TestAccPostgresqlDatabase_CollationVersionComputed(t *testing.T) {
	skipIfNotAcc(t)
	testSkipIfOlderThan(t, 150000, "collation_version")

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlDatabaseDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_database" "collation_computed_db" {
  name              = "collation_computed_db"
  locale_provider   = "icu"
  icu_locale        = "en-US"
}
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("postgresql_database.collation_computed_db", "collation_version"),
					func(s *terraform.State) error {
						want := testQueryDatabase(t, "SELECT datcollversion FROM pg_database WHERE datname = $1", "collation_computed_db")
						return resource.TestCheckResourceAttr("postgresql_database.collation_computed_db", "collation_version", want)(s)
					},
				),
			},
		},
	})
}

// Without a raw configuration (null), configCollationVersion returns an empty version.
func TestConfigCollationVersionNullConfig(t *testing.T) {
	d := resourcePostgreSQLDatabase().TestResourceData()
	if got := configCollationVersion(d); got != "" {
		t.Fatalf("expected an empty collation version, got %q", got)
	}
}
