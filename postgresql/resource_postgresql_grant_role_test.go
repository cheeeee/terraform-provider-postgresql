package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestCreateGrantRoleQuery(t *testing.T) {
	var roleName = "foo"
	var grantRoleName = "bar"

	cases := []struct {
		resource map[string]any
		expected string
	}{
		{
			resource: map[string]any{
				"role":       roleName,
				"grant_role": grantRoleName,
			},
			expected: fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier(grantRoleName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: map[string]any{
				"role":              roleName,
				"grant_role":        grantRoleName,
				"with_admin_option": false,
			},
			expected: fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier(grantRoleName), pq.QuoteIdentifier(roleName)),
		},
		{
			resource: map[string]any{
				"role":              roleName,
				"grant_role":        grantRoleName,
				"with_admin_option": true,
			},
			expected: fmt.Sprintf("GRANT %s TO %s WITH ADMIN OPTION", pq.QuoteIdentifier(grantRoleName), pq.QuoteIdentifier(roleName)),
		},
	}

	for _, c := range cases {
		out := createGrantRoleQuery(schema.TestResourceDataRaw(t, resourcePostgreSQLGrantRole().Schema, c.resource))
		if out != c.expected {
			t.Fatalf("error matching output and expected: %#v vs %#v", out, c.expected)
		}
	}
}

func TestRevokeRoleQuery(t *testing.T) {
	var roleName = "foo"
	var grantRoleName = "bar"

	expected := fmt.Sprintf("REVOKE %s FROM %s", pq.QuoteIdentifier(grantRoleName), pq.QuoteIdentifier(roleName))

	cases := []struct {
		resource map[string]any
	}{
		{
			resource: map[string]any{
				"role":       roleName,
				"grant_role": grantRoleName,
			},
		},
		{
			resource: map[string]any{
				"role":              roleName,
				"grant_role":        grantRoleName,
				"with_admin_option": false,
			},
		},
		{
			resource: map[string]any{
				"role":              roleName,
				"grant_role":        grantRoleName,
				"with_admin_option": true,
			},
		},
	}

	for _, c := range cases {
		out := createRevokeRoleQuery(schema.TestResourceDataRaw(t, resourcePostgreSQLGrantRole().Schema, c.resource))
		if out != expected {
			t.Fatalf("error matching output and expected: %#v vs %#v", out, expected)
		}
	}
}

func TestAccPostgresqlGrantRole(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	dbSuffix, teardown := setupTestDatabase(t, false, true)
	defer teardown()

	_, roleName := getTestDBNames(dbSuffix)

	grantedRoleName := "foo"

	testAccPostgresqlGrantRoleResources := fmt.Sprintf(`
	resource postgresql_role "grant" {
		name = "%s"
	}
	resource postgresql_grant_role "grant_role" {
		role              = "%s"
		grant_role        = postgresql_role.grant.name
		with_admin_option = true
	}
	`, grantedRoleName, roleName)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlGrantRoleResources,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(
						"postgresql_grant_role.grant_role", "role", roleName),
					resource.TestCheckResourceAttr(
						"postgresql_grant_role.grant_role", "grant_role", grantedRoleName),
					resource.TestCheckResourceAttr(
						"postgresql_grant_role.grant_role", "with_admin_option", strconv.FormatBool(true)),
					checkGrantRole(t, dsn, roleName, grantedRoleName, true),
				),
			},
		},
	})
}

func checkGrantRole(t *testing.T, dsn, role string, grantRole string, withAdmin bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatalf("could to create connection pool: %v", err)
		}
		defer closeDB(t, db)

		var _rez int
		err = db.QueryRow(`
		SELECT 1
		FROM pg_auth_members
		WHERE pg_get_userbyid(member) = $1
		AND pg_get_userbyid(roleid) = $2
		AND admin_option = $3;
		`, role, grantRole, withAdmin).Scan(&_rez)

		switch {
		case err == sql.ErrNoRows:
			return fmt.Errorf(
				"Role %s is not a member of %s",
				role, grantRole,
			)

		case err != nil:
			t.Fatalf("could not check granted role: %v", err)
		}

		return nil
	}
}

func TestGrantRoleOptionsInPlace(t *testing.T) {
	s := resourcePostgreSQLGrantRole().Schema
	for _, attr := range []string{"with_admin_option", "with_inherit_option", "with_set_option"} {
		if s[attr].ForceNew {
			t.Errorf("%s has to be changed in place", attr)
		}
	}
	for _, attr := range []string{"role", "grant_role", "grantor"} {
		if !s[attr].ForceNew {
			t.Errorf("%s cannot be changed in place", attr)
		}
	}
}

// membershipOptions are the options of a membership as they are in pg_auth_members.
type membershipOptions struct {
	admin, inherit, set bool
	grantor             string
}

func readMembership(client *Client, member, role string) (*membershipOptions, error) {
	db, err := client.Connect()
	if err != nil {
		return nil, err
	}
	inherit, set := "true", "true"
	if db.featureSupported(featureRoleMembershipOptions) {
		inherit, set = "inherit_option", "set_option"
	}

	var options membershipOptions
	err = db.QueryRow(fmt.Sprintf(`SELECT admin_option, %s, %s, pg_get_userbyid(grantor)
		FROM pg_auth_members WHERE pg_get_userbyid(member) = $1 AND pg_get_userbyid(roleid) = $2`, inherit, set),
		member, role).Scan(&options.admin, &options.inherit, &options.set, &options.grantor)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("could not read the membership of %s in %s: %w", member, role, err)
	}
	return &options, nil
}

// testAccCheckMembership checks a membership in the catalog. The inherit and set options
// are not checked if the server does not store them.
func testAccCheckMembership(member, role string, expected membershipOptions) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		actual, err := readMembership(client, member, role)
		if err != nil {
			return err
		}
		if actual == nil {
			return fmt.Errorf("%s is not a member of %s", member, role)
		}
		db, err := client.Connect()
		if err != nil {
			return err
		}
		if !db.featureSupported(featureRoleMembershipOptions) {
			expected.inherit, expected.set = true, true
		}
		if expected.grantor == "" {
			expected.grantor = actual.grantor
		}
		if *actual != expected {
			return fmt.Errorf("membership of %s in %s: expected %+v, got %+v", member, role, expected, *actual)
		}
		return nil
	}
}

func testAccCheckNoMembership(member, role string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		actual, err := readMembership(testAccProvider.Meta().(*Client), member, role)
		if err != nil {
			return err
		}
		if actual != nil {
			return fmt.Errorf("%s is still a member of %s", member, role)
		}
		return nil
	}
}

// testAccCheckMembershipOID remembers the identifier of a membership (PostgreSQL 16+) the first time
// and checks that it is the same the next times: the membership was changed in place.
func testAccCheckMembershipOID(member, role string, oid *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}
		if !db.featureSupported(featureRoleMembershipOptions) {
			return nil
		}
		var current string
		err = db.QueryRow(`SELECT oid::text FROM pg_auth_members WHERE pg_get_userbyid(member) = $1 AND pg_get_userbyid(roleid) = $2`,
			member, role).Scan(&current)
		if err != nil {
			return err
		}
		if *oid == "" {
			*oid = current
		} else if *oid != current {
			return fmt.Errorf("the membership of %s in %s has been recreated (oid %s, then %s)", member, role, *oid, current)
		}
		return nil
	}
}

// testAccCheckRoleSetRole checks if a session of the role can SET ROLE to another one.
func testAccCheckRoleSetRole(t *testing.T, role, password, target string, allowed bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := getTestConfig(t)
		config.Username = role
		config.Password = password
		db, err := sql.Open("postgres", config.connStr("postgres"))
		if err != nil {
			return fmt.Errorf("could not open SQL connection: %v", err)
		}
		defer closeDB(t, db)

		conn, err := db.Conn(context.Background())
		if err != nil {
			return fmt.Errorf("could not connect as role %s: %v", role, err)
		}
		defer func() { _ = conn.Close() }()

		_, err = conn.ExecContext(context.Background(), "SET ROLE "+pq.QuoteIdentifier(target))
		if allowed && err != nil {
			return fmt.Errorf("%s cannot SET ROLE %s: %v", role, target, err)
		}
		if !allowed && err == nil {
			return fmt.Errorf("%s can SET ROLE %s", role, target)
		}
		return nil
	}
}

// testAccCheckRoleInherits checks if a session of the role has the privileges of another one.
func testAccCheckRoleInherits(role, target string, expected bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		db, err := testAccProvider.Meta().(*Client).Connect()
		if err != nil {
			return err
		}
		var inherits bool
		if err := db.QueryRow("SELECT pg_has_role($1, $2, 'USAGE')", role, target).Scan(&inherits); err != nil {
			return err
		}
		if inherits != expected {
			return fmt.Errorf("%s inherits the privileges of %s: expected %t, got %t", role, target, expected, inherits)
		}
		return nil
	}
}

func TestAccPostgresqlGrantRole_AdminOption(t *testing.T) {
	skipIfNotAcc(t)
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	grantConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "gra_granted" {
  name = "gra_granted"
}

resource "postgresql_role" "gra_member" {
  name = "gra_member"
  # the memberships are managed by postgresql_grant_role
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "gra" {
  role       = postgresql_role.gra_member.name
  grant_role = postgresql_role.gra_granted.name
  %s
}
`, options)
	}
	noOption := testAccCheckMembership("gra_member", "gra_granted", membershipOptions{inherit: true, set: true})
	withAdmin := testAccCheckMembership("gra_member", "gra_granted", membershipOptions{admin: true, inherit: true, set: true})

	var oid string
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			return testAccCheckNoMembership("gra_member", "gra_granted")(s)
		},
		Steps: []resource.TestStep{
			{
				Config: grantConfig(``),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant_role.gra", "with_admin_option", "false"),
					noOption,
					testAccCheckMembershipOID("gra_member", "gra_granted", &oid),
				),
			},
			// enabled, in place
			{
				Config: grantConfig(`with_admin_option = true`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant_role.gra", "with_admin_option", "true"),
					withAdmin,
					testAccCheckMembershipOID("gra_member", "gra_granted", &oid),
				),
			},
			// disabled, in place
			{
				Config: grantConfig(`with_admin_option = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant_role.gra", "with_admin_option", "false"),
					noOption,
					testAccCheckMembershipOID("gra_member", "gra_granted", &oid),
				),
			},
			{
				Config: grantConfig(`with_admin_option = true`),
				Check:  withAdmin,
			},
			// the option is changed outside of Terraform
			{
				PreConfig: func() {
					dbExecute(t, dsn, "REVOKE ADMIN OPTION FOR gra_granted FROM gra_member")
				},
				Config:             grantConfig(`with_admin_option = true`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: grantConfig(`with_admin_option = true`),
				Check:  withAdmin,
			},
			// the membership is revoked outside of Terraform
			{
				PreConfig: func() {
					dbExecute(t, dsn, "REVOKE gra_granted FROM gra_member")
				},
				Config:             grantConfig(`with_admin_option = true`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: grantConfig(`with_admin_option = true`),
				Check:  withAdmin,
			},
		},
	})
}

// Test the options INHERIT and SET of a membership, which exist since PostgreSQL 16.
func TestAccPostgresqlGrantRole_MembershipOptions(t *testing.T) {
	skipIfNotAcc(t)
	config := getTestConfig(t)
	dsn := config.connStr("postgres")

	grantConfig := func(memberInherit bool, options string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "grm_granted" {
  name = "grm_granted"
}

resource "postgresql_role" "grm_member" {
  name     = "grm_member"
  login    = true
  password = "grm_pass"
  inherit  = %t
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "grm" {
  role       = postgresql_role.grm_member.name
  grant_role = postgresql_role.grm_granted.name
  %s
}
`, memberInherit, options)
	}
	check := func(admin, inherit, set bool) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("postgresql_grant_role.grm", "with_admin_option", strconv.FormatBool(admin)),
			resource.TestCheckResourceAttr("postgresql_grant_role.grm", "with_inherit_option", strconv.FormatBool(inherit)),
			resource.TestCheckResourceAttr("postgresql_grant_role.grm", "with_set_option", strconv.FormatBool(set)),
			testAccCheckMembership("grm_member", "grm_granted", membershipOptions{admin: admin, inherit: inherit, set: set}),
			// the effect of the options on a real session
			testAccCheckRoleInherits("grm_member", "grm_granted", inherit),
			testAccCheckRoleSetRole(t, "grm_member", "grm_pass", "grm_granted", set),
		)
	}

	var oid string
	sameMembership := testAccCheckMembershipOID("grm_member", "grm_granted", &oid)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureRoleMembershipOptions)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			return testAccCheckNoMembership("grm_member", "grm_granted")(s)
		},
		Steps: []resource.TestStep{
			// the server chooses: INHERIT follows the attribute of the member, SET is true
			{
				Config: grantConfig(true, ``),
				Check:  resource.ComposeTestCheckFunc(check(false, true, true), sameMembership),
			},
			// disabled, in place
			{
				Config: grantConfig(true, `with_inherit_option = false`),
				Check:  resource.ComposeTestCheckFunc(check(false, false, true), sameMembership),
			},
			{
				Config: grantConfig(true, `with_inherit_option = false
  with_set_option = false`),
				Check: resource.ComposeTestCheckFunc(check(false, false, false), sameMembership),
			},
			// enabled, in place
			{
				Config: grantConfig(true, `with_inherit_option = true
  with_set_option = false`),
				Check: resource.ComposeTestCheckFunc(check(false, true, false), sameMembership),
			},
			{
				Config: grantConfig(true, `with_inherit_option = true
  with_set_option = true
  with_admin_option = true`),
				Check: resource.ComposeTestCheckFunc(check(true, true, true), sameMembership),
			},
			// all the options together at once, in both directions
			{
				Config: grantConfig(true, `with_inherit_option = false
  with_set_option = false
  with_admin_option = false`),
				Check: resource.ComposeTestCheckFunc(check(false, false, false), sameMembership),
			},
			// removed from the configuration, the options are kept
			{
				Config: grantConfig(true, ``),
				Check:  resource.ComposeTestCheckFunc(check(false, false, false), sameMembership),
			},
			// changed outside of Terraform
			{
				PreConfig: func() {
					dbExecute(t, dsn, "GRANT grm_granted TO grm_member WITH INHERIT TRUE, SET TRUE")
				},
				Config:             grantConfig(true, `with_inherit_option = false`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: grantConfig(true, `with_inherit_option = false
  with_set_option = true`),
				Check: resource.ComposeTestCheckFunc(check(false, false, true), sameMembership),
			},
			// the default INHERIT is the one of the member, when it is created
			{
				Config: grantConfig(false, `with_set_option = true`),
				Check:  check(false, false, true),
			},
			{
				PreConfig: func() {
					dbExecute(t, dsn, "REVOKE grm_granted FROM grm_member")
				},
				Config:             grantConfig(false, `with_set_option = true`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: grantConfig(false, `with_set_option = true`),
				Check:  check(false, false, true),
			},
		},
	})
}

// The options which PostgreSQL does not have before the version 16 are refused.
func TestAccPostgresqlGrantRole_MembershipOptionsRefused(t *testing.T) {
	skipIfNotAcc(t)

	grantConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "grr_granted" {
  name = "grr_granted"
}

resource "postgresql_role" "grr_member" {
  name = "grr_member"
  # the memberships are managed by postgresql_grant_role
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "grr" {
  role       = postgresql_role.grr_member.name
  grant_role = postgresql_role.grr_granted.name
  %s
}
`, options)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
			client := testAccProvider.Meta().(*Client)
			db, err := client.Connect()
			if err != nil {
				t.Fatalf("could not connect to database: %v", err)
			}
			if db.featureSupported(featureRoleMembershipOptions) {
				t.Skipf("Skip test: the options of the memberships exist in Postgres %s", db.version)
			}
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      grantConfig(`with_inherit_option = false`),
				ExpectError: regexp.MustCompile(`with_inherit_option requires PostgreSQL 16 or later`),
			},
			{
				Config:      grantConfig(`with_set_option = true`),
				ExpectError: regexp.MustCompile(`with_set_option requires PostgreSQL 16 or later`),
			},
			// without the options, the membership is the same as before
			{
				Config: grantConfig(`with_admin_option = true`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckMembership("grr_member", "grr_granted", membershipOptions{admin: true}),
					resource.TestCheckResourceAttr("postgresql_grant_role.grr", "with_inherit_option", "true"),
					resource.TestCheckResourceAttr("postgresql_grant_role.grr", "with_set_option", "true"),
				),
			},
		},
	})
}

// Test GRANTED BY: the grantor is stored in the membership.
func TestAccPostgresqlGrantRole_Grantor(t *testing.T) {
	skipIfNotAcc(t)

	grantConfig := func(grantor string) string {
		if grantor != "" {
			grantor = fmt.Sprintf(`grantor    = %s`, grantor)
		}
		return fmt.Sprintf(`
resource "postgresql_role" "grg_granted" {
  name = "grg_granted"
}

resource "postgresql_role" "grg_member" {
  name = "grg_member"
  # the memberships are managed by postgresql_grant_role
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_role" "grg_grantor" {
  name = "grg_grantor"
  # the memberships are managed by postgresql_grant_role
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_role" "grg_grantor2" {
  name = "grg_grantor2"
  # the memberships are managed by postgresql_grant_role
  lifecycle {
    ignore_changes = [roles]
  }
}

# the grantors have the ADMIN option
resource "postgresql_grant_role" "grg_admin" {
  role              = postgresql_role.grg_grantor.name
  grant_role        = postgresql_role.grg_granted.name
  with_admin_option = true
}

resource "postgresql_grant_role" "grg_admin2" {
  role              = postgresql_role.grg_grantor2.name
  grant_role        = postgresql_role.grg_granted.name
  with_admin_option = true
}

resource "postgresql_grant_role" "grg" {
  role       = postgresql_role.grg_member.name
  grant_role = postgresql_role.grg_granted.name
  %s
  depends_on = [postgresql_grant_role.grg_admin, postgresql_grant_role.grg_admin2]
}
`, grantor)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
			// only a superuser can grant on behalf of another role
			testCheckCanExecute(t, "grant a role on behalf of another one",
				"CREATE ROLE tf_probe_grantor",
				"CREATE ROLE tf_probe_member",
				"CREATE ROLE tf_probe_granted",
				"GRANT tf_probe_granted TO tf_probe_grantor WITH ADMIN OPTION",
				"GRANT tf_probe_granted TO tf_probe_member GRANTED BY tf_probe_grantor",
			)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			return testAccCheckNoMembership("grg_member", "grg_granted")(s)
		},
		Steps: []resource.TestStep{
			{
				Config: grantConfig(`postgresql_role.grg_grantor.name`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant_role.grg", "grantor", "grg_grantor"),
					testAccCheckMembership("grg_member", "grg_granted", membershipOptions{inherit: true, set: true, grantor: "grg_grantor"}),
				),
			},
			// another grantor: the membership is granted again
			{
				Config: grantConfig(`postgresql_role.grg_grantor2.name`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_grant_role.grg", "grantor", "grg_grantor2"),
					testAccCheckMembership("grg_member", "grg_granted", membershipOptions{inherit: true, set: true, grantor: "grg_grantor2"}),
				),
			},
			// the options are changed in place, for the same grantor
			{
				Config: grantConfig(`postgresql_role.grg_grantor2.name
  with_admin_option = true`),
				Check: testAccCheckMembership("grg_member", "grg_granted", membershipOptions{admin: true, inherit: true, set: true, grantor: "grg_grantor2"}),
			},
			{
				SkipFunc: func() (bool, error) { return !grantRoleOptionsSupported(), nil },
				Config: grantConfig(`postgresql_role.grg_grantor2.name
  with_admin_option = true
  with_set_option = false
  with_inherit_option = false`),
				Check: testAccCheckMembership("grg_member", "grg_granted", membershipOptions{admin: true, grantor: "grg_grantor2"}),
			},
			// without grantor in the configuration, the grantor of the membership is kept
			{
				Config: grantConfig(``),
				Check:  testAccCheckMembership("grg_member", "grg_granted", membershipOptions{grantor: "grg_grantor2"}),
			},
		},
	})
}

func grantRoleOptionsSupported() bool {
	db, err := testAccProvider.Meta().(*Client).Connect()
	return err == nil && db.featureSupported(featureRoleMembershipOptions)
}
