package postgresql

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestAccPostgresqlUserMapping_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlUserMappingConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlUserMappingExists("postgresql_user_mapping.remote"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "server_name", "myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "user_name", "remote"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "options.user", "admin"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "options.password", "pass"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.special_chars", "options.password", "pass=$*'"),
				),
			},
		},
	})
}

func TestAccPostgresqlUserMapping_Update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlUserMappingConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_user_mapping.remote"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "server_name", "myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "options.password", "pass"),
				),
			},
			{
				Config: testAccPostgresqlUserMappingChanges2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_user_mapping.remote"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "options.password", "passUpdated"),
				),
			},
			{
				Config: testAccPostgresqlUserMappingChanges3,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_user_mapping.remote"),
					resource.TestCheckResourceAttr(
						"postgresql_user_mapping.remote", "options.%", "0"),
				),
			},
		},
	})
}

func checkUserMappingExists(txn *sql.Tx, username string, serverName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE FROM pg_user_mappings WHERE usename = $1 AND srvname = $2", username, serverName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about user mapping: %s", err)
	}

	return true, nil
}

func testAccCheckPostgresqlUserMappingDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_user_mapping" {
			continue
		}

		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		splitted := strings.Split(rs.Primary.ID, ".")
		exists, err := checkUserMappingExists(txn, splitted[0], splitted[1])

		if err != nil {
			return fmt.Errorf("error checking user mapping %s", err)
		}

		if exists {
			return fmt.Errorf("User mapping still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlUserMappingExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		username, ok := rs.Primary.Attributes[userMappingUserNameAttr]
		if !ok {
			return fmt.Errorf("No Attribute for username is set")
		}

		serverName, ok := rs.Primary.Attributes[userMappingServerNameAttr]
		if !ok {
			return fmt.Errorf("No Attribute for server name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkUserMappingExists(txn, username, serverName)

		if err != nil {
			return fmt.Errorf("error checking user mapping %s", err)
		}

		if !exists {
			return fmt.Errorf("User mapping not found")
		}

		return nil
	}
}

// testAccCheckUserMappingCatalog checks the options of a user mapping in the catalog.
func testAccCheckUserMappingCatalog(username, serverName string, options map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		txn, err := startTransaction(testAccProvider.Meta().(*Client), "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var umOptions []string
		err = txn.QueryRow(
			"SELECT umoptions FROM pg_user_mappings WHERE usename = $1 AND srvname = $2",
			username, serverName,
		).Scan(pq.Array(&umOptions))
		if err != nil {
			return fmt.Errorf("error reading user mapping %s on server %s: %w", username, serverName, err)
		}
		got := make(map[string]string)
		for _, o := range umOptions {
			pair := strings.SplitN(o, "=", 2)
			got[pair[0]] = pair[1]
		}
		if !reflect.DeepEqual(got, options) {
			return fmt.Errorf("options of user mapping %s on server %s are %v, expected %v", username, serverName, got, options)
		}
		return nil
	}
}

// testAccPostgresqlUserMappingOptionsConfig returns a server, a role and a user mapping
// of this role with the given options (lines of HCL).
func testAccPostgresqlUserMappingOptionsConfig(serverName, userName, options string) string {
	return fmt.Sprintf(`
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver" {
  server_name = %q
  fdw_name    = "postgres_fdw"

  depends_on = [postgresql_extension.ext_postgres_fdw]
}

resource "postgresql_role" "remote" {
  name = %q
}

resource "postgresql_user_mapping" "remote" {
  server_name = postgresql_server.myserver.server_name
  user_name   = postgresql_role.remote.name
  options = {
%s
  }
}
`, serverName, userName, options)
}

// A user mapping for PUBLIC is created, read back and changed.
func TestAccPostgresqlUserMapping_Public(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver" {
  server_name = "myserver_public"
  fdw_name    = "postgres_fdw"

  depends_on = [postgresql_extension.ext_postgres_fdw]
}

resource "postgresql_user_mapping" "public" {
  server_name = postgresql_server.myserver.server_name
  user_name   = "public"
  options = {
    user     = "admin"
    password = "pass"
  }
}
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_user_mapping.public", "user_name", "public"),
					testAccCheckUserMappingCatalog("public", "myserver_public", map[string]string{"user": "admin", "password": "pass"}),
				),
			},
		},
	})
}

// Options of a user mapping added, changed and removed on update.
func TestAccPostgresqlUserMapping_OptionsAddSetDrop(t *testing.T) {
	const serverName = "myserver_um_options"
	const userName = "remote_um_options"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlUserMappingOptionsConfig(serverName, userName, `
    user = "admin"`),
				Check: testAccCheckUserMappingCatalog(userName, serverName, map[string]string{"user": "admin"}),
			},
			{
				// SET user, ADD password
				Config: testAccPostgresqlUserMappingOptionsConfig(serverName, userName, `
    user = "admin2"
    password = "pass"`),
				Check: testAccCheckUserMappingCatalog(userName, serverName, map[string]string{"user": "admin2", "password": "pass"}),
			},
			{
				// DROP user, SET password
				Config: testAccPostgresqlUserMappingOptionsConfig(serverName, userName, `
    password = "pass2"`),
				Check: testAccCheckUserMappingCatalog(userName, serverName, map[string]string{"password": "pass2"}),
			},
		},
	})
}

// A user mapping is imported by its ID: the user name and the server name joined by a dot.
func TestAccPostgresqlUserMapping_Import(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlUserMappingOptionsConfig("myserver_um_import", "remote_um_import", `
    user     = "admin"
    password = "pass"`),
			},
			{
				ResourceName:      "postgresql_user_mapping.remote",
				ImportState:       true,
				ImportStateId:     "remote_um_import.myserver_um_import",
				ImportStateVerify: true,
			},
		},
	})
}

// Changes made outside of Terraform appear in the plan, and are reverted by an apply.
func TestAccPostgresqlUserMapping_ChangedOutsideTerraform(t *testing.T) {
	const serverName = "myserver_um_outside"
	const userName = "remote_um_outside"
	config := testAccPostgresqlUserMappingOptionsConfig(serverName, userName, `
    password = "pass"`)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlUserMappingDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  testAccCheckUserMappingCatalog(userName, serverName, map[string]string{"password": "pass"}),
			},
			{
				PreConfig: func() {
					testConfig := getTestConfig(t)
					dbExecute(t, testConfig.connStr("postgres"),
						fmt.Sprintf("ALTER USER MAPPING FOR %s SERVER %s OPTIONS (SET password 'changed')",
							pq.QuoteIdentifier(userName), pq.QuoteIdentifier(serverName)))
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check:  testAccCheckUserMappingCatalog(userName, serverName, map[string]string{"password": "pass"}),
			},
		},
	})
}

var testAccPostgresqlUserMappingConfig = `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
  server_name = "myserver_postgres"
  fdw_name    = "postgres_fdw"
  options = {
    host   = "foo"
    dbname = "foodb"
    port   = "5432"
  }

  depends_on = [postgresql_extension.ext_postgres_fdw]
}

resource "postgresql_role" "remote" {
  name = "remote"
}

resource "postgresql_user_mapping" "remote" {
  server_name = postgresql_server.myserver_postgres.server_name
  user_name   = postgresql_role.remote.name
  options = {
    user = "admin"
    password = "pass"
  }
}

resource "postgresql_role" "special" {
  name = "special"
}

resource "postgresql_user_mapping" "special_chars" {
  server_name = postgresql_server.myserver_postgres.server_name
  user_name   = postgresql_role.special.name
  options = {
	user = "admin"
	password = "pass=$*'"
  }
}
`

var testAccPostgresqlUserMappingChanges2 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
  }
  
  resource "postgresql_server" "myserver_postgres" {
	server_name = "myserver_postgres"
	fdw_name    = "postgres_fdw"
	options = {
	  host   = "foo"
	  dbname = "foodb"
	  port   = "5432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
  }
  
  resource "postgresql_role" "remote" {
	name = "remote"
  }
  
  resource "postgresql_user_mapping" "remote" {
	server_name = postgresql_server.myserver_postgres.server_name
	user_name   = postgresql_role.remote.name
	options = {
	  user = "admin"
	  password = "passUpdated"
	}
  }
`

var testAccPostgresqlUserMappingChanges3 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
  }
  
  resource "postgresql_server" "myserver_postgres" {
	server_name = "myserver_postgres"
	fdw_name    = "postgres_fdw"
	options = {
	  host   = "foo"
	  dbname = "foodb"
	  port   = "5432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
  }
  
  resource "postgresql_role" "remote" {
	name = "remote"
  }
  
  resource "postgresql_user_mapping" "remote" {
	server_name = postgresql_server.myserver_postgres.server_name
	user_name   = postgresql_role.remote.name
  }
`
