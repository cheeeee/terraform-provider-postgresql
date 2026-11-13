package postgresql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestAccPostgresqlRole_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlRoleConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("myrole2", nil, nil),
					resource.TestCheckResourceAttr("postgresql_role.myrole2", "name", "myrole2"),
					resource.TestCheckResourceAttr("postgresql_role.myrole2", "login", "true"),
					resource.TestCheckResourceAttr("postgresql_role.myrole2", "roles.#", "0"),

					testAccCheckPostgresqlRoleExists("role_default", nil, nil),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "name", "role_default"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "superuser", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "create_database", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "create_role", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "inherit", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "replication", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "bypass_row_level_security", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "connection_limit", "-1"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "encrypted_password", "true"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "password", ""),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "valid_until", "infinity"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "skip_drop_role", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "skip_reassign_owned", "false"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "statement_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "idle_in_transaction_session_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_defaults", "assume_role", ""),

					resource.TestCheckResourceAttr("postgresql_role.role_with_create_database", "name", "role_with_create_database"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_create_database", "create_database", "true"),

					testAccCheckPostgresqlRoleExists("sub_role", []string{"myrole2", "role_simple"}, nil),
					resource.TestCheckResourceAttr("postgresql_role.sub_role", "name", "sub_role"),
					resource.TestCheckResourceAttr("postgresql_role.sub_role", "roles.#", "2"),
					resource.TestCheckResourceAttr("postgresql_role.sub_role", "roles.0", "myrole2"),
					resource.TestCheckResourceAttr("postgresql_role.sub_role", "roles.1", "role_simple"),

					testAccCheckPostgresqlRoleExists("role_with_search_path", nil, []string{"bar", "foo-with-hyphen"}),
				),
			},
		},
	})
}

// Test creating a superuser role.
func TestAccPostgresqlRole_Superuser(t *testing.T) {

	roleConfig := `
resource "postgresql_role" "role_with_superuser" {
  name = "role_with_superuser"
  superuser = true
  login = true
  password = "mypass"
}`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
			// Need to a be a superuser to create a superuser
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.role_with_superuser", "name", "role_with_superuser"),
					resource.TestCheckResourceAttr("postgresql_role.role_with_superuser", "superuser", "true"),
				),
			},
		},
	})
}

func TestAccPostgresqlRole_Update(t *testing.T) {

	var configCreate = `
resource "postgresql_role" "update_role" {
  name = "update_role"
  login = true
  password = "toto"
  valid_until = "2099-05-04 12:00:00+00"
}
`

	var configUpdate = `
resource "postgresql_role" "group_role" {
  name = "group_role"
}

resource "postgresql_role" "update_role" {
  name = "update_role2"
  login = true
  connection_limit = 5
  password = "titi"
  roles = ["${postgresql_role.group_role.name}"]
  search_path = ["mysearchpath"]
  statement_timeout = 30000
  idle_in_transaction_session_timeout = 60000
  assume_role = "${postgresql_role.group_role.name}"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("update_role", []string{}, nil),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "name", "update_role"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "login", "true"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "connection_limit", "-1"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "password", "toto"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "valid_until", "2099-05-04 12:00:00+00"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "roles.#", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "search_path.#", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "statement_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "idle_in_transaction_session_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "assume_role", ""),
					testAccCheckRoleCanLogin(t, "update_role", "toto"),
				),
			},
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("update_role2", []string{"group_role"}, nil),
					resource.TestCheckResourceAttr(
						"postgresql_role.update_role", "name", "update_role2",
					),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "connection_limit", "5"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "login", "true"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "password", "titi"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "valid_until", "infinity"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "roles.#", "1"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "roles.0", "group_role"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "search_path.#", "1"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "search_path.0", "mysearchpath"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "statement_timeout", "30000"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "idle_in_transaction_session_timeout", "60000"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "assume_role", "group_role"),
					testAccCheckRoleCanLogin(t, "update_role2", "titi"),
				),
			},
			// apply the first one again to test that the granted role is correctly
			// revoked and the search path has been reset to default.
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("update_role", []string{}, nil),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "name", "update_role"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "login", "true"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "connection_limit", "-1"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "password", "toto"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "roles.#", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "search_path.#", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "statement_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "idle_in_transaction_session_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.update_role", "assume_role", ""),
					testAccCheckRoleCanLogin(t, "update_role", "toto"),
				),
			},
		},
	})
}

// Test to create a role with admin user (usually postgres) granted to it
// There were a bug on RDS like setup (with a non-superuser postgres role)
// where it couldn't delete the role in this case.
func TestAccPostgresqlRole_AdminGranted(t *testing.T) {
	admin := os.Getenv("PGUSER")
	if admin == "" {
		admin = "postgres"
	}

	roleConfig := fmt.Sprintf(`
resource "postgresql_role" "test_role" {
  name  = "test_role"
  roles = [
	  "%s"
  ]
}`, admin)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			client := testAccProvider.Meta().(*Client)
			db, err := client.Connect()
			if err != nil {
				t.Fatalf("could connect to database: %v", err)
			}
			// Requires >= 9 and <16
			// We disable this test for >= pg16 as it makes no sense with the new createRoleSelfGrant feature
			if !db.featureSupported(featurePrivileges) || db.featureSupported(featureCreateRoleSelfGrant) {
				t.Skipf("Skip extension tests for Postgres %s", db.version)
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("test_role", []string{admin}, nil),
					resource.TestCheckResourceAttr("postgresql_role.test_role", "name", "test_role"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlRoleDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_role" {
			continue
		}

		exists, err := checkRoleExists(client, rs.Primary.ID)

		if err != nil {
			return fmt.Errorf("error checking role %s", err)
		}

		if exists {
			return fmt.Errorf("Role still exists after destroy")
		}
	}

	return nil
}

func testAccCheckPostgresqlRoleExists(roleName string, grantedRoles []string, searchPath []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)

		exists, err := checkRoleExists(client, roleName)
		if err != nil {
			return fmt.Errorf("error checking role %s", err)
		}

		if !exists {
			return fmt.Errorf("Role not found")
		}

		if grantedRoles != nil {
			if err := checkGrantedRoles(client, roleName, grantedRoles); err != nil {
				return err
			}
		}

		if searchPath != nil {
			if err := checkSearchPath(client, roleName, searchPath); err != nil {
				return err
			}
		}
		return nil
	}
}

func checkRoleExists(client *Client, roleName string) (bool, error) {
	db, err := client.Connect()
	if err != nil {
		return false, err
	}
	var _rez int
	err = db.QueryRow("SELECT 1 from pg_roles d WHERE rolname=$1", roleName).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about role: %s", err)
	}

	return true, nil
}

func testAccCheckRoleCanLogin(t *testing.T, role, password string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := getTestConfig(t)
		config.Username = role
		config.Password = password
		db, err := sql.Open("postgres", config.connStr("postgres"))
		if err != nil {
			return fmt.Errorf("could not open SQL connection: %v", err)
		}
		if err := db.Ping(); err != nil {
			return fmt.Errorf("could not connect as role %s: %v", role, err)
		}
		return nil
	}
}

// testAccCheckRoleStoredPassword checks the password of a role as it is stored
// in the system catalogs, which only a superuser can read.
func testAccCheckRoleStoredPassword(role string, check func(storedPassword string) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		if !client.config.Superuser {
			return nil
		}

		db, err := client.Connect()
		if err != nil {
			return err
		}

		var storedPassword string
		err = db.QueryRow("SELECT COALESCE(rolpassword, '') FROM pg_catalog.pg_authid WHERE rolname = $1", role).Scan(&storedPassword)
		if err != nil {
			return fmt.Errorf("could not read the password of role %s: %w", role, err)
		}

		if err := check(storedPassword); err != nil {
			return fmt.Errorf("role %s: %w", role, err)
		}
		return nil
	}
}

func checkGrantedRoles(client *Client, roleName string, expectedRoles []string) error {
	db, err := client.Connect()
	if err != nil {
		return err
	}

	rows, err := db.Query(
		"SELECT pg_get_userbyid(roleid) as rolname from pg_auth_members WHERE pg_get_userbyid(member) = $1 ORDER BY rolname",
		roleName,
	)
	if err != nil {
		return fmt.Errorf("error reading granted roles: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("error closing rows: %v", err)
		}
	}()

	grantedRoles := []string{}
	for rows.Next() {
		var grantedRole string
		if err := rows.Scan(&grantedRole); err != nil {
			return fmt.Errorf("error scanning granted role: %v", err)
		}
		grantedRoles = append(grantedRoles, grantedRole)
	}

	sort.Strings(expectedRoles)
	if !reflect.DeepEqual(grantedRoles, expectedRoles) {
		return fmt.Errorf(
			"Role %s is not a member of the expected list of roles. expected %v - got %v",
			roleName, expectedRoles, grantedRoles,
		)
	}
	return nil
}

func checkSearchPath(client *Client, roleName string, expectedSearchPath []string) error {
	db, err := client.Connect()
	if err != nil {
		return err
	}

	var searchPathStr string
	err = db.QueryRow(
		"SELECT (pg_options_to_table(rolconfig)).option_value FROM pg_roles WHERE rolname=$1;",
		roleName,
	).Scan(&searchPathStr)

	// The query returns ErrNoRows if the search path hasn't been altered.
	if err != nil && err == sql.ErrNoRows {
		searchPathStr = "\"$user\", public"
	} else if err != nil {
		return fmt.Errorf("error reading search_path: %v", err)
	}

	searchPath := strings.Split(searchPathStr, ", ")
	for i := range searchPath {
		searchPath[i] = strings.Trim(searchPath[i], `"`)
	}
	sort.Strings(expectedSearchPath)
	if !reflect.DeepEqual(searchPath, expectedSearchPath) {
		return fmt.Errorf(
			"search_path is not equal to expected value. expected %v - got %v",
			expectedSearchPath, searchPath,
		)
	}
	return nil
}

var testAccPostgresqlRoleConfig = `
resource "postgresql_role" "myrole2" {
  name = "myrole2"
  login = true
}

resource "postgresql_role" "role_with_pwd" {
  name = "role_with_pwd"
  login = true
  password = "mypass"
}

resource "postgresql_role" "role_with_pwd_encr" {
  name = "role_with_pwd_encr"
  login = true
  password = "mypass"
  encrypted_password = true
}

resource "postgresql_role" "role_simple" {
  name = "role_simple"
}

resource "postgresql_role" "role_with_defaults" {
  name = "role_default"
  superuser = false
  create_database = false
  create_role = false
  inherit = false
  login = false
  replication = false
  bypass_row_level_security = false
  connection_limit = -1
  encrypted_password = true
  password = ""
  skip_drop_role = false
  valid_until = "infinity"
  statement_timeout = 0
  idle_in_transaction_session_timeout = 0
  assume_role = ""
}

resource "postgresql_role" "role_with_create_database" {
  name = "role_with_create_database"
  create_database = true
}

resource "postgresql_role" "sub_role" {
	name = "sub_role"
	roles = [
		"${postgresql_role.myrole2.id}",
		"${postgresql_role.role_simple.id}",
	]
}

resource "postgresql_role" "role_with_search_path" {
  name = "role_with_search_path"
  search_path = ["bar", "foo-with-hyphen"]
}
`

func TestAccPostgresqlRole_WriteOnlyPassword_Basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "wo_pwd_role" {
  name        = "wo_pwd_role"
  login       = true
  password_wo = "secretpass"
  password_wo_version = "1"
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("wo_pwd_role", nil, nil),
					resource.TestCheckNoResourceAttr("postgresql_role.wo_pwd_role", "password_wo"), // must be blank
					resource.TestCheckNoResourceAttr("postgresql_role.wo_pwd_role", "password"),    // must be blank
					testAccCheckRoleCanLogin(t, "wo_pwd_role", "secretpass"),                       // actually usable
				),
			},
		},
	})
}

// resetDBRegistry drops the connections kept by the provider, so the next
// ones are opened with the configuration of the provider of the next step.
func resetDBRegistry() {
	dbRegistryLock.Lock()
	defer dbRegistryLock.Unlock()
	dbRegistry = make(map[string]*DBConnection, 1)
}

func TestAccPostgresqlRole_HashPassword(t *testing.T) {
	// Generated by PostgreSQL itself for the password "pencil"
	hashedPassword := "SCRAM-SHA-256$4096:nBZw/pXHfglx9Uj38kTBaw==$kGNAwejTyDeIo7PF3ThjzBaKwu6Dw5If2OYxTD2AstI=:U/rTZUu3HZekCvxnzF65xDuso9CTdPudq8r2EvaCHao="

	isScramSHA256 := func(storedPassword string) error {
		if !scramSHA256PasswordRegexp.MatchString(storedPassword) {
			return fmt.Errorf("password is not stored as a SCRAM-SHA-256 hash: the provider has sent it in clear text")
		}
		return nil
	}
	isMD5 := func(storedPassword string) error {
		if !md5PasswordRegexp.MatchString(storedPassword) {
			return fmt.Errorf("password is not stored as a MD5 hash: the server does not hash with MD5")
		}
		return nil
	}

	// The server hashes the clear-text passwords it receives in the sessions of
	// this test with MD5, so a password stored as a SCRAM-SHA-256 hash has been
	// hashed by the provider.
	t.Setenv("PGOPTIONS", "-c password_encryption=md5")

	// By default the provider hashes the passwords itself
	providerConfig := ``
	clearTextProviderConfig := `
provider "postgresql" {
  password_authentication = "password"
}
`
	defaultConfig := `
resource "postgresql_role" "hash_pwd_role" {
  name     = "hash_pwd_role"
  login    = true
  password = "pass0"
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureScramSHA256)
		},
		// Unlike Providers, ProviderFactories lets the steps configure the provider
		ProviderFactories: map[string]func() (*schema.Provider, error){
			"postgresql": func() (*schema.Provider, error) { return testAccProvider, nil },
		},
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				// The provider keeps its connections, and the configuration they
				// were opened with, for the life of the process, which the tests
				// share: drop them so this configuration is used.
				PreConfig: resetDBRegistry,
				// With password_authentication = "password", the server hashes the password
				Config: clearTextProviderConfig + defaultConfig,
				Check:  testAccCheckRoleStoredPassword("hash_pwd_role", isMD5),
			},
			{
				// Changing password_authentication alone plans no change
				Config:             providerConfig + defaultConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				// Back to the default configuration
				PreConfig: resetDBRegistry,
				// The password re-sent on rename is hashed, and so is the
				// password of a role created
				Config: providerConfig + `
resource "postgresql_role" "hash_pwd_role" {
  name     = "hash_pwd_role_renamed"
  login    = true
  password = "pass0"
}

resource "postgresql_role" "hash_pwd_created_role" {
  name     = "hash_pwd_created_role"
  login    = true
  password = "pass1"
}
`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlRoleExists("hash_pwd_created_role", nil, nil),
					resource.TestCheckResourceAttr("postgresql_role.hash_pwd_created_role", "password", "pass1"),
					testAccCheckRoleStoredPassword("hash_pwd_created_role", isScramSHA256),
					testAccCheckRoleCanLogin(t, "hash_pwd_created_role", "pass1"),
					testAccCheckRoleStoredPassword("hash_pwd_role_renamed", isScramSHA256),
					testAccCheckRoleCanLogin(t, "hash_pwd_role_renamed", "pass0"),
				),
			},
			{
				// The password is updated
				Config: providerConfig + `
resource "postgresql_role" "hash_pwd_role" {
  name     = "hash_pwd_role"
  login    = true
  password = "pass2"
}
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.hash_pwd_role", "password", "pass2"),
					testAccCheckRoleStoredPassword("hash_pwd_role", isScramSHA256),
					testAccCheckRoleCanLogin(t, "hash_pwd_role", "pass2"),
				),
			},
			{
				// The write-only password is hashed too
				Config: providerConfig + `
resource "postgresql_role" "hash_pwd_role" {
  name                = "hash_pwd_role"
  login               = true
  password_wo         = "pass3"
  password_wo_version = "1"
}
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.hash_pwd_role", "password", ""),
					testAccCheckRoleStoredPassword("hash_pwd_role", isScramSHA256),
					testAccCheckRoleCanLogin(t, "hash_pwd_role", "pass3"),
				),
			},
			{
				// A password which is already hashed is sent as-is
				Config: providerConfig + fmt.Sprintf(`
resource "postgresql_role" "hash_pwd_role" {
  name     = "hash_pwd_role"
  login    = true
  password = "%s"
}
`, hashedPassword),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleStoredPassword("hash_pwd_role", func(storedPassword string) error {
						if storedPassword != hashedPassword {
							return fmt.Errorf("the hashed password has not been stored as-is")
						}
						return nil
					}),
					testAccCheckRoleCanLogin(t, "hash_pwd_role", "pencil"),
				),
			},
			{
				// A password norm.NFKC cannot normalize like PostgreSQL is refused
				Config: providerConfig + fmt.Sprintf(`
resource "postgresql_role" "hash_pwd_role" {
  name     = "hash_pwd_role"
  login    = true
  password = "a%s"
}
`, strings.Repeat(`́`, 31)),
				ExpectError: regexp.MustCompile("more than 30 combining characters in a row"),
			},
		},
	})
}

func TestAccPostgresqlRole_WriteOnlyPassword_Switch(t *testing.T) {
	configPassword := `
resource "postgresql_role" "switch_role" {
  name     = "switch_role"
  login    = true
  password = "initialpass"
}`

	configPasswordWO := `
resource "postgresql_role" "switch_role" {
  name        = "switch_role"
  login       = true
  password_wo = "wopass"
  password_wo_version = "1"
}`

	configPasswordWOChangedWithoutVersionChange := `
resource "postgresql_role" "switch_role" {
  name        = "switch_role"
  login       = true
  password_wo = "wopass123"
  password_wo_version = "1"
}`

	configPasswordReturn := `
resource "postgresql_role" "switch_role" {
  name     = "switch_role"
  login    = true
  password = "revertpass"
}`

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: configPassword,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password", "initialpass"),
					testAccCheckRoleCanLogin(t, "switch_role", "initialpass"),
				),
			},
			{
				Config: configPasswordWO,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "switch_role", "wopass"),
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password", ""), // value cleared from state
				),
			},
			{
				Config: configPasswordWOChangedWithoutVersionChange,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "switch_role", "wopass"), //because the version didn't change, the old password still works
				),
			},
			{
				Config: configPasswordReturn,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password", "revertpass"),
					testAccCheckRoleCanLogin(t, "switch_role", "revertpass"),
				),
			},
		},
	})
}

func TestAccPostgresqlRole_WriteOnlyPassword_WithVersioning(t *testing.T) {
	const roleName = "switch_role"

	configWOv1 := `
resource "postgresql_role" "switch_role" {
  name                 = "switch_role"
  login                = true
  password_wo          = "wopass1"
  password_wo_version  = 1
}`

	configWOv1NewPass := `
resource "postgresql_role" "switch_role" {
  name                 = "switch_role"
  login                = true
  password_wo          = "wopass2"
  password_wo_version  = "1"
}`

	configWOv2 := `
resource "postgresql_role" "switch_role" {
  name                 = "switch_role"
  login                = true
  password_wo          = "wopass2"
  password_wo_version  = "2"
}`

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: configWOv1,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password_wo_version", "1"),
					testAccCheckRoleCanLogin(t, roleName, "wopass1"),
				),
			},
			{
				// Update the password but keep the version the same → should still login with old password
				Config: configWOv1NewPass,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, roleName, "wopass1"),
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password_wo_version", "1"),
				),
			},
			{
				// Now bump the version → new password should apply
				Config: configWOv2,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, roleName, "wopass2"),
					resource.TestCheckResourceAttr("postgresql_role.switch_role", "password_wo_version", "2"),
				),
			},
		},
	})
}

func TestAccPostgresqlRole_Parameters(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "params_role" {
  name              = "params_role"
  statement_timeout = 30000
  %s
}
`, parameters)
	}

	configCreate := roleConfig(`parameters = {
    work_mem = "64MB"
    timezone = "UTC"
  }`)
	configUpdate := roleConfig(`parameters = {
    work_mem           = "128MB"
    extra_float_digits = "2"
    application_name   = "it's \\ me"
  }`)
	configNoParameters := roleConfig("")
	configEmptyParameters := roleConfig("parameters = {}")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "2"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.work_mem", "64MB"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.timezone", "UTC"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "statement_timeout", "30000"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=64MB", "TimeZone=UTC",
					}),
				),
			},
			// Parameters are changed, removed and added.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "3"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.work_mem", "128MB"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.application_name", `it's \ me`),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=128MB", "extra_float_digits=2", `application_name=it's \ me`,
					}),
				),
			},
			// Changes made outside of Terraform are detected...
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE params_role RESET work_mem")
					dbExecute(t, dsn, "ALTER ROLE params_role SET lock_timeout TO '5s'")
				},
				Config:             configUpdate,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// ...and reverted.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "3"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=128MB", "extra_float_digits=2", `application_name=it's \ me`,
					}),
				),
			},
			// The existing parameters are read by the import.
			{
				ResourceName:      "postgresql_role.params_role",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// An empty map resets all the parameters, the ones with a dedicated attribute are kept.
			{
				Config: configEmptyParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "0"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{"statement_timeout=30000"}),
				),
			},
			// Without parameters in the resource, the ones set outside of Terraform
			// are read but left untouched.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE params_role SET work_mem TO '32MB'")
				},
				Config: configNoParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "1"),
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.work_mem", "32MB"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=32MB",
					}),
				),
			},
			// As soon as parameters is defined, the ones which are not in the resource are reset.
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "2"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=64MB", "TimeZone=UTC",
					}),
				),
			},
			// Removing parameters from the resource keeps the existing ones.
			{
				Config: configNoParameters,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.params_role", "parameters.%", "2"),
					testAccCheckConfigParameters(testRoleConfigQuery, "params_role", []string{
						"statement_timeout=30000", "work_mem=64MB", "TimeZone=UTC",
					}),
				),
			},
		},
	})
}

// Test every parameter PostgreSQL allows to set for a role.
func TestAccPostgresqlRole_ParametersAllSettable(t *testing.T) {
	skipIfNotAcc(t)

	rounds := testParametersRounds(testSettableParameters(t, roleReservedParameters))

	roleConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "all_params_role" {
  name       = "all_params_role"
  parameters = %s
}
`, parameters)
	}

	// All the parameters are set at once, with other values at each step
	var steps []resource.TestStep
	for _, parameters := range rounds {
		steps = append(steps, resource.TestStep{
			Config: roleConfig(testParametersHCL(parameters)),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_role.all_params_role", "parameters.%", strconv.Itoa(len(parameters))),
				testAccCheckAllConfigParameters(testRoleConfigQuery, "all_params_role", roleReservedParameters, parameters),
			),
		})
	}
	steps = append(steps,
		resource.TestStep{
			ResourceName:      "postgresql_role.all_params_role",
			ImportState:       true,
			ImportStateVerify: true,
		},
		resource.TestStep{
			Config: roleConfig("{}"),
			Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("postgresql_role.all_params_role", "parameters.%", "0"),
				testAccCheckConfigParameters(testRoleConfigQuery, "all_params_role", []string{}),
			),
		},
	)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps:        steps,
	})
}

func TestAccPostgresqlRole_ParametersReserved(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "params_role" {
  name = "params_role"
  parameters = {
    search_path = "foo"
  }
}
`,
				ExpectError: regexp.MustCompile(`"search_path" cannot be set here`),
			},
		},
	})
}

// Test that the error of PostgreSQL is reported when a parameter cannot be set
// for a role (e.g.: the ones which need a restart of the server).
func TestAccPostgresqlRole_ParametersRejected(t *testing.T) {
	roleConfig := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "rejected_params_role" {
  name = "rejected_params_role"
  %s
}
`, parameters)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			// at the creation, the role is not created
			{
				Config:      roleConfig(`parameters = { shared_buffers = "1GB" }`),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for role rejected_params_role`),
			},
			{
				Config: roleConfig(`parameters = { work_mem = "64MB" }`),
				Check:  testAccCheckConfigParameters(testRoleConfigQuery, "rejected_params_role", []string{"work_mem=64MB"}),
			},
			// on update, the other changes are not applied
			{
				Config:      roleConfig(`parameters = { work_mem = "128MB", shared_buffers = "1GB" }`),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for role rejected_params_role`),
			},
			{
				Config: roleConfig(`parameters = { work_mem = "64MB" }`),
				Check:  testAccCheckConfigParameters(testRoleConfigQuery, "rejected_params_role", []string{"work_mem=64MB"}),
			},
		},
	})
}

// Test the pgaudit settings of a role, this is the way to enable / disable
// the audit of a specific role (e.g.: on AWS RDS).
func TestAccPostgresqlRole_ParametersPgaudit(t *testing.T) {
	roleConfig := func(pgauditLog string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "audited_role" {
  name     = "audited_role"
  login    = true
  password = "toto"
  parameters = {
    "pgaudit.log" = "%s"
  }
}
`, pgauditLog)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
			testCheckPgauditLoaded(t)
			testCheckCanSetParameter(t, "pgaudit.log", "none")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig("ddl, write"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.audited_role", "parameters.pgaudit.log", "ddl, write"),
					testAccCheckConfigParameters(testRoleConfigQuery, "audited_role", []string{"pgaudit.log=ddl, write"}),
					testAccCheckSessionParameter(t, "audited_role", "toto", "postgres", "pgaudit.log", "ddl, write"),
				),
			},
			{
				Config: roleConfig("none"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.audited_role", "parameters.pgaudit.log", "none"),
					testAccCheckSessionParameter(t, "audited_role", "toto", "postgres", "pgaudit.log", "none"),
				),
			},
		},
	})
}

const testRoleDatabaseConfigQuery = `SELECT COALESCE((
	SELECT s.setconfig FROM pg_catalog.pg_db_role_setting AS s
	JOIN pg_catalog.pg_database AS d ON d.oid = s.setdatabase
	JOIN pg_catalog.pg_roles AS r ON r.oid = s.setrole
	WHERE d.datname = $1 AND r.rolname = $2
), '{}'::text[])`

// testAccCheckRoleDatabaseConfig checks the settings of a role in a database
// (pg_db_role_setting) in the catalog.
func testAccCheckRoleDatabaseConfig(role, database string, expected []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		var config pq.StringArray
		if err := db.QueryRow(testRoleDatabaseConfigQuery, database, role).Scan(&config); err != nil {
			return fmt.Errorf("could not read the parameters of %s in %s: %w", role, database, err)
		}
		actual := []string(config)
		sort.Strings(actual)
		sort.Strings(expected)
		if len(actual) == 0 && len(expected) == 0 {
			return nil
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("parameters of %s in %s: expected %v, got %v", role, database, expected, actual)
		}
		return nil
	}
}

// Test the parameters of a role in a database: ALTER ROLE ... IN DATABASE ... SET.
func TestAccPostgresqlRole_DatabaseParameters(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(roleParameters, blocks string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "dbp_one" {
  name       = "dbp_one"
  parameters = {
    work_mem             = "8MB"
    maintenance_work_mem = "12MB"
  }
}

resource "postgresql_database" "dbp_two" {
  name       = "dbp_two"
  parameters = {
    work_mem = "8MB"
  }
}

resource "postgresql_role" "dbp_role" {
  name       = "dbp_role"
  login      = true
  password   = "dbp_pass"
  %s
  %s
}
`, roleParameters, blocks)
	}
	roleParameters := `parameters = { work_mem = "16MB" }`
	block := func(database, parameters string) string {
		return fmt.Sprintf(`
  database_parameters {
    database   = postgresql_database.%s.name
    parameters = %s
  }`, database, parameters)
	}

	session := func(database, parameter, expected string) resource.TestCheckFunc {
		return testAccCheckSessionParameter(t, "dbp_role", "dbp_pass", database, parameter, expected)
	}

	configCreate := roleConfig(roleParameters, block("dbp_one", `{ work_mem = "32MB", extra_float_digits = "2" }`))
	configUpdate := roleConfig(roleParameters,
		block("dbp_one", `{ work_mem = "64MB" }`)+
			block("dbp_two", `{ temp_tablespaces = "pg_default,pg_default", application_name = "it's \\ me" }`))
	configOnlyTwo := roleConfig(roleParameters,
		block("dbp_two", `{ temp_tablespaces = "pg_default,pg_default", application_name = "it's \\ me" }`))
	configNoBlock := roleConfig(roleParameters, "")
	configEmptyBlock := roleConfig(roleParameters, block("dbp_two", `{}`))

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			if err := testAccCheckPostgresqlRoleDestroy(s); err != nil {
				return err
			}
			return testAccCheckPostgresqlDatabaseDestroy(s)
		},
		Steps: []resource.TestStep{
			// The setting of the role in the database wins over the one of the role,
			// which wins over the one of the database.
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.dbp_role", "database_parameters.#", "1"),
					testAccCheckConfigParameters(testRoleConfigQuery, "dbp_role", []string{"work_mem=16MB"}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_one", []string{"work_mem=32MB", "extra_float_digits=2"}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{}),
					session("dbp_one", "work_mem", "32MB"),
					session("dbp_one", "extra_float_digits", "2"),
					session("dbp_one", "maintenance_work_mem", "12MB"),
					session("dbp_two", "work_mem", "16MB"),
					session("postgres", "work_mem", "16MB"),
				),
			},
			// A parameter is changed and another one is removed, a database is added.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.dbp_role", "database_parameters.#", "2"),
					testAccCheckConfigParameters(testRoleConfigQuery, "dbp_role", []string{"work_mem=16MB"}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_one", []string{"work_mem=64MB"}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{
						"temp_tablespaces=pg_default, pg_default", `application_name=it's \ me`,
					}),
					session("dbp_one", "work_mem", "64MB"),
					session("dbp_one", "extra_float_digits", "1"),
					session("dbp_two", "temp_tablespaces", "pg_default, pg_default"),
					session("dbp_two", "application_name", `it's \ me`),
					session("dbp_two", "work_mem", "16MB"),
				),
			},
			// Changes made outside of Terraform are detected...
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE dbp_role IN DATABASE dbp_one RESET work_mem")
					dbExecute(t, dsn, "ALTER ROLE dbp_role IN DATABASE dbp_one SET lock_timeout TO '5s'")
				},
				Config:             configUpdate,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// ...and reverted.
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_one", []string{"work_mem=64MB"}),
					session("dbp_one", "work_mem", "64MB"),
					session("dbp_one", "lock_timeout", "0"),
				),
			},
			// A database removed from the resource is not managed anymore: its parameters are reset.
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_one", []string{"work_mem=32MB", "extra_float_digits=2"}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{}),
					session("dbp_two", "work_mem", "16MB"),
				),
			},
			// The databases which are not in the resource are read-only.
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE dbp_role IN DATABASE dbp_two SET work_mem TO '1MB'")
				},
				Config:   configCreate,
				PlanOnly: true,
			},
			{
				Config: configOnlyTwo,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_one", []string{}),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{
						"temp_tablespaces=pg_default, pg_default", `application_name=it's \ me`,
					}),
				),
			},
			// Without database_parameters in the resource, the ones of the database are left untouched.
			{
				Config: configNoBlock,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.dbp_role", "database_parameters.#", "1"),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{
						"temp_tablespaces=pg_default, pg_default", `application_name=it's \ me`,
					}),
				),
			},
			// The parameters of the databases are read by the import. The only difference is the spelling of
			// a list, which the state keeps as it is written ("pg_default,pg_default").
			{
				ResourceName:            "postgresql_role.dbp_role",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password", "database_parameters.0.parameters.temp_tablespaces"},
			},
			// An empty map resets all the parameters of the database.
			{
				Config: configEmptyBlock,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.dbp_role", "database_parameters.#", "1"),
					testAccCheckRoleDatabaseConfig("dbp_role", "dbp_two", []string{}),
					testAccCheckConfigParameters(testRoleConfigQuery, "dbp_role", []string{"work_mem=16MB"}),
				),
			},
		},
	})
}

func TestAccPostgresqlRole_DatabaseParametersRejected(t *testing.T) {
	roleConfig := func(blocks string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "dbp_rejected" {
  name = "dbp_rejected"
  %s
}
`, blocks)
	}
	block := func(database, parameters string) string {
		return fmt.Sprintf(`
  database_parameters {
    database   = "%s"
    parameters = %s
  }`, database, parameters)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config:      roleConfig(block("postgres", `{ search_path = "public" }`)),
				ExpectError: regexp.MustCompile(`"search_path" cannot be set here, use the "search_path" attribute instead`),
			},
			{
				Config:      roleConfig(block("postgres", `{ statement_timeout = "1" }`)),
				ExpectError: regexp.MustCompile(`"statement_timeout" cannot be set here`),
			},
			{
				Config:      roleConfig(block("postgres", `{ role = "foo" }`)),
				ExpectError: regexp.MustCompile(`"role" cannot be set here`),
			},
			// refused when the plan is made, before anything is applied
			{
				Config:      roleConfig(block("postgres", `{ work_mem = "1MB" }`) + block("postgres", `{ lock_timeout = "1s" }`)),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`the database "postgres" is defined twice`),
			},
			{
				Config:      roleConfig(block("postgres", `{ Work_Mem = "1MB" }`)),
				ExpectError: regexp.MustCompile(`is not a valid parameter name`),
			},
			{
				Config:      roleConfig(block("postgres", `{ work_mem = "1MB" }`) + block("postgres", `{ lock_timeout = "1s" }`)),
				ExpectError: regexp.MustCompile(`the database "postgres" is defined twice`),
			},
			// at the creation, the role is not created
			{
				Config:      roleConfig(block("postgres", `{ shared_buffers = "1GB" }`)),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for role dbp_rejected in database postgres`),
			},
			{
				Config:      roleConfig(block("does_not_exist", `{ work_mem = "1MB" }`)),
				ExpectError: regexp.MustCompile(`could not set parameter work_mem for role dbp_rejected in database does_not_exist`),
			},
			{
				Config: roleConfig(block("postgres", `{ work_mem = "64MB" }`)),
				Check:  testAccCheckRoleDatabaseConfig("dbp_rejected", "postgres", []string{"work_mem=64MB"}),
			},
			// on update, the other changes are not applied
			{
				Config:      roleConfig(block("postgres", `{ work_mem = "128MB", shared_buffers = "1GB" }`)),
				ExpectError: regexp.MustCompile(`could not set parameter shared_buffers for role dbp_rejected in database postgres`),
			},
			{
				Config: roleConfig(block("postgres", `{ work_mem = "64MB" }`)),
				Check:  testAccCheckRoleDatabaseConfig("dbp_rejected", "postgres", []string{"work_mem=64MB"}),
			},
		},
	})
}

// testAccCheckRoleCannotLogin checks that a session cannot be opened with the password,
// and that the server refused it: invalid_password (28P01, wrong or expired password) or
// invalid_authorization_specification (28000, a role which cannot log in). Any other
// error (the server is down, the port is wrong...) fails the check.
func testAccCheckRoleCannotLogin(t *testing.T, role, password string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := getTestConfig(t)
		config.Username = role
		config.Password = password
		db, err := sql.Open("postgres", config.connStr("postgres"))
		if err != nil {
			return fmt.Errorf("could not open SQL connection: %v", err)
		}
		defer closeDB(t, db)
		err = db.Ping()
		if err == nil {
			return fmt.Errorf("role %s can login with the password %q", role, password)
		}
		var pqErr *pq.Error
		if !errors.As(err, &pqErr) || (pqErr.Code != "28P01" && pqErr.Code != "28000") {
			return fmt.Errorf("the login of role %s was not refused by the server (expected SQLSTATE 28P01 or 28000): %v", role, err)
		}
		return nil
	}
}

// testAccCheckRoleColumn checks a column of pg_roles.
func testAccCheckRoleColumn(role, column string, expected any) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}
		var actual any
		switch expected.(type) {
		case bool:
			var v bool
			err = db.QueryRow(fmt.Sprintf("SELECT %s FROM pg_catalog.pg_roles WHERE rolname = $1", column), role).Scan(&v)
			actual = v
		case int:
			var v int
			err = db.QueryRow(fmt.Sprintf("SELECT %s FROM pg_catalog.pg_roles WHERE rolname = $1", column), role).Scan(&v)
			actual = v
		default:
			var v string
			err = db.QueryRow(fmt.Sprintf("SELECT %s::text FROM pg_catalog.pg_roles WHERE rolname = $1", column), role).Scan(&v)
			actual = v
		}
		if err != nil {
			return fmt.Errorf("could not read %s of role %s: %w", column, role, err)
		}
		if actual != expected {
			return fmt.Errorf("%s of role %s: expected %v, got %v", column, role, expected, actual)
		}
		return nil
	}
}

// testAccCheckRoleValidUntil checks the expiration of the password of a role, "infinity" for none.
func testAccCheckRoleValidUntil(role, validUntil string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}
		var ok bool
		err = db.QueryRow(`SELECT COALESCE(rolvaliduntil = $2::timestamptz, $2 = 'infinity') FROM pg_catalog.pg_roles WHERE rolname = $1`,
			role, validUntil).Scan(&ok)
		if err != nil {
			return fmt.Errorf("could not read the expiration of role %s: %w", role, err)
		}
		if !ok {
			return fmt.Errorf("the password of role %s does not expire at %s", role, validUntil)
		}
		return nil
	}
}

// testRoleBoolAttribute tests a boolean attribute of a role, at the creation and at the update, in both
// directions, in the catalog and when it is changed outside of Terraform.
// Two roles are used: the first one is created with true, the other with false.
func testRoleBoolAttribute(t *testing.T, attr, column, enable, disable string, probe ...string) {
	roleA, roleB := "bool_"+attr+"_a", "bool_"+attr+"_b"
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(a, b bool) string {
		return fmt.Sprintf(`
resource "postgresql_role" "a" {
  name = "%s"
  %s = %t
}

resource "postgresql_role" "b" {
  name = "%s"
  %s = %t
}
`, roleA, attr, a, roleB, attr, b)
	}
	check := func(a, b bool) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_role.a", attr, strconv.FormatBool(a)),
			resource.TestCheckResourceAttr("postgresql_role.b", attr, strconv.FormatBool(b)),
			testAccCheckRoleColumn(roleA, column, a),
			testAccCheckRoleColumn(roleB, column, b),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
			if len(probe) > 0 {
				testCheckCanExecute(t, "set "+enable, probe...)
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{Config: roleConfig(true, false), Check: check(true, false)},
			{Config: roleConfig(false, true), Check: check(false, true)},
			{Config: roleConfig(true, false), Check: check(true, false)},
			// changed outside of Terraform, in both directions
			{
				PreConfig: func() {
					dbExecute(t, dsn, fmt.Sprintf("ALTER ROLE %s WITH %s", roleA, disable))
					dbExecute(t, dsn, fmt.Sprintf("ALTER ROLE %s WITH %s", roleB, enable))
				},
				Config:             roleConfig(true, false),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: roleConfig(true, false), Check: check(true, false)},
			{
				ResourceName:      "postgresql_role.a",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccPostgresqlRole_AttributeSuperuser(t *testing.T) {
	testRoleBoolAttribute(t, "superuser", "rolsuper", "SUPERUSER", "NOSUPERUSER",
		"CREATE ROLE tf_probe_su SUPERUSER")
}

func TestAccPostgresqlRole_AttributeCreateDatabase(t *testing.T) {
	testRoleBoolAttribute(t, "create_database", "rolcreatedb", "CREATEDB", "NOCREATEDB")
}

func TestAccPostgresqlRole_AttributeCreateRole(t *testing.T) {
	testRoleBoolAttribute(t, "create_role", "rolcreaterole", "CREATEROLE", "NOCREATEROLE")
}

func TestAccPostgresqlRole_AttributeInherit(t *testing.T) {
	testRoleBoolAttribute(t, "inherit", "rolinherit", "INHERIT", "NOINHERIT")
}

func TestAccPostgresqlRole_AttributeLogin(t *testing.T) {
	testRoleBoolAttribute(t, "login", "rolcanlogin", "LOGIN", "NOLOGIN")
}

func TestAccPostgresqlRole_AttributeReplication(t *testing.T) {
	testRoleBoolAttribute(t, "replication", "rolreplication", "REPLICATION", "NOREPLICATION",
		"CREATE ROLE tf_probe_repl REPLICATION")
}

func TestAccPostgresqlRole_AttributeBypassRLS(t *testing.T) {
	testRoleBoolAttribute(t, "bypass_row_level_security", "rolbypassrls", "BYPASSRLS", "NOBYPASSRLS",
		"CREATE ROLE tf_probe_rls BYPASSRLS")
}

// Test the numbers and the dates of a role, at the creation and at the update.
func TestAccPostgresqlRole_AttributeLimitAndExpiration(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(limitA, validUntilA, limitB, validUntilB string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "a" {
  name             = "limit_role_a"
  %s
  %s
}

resource "postgresql_role" "b" {
  name             = "limit_role_b"
  %s
  %s
}
`, limitA, validUntilA, limitB, validUntilB)
	}
	limit := func(n int) string { return fmt.Sprintf("connection_limit = %d", n) }
	until := func(v string) string { return fmt.Sprintf("valid_until = %q", v) }
	check := func(limitA int, untilA string, limitB int, untilB string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_role.a", "connection_limit", strconv.Itoa(limitA)),
			resource.TestCheckResourceAttr("postgresql_role.a", "valid_until", untilA),
			resource.TestCheckResourceAttr("postgresql_role.b", "connection_limit", strconv.Itoa(limitB)),
			resource.TestCheckResourceAttr("postgresql_role.b", "valid_until", untilB),
			testAccCheckRoleColumn("limit_role_a", "rolconnlimit", limitA),
			testAccCheckRoleColumn("limit_role_b", "rolconnlimit", limitB),
			testAccCheckRoleValidUntil("limit_role_a", untilA),
			testAccCheckRoleValidUntil("limit_role_b", untilB),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			// a date and the infinity at the creation, a positive limit and none
			{
				Config: roleConfig(limit(5), until("2099-05-04 12:00:00+00"), limit(-1), until("infinity")),
				Check:  check(5, "2099-05-04 12:00:00+00", -1, "infinity"),
			},
			// in both directions, with another date
			{
				Config: roleConfig(limit(-1), until("infinity"), limit(2), until("2098-01-02 03:04:05+00")),
				Check:  check(-1, "infinity", 2, "2098-01-02 03:04:05+00"),
			},
			{
				Config: roleConfig(limit(0), until("2097-12-31 23:59:59+00"), limit(10), until("infinity")),
				Check:  check(0, "2097-12-31 23:59:59+00", 10, "infinity"),
			},
			// the default values
			{
				Config: roleConfig("", "", "", ""),
				Check:  check(-1, "infinity", -1, "infinity"),
			},
			// changed outside of Terraform
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE limit_role_a CONNECTION LIMIT 3")
					dbExecute(t, dsn, "ALTER ROLE limit_role_b VALID UNTIL '2090-01-01 00:00:00+00'")
				},
				Config:             roleConfig("", "", "", ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig("", "", "", ""),
				Check:  check(-1, "infinity", -1, "infinity"),
			},
			{
				ResourceName:      "postgresql_role.a",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "postgresql_role.b",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// Test the password of a role: set, changed, removed with NULL, removed from the configuration.
func TestAccPostgresqlRole_Password(t *testing.T) {
	roleConfig := func(password string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "pwd_role" {
  name  = "pwd_role"
  login = true
  %s
}

resource "postgresql_role" "pwd_null_role" {
  name     = "pwd_null_role"
  login    = true
  password = "NULL"
}
`, password)
	}
	password := func(p string) string { return fmt.Sprintf("password = %q", p) }
	scram := func(role string) resource.TestCheckFunc {
		return testAccCheckRoleStoredPassword(role, func(stored string) error {
			if !scramSHA256PasswordRegexp.MatchString(stored) {
				return fmt.Errorf("the password is not a SCRAM-SHA-256 hash: %q", stored)
			}
			return nil
		})
	}
	noPassword := func(role string) resource.TestCheckFunc {
		return testAccCheckRoleStoredPassword(role, func(stored string) error {
			if stored != "" {
				return fmt.Errorf("the role has a password: %q", stored)
			}
			return nil
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig(password("pwd1")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_role", "pwd1"),
					scram("pwd_role"),
					// NULL at the creation
					testAccCheckRoleCannotLogin(t, "pwd_null_role", "NULL"),
					testAccCheckRoleCannotLogin(t, "pwd_null_role", ""),
					noPassword("pwd_null_role"),
				),
			},
			{
				Config: roleConfig(password("pwd2")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_role", "pwd2"),
					testAccCheckRoleCannotLogin(t, "pwd_role", "pwd1"),
					scram("pwd_role"),
				),
			},
			// NULL at the update: the password is removed
			{
				Config: roleConfig(password("NULL")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCannotLogin(t, "pwd_role", "pwd2"),
					testAccCheckRoleCannotLogin(t, "pwd_role", "pwd1"),
					testAccCheckRoleCannotLogin(t, "pwd_role", "NULL"),
					testAccCheckRoleCannotLogin(t, "pwd_role", ""),
					noPassword("pwd_role"),
				),
			},
			{
				Config: roleConfig(password("pwd3")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_role", "pwd3"),
					scram("pwd_role"),
				),
			},
			// The password is removed from the configuration: the role keeps it.
			// (removing it needs password = "NULL")
			{
				Config: roleConfig(""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_role", "pwd3"),
					scram("pwd_role"),
				),
			},
			{
				Config: roleConfig(password("NULL")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCannotLogin(t, "pwd_role", "pwd3"),
					noPassword("pwd_role"),
				),
			},
			// a password given again after NULL, and the role is renamed
			{
				Config: strings.ReplaceAll(roleConfig(password("pwd4")), `"pwd_role"`, `"pwd_role_renamed"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_role_renamed", "pwd4"),
					testAccCheckRoleCannotLogin(t, "pwd_role_renamed", "pwd3"),
				),
			},
		},
	})
}

// The passwords are sent in clear text if it is asked, and removed too.
func TestAccPostgresqlRole_PasswordClearText(t *testing.T) {
	roleConfig := func(password string) string {
		return fmt.Sprintf(`
provider "postgresql" {
  password_authentication = "password"
}

resource "postgresql_role" "pwd_clear_role" {
  name     = "pwd_clear_role"
  login    = true
  password = %q
}
`, password)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		// Unlike Providers, ProviderFactories lets the steps configure the provider
		ProviderFactories: map[string]func() (*schema.Provider, error){
			"postgresql": func() (*schema.Provider, error) { return testAccProvider, nil },
		},
		CheckDestroy: func(s *terraform.State) error {
			// the other tests use the default configuration of the provider
			resetDBRegistry()
			return testAccCheckPostgresqlRoleDestroy(s)
		},
		Steps: []resource.TestStep{
			{
				PreConfig: resetDBRegistry,
				Config:    roleConfig("clear1"),
				Check:     testAccCheckRoleCanLogin(t, "pwd_clear_role", "clear1"),
			},
			{
				Config: roleConfig("clear2"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwd_clear_role", "clear2"),
					testAccCheckRoleCannotLogin(t, "pwd_clear_role", "clear1"),
				),
			},
			{
				Config: roleConfig("NULL"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCannotLogin(t, "pwd_clear_role", "clear2"),
					testAccCheckRoleCannotLogin(t, "pwd_clear_role", "NULL"),
				),
			},
		},
	})
}

// PostgreSQL does not accept UNENCRYPTED passwords anymore.
func TestAccPostgresqlRole_PasswordUnencrypted(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureScramSHA256)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "unencrypted_role" {
  name               = "unencrypted_role"
  password           = "pwd"
  encrypted_password = false
}`,
				// refused by the provider when the plan is made, not by the server at the apply
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`encrypted_password = false is not supported[\s\S]*UNENCRYPTED PASSWORD is no longer supported`),
			},
		},
	})
}

// Test the roles of a role: added and removed, in the catalog, and the memberships
// which are managed by postgresql_grant_role are left as they are.
func TestAccPostgresqlRole_Roles(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(roles, login string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "roles_g1" {
  name = "roles_g1"
}

resource "postgresql_role" "roles_g2" {
  name = "roles_g2"
}

resource "postgresql_role" "roles_g3" {
  name = "roles_g3"
}

resource "postgresql_role" "roles_member" {
  name  = "roles_member"
  login = %s
  roles = %s
}
`, login, roles)
	}
	ref := func(names ...string) string {
		refs := make([]string, len(names))
		for i, name := range names {
			refs[i] = "postgresql_role.roles_" + name + ".name"
		}
		return "[" + strings.Join(refs, ", ") + "]"
	}
	member := func(roles ...string) resource.TestCheckFunc {
		granted := make([]string, len(roles))
		for i, role := range roles {
			granted[i] = "roles_" + role
		}
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_role.roles_member", "roles.#", strconv.Itoa(len(roles))),
			testAccCheckPostgresqlRoleExists("roles_member", granted, nil),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{Config: roleConfig(ref("g1", "g2"), "false"), Check: member("g1", "g2")},
			// one is removed and another one is added
			{Config: roleConfig(ref("g2", "g3"), "false"), Check: member("g2", "g3")},
			{Config: roleConfig(ref("g1", "g2", "g3"), "false"), Check: member("g1", "g2", "g3")},
			{Config: roleConfig(ref("g3"), "false"), Check: member("g3")},
			{Config: roleConfig("[]", "false"), Check: member()},
			{Config: roleConfig(ref("g1"), "false"), Check: member("g1")},
			// changed outside of Terraform, in both directions
			{
				PreConfig: func() {
					dbExecute(t, dsn, "GRANT roles_g2 TO roles_member")
					dbExecute(t, dsn, "REVOKE roles_g1 FROM roles_member")
				},
				Config:             roleConfig(ref("g1"), "false"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: roleConfig(ref("g1"), "false"), Check: member("g1")},
			{
				ResourceName:      "postgresql_role.roles_member",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// The memberships managed by postgresql_grant_role are not changed when the role is updated.
func TestAccPostgresqlRole_RolesAndGrantRole(t *testing.T) {
	roleConfig := func(login bool) string {
		return fmt.Sprintf(`
resource "postgresql_role" "rag_g1" {
  name = "rag_g1"
}

resource "postgresql_role" "rag_g2" {
  name = "rag_g2"
}

resource "postgresql_role" "rag_member" {
  name  = "rag_member"
  login = %t
  roles = [postgresql_role.rag_g1.name]
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "rag" {
  role              = postgresql_role.rag_member.name
  grant_role        = postgresql_role.rag_g2.name
  with_admin_option = true
}
`, login)
	}
	check := resource.ComposeTestCheckFunc(
		testAccCheckMembership("rag_member", "rag_g1", membershipOptions{inherit: true, set: true}),
		testAccCheckMembership("rag_member", "rag_g2", membershipOptions{admin: true, inherit: true, set: true}),
	)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{Config: roleConfig(false), Check: check},
			{Config: roleConfig(true), Check: check},
			{Config: roleConfig(false), Check: check},
		},
	})
}

// The memberships of CREATE ROLE ... IN ROLE / ROLE / ADMIN are the ones of the roles
// attribute and of postgresql_grant_role.
func TestAccPostgresqlRole_CreateRoleMembershipClauses(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := `
resource "postgresql_role" "cm_in" {
  name = "cm_in"
}

# CREATE ROLE cm_ctl_in IN ROLE cm_in_group
resource "postgresql_role" "cm_in_group" {
  name = "cm_in_group"
}

resource "postgresql_role" "cm_member_in" {
  name  = "cm_member_in"
  roles = [postgresql_role.cm_in_group.name]
}

# CREATE ROLE cm_target ROLE cm_member ADMIN cm_admin
resource "postgresql_role" "cm_target" {
  name = "cm_target"
}

resource "postgresql_role" "cm_member" {
  name = "cm_member"
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_role" "cm_admin" {
  name = "cm_admin"
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "cm_member" {
  role       = postgresql_role.cm_member.name
  grant_role = postgresql_role.cm_target.name
}

resource "postgresql_grant_role" "cm_admin" {
  role              = postgresql_role.cm_admin.name
  grant_role        = postgresql_role.cm_target.name
  with_admin_option = true
}
`
	sameMembership := func(member, role, controlMember, controlRole string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			client := testAccProvider.Meta().(*Client)
			actual, err := readMembership(client, member, role)
			if err != nil {
				return err
			}
			expected, err := readMembership(client, controlMember, controlRole)
			if err != nil {
				return err
			}
			if actual == nil || expected == nil {
				return fmt.Errorf("missing membership: %s in %s %v, %s in %s %v", member, role, actual, controlMember, controlRole, expected)
			}
			actual.grantor, expected.grantor = "", ""
			if *actual != *expected {
				return fmt.Errorf("%s in %s: %+v, %s in %s: %+v", member, role, *actual, controlMember, controlRole, *expected)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			for _, role := range []string{"cm_ctl_in", "cm_ctl_target", "cm_ctl_member", "cm_ctl_admin", "cm_ctl_group"} {
				dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
			}
			return testAccCheckPostgresqlRoleDestroy(s)
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					for _, role := range []string{"cm_ctl_in", "cm_ctl_target", "cm_ctl_member", "cm_ctl_admin", "cm_ctl_group"} {
						dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
					}
					dbExecute(t, dsn, "CREATE ROLE cm_ctl_group")
					dbExecute(t, dsn, "CREATE ROLE cm_ctl_in IN ROLE cm_ctl_group")
					dbExecute(t, dsn, "CREATE ROLE cm_ctl_member")
					dbExecute(t, dsn, "CREATE ROLE cm_ctl_admin")
					dbExecute(t, dsn, "CREATE ROLE cm_ctl_target ROLE cm_ctl_member ADMIN cm_ctl_admin")
				},
				Config: roleConfig,
				Check: resource.ComposeTestCheckFunc(
					sameMembership("cm_member_in", "cm_in_group", "cm_ctl_in", "cm_ctl_group"),
					sameMembership("cm_member", "cm_target", "cm_ctl_member", "cm_ctl_target"),
					sameMembership("cm_admin", "cm_target", "cm_ctl_admin", "cm_ctl_target"),
				),
			},
		},
	})
}

// Test the settings which have their own attribute, at the creation and at the update, in both directions.
func TestAccPostgresqlRole_Settings(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(settings string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "set_g1" {
  name = "set_g1"
}

resource "postgresql_role" "set_g2" {
  name = "set_g2"
}

resource "postgresql_role" "set_role" {
  name  = "set_role"
  login = true
  roles = [postgresql_role.set_g1.name, postgresql_role.set_g2.name]
  %s
}
`, settings)
	}
	query := func(role string, parameters ...string) resource.TestCheckFunc {
		return testAccCheckConfigParameters(testRoleConfigQuery, role, parameters)
	}

	create := `search_path = ["bar", "foo-with-hyphen"]
  statement_timeout = 30000
  idle_in_transaction_session_timeout = 60000
  assume_role = postgresql_role.set_g1.name`
	update := `search_path = ["other", "Mixed Case", "public"]
  statement_timeout = 1
  idle_in_transaction_session_timeout = 2
  assume_role = postgresql_role.set_g2.name`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig(create),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.set_role", "search_path.#", "2"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "search_path.1", "foo-with-hyphen"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "statement_timeout", "30000"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "idle_in_transaction_session_timeout", "60000"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "assume_role", "set_g1"),
					query("set_role", `search_path=bar, "foo-with-hyphen"`, "statement_timeout=30000",
						"idle_in_transaction_session_timeout=60000", "role=set_g1"),
				),
			},
			{
				Config: roleConfig(update),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.set_role", "search_path.#", "3"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "statement_timeout", "1"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "idle_in_transaction_session_timeout", "2"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "assume_role", "set_g2"),
					query("set_role", `search_path=other, "Mixed Case", public`, "statement_timeout=1",
						"idle_in_transaction_session_timeout=2", "role=set_g2"),
				),
			},
			// changed outside of Terraform
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE set_role SET statement_timeout TO 5")
					dbExecute(t, dsn, "ALTER ROLE set_role RESET role")
				},
				Config:             roleConfig(update),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig(update),
				Check: query("set_role", `search_path=other, "Mixed Case", public`, "statement_timeout=1",
					"idle_in_transaction_session_timeout=2", "role=set_g2"),
			},
			// the settings are reset: they are removed from the catalog
			{
				Config: roleConfig(``),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.set_role", "search_path.#", "0"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "statement_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "idle_in_transaction_session_timeout", "0"),
					resource.TestCheckResourceAttr("postgresql_role.set_role", "assume_role", ""),
					query("set_role"),
				),
			},
			// and set again
			{
				Config: roleConfig(create),
				Check: query("set_role", `search_path=bar, "foo-with-hyphen"`, "statement_timeout=30000",
					"idle_in_transaction_session_timeout=60000", "role=set_g1"),
			},
			{
				ResourceName:      "postgresql_role.set_role",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// Test the rename of a role: what the role has is kept.
func TestAccPostgresqlRole_Rename(t *testing.T) {
	roleConfig := func(name string) string {
		return fmt.Sprintf(`
resource "postgresql_database" "rename_db" {
  name = "rename_db"
}

resource "postgresql_role" "rename_g" {
  name = "rename_g"
}

resource "postgresql_role" "rename_role" {
  name             = "%s"
  login            = true
  password         = "rename_pwd"
  connection_limit = 4
  roles            = [postgresql_role.rename_g.name]
  parameters       = { work_mem = "16MB" }
  database_parameters {
    database   = postgresql_database.rename_db.name
    parameters = { work_mem = "32MB" }
  }
}
`, name)
	}
	check := func(name string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_role.rename_role", "name", name),
			resource.TestCheckResourceAttr("postgresql_role.rename_role", "id", name),
			testAccCheckPostgresqlRoleExists(name, []string{"rename_g"}, nil),
			testAccCheckRoleColumn(name, "rolconnlimit", 4),
			testAccCheckRoleCanLogin(t, name, "rename_pwd"),
			testAccCheckConfigParameters(testRoleConfigQuery, name, []string{"work_mem=16MB"}),
			testAccCheckRoleDatabaseConfig(name, "rename_db", []string{"work_mem=32MB"}),
			testAccCheckSessionParameter(t, name, "rename_pwd", "rename_db", "work_mem", "32MB"),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			if err := testAccCheckPostgresqlRoleDestroy(s); err != nil {
				return err
			}
			return testAccCheckPostgresqlDatabaseDestroy(s)
		},
		Steps: []resource.TestStep{
			{Config: roleConfig("rename_role"), Check: check("rename_role")},
			{Config: roleConfig("rename_role2"), Check: check("rename_role2")},
			{Config: roleConfig("rename_role"), Check: check("rename_role")},
		},
	})
}

// A role is changed or dropped outside of Terraform.
func TestAccPostgresqlRole_ChangedOutside(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := `
resource "postgresql_role" "outside_role" {
  name  = "outside_role"
  login = true
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check:  testAccCheckRoleColumn("outside_role", "rolcanlogin", true),
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "ALTER ROLE outside_role NOLOGIN")
				},
				Config:             roleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig,
				Check:  testAccCheckRoleColumn("outside_role", "rolcanlogin", true),
			},
			// dropped: it is created again
			{
				PreConfig: func() {
					dbExecute(t, dsn, "DROP ROLE outside_role")
				},
				Config:             roleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig,
				Check:  testAccCheckRoleColumn("outside_role", "rolcanlogin", true),
			},
			// dropped, then destroyed: nothing to do
			{
				PreConfig: func() {
					dbExecute(t, dsn, "DROP ROLE outside_role")
				},
				Config:  roleConfig,
				Destroy: true,
			},
		},
	})
}

// A role which owns objects is dropped after its objects are reassigned to the connected user.
func TestAccPostgresqlRole_ReassignOwned(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := `
resource "postgresql_role" "owner_role" {
  name = "owner_role"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			if err := testAccCheckPostgresqlRoleDestroy(s); err != nil {
				return err
			}
			var owner string
			db, err := testAccProvider.Meta().(*Client).Connect()
			if err != nil {
				return err
			}
			if err := db.QueryRow("SELECT pg_get_userbyid(relowner) FROM pg_class WHERE relname = 'owner_role_table'").Scan(&owner); err != nil {
				return fmt.Errorf("the table of the role has been dropped: %w", err)
			}
			if owner != config.Username {
				return fmt.Errorf("the table is owned by %s instead of %s", owner, config.Username)
			}
			dbExecute(t, dsn, "DROP TABLE owner_role_table")
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check: func(s *terraform.State) error {
					// the connected user has to be able to give the table to the role (and to take it back)
					dbExecute(t, dsn, "GRANT owner_role TO CURRENT_USER")
					dbExecute(t, dsn, "GRANT CREATE ON SCHEMA public TO owner_role")
					dbExecute(t, dsn, "CREATE TABLE owner_role_table (id int)")
					dbExecute(t, dsn, "ALTER TABLE owner_role_table OWNER TO owner_role")
					return nil
				},
			},
		},
	})
}

// skip_reassign_owned: the objects of the role are not reassigned, so the role cannot be dropped.
func TestAccPostgresqlRole_SkipReassignOwned(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := func(skip bool) string {
		return fmt.Sprintf(`
resource "postgresql_role" "skip_reassign_role" {
  name                = "skip_reassign_role"
  skip_reassign_owned = %t
}
`, skip)
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			dbExecute(t, dsn, "DROP TABLE IF EXISTS skip_reassign_table")
			return testAccCheckPostgresqlRoleDestroy(s)
		},
		Steps: []resource.TestStep{
			{
				Config: roleConfig(true),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_role.skip_reassign_role", "skip_reassign_owned", "true"),
					func(s *terraform.State) error {
						dbExecute(t, dsn, "GRANT skip_reassign_role TO CURRENT_USER")
						dbExecute(t, dsn, "GRANT CREATE ON SCHEMA public TO skip_reassign_role")
						dbExecute(t, dsn, "CREATE TABLE skip_reassign_table (id int)")
						dbExecute(t, dsn, "ALTER TABLE skip_reassign_table OWNER TO skip_reassign_role")
						return nil
					},
				),
			},
			// the role cannot be dropped because it owns the table
			{
				Config:      roleConfig(true),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`could not delete role skip_reassign_role`),
			},
			// the objects are reassigned if the option is removed
			{
				Config: roleConfig(false),
				Check:  resource.TestCheckResourceAttr("postgresql_role.skip_reassign_role", "skip_reassign_owned", "false"),
			},
		},
	})
}

// skip_drop_role: the role is not dropped.
func TestAccPostgresqlRole_SkipDropRole(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			exists, err := checkRoleExists(testAccProvider.Meta().(*Client), "skip_drop_role")
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("the role has been dropped")
			}
			dbExecute(t, dsn, "DROP ROLE skip_drop_role")
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_role" "skip_drop_role" {
  name           = "skip_drop_role"
  skip_drop_role = true
}`,
				Check: resource.TestCheckResourceAttr("postgresql_role.skip_drop_role", "skip_drop_role", "true"),
			},
		},
	})
}

func TestHashRoleDatabaseParameters(t *testing.T) {
	block := func(database string, parameters map[string]any) map[string]any {
		return map[string]any{"database": database, "parameters": parameters}
	}

	same := [][2]map[string]any{
		// the order of the parameters does not matter
		{block("a", map[string]any{"work_mem": "1MB", "lock_timeout": "1s"}), block("a", map[string]any{"lock_timeout": "1s", "work_mem": "1MB"})},
		// a list is the same list whatever the way it is written
		{block("a", map[string]any{"temp_tablespaces": "foo,bar"}), block("a", map[string]any{"temp_tablespaces": `foo, "bar"`})},
	}
	for _, c := range same {
		if hashRoleDatabaseParameters(c[0]) != hashRoleDatabaseParameters(c[1]) {
			t.Errorf("%v and %v have to be the same block", c[0], c[1])
		}
	}

	different := [][2]map[string]any{
		{block("a", map[string]any{"work_mem": "1MB"}), block("b", map[string]any{"work_mem": "1MB"})},
		{block("a", map[string]any{"work_mem": "1MB"}), block("a", map[string]any{"work_mem": "2MB"})},
		{block("a", map[string]any{"temp_tablespaces": "foo,bar"}), block("a", map[string]any{"temp_tablespaces": "bar,foo"})},
		{block("a", map[string]any{}), block("a", map[string]any{"work_mem": "1MB"})},
	}
	for _, c := range different {
		if hashRoleDatabaseParameters(c[0]) == hashRoleDatabaseParameters(c[1]) {
			t.Errorf("%v and %v have to be different blocks", c[0], c[1])
		}
	}
}

func TestRoleDatabaseParametersDuplicate(t *testing.T) {
	set := schema.NewSet(hashRoleDatabaseParameters, []any{
		map[string]any{"database": "a", "parameters": map[string]any{"work_mem": "1MB"}},
		map[string]any{"database": "a", "parameters": map[string]any{"work_mem": "2MB"}},
	})
	if _, err := roleDatabaseParameters(set); err == nil {
		t.Fatal("a database defined twice has to be refused")
	}
}

// Test that an expiry is removed: with an empty value and by removing the attribute,
// the role has no expiry in the catalog and can log in again.
func TestAccPostgresqlRole_ValidUntilRemoved(t *testing.T) {
	roleConfig := func(validUntil string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "vu_role" {
  name     = "vu_role"
  login    = true
  password = "vu_pwd"
  %s
}

resource "postgresql_role" "vu_nologin" {
  name     = "vu_nologin"
  login    = false
  password = "vu_pwd"
}
`, validUntil)
	}
	expired := `valid_until = "2000-01-01 00:00:00+00"`
	noExpiry := resource.ComposeTestCheckFunc(
		testAccCheckRoleValidUntil("vu_role", "infinity"),
		testAccCheckRoleColumn("vu_role", "rolvaliduntil", "infinity"),
		testAccCheckRoleCanLogin(t, "vu_role", "vu_pwd"),
	)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig(expired),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleValidUntil("vu_role", "2000-01-01 00:00:00+00"),
					// the password has expired, the right one is refused
					testAccCheckRoleCannotLogin(t, "vu_role", "vu_pwd"),
					// a role which cannot log in
					testAccCheckRoleCannotLogin(t, "vu_nologin", "vu_pwd"),
				),
			},
			// an empty value removes the expiry
			{
				Config: roleConfig(`valid_until = ""`),
				Check:  noExpiry,
			},
			{Config: roleConfig(`valid_until = ""`), PlanOnly: true},
			{
				Config: roleConfig(expired),
				Check:  testAccCheckRoleCannotLogin(t, "vu_role", "vu_pwd"),
			},
			// so does removing the attribute
			{
				Config: roleConfig(``),
				Check:  testAccCheckRoleValidUntil("vu_role", "infinity"),
			},
			{Config: roleConfig(``), PlanOnly: true},
			{
				Config: roleConfig(`valid_until = "2099-01-01 00:00:00+00"`),
				Check:  testAccCheckRoleValidUntil("vu_role", "2099-01-01 00:00:00+00"),
			},
			{
				Config: roleConfig(``),
				Check:  testAccCheckRoleValidUntil("vu_role", "infinity"),
			},
		},
	})
}

// A password which is removed outside of Terraform, and a password which is added while the
// configuration says NULL, are changes of the plan (the provider reads the passwords of a superuser).
func TestAccPostgresqlRole_PasswordDrift(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := `
resource "postgresql_role" "pwdd_role" {
  name     = "pwdd_role"
  login    = true
  password = "pwdd1"
}

resource "postgresql_role" "pwdd_null_role" {
  name     = "pwdd_null_role"
  login    = true
  password = "NULL"
}
`
	noPassword := func(role string) resource.TestCheckFunc {
		return testAccCheckRoleStoredPassword(role, func(stored string) error {
			if stored != "" {
				return fmt.Errorf("the role has a password: %q", stored)
			}
			return nil
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testSuperuserPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckRoleCanLogin(t, "pwdd_role", "pwdd1"),
					noPassword("pwdd_null_role"),
				),
			},
			{
				PreConfig:          func() { dbExecute(t, dsn, "ALTER ROLE pwdd_role PASSWORD NULL") },
				Config:             roleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig,
				Check:  testAccCheckRoleCanLogin(t, "pwdd_role", "pwdd1"),
			},
			{
				// an md5 hash: the provider cannot tell whether a SCRAM-SHA-256 hash is the one of the state
				PreConfig:          func() { dbExecute(t, dsn, "ALTER ROLE pwdd_null_role PASSWORD 'md5c98cbfeb6a347a47eb8e96cfb4c4b890'") },
				Config:             roleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig,
				Check:  noPassword("pwdd_null_role"),
			},
		},
	})
}

// PostgreSQL before 10 does not know SCRAM-SHA-256 and has no UNENCRYPTED PASSWORD to refuse: with
// the version of the server set by the configuration of the provider, encrypted_password = false is accepted.
func TestAccPostgresqlRole_PasswordUnencryptedOldServer(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureScramSHA256)
		},
		ProviderFactories: map[string]func() (*schema.Provider, error){
			"postgresql": func() (*schema.Provider, error) { return testAccProvider, nil },
		},
		CheckDestroy: func(s *terraform.State) error {
			resetDBRegistry()
			return nil
		},
		Steps: []resource.TestStep{
			{
				PreConfig: resetDBRegistry,
				Config: `
provider "postgresql" {
  expected_version = "9.6.0"
}

resource "postgresql_role" "unencrypted_old_role" {
  name               = "unencrypted_old_role"
  password           = "pwd"
  encrypted_password = false
}`,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// The databases in which the role has parameters are put in the state by the import (the function is
// called as it is: a step of import cannot tell a missing resource from an empty one).
func TestAccPostgresqlRole_ImportFunction(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)
	testCheckCompatibleVersion(t, featurePrivileges)
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	for _, role := range []string{"rim_with", "rim_without"} {
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
		dbExecute(t, dsn, "CREATE ROLE "+role)
	}
	t.Cleanup(func() {
		for _, role := range []string{"rim_with", "rim_without"} {
			dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
		}
	})
	dbExecute(t, dsn, "ALTER ROLE rim_with IN DATABASE template1 SET work_mem TO '3MB'")
	dbExecute(t, dsn, "ALTER ROLE rim_with IN DATABASE postgres SET work_mem TO '4MB'")

	databases := func(role string) []string {
		d := resourcePostgreSQLRole().TestResourceData()
		d.SetId(role)
		imported, err := resourcePostgreSQLRoleImport(context.Background(), d, testAccProvider.Meta())
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if len(imported) != 1 || imported[0].Id() != role {
			t.Fatalf("%s: expected the role itself, got %v", role, imported)
		}
		names := []string{}
		for _, block := range imported[0].Get(roleDatabaseParametersAttr).(*schema.Set).List() {
			names = append(names, block.(map[string]any)["database"].(string))
		}
		sort.Strings(names)
		return names
	}
	if got := databases("rim_with"); !reflect.DeepEqual(got, []string{"postgres", "template1"}) {
		t.Errorf("databases of rim_with: got %v", got)
	}
	if got := databases("rim_without"); len(got) != 0 {
		t.Errorf("databases of rim_without: got %v", got)
	}
}

// A parameter of a database which is changed outside of Terraform is a change of the plan, even if
// the new value is spelled like a list and the old one is the same list: only the parameters which
// are lists are compared as such.
func TestAccPostgresqlRole_DatabaseParametersDrift(t *testing.T) {
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	roleConfig := `
resource "postgresql_role" "dbpd_role" {
  name = "dbpd_role"
  database_parameters {
    database   = "postgres"
    parameters = { application_name = "a,b" }
  }
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: roleConfig,
				Check:  testAccCheckRoleDatabaseConfig("dbpd_role", "postgres", []string{"application_name=a,b"}),
			},
			{
				PreConfig:          func() { dbExecute(t, dsn, "ALTER ROLE dbpd_role IN DATABASE postgres SET application_name TO 'a, b'") },
				Config:             roleConfig,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: roleConfig,
				Check:  testAccCheckRoleDatabaseConfig("dbpd_role", "postgres", []string{"application_name=a,b"}),
			},
		},
	})
}
