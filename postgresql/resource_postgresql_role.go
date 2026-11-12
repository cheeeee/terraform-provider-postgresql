package postgresql

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/lib/pq"
)

const (
	roleBypassRLSAttr                       = "bypass_row_level_security"
	roleConnLimitAttr                       = "connection_limit"
	roleCreateDBAttr                        = "create_database"
	roleCreateRoleAttr                      = "create_role"
	roleEncryptedPassAttr                   = "encrypted_password"
	roleIdleInTransactionSessionTimeoutAttr = "idle_in_transaction_session_timeout"
	roleInheritAttr                         = "inherit"
	roleLoginAttr                           = "login"
	roleNameAttr                            = "name"
	rolePasswordAttr                        = "password"
	rolePasswordWOAttr                      = "password_wo"
	rolePasswordWOVersionAttr               = "password_wo_version"
	roleReplicationAttr                     = "replication"
	roleSkipDropRoleAttr                    = "skip_drop_role"
	roleSkipReassignOwnedAttr               = "skip_reassign_owned"
	roleSuperuserAttr                       = "superuser"
	roleValidUntilAttr                      = "valid_until"
	roleRolesAttr                           = "roles"
	roleSearchPathAttr                      = "search_path"
	roleStatementTimeoutAttr                = "statement_timeout"
	roleAssumeRoleAttr                      = "assume_role"
	roleParametersAttr                      = "parameters"
	roleDatabaseParametersAttr              = "database_parameters"

	// Deprecated options
	roleDepEncryptedAttr = "encrypted"
)

// roleReservedParameters are the configuration parameters managed by a
// dedicated attribute, they cannot be set with the parameters attribute.
var roleReservedParameters = map[string]string{
	"search_path":                         roleSearchPathAttr,
	"statement_timeout":                   roleStatementTimeoutAttr,
	"idle_in_transaction_session_timeout": roleIdleInTransactionSessionTimeoutAttr,
	"role":                                roleAssumeRoleAttr,
}

func resourcePostgreSQLRole() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLRoleCreate),
		Read:   PGResourceFunc(resourcePostgreSQLRoleRead),
		Update: PGResourceFunc(resourcePostgreSQLRoleUpdate),
		Delete: PGResourceFunc(resourcePostgreSQLRoleDelete),
		Exists: PGResourceExistsFunc(resourcePostgreSQLRoleExists),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		CustomizeDiff: validateRoleDatabaseParameters,

		Schema: map[string]*schema.Schema{
			roleNameAttr: {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The name of the role",
			},
			rolePasswordAttr: {
				Type:          schema.TypeString,
				Optional:      true,
				Sensitive:     true,
				ConflictsWith: []string{rolePasswordWOAttr, rolePasswordWOVersionAttr},
				Description:   "Sets the role's password",
			},
			rolePasswordWOAttr: {
				Type:          schema.TypeString,
				Optional:      true,
				Sensitive:     true,
				ConflictsWith: []string{rolePasswordAttr},
				RequiredWith:  []string{rolePasswordWOVersionAttr},
				WriteOnly:     true,
				Description:   "Sets the role's password without storing it in the state file.",
			},
			rolePasswordWOVersionAttr: {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{rolePasswordAttr},
				RequiredWith:  []string{rolePasswordWOAttr},
				Description:   "Prevents applies from updating the role password on every apply unless the value changes.",
			},
			roleDepEncryptedAttr: {
				Type:       schema.TypeString,
				Optional:   true,
				Deprecated: fmt.Sprintf("Rename PostgreSQL role resource attribute %q to %q", roleDepEncryptedAttr, roleEncryptedPassAttr),
			},
			roleRolesAttr: {
				Type:        schema.TypeSet,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Set:         schema.HashString,
				MinItems:    0,
				Description: "Role(s) to grant to this new role",
			},
			roleSearchPathAttr: {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				MinItems:    0,
				Description: "Sets the role's search path",
			},
			roleEncryptedPassAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "Control whether the password is stored encrypted in the system catalogs",
			},
			roleValidUntilAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "infinity",
				Description: "Sets a date and time after which the role's password is no longer valid",
			},
			roleConnLimitAttr: {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      -1,
				Description:  "How many concurrent connections can be made with this role",
				ValidateFunc: validation.IntAtLeast(-1),
			},
			roleSuperuserAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: `Determine whether the new role is a "superuser"`,
			},
			roleCreateDBAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Define a role's ability to create databases",
			},
			roleCreateRoleAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Determine whether this role will be permitted to create new roles",
			},
			roleIdleInTransactionSessionTimeoutAttr: {
				Type:         schema.TypeInt,
				Optional:     true,
				Description:  "Terminate any session with an open transaction that has been idle for longer than the specified duration in milliseconds",
				ValidateFunc: validation.IntAtLeast(0),
			},
			roleInheritAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: `Determine whether a role "inherits" the privileges of roles it is a member of`,
			},
			roleLoginAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Determine whether a role is allowed to log in",
			},
			roleReplicationAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Determine whether a role is allowed to initiate streaming replication or put the system in and out of backup mode",
			},
			roleBypassRLSAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Determine whether a role bypasses every row-level security (RLS) policy",
			},
			roleSkipDropRoleAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Skip actually running the DROP ROLE command when removing a ROLE from PostgreSQL",
			},
			roleSkipReassignOwnedAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Skip actually running the REASSIGN OWNED command when removing a role from PostgreSQL",
			},
			roleStatementTimeoutAttr: {
				Type:         schema.TypeInt,
				Optional:     true,
				Description:  "Abort any statement that takes more than the specified number of milliseconds",
				ValidateFunc: validation.IntAtLeast(0),
			},
			roleAssumeRoleAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Role to switch to at login",
			},
			roleParametersAttr: {
				Type:             schema.TypeMap,
				Optional:         true,
				Computed:         true,
				Elem:             &schema.Schema{Type: schema.TypeString},
				ValidateFunc:     validateConfigParameters(roleReservedParameters),
				DiffSuppressFunc: suppressConfigParametersDiff,
				Description:      "Configuration parameters set for this role (ALTER ROLE ... SET)",
			},
			roleDatabaseParametersAttr: {
				Type:        schema.TypeSet,
				Optional:    true,
				Computed:    true,
				Set:         hashRoleDatabaseParameters,
				Description: "Configuration parameters set for this role in a database (ALTER ROLE ... IN DATABASE ... SET)",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"database": {
							Type:        schema.TypeString,
							Required:    true,
							Description: "The name of the database",
						},
						"parameters": {
							Type:         schema.TypeMap,
							Required:     true,
							Elem:         &schema.Schema{Type: schema.TypeString},
							ValidateFunc: validateConfigParameters(roleReservedParameters),
							Description:  "Configuration parameters set for this role in this database",
						},
					},
				},
			},
		},
	}
}

func resourcePostgreSQLRoleCreate(db *DBConnection, d *schema.ResourceData) error {
	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	stringOpts := []struct {
		hclKey string
		sqlKey string
	}{
		{roleValidUntilAttr, "VALID UNTIL"}, // always keep this
	}

	// Decide which password attribute (if any) is set and prepend it.
	// NOTE: getWO returns (string, bool) just like d.GetOk.
	if v, ok := d.GetOk(rolePasswordAttr); ok && v.(string) != "" {
		// normal password stored in state
		stringOpts = append(
			[]struct{ hclKey, sqlKey string }{{rolePasswordAttr, "PASSWORD"}},
			stringOpts...,
		)
	} else if _, ok := getWO(d, rolePasswordWOAttr); ok {
		// write-only password
		stringOpts = append(
			[]struct{ hclKey, sqlKey string }{{rolePasswordWOAttr, "PASSWORD"}},
			stringOpts...,
		)
	}

	intOpts := []struct {
		hclKey string
		sqlKey string
	}{
		{roleConnLimitAttr, "CONNECTION LIMIT"},
	}

	type boolOptType struct {
		hclKey        string
		sqlKeyEnable  string
		sqlKeyDisable string
	}
	boolOpts := []boolOptType{
		{roleSuperuserAttr, "SUPERUSER", "NOSUPERUSER"},
		{roleCreateDBAttr, "CREATEDB", "NOCREATEDB"},
		{roleCreateRoleAttr, "CREATEROLE", "NOCREATEROLE"},
		{roleInheritAttr, "INHERIT", "NOINHERIT"},
		{roleLoginAttr, "LOGIN", "NOLOGIN"},
		// roleEncryptedPassAttr is used only when rolePasswordAttr is set.
		// {roleEncryptedPassAttr, "ENCRYPTED", "UNENCRYPTED"},
	}

	if db.featureSupported(featureRLS) {
		boolOpts = append(boolOpts, boolOptType{roleBypassRLSAttr, "BYPASSRLS", "NOBYPASSRLS"})
	}

	if db.featureSupported(featureReplication) {
		boolOpts = append(boolOpts, boolOptType{roleReplicationAttr, "REPLICATION", "NOREPLICATION"})
	}

	createOpts := make([]string, 0, len(stringOpts)+len(intOpts)+len(boolOpts))

	for _, opt := range stringOpts {
		var val string
		var ok bool

		if opt.hclKey == rolePasswordWOAttr {
			v, found := getWO(d, opt.hclKey)
			if found {
				val = v
				if val != "" {
					ok = true
				}
			}
		} else {
			v, found := d.GetOk(opt.hclKey)
			if found {
				val = v.(string)
				if val != "" {
					ok = true
				}
			}
		}

		if !ok {
			continue
		}

		switch opt.hclKey {
		case rolePasswordWOAttr, rolePasswordAttr:
			if strings.ToUpper(val) == "NULL" {
				createOpts = append(createOpts, "PASSWORD NULL")
			} else {
				if db.hashesRolePasswords() {
					if val, err = hashRolePassword(db, txn, val); err != nil {
						return err
					}
				}
				if d.Get(roleEncryptedPassAttr).(bool) {
					createOpts = append(createOpts, "ENCRYPTED")
				} else {
					createOpts = append(createOpts, "UNENCRYPTED")
				}
				createOpts = append(createOpts,
					fmt.Sprintf("%s '%s'", opt.sqlKey, pqQuoteLiteral(val)))
			}

		case roleValidUntilAttr:
			if val == "" || strings.ToLower(val) == "infinity" {
				createOpts = append(createOpts,
					fmt.Sprintf("%s 'infinity'", opt.sqlKey))
			} else {
				createOpts = append(createOpts,
					fmt.Sprintf("%s '%s'", opt.sqlKey, pqQuoteLiteral(val)))
			}

		default:
			createOpts = append(createOpts,
				fmt.Sprintf("%s %s", opt.sqlKey, pq.QuoteIdentifier(val)))
		}
	}

	for _, opt := range intOpts {
		val := d.Get(opt.hclKey).(int)
		createOpts = append(createOpts, fmt.Sprintf("%s %d", opt.sqlKey, val))
	}

	for _, opt := range boolOpts {
		if opt.hclKey == roleEncryptedPassAttr {
			// This attribute is handled above in the stringOpts
			// loop.
			continue
		}
		val := d.Get(opt.hclKey).(bool)
		valStr := opt.sqlKeyDisable
		if val {
			valStr = opt.sqlKeyEnable
		}
		createOpts = append(createOpts, valStr)
	}

	roleName := d.Get(roleNameAttr).(string)
	createStr := strings.Join(createOpts, " ")
	if len(createOpts) > 0 {
		if db.featureSupported(featureCreateRoleWith) {
			createStr = " WITH " + createStr
		} else {
			// NOTE(seanc@): Work around ParAccel/AWS RedShift's ancient fork of PostgreSQL
			createStr = " " + createStr
		}
	}

	sql := fmt.Sprintf("CREATE ROLE %s%s", pq.QuoteIdentifier(roleName), createStr)

	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error creating role %s: %w", roleName, err)
	}

	if err = grantRoles(txn, d); err != nil {
		return err
	}

	if err = alterSearchPath(txn, d); err != nil {
		return err
	}

	if err = setStatementTimeout(txn, d); err != nil {
		return err
	}

	if err = setIdleInTransactionSessionTimeout(txn, d); err != nil {
		return err
	}

	if err = setAssumeRole(txn, d); err != nil {
		return err
	}

	if err = setRoleParameters(txn, d); err != nil {
		return err
	}

	if err = setRoleDatabaseParameters(txn, d); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	d.SetId(roleName)

	return resourcePostgreSQLRoleReadImpl(db, d)
}

func resourcePostgreSQLRoleDelete(db *DBConnection, d *schema.ResourceData) error {
	roleName := d.Get(roleNameAttr).(string)

	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if err := pgLockRole(txn, roleName); err != nil {
		return err
	}

	if !d.Get(roleSkipReassignOwnedAttr).(bool) {
		if err := withRolesGranted(txn, []string{roleName}, func() error {
			currentUser := db.client.config.getDatabaseUsername()
			if _, err := txn.Exec(fmt.Sprintf("REASSIGN OWNED BY %s TO %s", pq.QuoteIdentifier(roleName), pq.QuoteIdentifier(currentUser))); err != nil {
				return fmt.Errorf("could not reassign owned by role %s to %s: %w", roleName, currentUser, err)
			}

			if _, err := txn.Exec(fmt.Sprintf("DROP OWNED BY %s", pq.QuoteIdentifier(roleName))); err != nil {
				return fmt.Errorf("could not drop owned by role %s: %w", roleName, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if !d.Get(roleSkipDropRoleAttr).(bool) {
		if _, err := txn.Exec(fmt.Sprintf("DROP ROLE %s", pq.QuoteIdentifier(roleName))); err != nil {
			return fmt.Errorf("could not delete role %s: %w", roleName, err)
		}
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("error committing schema: %w", err)
	}

	d.SetId("")

	return nil
}

func resourcePostgreSQLRoleExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	var roleName string
	err := db.QueryRow("SELECT rolname FROM pg_catalog.pg_roles WHERE rolname=$1", d.Id()).Scan(&roleName)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}

	return true, nil
}

func resourcePostgreSQLRoleRead(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLRoleReadImpl(db, d)
}

func resourcePostgreSQLRoleReadImpl(db *DBConnection, d *schema.ResourceData) error {
	var roleSuperuser, roleInherit, roleCreateRole, roleCreateDB, roleCanLogin, roleReplication, roleBypassRLS bool
	var roleConnLimit int
	var roleName, roleValidUntil string
	var roleRoles pq.ByteaArray
	// rolconfig is a text array, pq.ByteaArray would fail on values with a backslash
	var roleConfig pq.StringArray

	roleID := d.Id()

	columns := []string{
		"rolname",
		"rolsuper",
		"rolinherit",
		"rolcreaterole",
		"rolcreatedb",
		"rolcanlogin",
		"rolconnlimit",
		`COALESCE(rolvaliduntil::TEXT, 'infinity')`,
		"rolconfig",
	}

	values := []any{
		&roleRoles,
		&roleName,
		&roleSuperuser,
		&roleInherit,
		&roleCreateRole,
		&roleCreateDB,
		&roleCanLogin,
		&roleConnLimit,
		&roleValidUntil,
		&roleConfig,
	}

	if db.featureSupported(featureReplication) {
		columns = append(columns, "rolreplication")
		values = append(values, &roleReplication)
	}

	if db.featureSupported(featureRLS) {
		columns = append(columns, "rolbypassrls")
		values = append(values, &roleBypassRLS)
	}

	roleSQL := fmt.Sprintf(`SELECT ARRAY(
			SELECT pg_get_userbyid(roleid) FROM pg_catalog.pg_auth_members members WHERE member = pg_roles.oid
		), %s
		FROM pg_catalog.pg_roles WHERE rolname=$1`,
		// select columns
		strings.Join(columns, ", "),
	)
	err := db.QueryRow(roleSQL, roleID).Scan(values...)

	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL ROLE (%s) not found", roleID)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading ROLE: %w", err)
	}

	d.Set(roleNameAttr, roleName)
	d.Set(roleConnLimitAttr, roleConnLimit)
	d.Set(roleCreateDBAttr, roleCreateDB)
	d.Set(roleCreateRoleAttr, roleCreateRole)
	d.Set(roleEncryptedPassAttr, true)
	d.Set(roleInheritAttr, roleInherit)
	d.Set(roleLoginAttr, roleCanLogin)
	d.Set(roleSkipDropRoleAttr, d.Get(roleSkipDropRoleAttr).(bool))
	d.Set(roleSkipReassignOwnedAttr, d.Get(roleSkipReassignOwnedAttr).(bool))
	d.Set(roleSuperuserAttr, roleSuperuser)
	d.Set(roleValidUntilAttr, roleValidUntil)
	d.Set(roleReplicationAttr, roleReplication)
	d.Set(roleBypassRLSAttr, roleBypassRLS)
	d.Set(roleRolesAttr, pgArrayToSet(roleRoles))
	d.Set(roleSearchPathAttr, readSearchPath(roleConfig))
	d.Set(roleAssumeRoleAttr, readAssumeRole(roleConfig))
	d.Set(roleParametersAttr, parseConfigParameters(roleConfig, roleReservedParameters))
	if err := readRoleDatabaseParameters(db, d); err != nil {
		return err
	}

	statementTimeout, err := readStatementTimeout(roleConfig)
	if err != nil {
		return err
	}

	d.Set(roleStatementTimeoutAttr, statementTimeout)

	idleInTransactionSessionTimeout, err := readIdleInTransactionSessionTimeout(roleConfig)
	if err != nil {
		return err
	}

	d.Set(roleIdleInTransactionSessionTimeoutAttr, idleInTransactionSessionTimeout)

	d.SetId(roleName)

	if _, ok := d.GetOk(rolePasswordAttr); ok {
		password, err := readRolePassword(db, d, roleCanLogin)
		if err != nil {
			return err
		}
		d.Set(rolePasswordAttr, password)
	}
	return nil
}

// readSearchPath searches for a search_path entry in the rolconfig array.
// In case no such value is present, it returns nil.
func readSearchPath(roleConfig pq.StringArray) []string {
	for _, v := range roleConfig {
		config := string(v)
		if strings.HasPrefix(config, roleSearchPathAttr) {
			var result = strings.Split(strings.TrimPrefix(config, roleSearchPathAttr+"="), ", ")
			for i := range result {
				result[i] = strings.Trim(result[i], `"`)
			}
			return result
		}
	}
	return nil
}

// readIdleInTransactionSessionTimeout searches for an idle_in_transaction_session_timeout entry in the rolconfig array.
// In case no such value is present, it returns nil.
func readIdleInTransactionSessionTimeout(roleConfig pq.StringArray) (int, error) {
	for _, v := range roleConfig {
		config := string(v)
		if strings.HasPrefix(config, roleIdleInTransactionSessionTimeoutAttr) {
			var result = strings.Split(strings.TrimPrefix(config, roleIdleInTransactionSessionTimeoutAttr+"="), ", ")
			res, err := strconv.Atoi(result[0])
			if err != nil {
				return -1, fmt.Errorf("error reading statement_timeout: %w", err)
			}
			return res, nil
		}
	}
	return 0, nil
}

// readStatementTimeout searches for a statement_timeout entry in the rolconfig array.
// In case no such value is present, it returns nil.
func readStatementTimeout(roleConfig pq.StringArray) (int, error) {
	for _, v := range roleConfig {
		config := string(v)
		if strings.HasPrefix(config, roleStatementTimeoutAttr) {
			var result = strings.Split(strings.TrimPrefix(config, roleStatementTimeoutAttr+"="), ", ")
			res, err := strconv.Atoi(result[0])
			if err != nil {
				return -1, fmt.Errorf("error reading statement_timeout: %w", err)
			}
			return res, nil
		}
	}
	return 0, nil
}

// readAssumeRole searches for a role entry in the rolconfig array.
// In case no such value is present, it returns empty string.
func readAssumeRole(roleConfig pq.StringArray) string {
	var res string
	var assumeRoleAttr = "role"
	for _, v := range roleConfig {
		config := string(v)
		if strings.HasPrefix(config, assumeRoleAttr) {
			res = strings.TrimPrefix(config, assumeRoleAttr+"=")
		}
	}
	return res
}

// readRolePassword reads password either from Postgres if admin user is a superuser
// or only from Terraform state.
func readRolePassword(db *DBConnection, d *schema.ResourceData, roleCanLogin bool) (string, error) {
	statePassword := d.Get(rolePasswordAttr).(string)

	// Role which cannot login does not have password in pg_shadow.
	// Also, if user specifies that admin is not a superuser we don't try to read pg_shadow
	// (only superuser can read pg_shadow)
	if !roleCanLogin || !db.client.config.Superuser {
		return statePassword, nil
	}

	// Otherwise we check if connected user is really a superuser
	// (in order to warn user instead of having a permission denied error)
	superuser, err := db.isSuperuser()
	if err != nil {
		return "", err
	}
	if !superuser {
		return "", fmt.Errorf(
			"could not read role password from Postgres as "+
				"connected user %s is not a SUPERUSER. "+
				"You can set `superuser = false` in the provider configuration "+
				"so it will not try to read the password from Postgres",
			db.client.config.getDatabaseUsername(),
		)
	}

	var rolePassword string
	err = db.QueryRow("SELECT COALESCE(passwd, '') FROM pg_catalog.pg_shadow AS s WHERE s.usename = $1", d.Id()).Scan(&rolePassword)
	switch {
	case err == sql.ErrNoRows:
		// They don't have a password
		return "", nil
	case err != nil:
		return "", fmt.Errorf("error reading role: %w", err)
	}
	// "NULL" removes the password: the role has none in the catalog
	if strings.ToUpper(statePassword) == "NULL" && rolePassword == "" {
		return statePassword, nil
	}
	// If the password isn't already in md5 format, but hashing the input
	// matches the password in the database for the user, they are the same
	if statePassword != "" && !strings.HasPrefix(statePassword, "md5") && !strings.HasPrefix(statePassword, "SCRAM-SHA-256") {
		if strings.HasPrefix(rolePassword, "md5") {
			hasher := md5.New()
			if _, err := hasher.Write([]byte(statePassword + d.Id())); err != nil {
				return "", err
			}
			hashedPassword := "md5" + hex.EncodeToString(hasher.Sum(nil))

			if hashedPassword == rolePassword {
				// The passwords are actually the same
				// make Terraform think they are the same
				return statePassword, nil
			}
		}
		if strings.HasPrefix(rolePassword, "SCRAM-SHA-256") {
			return statePassword, nil
			// TODO : implement scram-sha-256 challenge request to the server
		}
	}
	return rolePassword, nil
}

func resourcePostgreSQLRoleUpdate(db *DBConnection, d *schema.ResourceData) error {
	txn, err := startTransaction(db.client, "")
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	oldName, _ := d.GetChange(roleNameAttr)
	if err := pgLockRole(txn, oldName.(string)); err != nil {
		return err
	}

	if err := setRoleName(txn, d); err != nil {
		return err
	}

	if err := setRolePassword(db, txn, d); err != nil {
		return err
	}

	if err := setRoleBypassRLS(db, txn, d); err != nil {
		return err
	}

	if err := setRoleConnLimit(txn, d); err != nil {
		return err
	}

	if err := setRoleCreateDB(txn, d); err != nil {
		return err
	}

	if err := setRoleCreateRole(txn, d); err != nil {
		return err
	}

	if err := setRoleInherit(txn, d); err != nil {
		return err
	}

	if err := setRoleLogin(txn, d); err != nil {
		return err
	}

	if err := setRoleReplication(txn, d); err != nil {
		return err
	}

	if err := setRoleSuperuser(txn, d); err != nil {
		return err
	}

	if err := setRoleValidUntil(txn, d); err != nil {
		return err
	}

	// applying roles: revoke the ones which are removed, grant the added ones.
	// The memberships which are not changed are kept as they are, they can have
	// options which are managed with postgresql_grant_role.
	if err = changeRoles(txn, d); err != nil {
		return err
	}

	if err = alterSearchPath(txn, d); err != nil {
		return err
	}

	if err = setStatementTimeout(txn, d); err != nil {
		return err
	}

	if err = setIdleInTransactionSessionTimeout(txn, d); err != nil {
		return err
	}

	if err = setAssumeRole(txn, d); err != nil {
		return err
	}

	if err = setRoleParameters(txn, d); err != nil {
		return err
	}

	if err = setRoleDatabaseParameters(txn, d); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	return resourcePostgreSQLRoleReadImpl(db, d)
}

func setRoleName(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleNameAttr) {
		return nil
	}

	oraw, nraw := d.GetChange(roleNameAttr)
	o := oraw.(string)
	n := nraw.(string)
	if n == "" {
		return errors.New("error setting role name to an empty string")
	}

	sql := fmt.Sprintf("ALTER ROLE %s RENAME TO %s", pq.QuoteIdentifier(o), pq.QuoteIdentifier(n))
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role NAME: %w", err)
	}

	d.SetId(n)

	return nil
}

func setRolePassword(db *DBConnection, txn *sql.Tx, d *schema.ResourceData) error {

	// Early exit if password WO and version are set, and version has not changed
	if _, ok := getWO(d, rolePasswordWOAttr); ok {
		if !d.HasChange(rolePasswordWOVersionAttr) {
			return nil
		}
	} else {
		// Only for regular password attribute: exit if neither password nor role name changed
		if !d.HasChange(rolePasswordAttr) && !d.HasChange(roleNameAttr) {
			return nil
		}
	}

	roleName := d.Get(roleNameAttr).(string)

	var password string
	if v, ok := getWO(d, rolePasswordWOAttr); ok {
		password = v // use the value from password_wo and reset the password state.
		d.Set(rolePasswordAttr, "")
	} else if v, ok := d.GetOk(rolePasswordAttr); ok && v.(string) != "" {
		password = v.(string) // use the clear-text password
	} else {
		// Nothing to set
		return nil
	}

	sql := fmt.Sprintf("ALTER ROLE %s PASSWORD NULL", pq.QuoteIdentifier(roleName))
	if strings.ToUpper(password) != "NULL" {
		if db.hashesRolePasswords() {
			var err error
			if password, err = hashRolePassword(db, txn, password); err != nil {
				return err
			}
		}
		sql = fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", pq.QuoteIdentifier(roleName), pqQuoteLiteral(password))
	}
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role password: %w", err)
	}

	return nil
}

// hashesRolePasswords returns true if the provider hashes the passwords of the
// roles itself instead of sending them in clear-text.
// It is the default as soon as the server supports SCRAM-SHA-256; it is refused
// by hashRolePassword when it is asked for explicitly on a server which does not.
func (db *DBConnection) hashesRolePasswords() bool {
	switch db.client.config.PasswordAuthentication {
	case "password":
		return false
	case "scram-sha-256":
		return true
	default:
		return db.featureSupported(featureScramSHA256)
	}
}

// hashRolePassword hashes a clear-text password with SCRAM-SHA-256, the way
// PostgreSQL would hash it, so it is not sent in clear-text in the
// CREATE ROLE / ALTER ROLE statement (which the server may log).
// A password which is already hashed is returned as-is.
func hashRolePassword(db *DBConnection, txn *sql.Tx, password string) (string, error) {
	if !db.featureSupported(featureScramSHA256) {
		return "", errors.New(`password_authentication = "scram-sha-256" requires PostgreSQL 10 or later`)
	}

	if isHashedPassword(password) {
		return password, nil
	}

	iterations := scramSHA256DefaultIterations
	if db.featureSupported(featureScramIterations) {
		if err := txn.QueryRow("SHOW scram_iterations").Scan(&iterations); err != nil {
			return "", fmt.Errorf("could not read scram_iterations: %w", err)
		}
	}

	salt, err := newScramSHA256Salt()
	if err != nil {
		return "", err
	}

	return scramSHA256Password(password, salt, iterations)
}

func setRoleBypassRLS(db *DBConnection, txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleBypassRLSAttr) {
		return nil
	}

	if !db.featureSupported(featureRLS) {
		return fmt.Errorf("PostgreSQL client is talking with a server (%q) that does not support PostgreSQL Row-Level Security", db.version.String())
	}

	bypassRLS := d.Get(roleBypassRLSAttr).(bool)
	tok := "NOBYPASSRLS"
	if bypassRLS {
		tok = "BYPASSRLS"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role BYPASSRLS: %w", err)
	}

	return nil
}

func setRoleConnLimit(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleConnLimitAttr) {
		return nil
	}

	connLimit := d.Get(roleConnLimitAttr).(int)
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s CONNECTION LIMIT %d", pq.QuoteIdentifier(roleName), connLimit)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role CONNECTION LIMIT: %w", err)
	}

	return nil
}

func setRoleCreateDB(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleCreateDBAttr) {
		return nil
	}

	createDB := d.Get(roleCreateDBAttr).(bool)
	tok := "NOCREATEDB"
	if createDB {
		tok = "CREATEDB"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role CREATEDB: %w", err)
	}

	return nil
}

func setRoleCreateRole(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleCreateRoleAttr) {
		return nil
	}

	createRole := d.Get(roleCreateRoleAttr).(bool)
	tok := "NOCREATEROLE"
	if createRole {
		tok = "CREATEROLE"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role CREATEROLE: %w", err)
	}

	return nil
}

func setRoleInherit(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleInheritAttr) {
		return nil
	}

	inherit := d.Get(roleInheritAttr).(bool)
	tok := "NOINHERIT"
	if inherit {
		tok = "INHERIT"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role INHERIT: %w", err)
	}

	return nil
}

func setRoleLogin(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleLoginAttr) {
		return nil
	}

	login := d.Get(roleLoginAttr).(bool)
	tok := "NOLOGIN"
	if login {
		tok = "LOGIN"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role LOGIN: %w", err)
	}

	return nil
}

func setRoleReplication(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleReplicationAttr) {
		return nil
	}

	replication := d.Get(roleReplicationAttr).(bool)
	tok := "NOREPLICATION"
	if replication {
		tok = "REPLICATION"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role REPLICATION: %w", err)
	}

	return nil
}

func setRoleSuperuser(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleSuperuserAttr) {
		return nil
	}

	superuser := d.Get(roleSuperuserAttr).(bool)
	tok := "NOSUPERUSER"
	if superuser {
		tok = "SUPERUSER"
	}
	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s WITH %s", pq.QuoteIdentifier(roleName), tok)
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role SUPERUSER: %w", err)
	}

	return nil
}

func setRoleValidUntil(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleValidUntilAttr) {
		return nil
	}

	validUntil := d.Get(roleValidUntilAttr).(string)
	if validUntil == "" {
		return nil
	} else if strings.ToLower(validUntil) == "infinity" {
		validUntil = "infinity"
	}

	roleName := d.Get(roleNameAttr).(string)
	sql := fmt.Sprintf("ALTER ROLE %s VALID UNTIL '%s'", pq.QuoteIdentifier(roleName), pqQuoteLiteral(validUntil))
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating role VALID UNTIL: %w", err)
	}

	return nil
}

// changeRoles revokes the roles which are not in the resource anymore and grants the new ones.
func changeRoles(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleRolesAttr) {
		return nil
	}

	role := d.Get(roleNameAttr).(string)
	oldRoles, newRoles := d.GetChange(roleRolesAttr)
	removed := oldRoles.(*schema.Set).Difference(newRoles.(*schema.Set)).List()
	added := newRoles.(*schema.Set).Difference(oldRoles.(*schema.Set)).List()

	for _, revokedRole := range removed {
		// The role can have been dropped (with its memberships) in the same apply
		var member bool
		err := txn.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members
			WHERE pg_get_userbyid(roleid) = $1 AND pg_get_userbyid(member) = $2)`, revokedRole, role).Scan(&member)
		if err != nil {
			return fmt.Errorf("could not read the membership of %s in %s: %w", role, revokedRole, err)
		}
		if !member {
			continue
		}

		query := fmt.Sprintf("REVOKE %s FROM %s", pq.QuoteIdentifier(revokedRole.(string)), pq.QuoteIdentifier(role))
		if _, err := txn.Exec(query); err != nil {
			return fmt.Errorf("could not revoke role %s from %s: %w", revokedRole, role, err)
		}
	}

	for _, grantedRole := range added {
		query := fmt.Sprintf("GRANT %s TO %s", pq.QuoteIdentifier(grantedRole.(string)), pq.QuoteIdentifier(role))
		if _, err := txn.Exec(query); err != nil {
			return fmt.Errorf("could not grant role %s to %s: %w", grantedRole, role, err)
		}
	}

	return nil
}

func grantRoles(txn *sql.Tx, d *schema.ResourceData) error {
	role := d.Get(roleNameAttr).(string)

	for _, grantingRole := range d.Get("roles").(*schema.Set).List() {
		query := fmt.Sprintf(
			"GRANT %s TO %s", pq.QuoteIdentifier(grantingRole.(string)), pq.QuoteIdentifier(role),
		)
		if _, err := txn.Exec(query); err != nil {
			return fmt.Errorf("could not grant role %s to %s: %w", grantingRole, role, err)
		}
	}
	return nil
}

func alterSearchPath(txn *sql.Tx, d *schema.ResourceData) error {
	role := d.Get(roleNameAttr).(string)
	searchPathInterface := d.Get(roleSearchPathAttr).([]any)

	var searchPathString []string
	if len(searchPathInterface) > 0 {
		searchPathString = make([]string, len(searchPathInterface))
		for i, searchPathPart := range searchPathInterface {
			if strings.Contains(searchPathPart.(string), ", ") {
				return fmt.Errorf("search_path cannot contain `, `: %v", searchPathPart)
			}
			searchPathString[i] = pq.QuoteIdentifier(searchPathPart.(string))
		}
	} else {
		searchPathString = []string{"DEFAULT"}
	}
	searchPath := strings.Join(searchPathString[:], ", ")

	query := fmt.Sprintf(
		"ALTER ROLE %s SET search_path TO %s", pq.QuoteIdentifier(role), searchPath,
	)
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not set search_path %s for %s: %w", searchPath, role, err)
	}
	return nil
}

func setStatementTimeout(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleStatementTimeoutAttr) {
		return nil
	}

	roleName := d.Get(roleNameAttr).(string)
	statementTimeout := d.Get(roleStatementTimeoutAttr).(int)
	if statementTimeout != 0 {
		sql := fmt.Sprintf(
			"ALTER ROLE %s SET statement_timeout TO %d", pq.QuoteIdentifier(roleName), statementTimeout,
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not set statement_timeout %d for %s: %w", statementTimeout, roleName, err)
		}
	} else {
		sql := fmt.Sprintf(
			"ALTER ROLE %s RESET statement_timeout", pq.QuoteIdentifier(roleName),
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not reset statement_timeout for %s: %w", roleName, err)
		}
	}
	return nil
}

func setIdleInTransactionSessionTimeout(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleIdleInTransactionSessionTimeoutAttr) {
		return nil
	}

	roleName := d.Get(roleNameAttr).(string)
	idleInTransactionSessionTimeout := d.Get(roleIdleInTransactionSessionTimeoutAttr).(int)
	if idleInTransactionSessionTimeout != 0 {
		sql := fmt.Sprintf(
			"ALTER ROLE %s SET idle_in_transaction_session_timeout TO %d", pq.QuoteIdentifier(roleName), idleInTransactionSessionTimeout,
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not set idle_in_transaction_session_timeout %d for %s: %w", idleInTransactionSessionTimeout, roleName, err)
		}
	} else {
		sql := fmt.Sprintf(
			"ALTER ROLE %s RESET idle_in_transaction_session_timeout", pq.QuoteIdentifier(roleName),
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not reset idle_in_transaction_session_timeout for %s: %w", roleName, err)
		}
	}
	return nil
}

func setAssumeRole(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleAssumeRoleAttr) {
		return nil
	}

	roleName := d.Get(roleNameAttr).(string)
	assumeRole := d.Get(roleAssumeRoleAttr).(string)
	if assumeRole != "" {
		sql := fmt.Sprintf(
			"ALTER ROLE %s SET ROLE TO %s", pq.QuoteIdentifier(roleName), pq.QuoteIdentifier(assumeRole),
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not set role %s for %s: %w", assumeRole, roleName, err)
		}
	} else {
		sql := fmt.Sprintf(
			"ALTER ROLE %s RESET ROLE", pq.QuoteIdentifier(roleName),
		)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("could not reset role for %s: %w", roleName, err)
		}
	}
	return nil
}

func setRoleParameters(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleParametersAttr) {
		return nil
	}

	roleName := d.Get(roleNameAttr).(string)
	oldParams, newParams := d.GetChange(roleParametersAttr)

	return alterConfigParameters(txn, "ROLE", roleName, oldParams.(map[string]any), newParams.(map[string]any))
}

// roleDatabaseParameters returns the parameters set for the role in each database.
func roleDatabaseParameters(set *schema.Set) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, set.Len())
	for _, v := range set.List() {
		block := v.(map[string]any)
		database := block["database"].(string)
		if _, ok := result[database]; ok {
			return nil, fmt.Errorf("%s: the database %q is defined twice", roleDatabaseParametersAttr, database)
		}
		parameters, _ := block["parameters"].(map[string]any)
		if parameters == nil {
			parameters = map[string]any{}
		}
		result[database] = parameters
	}
	return result, nil
}

func validateRoleDatabaseParameters(_ context.Context, d *schema.ResourceDiff, _ any) error {
	set, ok := d.Get(roleDatabaseParametersAttr).(*schema.Set)
	if !ok {
		return nil
	}
	_, err := roleDatabaseParameters(set)
	return err
}

// hashRoleDatabaseParameters identifies a database_parameters block by its database
// and its parameters. The lists of the list parameters are compared element by element
// because PostgreSQL does not store them the way they are written.
func hashRoleDatabaseParameters(v any) int {
	block := v.(map[string]any)
	parameters, _ := block["parameters"].(map[string]any)
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "%s;", block["database"])
	for _, name := range names {
		value, _ := parameters[name].(string)
		if listQuoteParameters[name] {
			value = strings.Join(splitListParameter(value), "\x00")
		}
		fmt.Fprintf(&b, "%s=%s;", name, value)
	}
	return schema.HashString(b.String())
}

func readRoleDatabaseConfig(db QueryAble, roleName, database string) (map[string]any, error) {
	var config pq.StringArray
	err := db.QueryRow(`SELECT COALESCE((
		SELECT s.setconfig FROM pg_catalog.pg_db_role_setting AS s
		JOIN pg_catalog.pg_database AS d ON d.oid = s.setdatabase
		JOIN pg_catalog.pg_roles AS r ON r.oid = s.setrole
		WHERE d.datname = $1 AND r.rolname = $2
	), '{}'::text[])`, database, roleName).Scan(&config)
	if err != nil {
		return nil, fmt.Errorf("error reading parameters of role %s in database %s: %w", roleName, database, err)
	}
	return parseConfigParameters(config, roleReservedParameters), nil
}

// readRoleDatabaseParameters reads the parameters of the databases which are in
// the resource: the other databases are not managed.
func readRoleDatabaseParameters(db QueryAble, d *schema.ResourceData) error {
	set, ok := d.Get(roleDatabaseParametersAttr).(*schema.Set)
	if !ok {
		return nil
	}
	known, err := roleDatabaseParameters(set)
	if err != nil {
		return err
	}

	blocks := make([]any, 0, len(known))
	for database, previous := range known {
		stored, err := readRoleDatabaseConfig(db, d.Id(), database)
		if err != nil {
			return err
		}
		// keep the way a list is written if it is the same list
		for name, value := range stored {
			if prev, ok := previous[name].(string); ok && listQuoteParameters[name] {
				if v, _ := value.(string); slices.Equal(splitListParameter(prev), splitListParameter(v)) {
					stored[name] = prev
				}
			}
		}
		blocks = append(blocks, map[string]any{"database": database, "parameters": stored})
	}
	return d.Set(roleDatabaseParametersAttr, blocks)
}

func setRoleDatabaseParameters(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(roleDatabaseParametersAttr) {
		return nil
	}

	oldSet, newSet := d.GetChange(roleDatabaseParametersAttr)
	oldParams, err := roleDatabaseParameters(oldSet.(*schema.Set))
	if err != nil {
		return err
	}
	newParams, err := roleDatabaseParameters(newSet.(*schema.Set))
	if err != nil {
		return err
	}

	roleName := d.Get(roleNameAttr).(string)
	databases := make([]string, 0, len(oldParams)+len(newParams))
	for database := range oldParams {
		databases = append(databases, database)
	}
	for database := range newParams {
		if _, ok := oldParams[database]; !ok {
			databases = append(databases, database)
		}
	}
	sort.Strings(databases)

	for _, database := range databases {
		read := func() (map[string]any, error) { return readRoleDatabaseConfig(txn, roleName, database) }
		old, ok := oldParams[database]
		if !ok {
			// A database which enters the resource: the parameters which are
			// already set and not in the resource are reset.
			if old, err = read(); err != nil {
				return err
			}
		}
		// A database which is not in the resource anymore is not managed anymore: its parameters are reset.
		err := alterConfigParametersOn(
			txn,
			fmt.Sprintf("ROLE %s IN DATABASE %s", pq.QuoteIdentifier(roleName), pq.QuoteIdentifier(database)),
			fmt.Sprintf("role %s in database %s", roleName, database),
			read, old, newParams[database],
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func getWO(d *schema.ResourceData, attribute string) (string, bool) {
	// Special case for write-only fields
	raw, diags := d.GetRawConfigAt(cty.GetAttrPath(attribute))
	if diags.HasError() || raw.IsNull() || !raw.Type().Equals(cty.String) {
		return "", false
	}
	if raw.AsString() == "" {
		return "", false
	}
	return raw.AsString(), true // return the value
}
