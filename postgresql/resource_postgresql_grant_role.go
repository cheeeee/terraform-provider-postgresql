package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/lib/pq"
)

const (
	grantRoleWithAdminOptionAttr   = "with_admin_option"
	grantRoleWithInheritOptionAttr = "with_inherit_option"
	grantRoleWithSetOptionAttr     = "with_set_option"
	grantRoleGrantorAttr           = "grantor"
)

// getGrantRoleQuery returns the role membership for role, grant_role.
// The inherit and set options are stored by PostgreSQL 16+.
// A membership can be granted several times, by different grantors (PostgreSQL 16+,
// before there is only one): the one of the grantor of the resource is read if it is known.
func getGrantRoleQuery(db *DBConnection, withGrantor bool) string {
	inheritOption, setOption := "true", "true"
	if db.featureSupported(featureRoleMembershipOptions) {
		inheritOption, setOption = "inherit_option", "set_option"
	}
	grantorFilter := ""
	if withGrantor {
		grantorFilter = " AND pg_get_userbyid(grantor) = $3"
	}

	return fmt.Sprintf(`
SELECT
  pg_get_userbyid(member) as role,
  pg_get_userbyid(roleid) as grant_role,
  admin_option,
  %s,
  %s,
  pg_get_userbyid(grantor)
FROM
  pg_auth_members
WHERE
  pg_get_userbyid(member) = $1 AND
  pg_get_userbyid(roleid) = $2%s
ORDER BY grantor
LIMIT 1;
`, inheritOption, setOption, grantorFilter)
}

func resourcePostgreSQLGrantRole() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLGrantRoleCreate),
		Read:   PGResourceFunc(resourcePostgreSQLGrantRoleRead),
		Update: PGResourceFunc(resourcePostgreSQLGrantRoleUpdate),
		Delete: PGResourceFunc(resourcePostgreSQLGrantRoleDelete),
		Importer: &schema.ResourceImporter{
			StateContext: resourcePostgreSQLGrantRoleImport,
		},

		Schema: map[string]*schema.Schema{
			"role": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the role to grant grant_role",
			},
			"grant_role": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the role that is granted to role",
			},
			grantRoleWithAdminOptionAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Permit the grant recipient to grant it to others",
			},
			grantRoleWithInheritOptionAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				Description: "Permit the grant recipient to use the privileges of the granted role (PostgreSQL 16+). Defaults to the INHERIT attribute of the role",
			},
			grantRoleWithSetOptionAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				Description: "Permit the grant recipient to use SET ROLE to the granted role (PostgreSQL 16+). Defaults to true",
			},
			grantRoleGrantorAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "The role recorded as the grantor of the membership (GRANTED BY, only a superuser can name another role than the connected one). Defaults to the role which runs the GRANT",
			},
		},
	}
}

func resourcePostgreSQLGrantRoleRead(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePrivileges) {
		return fmt.Errorf(
			"postgresql_grant_role resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	return readGrantRole(db, d)
}

func resourcePostgreSQLGrantRoleCreate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePrivileges) {
		return fmt.Errorf(
			"postgresql_grant_role resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	if err := checkGrantRoleOptions(db, d); err != nil {
		return err
	}

	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	// Revoke the granted roles before granting them again.
	if err = revokeRole(txn, d); err != nil {
		return err
	}

	if err = grantRole(txn, d); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	d.SetId(generateGrantRoleID(d))

	return readGrantRole(db, d)
}

func resourcePostgreSQLGrantRoleUpdate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePrivileges) {
		return fmt.Errorf(
			"postgresql_grant_role resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	if err := checkGrantRoleOptions(db, d); err != nil {
		return err
	}

	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	// The options are changed in place: GRANT ... WITH <option> to enable one,
	// REVOKE <option> OPTION FOR to disable it. An option which is not in the
	// configuration is never changed by a plan (its argument is computed).
	grantedBy, err := grantRoleGrantorClause(txn, d)
	if err != nil {
		return err
	}
	for _, option := range grantRoleOptions {
		if !d.HasChange(option.attr) {
			continue
		}

		var query string
		if d.Get(option.attr).(bool) {
			query = createGrantRoleQuery(d, grantedBy, option)
		} else {
			query = createRevokeRoleOptionQuery(d, grantedBy, option)
		}
		if _, err := txn.Exec(query); err != nil {
			return fmt.Errorf("could not change %s of the grant role: %w", option.sql, err)
		}
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	return readGrantRole(db, d)
}

func resourcePostgreSQLGrantRoleDelete(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePrivileges) {
		return fmt.Errorf(
			"postgresql_grant_role resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if err = revokeRole(txn, d); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	return nil
}

func readGrantRole(db *DBConnection, d *schema.ResourceData) error {
	var roleName, grantRoleName, grantor string
	var withAdminOption, withInheritOption, withSetOption bool

	grantRoleID := d.Id()

	values := []any{
		&roleName,
		&grantRoleName,
		&withAdminOption,
		&withInheritOption,
		&withSetOption,
		&grantor,
	}

	args := []any{d.Get("role"), d.Get("grant_role")}
	if g, _ := d.Get(grantRoleGrantorAttr).(string); g != "" {
		args = append(args, g)
	}

	err := db.QueryRow(getGrantRoleQuery(db, len(args) == 3), args...).Scan(values...)
	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL grant role (%q) not found", grantRoleID)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading grant role: %w", err)
	}

	d.Set("role", roleName)
	d.Set("grant_role", grantRoleName)
	d.Set(grantRoleWithAdminOptionAttr, withAdminOption)
	d.Set(grantRoleWithInheritOptionAttr, withInheritOption)
	d.Set(grantRoleWithSetOptionAttr, withSetOption)
	d.Set(grantRoleGrantorAttr, grantor)

	d.SetId(generateGrantRoleID(d))

	return nil
}

// grantRoleOption is an option of a role membership.
type grantRoleOption struct {
	attr string
	// sql is the name of the option in GRANT ... WITH <sql> and REVOKE <sql> OPTION FOR
	sql string
}

var grantRoleOptions = []grantRoleOption{
	{grantRoleWithAdminOptionAttr, "ADMIN"},
	{grantRoleWithInheritOptionAttr, "INHERIT"},
	{grantRoleWithSetOptionAttr, "SET"},
}

// rawConfigBool returns the value of a boolean attribute as it is written
// in the configuration, and whether it is written at all.
func rawConfigBool(d *schema.ResourceData, attr string) (bool, bool) {
	config := d.GetRawConfig()
	if config.IsNull() || !config.IsKnown() || !config.Type().IsObjectType() || !config.Type().HasAttribute(attr) {
		return false, false
	}
	value := config.GetAttr(attr)
	if value.IsNull() || !value.IsKnown() {
		return false, false
	}
	return value.True(), true
}

// checkGrantRoleOptions refuses the options which the server does not know.
func checkGrantRoleOptions(db *DBConnection, d *schema.ResourceData) error {
	if db.featureSupported(featureRoleMembershipOptions) {
		return nil
	}
	for _, attr := range []string{grantRoleWithInheritOptionAttr, grantRoleWithSetOptionAttr} {
		if _, ok := rawConfigBool(d, attr); ok {
			return fmt.Errorf("%s requires PostgreSQL 16 or later (server version: %s)", attr, db.version)
		}
	}
	return nil
}

// grantedByClause returns the GRANTED BY clause of a grantor, nothing without grantor.
func grantedByClause(grantor string) string {
	if grantor == "" {
		return ""
	}
	return " GRANTED BY " + pq.QuoteIdentifier(grantor)
}

// grantRoleGrantorClause returns the GRANTED BY clause of the statements of the membership.
//
// The grantor of the configuration is always named. The one which was only read from the catalog
// is named when the connected user is allowed to (it has the privileges of this role): PostgreSQL
// can record another role than the one which runs the GRANT (on AWS RDS, the memberships granted
// by the master user are recorded as granted by rdsadmin, which it cannot name). Without the
// clause PostgreSQL chooses the same grantor as it did for the GRANT.
func grantRoleGrantorClause(db QueryAble, d *schema.ResourceData) (string, error) {
	grantor, _ := d.Get(grantRoleGrantorAttr).(string)
	if grantor == "" {
		return "", nil
	}
	if config := d.GetRawConfig(); !config.IsNull() && config.IsKnown() && config.Type().IsObjectType() &&
		config.Type().HasAttribute(grantRoleGrantorAttr) {
		if value := config.GetAttr(grantRoleGrantorAttr); !value.IsNull() && value.IsKnown() {
			return grantedByClause(grantor), nil
		}
	}
	var allowed bool
	if err := db.QueryRow(
		"SELECT COALESCE((SELECT pg_catalog.pg_has_role(oid, 'USAGE') FROM pg_catalog.pg_roles WHERE rolname = $1), false)", grantor,
	).Scan(&allowed); err != nil {
		return "", fmt.Errorf("could not check the grantor %s of the membership: %w", grantor, err)
	}
	if !allowed {
		return "", nil
	}
	return grantedByClause(grantor), nil
}

// createGrantRoleQuery returns the GRANT statement. Without options, it grants
// the membership with the options which are in the configuration; with options,
// only these ones (they are changed in place for an existing membership).
func createGrantRoleQuery(d *schema.ResourceData, grantedBy string, only ...grantRoleOption) string {
	grantRole, _ := d.Get("grant_role").(string)
	role, _ := d.Get("role").(string)

	query := fmt.Sprintf(
		"GRANT %s TO %s",
		pq.QuoteIdentifier(grantRole),
		pq.QuoteIdentifier(role),
	)

	var options []string
	if len(only) > 0 {
		for _, option := range only {
			options = append(options, grantRoleOptionSQL(option, d.Get(option.attr).(bool)))
		}
	} else {
		if wao, _ := d.Get(grantRoleWithAdminOptionAttr).(bool); wao {
			options = append(options, "ADMIN OPTION")
		}
		for _, option := range grantRoleOptions[1:] {
			if value, ok := rawConfigBool(d, option.attr); ok {
				options = append(options, grantRoleOptionSQL(option, value))
			}
		}
	}
	if len(options) > 0 {
		query += " WITH " + strings.Join(options, ", ")
	}

	return query + grantedBy
}

func grantRoleOptionSQL(option grantRoleOption, value bool) string {
	if option.attr == grantRoleWithAdminOptionAttr && value {
		return "ADMIN OPTION"
	}
	return fmt.Sprintf("%s %s", option.sql, strings.ToUpper(strconv.FormatBool(value)))
}

func createRevokeRoleQuery(d *schema.ResourceData, grantedBy string) string {
	grantRole, _ := d.Get("grant_role").(string)
	role, _ := d.Get("role").(string)

	return fmt.Sprintf(
		"REVOKE %s FROM %s%s",
		pq.QuoteIdentifier(grantRole),
		pq.QuoteIdentifier(role),
		grantedBy,
	)
}

func createRevokeRoleOptionQuery(d *schema.ResourceData, grantedBy string, option grantRoleOption) string {
	grantRole, _ := d.Get("grant_role").(string)
	role, _ := d.Get("role").(string)

	return fmt.Sprintf(
		"REVOKE %s OPTION FOR %s FROM %s%s",
		option.sql,
		pq.QuoteIdentifier(grantRole),
		pq.QuoteIdentifier(role),
		grantedBy,
	)
}

func grantRole(txn *sql.Tx, d *schema.ResourceData) error {
	grantedBy, err := grantRoleGrantorClause(txn, d)
	if err != nil {
		return err
	}
	query := createGrantRoleQuery(d, grantedBy)
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not execute grant query: %w", err)
	}
	return nil
}

func revokeRole(txn *sql.Tx, d *schema.ResourceData) error {
	grantedBy, err := grantRoleGrantorClause(txn, d)
	if err != nil {
		return err
	}
	query := createRevokeRoleQuery(d, grantedBy)
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not execute revoke query: %w", err)
	}
	return nil
}

// generateGrantRoleID returns the ID of the membership: <role>_<grant_role>. The options
// are not part of it because they are changed in place, the ID must not change with them.
// Before, the ID ended with _<with_admin_option>: such an ID is rewritten by the next refresh.
func generateGrantRoleID(d *schema.ResourceData) string {
	return grantRoleID(d.Get("role").(string), d.Get("grant_role").(string))
}

func grantRoleID(role, grantRole string) string {
	return role + "_" + grantRole
}

// grantRoleImportCandidates returns the (role, grant_role) pairs which an import ID can
// stand for: the names can contain underscores, so every split is a candidate, with or
// without the _true / _false suffix of the IDs of the former versions.
func grantRoleImportCandidates(id string) [][2]string {
	ids := []string{id}
	for _, suffix := range []string{"_true", "_false"} {
		if strings.HasSuffix(id, suffix) {
			ids = append(ids, strings.TrimSuffix(id, suffix))
		}
	}

	var candidates [][2]string
	for _, s := range ids {
		for i, c := range s {
			if c == '_' && i > 0 && i < len(s)-1 {
				candidates = append(candidates, [2]string{s[:i], s[i+1:]})
			}
		}
	}
	return candidates
}

// resourcePostgreSQLGrantRoleImport imports a membership from its ID <role>_<grant_role>
// (the former <role>_<grant_role>_<with_admin_option> is accepted). The split of the
// names at an underscore is checked against pg_auth_members.
func resourcePostgreSQLGrantRoleImport(_ context.Context, d *schema.ResourceData, meta any) ([]*schema.ResourceData, error) {
	db, err := meta.(*Client).Connect()
	if err != nil {
		return nil, err
	}

	var found [][2]string
	for _, c := range grantRoleImportCandidates(d.Id()) {
		var exists bool
		err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_auth_members WHERE pg_get_userbyid(member) = $1 AND pg_get_userbyid(roleid) = $2)`, c[0], c[1]).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("error reading grant role: %w", err)
		}
		if exists {
			found = append(found, c)
		}
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no membership found for the ID %q, expected <role>_<grant_role>", d.Id())
	case 1:
		d.Set("role", found[0][0])
		d.Set("grant_role", found[0][1])
		return []*schema.ResourceData{d}, nil
	default:
		return nil, fmt.Errorf("the ID %q is ambiguous: it matches the memberships %v", d.Id(), found)
	}
}
