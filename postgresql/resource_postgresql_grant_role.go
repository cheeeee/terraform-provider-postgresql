package postgresql

import (
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
	// REVOKE <option> OPTION FOR to disable it.
	for _, option := range grantRoleOptions {
		if !d.HasChange(option.attr) {
			continue
		}
		if _, configured := rawConfigBool(d, option.attr); !configured && option.attr != grantRoleWithAdminOptionAttr {
			continue
		}

		var query string
		if d.Get(option.attr).(bool) {
			query = createGrantRoleQuery(d, option)
		} else {
			query = createRevokeRoleOptionQuery(d, option)
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

func grantRoleGrantorClause(d *schema.ResourceData) string {
	if grantor, _ := d.Get(grantRoleGrantorAttr).(string); grantor != "" {
		return " GRANTED BY " + pq.QuoteIdentifier(grantor)
	}
	return ""
}

// createGrantRoleQuery returns the GRANT statement. Without options, it grants
// the membership with the options which are in the configuration; with options,
// only these ones (they are changed in place for an existing membership).
func createGrantRoleQuery(d *schema.ResourceData, only ...grantRoleOption) string {
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

	return query + grantRoleGrantorClause(d)
}

func grantRoleOptionSQL(option grantRoleOption, value bool) string {
	if option.attr == grantRoleWithAdminOptionAttr && value {
		return "ADMIN OPTION"
	}
	return fmt.Sprintf("%s %s", option.sql, strings.ToUpper(strconv.FormatBool(value)))
}

func createRevokeRoleQuery(d *schema.ResourceData) string {
	grantRole, _ := d.Get("grant_role").(string)
	role, _ := d.Get("role").(string)

	return fmt.Sprintf(
		"REVOKE %s FROM %s%s",
		pq.QuoteIdentifier(grantRole),
		pq.QuoteIdentifier(role),
		grantRoleGrantorClause(d),
	)
}

func createRevokeRoleOptionQuery(d *schema.ResourceData, option grantRoleOption) string {
	grantRole, _ := d.Get("grant_role").(string)
	role, _ := d.Get("role").(string)

	return fmt.Sprintf(
		"REVOKE %s OPTION FOR %s FROM %s%s",
		option.sql,
		pq.QuoteIdentifier(grantRole),
		pq.QuoteIdentifier(role),
		grantRoleGrantorClause(d),
	)
}

func grantRole(txn *sql.Tx, d *schema.ResourceData) error {
	query := createGrantRoleQuery(d)
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not execute grant query: %w", err)
	}
	return nil
}

func revokeRole(txn *sql.Tx, d *schema.ResourceData) error {
	query := createRevokeRoleQuery(d)
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not execute revoke query: %w", err)
	}
	return nil
}

func generateGrantRoleID(d *schema.ResourceData) string {
	return strings.Join([]string{d.Get("role").(string), d.Get("grant_role").(string), strconv.FormatBool(d.Get("with_admin_option").(bool))}, "_")
}
