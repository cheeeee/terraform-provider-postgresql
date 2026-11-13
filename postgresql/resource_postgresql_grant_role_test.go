package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/go-cty/cty"
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
		out := createGrantRoleQuery(schema.TestResourceDataRaw(t, resourcePostgreSQLGrantRole().Schema, c.resource), "")
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
		out := createRevokeRoleQuery(schema.TestResourceDataRaw(t, resourcePostgreSQLGrantRole().Schema, c.resource), "")
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

func TestGrantRoleImportCandidates(t *testing.T) {
	tests := []struct {
		id   string
		want [][2]string
	}{
		{"a_b_c_true", [][2]string{{"a", "b_c_true"}, {"a_b", "c_true"}, {"a_b_c", "true"}, {"a", "b_c"}, {"a_b", "c"}}},
		{"a_b", [][2]string{{"a", "b"}}},
		{"a_b_false", [][2]string{{"a", "b_false"}, {"a_b", "false"}, {"a", "b"}}},
		// an underscore at the start or at the end of the ID separates no name from another
		{"_a_b", [][2]string{{"_a", "b"}}},
		{"a_b_", [][2]string{{"a", "b_"}}},
		{"_ab", nil},
		{"ab_", nil},
		{"_", nil},
		{"__", nil},
		{"ab", nil},
		{"", nil},
		// once the suffix is removed, the ID ends with an underscore
		{"a__true", [][2]string{{"a", "_true"}, {"a_", "true"}}},
	}
	for _, test := range tests {
		got := grantRoleImportCandidates(test.id)
		if fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("%q: got %v, want %v", test.id, got, test.want)
		}
	}
}

// The grantor which is only read from the catalog is named in the statements when the connected
// user is allowed to name it. On AWS RDS the memberships granted by the master user are recorded
// as granted by rdsadmin: GRANTED BY rdsadmin is refused, the statement without it is accepted.
func TestAccPostgresqlGrantRole_GrantorClause(t *testing.T) {
	skipIfNotAcc(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(t, db)
	var currentUser string
	var superuser bool
	if err := db.QueryRow("SELECT current_user, rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&currentUser, &superuser); err != nil {
		t.Fatal(err)
	}
	// a role the connected user is not a member of: only a superuser has its privileges
	dbExecute(t, dsn, "DROP ROLE IF EXISTS grc_other")
	dbExecute(t, dsn, "CREATE ROLE grc_other")
	defer dbExecute(t, dsn, "DROP ROLE IF EXISTS grc_other")
	if !superuser {
		dbExecute(t, dsn, "REVOKE grc_other FROM "+pq.QuoteIdentifier(currentUser))
	}
	otherClause := ""
	if superuser {
		otherClause = ` GRANTED BY "grc_other"`
	}

	state := func(grantor string) *terraform.InstanceState {
		return &terraform.InstanceState{ID: "a_b", Attributes: map[string]string{
			"role": "a", "grant_role": "b", "with_admin_option": "false", "grantor": grantor,
		}}
	}
	for _, test := range []struct {
		name     string
		state    *terraform.InstanceState
		raw      cty.Value
		expected string
	}{
		{"no grantor", nil, grantRoleRawConfig(nil), ""},
		{"the connected user, read from the catalog", state(currentUser), grantRoleRawConfig(nil), ` GRANTED BY "` + currentUser + `"`},
		{"another role, read from the catalog", state("grc_other"), grantRoleRawConfig(nil), otherClause},
		{"another role, read from the catalog, on destroy", state("grc_other"), cty.NullVal(cty.DynamicPseudoType), otherClause},
		{"a role which does not exist anymore", state("grc_dropped"), grantRoleRawConfig(nil), ""},
		// the grantor of the configuration is named in every case: PostgreSQL tells if it is refused
		{
			"another role, in the configuration", state("grc_other"),
			grantRoleRawConfig(map[string]cty.Value{"grantor": cty.StringVal("grc_other")}), ` GRANTED BY "grc_other"`,
		},
	} {
		resourceConfig := map[string]any{"role": "a", "grant_role": "b"}
		if !test.raw.IsNull() && test.raw.Type().HasAttribute("grantor") {
			resourceConfig["grantor"] = test.raw.GetAttr("grantor").AsString()
		}
		d := grantRoleResourceData(t, test.state, resourceConfig, test.raw)
		got, err := grantRoleGrantorClause(db, d)
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
		}
		if got != test.expected {
			t.Errorf("%s: got %q, want %q", test.name, got, test.expected)
		}
	}
}

// grantRoleResourceData builds the data a resource function receives in a real run: the
// state, the diff of the plan and the raw configuration (null for an attribute which is not
// written in the configuration).
func grantRoleResourceData(t *testing.T, state *terraform.InstanceState, config map[string]any, rawConfig cty.Value) *schema.ResourceData {
	t.Helper()
	s := schema.InternalMap(resourcePostgreSQLGrantRole().Schema)
	diff, err := s.Diff(context.Background(), state, terraform.NewResourceConfigRaw(config), nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		diff = &terraform.InstanceDiff{}
	}
	diff.RawConfig = rawConfig
	d, err := s.Data(state, diff)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func grantRoleRawConfig(attrs map[string]cty.Value) cty.Value {
	raw := map[string]cty.Value{"role": cty.StringVal("a"), "grant_role": cty.StringVal("b")}
	for attr, value := range attrs {
		raw[attr] = value
	}
	return cty.ObjectVal(raw)
}

func TestRawConfigBool(t *testing.T) {
	object := grantRoleRawConfig(map[string]cty.Value{"with_inherit_option": cty.NullVal(cty.Bool)})
	tests := []struct {
		name         string
		raw          cty.Value
		expected, ok bool
	}{
		{"attribute true", grantRoleRawConfig(map[string]cty.Value{"with_set_option": cty.True}), true, true},
		{"attribute false", grantRoleRawConfig(map[string]cty.Value{"with_set_option": cty.False}), false, true},
		{"attribute null", grantRoleRawConfig(map[string]cty.Value{"with_set_option": cty.NullVal(cty.Bool)}), false, false},
		{"attribute unknown", grantRoleRawConfig(map[string]cty.Value{"with_set_option": cty.UnknownVal(cty.Bool)}), false, false},
		{"attribute missing from the object", cty.ObjectVal(map[string]cty.Value{"role": cty.StringVal("a")}), false, false},
		{"unknown configuration", cty.UnknownVal(object.Type()), false, false},
		{"configuration which is not an object", cty.MapVal(map[string]cty.Value{"with_set_option": cty.True}), false, false},
	}
	for _, test := range tests {
		d := grantRoleResourceData(t, nil, map[string]any{"role": "a", "grant_role": "b"}, test.raw)
		if value, ok := rawConfigBool(d, "with_set_option"); value != test.expected || ok != test.ok {
			t.Errorf("%s: got (%t, %t), want (%t, %t)", test.name, value, ok, test.expected, test.ok)
		}
	}

	// no configuration at all
	d := schema.TestResourceDataRaw(t, resourcePostgreSQLGrantRole().Schema, map[string]any{"role": "a", "grant_role": "b"})
	if value, ok := rawConfigBool(d, "with_set_option"); value || ok {
		t.Errorf("null configuration: got (%t, %t), want (false, false)", value, ok)
	}
}

// The options of the configuration are written once in the GRANT of a new membership, and
// the options of the arguments only when the options are changed in place.
func TestCreateGrantRoleQueryOptions(t *testing.T) {
	grant := func(role string) string {
		return fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier("b"), pq.QuoteIdentifier("a")) + role
	}
	admin, inherit, set := grantRoleOptions[0], grantRoleOptions[1], grantRoleOptions[2]

	tests := []struct {
		name     string
		config   map[string]any
		raw      map[string]cty.Value
		only     []grantRoleOption
		expected string
	}{
		{
			name:     "options of the configuration",
			config:   map[string]any{"with_admin_option": true, "with_inherit_option": false, "with_set_option": true},
			raw:      map[string]cty.Value{"with_admin_option": cty.True, "with_inherit_option": cty.False, "with_set_option": cty.True},
			expected: grant(" WITH ADMIN OPTION, INHERIT FALSE, SET TRUE"),
		},
		{
			name:     "admin option written false is not an option of the statement",
			config:   map[string]any{"with_admin_option": false, "with_set_option": true},
			raw:      map[string]cty.Value{"with_admin_option": cty.False, "with_set_option": cty.True},
			expected: grant(" WITH SET TRUE"),
		},
		{
			name:     "option which is not in the configuration",
			config:   map[string]any{"with_inherit_option": true},
			raw:      map[string]cty.Value{"with_inherit_option": cty.True, "with_set_option": cty.NullVal(cty.Bool)},
			expected: grant(" WITH INHERIT TRUE"),
		},
		{
			name:     "one option only",
			config:   map[string]any{"with_admin_option": true, "with_inherit_option": true},
			raw:      map[string]cty.Value{"with_admin_option": cty.True, "with_inherit_option": cty.True},
			only:     []grantRoleOption{inherit},
			expected: grant(" WITH INHERIT TRUE"),
		},
		{
			name:     "one option only, set to false",
			config:   map[string]any{"with_set_option": false},
			raw:      map[string]cty.Value{"with_set_option": cty.False},
			only:     []grantRoleOption{set},
			expected: grant(" WITH SET FALSE"),
		},
		{
			name:     "the admin option only",
			config:   map[string]any{"with_admin_option": true},
			raw:      map[string]cty.Value{"with_admin_option": cty.True},
			only:     []grantRoleOption{admin},
			expected: grant(" WITH ADMIN OPTION"),
		},
		{
			name:     "two options only",
			config:   map[string]any{"with_admin_option": true, "with_inherit_option": false, "with_set_option": true},
			raw:      map[string]cty.Value{"with_admin_option": cty.True, "with_inherit_option": cty.False, "with_set_option": cty.True},
			only:     []grantRoleOption{inherit, set},
			expected: grant(" WITH INHERIT FALSE, SET TRUE"),
		},
	}
	for _, test := range tests {
		config := map[string]any{"role": "a", "grant_role": "b"}
		for k, v := range test.config {
			config[k] = v
		}
		d := grantRoleResourceData(t, nil, config, grantRoleRawConfig(test.raw))
		if got := createGrantRoleQuery(d, grantedByClause(d.Get("grantor").(string)), test.only...); got != test.expected {
			t.Errorf("%s: got %q, want %q", test.name, got, test.expected)
		}
	}
}

// The ID does not change when the option is changed in place, and the membership is imported.
func TestAccPostgresqlGrantRole_ID(t *testing.T) {
	skipIfNotAcc(t)

	grantConfig := func(options string) string {
		return fmt.Sprintf(`
resource "postgresql_role" "gri_granted" {
  name = "gri_granted_role"
}

resource "postgresql_role" "gri_member" {
  name = "gri_member"
  lifecycle {
    ignore_changes = [roles]
  }
}

resource "postgresql_grant_role" "gri" {
  role       = postgresql_role.gri_member.name
  grant_role = postgresql_role.gri_granted.name
  %s
}
`, options)
	}
	const id = "gri_member_gri_granted_role"
	checkID := resource.TestCheckResourceAttr("postgresql_grant_role.gri", "id", id)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featurePrivileges)
		},
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			return testAccCheckNoMembership("gri_member", "gri_granted_role")(s)
		},
		Steps: []resource.TestStep{
			{Config: grantConfig(``), Check: checkID},
			{Config: grantConfig(`with_admin_option = true`), Check: resource.ComposeTestCheckFunc(checkID,
				testAccCheckMembership("gri_member", "gri_granted_role", membershipOptions{admin: true, inherit: true, set: true}))},
			// nothing to do after the toggle
			{Config: grantConfig(`with_admin_option = true`), PlanOnly: true},
			{Config: grantConfig(`with_admin_option = true`), ResourceName: "postgresql_grant_role.gri", ImportState: true, ImportStateVerify: true},
			// the ID of the former versions, which ended with with_admin_option
			{
				Config:            grantConfig(`with_admin_option = true`),
				ResourceName:      "postgresql_grant_role.gri",
				ImportState:       true,
				ImportStateId:     id + "_true",
				ImportStateVerify: true,
			},
			{Config: grantConfig(`with_admin_option = false`), Check: checkID},
			{Config: grantConfig(`with_admin_option = false`), PlanOnly: true},
			{Config: grantConfig(`with_admin_option = false`), ResourceName: "postgresql_grant_role.gri", ImportState: true, ImportStateVerify: true},
		},
	})
}

// testGrantRoleUpdate runs the Update of a membership the way Terraform does for a plan
// which changes the attributes of the configuration, from a state which is given as is
// (it can differ from the catalog, or lack the attributes of a former version).
func testGrantRoleUpdate(t *testing.T, state map[string]string, config map[string]any, raw map[string]cty.Value) *schema.ResourceData {
	t.Helper()
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatal(err)
	}
	instance := &terraform.InstanceState{ID: state["role"] + "_" + state["grant_role"], Attributes: state}
	d := grantRoleResourceData(t, instance, config, grantRoleRawConfig(raw))
	if err := resourcePostgreSQLGrantRoleUpdate(db, d); err != nil {
		t.Fatal(err)
	}
	return d
}

// The Update changes the options which are changed by the plan, and only these ones.
func TestAccPostgresqlGrantRole_UpdateChangedOptionsOnly(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)
	testCheckCompatibleVersion(t, featurePrivileges)
	db, err := testAccProvider.Meta().(*Client).Connect()
	if err != nil {
		t.Fatal(err)
	}
	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	const member, granted = "gup_member", "gup_granted"

	setup := func(t *testing.T) string {
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+member)
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+granted)
		dbExecute(t, dsn, "CREATE ROLE "+granted)
		dbExecute(t, dsn, "CREATE ROLE "+member)
		dbExecute(t, dsn, fmt.Sprintf("GRANT %s TO %s WITH ADMIN OPTION", granted, member))
		t.Cleanup(func() {
			dbExecute(t, dsn, "DROP ROLE IF EXISTS "+member)
			dbExecute(t, dsn, "DROP ROLE IF EXISTS "+granted)
		})
		m, err := readMembership(testAccProvider.Meta().(*Client), member, granted)
		if err != nil || m == nil {
			t.Fatalf("membership not found: %v", err)
		}
		return m.grantor
	}
	baseState := func(grantor string, admin bool) map[string]string {
		return map[string]string{
			"role": member, "grant_role": granted, "grantor": grantor, "with_admin_option": strconv.FormatBool(admin),
		}
	}
	baseConfig := func(admin bool) map[string]any {
		return map[string]any{"role": member, "grant_role": granted, "with_admin_option": admin}
	}
	expect := func(t *testing.T, want membershipOptions) {
		t.Helper()
		got, err := readMembership(testAccProvider.Meta().(*Client), member, granted)
		if err != nil || got == nil {
			t.Fatalf("membership not found: %v", err)
		}
		if !db.featureSupported(featureRoleMembershipOptions) {
			want.inherit, want.set = true, true
		}
		want.grantor = got.grantor
		if *got != want {
			t.Errorf("membership: expected %+v, got %+v", want, *got)
		}
	}

	// An option which is not in the configuration is never part of the plan (a computed
	// argument keeps its value, a state from a provider which did not know the option has
	// none): the Update does not write it.
	t.Run("options which are not in the configuration", func(t *testing.T) {
		grantor := setup(t)
		raw := map[string]cty.Value{
			"with_admin_option": cty.False, "with_inherit_option": cty.NullVal(cty.Bool), "with_set_option": cty.NullVal(cty.Bool),
		}
		for name, state := range map[string]map[string]string{
			"state of a former version": baseState(grantor, true),
			"state with the options":    {"with_inherit_option": "true", "with_set_option": "true"},
		} {
			for k, v := range baseState(grantor, true) {
				if _, ok := state[k]; !ok {
					state[k] = v
				}
			}
			d := testGrantRoleUpdate(t, state, baseConfig(false), raw)
			if d.HasChange("with_inherit_option") || d.HasChange("with_set_option") {
				t.Errorf("%s: the plan changes an option which is not in the configuration", name)
			}
			expect(t, membershipOptions{admin: false, inherit: true, set: true})
			dbExecute(t, dsn, fmt.Sprintf("GRANT %s TO %s WITH ADMIN OPTION", granted, member))
		}
	})

	// The state says that the admin option is off while it is on in the catalog (a plan without refresh):
	// only what the plan changes is written.
	t.Run("options which are not changed", func(t *testing.T) {
		testCheckCompatibleVersion(t, featureRoleMembershipOptions)
		grantor := setup(t)
		state := baseState(grantor, false)
		state["with_inherit_option"] = "true"
		state["with_set_option"] = "true"
		config := baseConfig(false)
		config["with_inherit_option"] = false
		config["with_set_option"] = true
		testGrantRoleUpdate(t, state, config, map[string]cty.Value{
			"with_admin_option": cty.False, "with_inherit_option": cty.False, "with_set_option": cty.True,
		})
		expect(t, membershipOptions{admin: true, inherit: false, set: true})
	})
}

// The import function splits the ID in the names of the role and of the granted role.
func TestAccPostgresqlGrantRole_ImportFunction(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)
	testCheckCompatibleVersion(t, featurePrivileges)
	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	roles := []string{"gim_m", "gim_m_x", "x_g", "g", "gim_ab", "gim_c"}
	for _, role := range roles {
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
		dbExecute(t, dsn, "CREATE ROLE "+role)
	}
	t.Cleanup(func() {
		for _, role := range roles {
			dbExecute(t, dsn, "DROP ROLE IF EXISTS "+role)
		}
	})
	dbExecute(t, dsn, "GRANT gim_c TO gim_ab")
	// "gim_m_x_g" is gim_m member of x_g, or gim_m_x member of g
	dbExecute(t, dsn, "GRANT x_g TO gim_m")
	dbExecute(t, dsn, "GRANT g TO gim_m_x")

	tests := []struct {
		id, role, grantRole, expectedError string
	}{
		{id: "gim_ab_gim_c", role: "gim_ab", grantRole: "gim_c"},
		{id: "gim_ab_gim_c_false", role: "gim_ab", grantRole: "gim_c"},
		{id: "gim_c_gim_ab", expectedError: "no membership found"},
		{id: "gim_m_x_g", expectedError: "ambiguous"},
	}
	for _, test := range tests {
		d := resourcePostgreSQLGrantRole().TestResourceData()
		d.SetId(test.id)
		imported, err := resourcePostgreSQLGrantRoleImport(context.Background(), d, testAccProvider.Meta())
		if test.expectedError != "" {
			if err == nil || !regexp.MustCompile(test.expectedError).MatchString(err.Error()) {
				t.Errorf("%s: expected the error %q, got %v", test.id, test.expectedError, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", test.id, err)
			continue
		}
		if len(imported) != 1 || imported[0].Get("role") != test.role || imported[0].Get("grant_role") != test.grantRole {
			t.Errorf("%s: expected the membership of %s in %s, got %v", test.id, test.role, test.grantRole, imported)
		}
	}
}
