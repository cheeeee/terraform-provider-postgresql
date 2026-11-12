package postgresql

import (
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestAccPostgresqlServer_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw", "file_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_file"),
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_with_owner"),
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_with_version"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_name", "myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_owner", "postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "fdw_name", "postgres_fdw"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.host", "foo"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.dbname", "foodb"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.port", "5432"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_file", "server_name", "myserver_file"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_file", "server_owner", "postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_file", "fdw_name", "file_fdw"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_with_owner", "server_owner", "owner"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_with_type", "server_type", "slave"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_with_version", "server_version", "1.1.1"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlServerDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_server" {
			continue
		}

		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkServerExists(txn, rs.Primary.ID)

		if err != nil {
			return fmt.Errorf("error checking foreign server %s", err)
		}

		if exists {
			return fmt.Errorf("Foreign Server still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlServerExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		serverName, ok := rs.Primary.Attributes[serverNameAttr]
		if !ok {
			return fmt.Errorf("No Attribute for server name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkServerExists(txn, serverName)

		if err != nil {
			return fmt.Errorf("error checking foreign server %s", err)
		}

		if !exists {
			return fmt.Errorf("Foreign server not found")
		}

		return nil
	}
}

func TestAccPostgresqlServer_Update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerChanges1,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_name", "myserver_postgres"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_name", "myserver_postgres_updated"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges3,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_type", "custom"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges4,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "server_version", "1.2.3"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges5,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.host", "local"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.dbname", "mydb"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.port", "25432"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges6,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.sslmode", "require"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges7,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
				),
			},
			{
				Config: testAccPostgresqlServerChanges8,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.myserver_postgres"),
					resource.TestCheckResourceAttr(
						"postgresql_server.myserver_postgres", "options.%", "0"),
				),
			},
		},
	})
}

func checkServerExists(txn *sql.Tx, serverName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE FROM pg_foreign_server WHERE srvname=$1", serverName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about foreign server: %s", err)
	}

	return true, nil
}

func TestAccPostgresqlServer_DropCascade(t *testing.T) {
	skipIfNotAcc(t)
	testCheckCanCreateExtension(t, "postgres_fdw")

	var testAccPostgresqlServerConfig = `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}
	
resource "postgresql_server" "cascade" {
  server_name = "myserver"
  fdw_name    = "postgres_fdw"
  options = {
	host   = "foo"
	dbname = "foodb"
	port   = "5432"
  }
  drop_cascade = true

  depends_on = [postgresql_extension.ext_postgres_fdw]
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlServerExists("postgresql_server.cascade"),
					resource.TestCheckResourceAttr("postgresql_server.cascade", "server_name", "myserver"),
					// This will create a dependency on the server.
					testAccCreateServerDependency("myserver"),
				),
			},
		},
	})
}

func testAccCreateServerDependency(serverName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {

		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}
		currentUser, err := getCurrentUser(db)
		if err != nil {
			return err
		}
		_, err = db.Exec(fmt.Sprintf("CREATE USER MAPPING FOR %s SERVER %s OPTIONS (user 'admin', password 'admin');", currentUser, serverName))
		if err != nil {
			return fmt.Errorf("could not create user mapping: %s", err)
		}

		return nil
	}
}

// testAccOptionsFromCatalog converts the options of pg_foreign_server.srvoptions or
// pg_user_mappings.umoptions to a map: the value of an option follows its first "=".
func testAccOptionsFromCatalog(options []string) map[string]string {
	m := make(map[string]string)
	for _, o := range options {
		pair := strings.SplitN(o, "=", 2)
		m[pair[0]] = pair[1]
	}
	return m
}

// testAccCheckServerCatalog checks the version and the options of a foreign server in the catalog.
func testAccCheckServerCatalog(serverName, version string, options map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		txn, err := startTransaction(testAccProvider.Meta().(*Client), "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var srvVersion string
		var srvOptions []string
		err = txn.QueryRow(
			"SELECT COALESCE(srvversion, ''), srvoptions FROM pg_foreign_server WHERE srvname = $1",
			serverName,
		).Scan(&srvVersion, pq.Array(&srvOptions))
		if err != nil {
			return fmt.Errorf("error reading foreign server %s: %w", serverName, err)
		}
		if srvVersion != version {
			return fmt.Errorf("version of foreign server %s is %q, expected %q", serverName, srvVersion, version)
		}
		if got := testAccOptionsFromCatalog(srvOptions); !reflect.DeepEqual(got, options) {
			return fmt.Errorf("options of foreign server %s are %v, expected %v", serverName, got, options)
		}
		return nil
	}
}

// testAccCheckServerOwner checks the owner of a foreign server in the catalog.
func testAccCheckServerOwner(serverName, owner string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		txn, err := startTransaction(testAccProvider.Meta().(*Client), "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var srvOwner string
		err = txn.QueryRow(
			"SELECT pg_get_userbyid(srvowner) FROM pg_foreign_server WHERE srvname = $1",
			serverName,
		).Scan(&srvOwner)
		if err != nil {
			return fmt.Errorf("error reading foreign server %s: %w", serverName, err)
		}
		if srvOwner != owner {
			return fmt.Errorf("owner of foreign server %s is %q, expected %q", serverName, srvOwner, owner)
		}
		return nil
	}
}

// testAccCheckServerNotExists checks that a foreign server is not in the catalog.
func testAccCheckServerNotExists(serverName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		txn, err := startTransaction(testAccProvider.Meta().(*Client), "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkServerExists(txn, serverName)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("foreign server %s still exists", serverName)
		}
		return nil
	}
}

// testAccDropServerDependency drops the user mapping of the connected user created by
// testAccCreateServerDependency, which lets the server be dropped.
func testAccDropServerDependency(t *testing.T, serverName string) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}
	currentUser, err := getCurrentUser(db)
	if err != nil {
		t.Fatalf("could not get the current user: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf("DROP USER MAPPING FOR %s SERVER %s", pq.QuoteIdentifier(currentUser), pq.QuoteIdentifier(serverName))); err != nil {
		t.Fatalf("could not drop user mapping: %v", err)
	}
}

// testAccPostgresqlServerOptionsConfig returns a foreign server with the given options
// (lines of HCL) and the extension of its foreign-data wrapper.
func testAccPostgresqlServerOptionsConfig(serverName, options string) string {
	return fmt.Sprintf(`
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver" {
  server_name = %q
  fdw_name    = "postgres_fdw"
  options = {
%s
  }

  depends_on = [postgresql_extension.ext_postgres_fdw]
}
`, serverName, options)
}

func TestAccPostgresqlServer_OptionsAddSetDrop(t *testing.T) {
	const serverName = "myserver_options"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				// Creation
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host   = "foo"
    dbname = "foodb"
    port   = "5432"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{"host": "foo", "dbname": "foodb", "port": "5432"}),
				),
			},
			{
				// SET host, ADD sslmode, DROP port, and dbname is kept
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host    = "bar"
    dbname  = "foodb"
    sslmode = "require"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{"host": "bar", "dbname": "foodb", "sslmode": "require"}),
				),
			},
			{
				// DROP all the options
				Config: testAccPostgresqlServerOptionsConfig(serverName, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{}),
				),
			},
			{
				// ADD to a server without options
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host = "baz"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{"host": "baz"}),
					resource.TestCheckResourceAttr("postgresql_server.myserver", "options.host", "baz"),
				),
			},
		},
	})
}

// An option value which contains "=" is read back in full.
func TestAccPostgresqlServer_OptionValueWithEquals(t *testing.T) {
	const serverName = "myserver_equals"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host   = "a=b"
    dbname = "foodb"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{"host": "a=b", "dbname": "foodb"}),
					resource.TestCheckResourceAttr("postgresql_server.myserver", "options.host", "a=b"),
				),
			},
		},
	})
}

// server_version is set, changed and removed from the configuration.
func TestAccPostgresqlServer_Version(t *testing.T) {
	const serverName = "myserver_version"
	config := func(version string) string {
		versionLine := ""
		if version != "" {
			versionLine = fmt.Sprintf("  server_version = %q\n", version)
		}
		return fmt.Sprintf(`
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver" {
  server_name = %q
  fdw_name    = "postgres_fdw"
%s
  depends_on = [postgresql_extension.ext_postgres_fdw]
}
`, serverName, versionLine)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("1.0"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "1.0", map[string]string{}),
				),
			},
			{
				Config: config("2.0"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "2.0", map[string]string{}),
				),
			},
			{
				// Removing the attribute removes the version in the catalog
				Config: config(""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerCatalog(serverName, "", map[string]string{}),
					resource.TestCheckResourceAttr("postgresql_server.myserver", "server_version", ""),
				),
			},
		},
	})
}

// The owner of a server is changed on update.
func TestAccPostgresqlServer_OwnerChange(t *testing.T) {
	const serverName = "myserver_owner"
	config := func(owner string) string {
		return fmt.Sprintf(`
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_role" "owner_a" {
  name = "srv_owner_a"
}

resource "postgresql_role" "owner_b" {
  name = "srv_owner_b"
}

resource "postgresql_server" "myserver" {
  server_name  = %q
  fdw_name     = "postgres_fdw"
  server_owner = postgresql_role.%s.name

  depends_on = [postgresql_extension.ext_postgres_fdw]
}
`, serverName, owner)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("owner_a"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerOwner(serverName, "srv_owner_a"),
				),
			},
			{
				Config: config("owner_b"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerOwner(serverName, "srv_owner_b"),
					resource.TestCheckResourceAttr("postgresql_server.myserver", "server_owner", "srv_owner_b"),
				),
			},
		},
	})
}

// The server is renamed on update, its options are kept.
func TestAccPostgresqlServer_Rename(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerOptionsConfig("myserver_rename_old", `
    host = "foo"`),
				Check: testAccCheckServerCatalog("myserver_rename_old", "", map[string]string{"host": "foo"}),
			},
			{
				Config: testAccPostgresqlServerOptionsConfig("myserver_rename_new", `
    host = "foo"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServerNotExists("myserver_rename_old"),
					testAccCheckServerCatalog("myserver_rename_new", "", map[string]string{"host": "foo"}),
				),
			},
		},
	})
}

// drop_cascade = false (RESTRICT) refuses to drop a server which has a dependent user mapping.
func TestAccPostgresqlServer_DropRestrict(t *testing.T) {
	const serverName = "myserver_restrict"
	extensionOnly := `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host = "foo"`),
				Check: testAccCreateServerDependency(serverName),
			},
			{
				// The server cannot be dropped while the user mapping exists
				Config:      extensionOnly,
				ExpectError: regexp.MustCompile(`cannot drop server`),
			},
			{
				// Once the dependency is removed, the server is dropped
				PreConfig: func() {
					testAccDropServerDependency(t, serverName)
				},
				Config: extensionOnly,
				Check:  testAccCheckServerNotExists(serverName),
			},
		},
	})
}

// A server is imported by its name.
func TestAccPostgresqlServer_Import(t *testing.T) {
	const serverName = "myserver_import"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlServerOptionsConfig(serverName, `
    host = "foo"`),
			},
			{
				ResourceName:      "postgresql_server.myserver",
				ImportState:       true,
				ImportStateId:     serverName,
				ImportStateVerify: true,
				// drop_cascade is a setting of the configuration, not of the server
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
		},
	})
}

// Changes made outside of Terraform appear in the plan, and are reverted by an apply.
func TestAccPostgresqlServer_ChangedOutsideTerraform(t *testing.T) {
	const serverName = "myserver_outside"
	config := testAccPostgresqlServerOptionsConfig(serverName, `
    host = "foo"`)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureServer)
			testCheckCanCreateExtension(t, "postgres_fdw")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlServerDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  testAccCheckServerCatalog(serverName, "", map[string]string{"host": "foo"}),
			},
			{
				PreConfig: func() {
					testConfig := getTestConfig(t)
					dbExecute(t, testConfig.connStr("postgres"),
						fmt.Sprintf("ALTER SERVER %s VERSION '9.9' OPTIONS (SET host 'changed')", pq.QuoteIdentifier(serverName)))
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check:  testAccCheckServerCatalog(serverName, "", map[string]string{"host": "foo"}),
			},
		},
	})
}

var testAccPostgresqlServerConfig = `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_extension" "ext_file_fdw" {
  name = "file_fdw"
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


resource "postgresql_server" "myserver_file" {
  server_name = "myserver_file"
  fdw_name    = "file_fdw"  
  depends_on = [postgresql_extension.ext_file_fdw]
}

resource "postgresql_role" "owner" {
  name = "owner"
}

resource "postgresql_server" "myserver_with_owner" {
  server_name  = "with_owner"
  server_owner = postgresql_role.owner.name
  fdw_name     = "postgres_fdw"
  options = {
    host   = "foo"
    dbname = "foodb"
    port   = "5432"
  }

  depends_on = [postgresql_extension.ext_postgres_fdw]
}

resource "postgresql_server" "myserver_with_type" {
  server_name = "myserver_with_type"
  server_type = "slave"
  fdw_name    = "postgres_fdw"
  options = {
    host   = "foo"
    dbname = "foodb"
    port   = "5432"
  }

  depends_on = [postgresql_extension.ext_postgres_fdw]
}


resource "postgresql_server" "myserver_with_version" {
  server_name    = "myserver_with_version"
  server_version = "1.1.1"
  fdw_name       = "postgres_fdw"
  options = {
    host   = "foo"
    dbname = "foodb"
    port   = "5432"
  }

depends_on = [postgresql_extension.ext_postgres_fdw]
}

`

var testAccPostgresqlServerChanges1 = `
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
`

var testAccPostgresqlServerChanges2 = `
resource "postgresql_extension" "ext_postgres_fdw" {
  name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name = "myserver_postgres_updated"
	fdw_name    = "postgres_fdw"
	options = {
		host   = "foo"
		dbname = "foodb"
		port   = "5432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges3 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name = "myserver_postgres_updated"
	server_type = "custom"
	fdw_name    = "postgres_fdw"
	options = {
		host   = "foo"
		dbname = "foodb"
		port   = "5432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges4 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name    = "myserver_postgres_updated"
	server_version = "1.2.3"
	fdw_name       = "postgres_fdw"
	options = {
		host   = "foo"
		dbname = "foodb"
		port   = "5432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges5 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name    = "myserver_postgres_updated"
	server_version = "1.2.3"
	fdw_name       = "postgres_fdw"
	options = {
		host   = "local"
		dbname = "mydb"
		port   = "25432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges6 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name    = "myserver_postgres_updated"
	server_version = "1.2.3"
	fdw_name       = "postgres_fdw"
	options = {
		host    = "local"
		dbname  = "mydb"
		port    = "25432"
		sslmode = "require"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges7 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name    = "myserver_postgres_updated"
	server_version = "1.2.3"
	fdw_name       = "postgres_fdw"
	options = {
		host   = "local"
		dbname = "mydb"
		port   = "25432"
	}
  
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`

var testAccPostgresqlServerChanges8 = `
resource "postgresql_extension" "ext_postgres_fdw" {
	name = "postgres_fdw"
}

resource "postgresql_server" "myserver_postgres" {
	server_name    = "myserver_postgres_updated"
	server_version = "1.2.3"
	fdw_name       = "postgres_fdw"
	depends_on = [postgresql_extension.ext_postgres_fdw]
}
`
