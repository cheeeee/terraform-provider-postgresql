package postgresql

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/lib/pq"
	acl "github.com/sean-/postgresql-acl"
)

const (
	schemaNameAttr     = "name"
	schemaDatabaseAttr = "database"
	schemaOwnerAttr    = "owner"
	schemaPolicyAttr   = "policy"
	schemaIfNotExists  = "if_not_exists"
	schemaDropCascade  = "drop_cascade"

	schemaPolicyCreateAttr          = "create"
	schemaPolicyCreateWithGrantAttr = "create_with_grant"
	schemaPolicyRoleAttr            = "role"
	schemaPolicyUsageAttr           = "usage"
	schemaPolicyUsageWithGrantAttr  = "usage_with_grant"
)

func resourcePostgreSQLSchema() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLSchemaCreate),
		Read:   PGResourceFunc(resourcePostgreSQLSchemaRead),
		Update: PGResourceFunc(resourcePostgreSQLSchemaUpdate),
		Delete: PGResourceFunc(resourcePostgreSQLSchemaDelete),
		Exists: PGResourceExistsFunc(resourcePostgreSQLSchemaExists),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			schemaNameAttr: {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The name of the schema",
			},
			schemaDatabaseAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "The database name to alter schema",
			},
			schemaOwnerAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "The ROLE name who owns the schema",
			},
			schemaIfNotExists: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
				Description: "When true, use the existing schema if it exists",
			},
			schemaDropCascade: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "When true, will also drop all the objects that are contained in the schema",
			},
			schemaPolicyAttr: {
				Type:       schema.TypeSet,
				Optional:   true,
				Computed:   true,
				Deprecated: "Use postgresql_grant resource instead (with object_type=\"schema\")",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						schemaPolicyCreateAttr: {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "If true, allow the specified ROLEs to CREATE new objects within the schema(s)",
						},
						schemaPolicyCreateWithGrantAttr: {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "If true, allow the specified ROLEs to CREATE new objects within the schema(s) and GRANT the same CREATE privilege to different ROLEs",
						},
						schemaPolicyRoleAttr: {
							Type:        schema.TypeString,
							Elem:        &schema.Schema{Type: schema.TypeString},
							Optional:    true,
							Default:     "",
							Description: "ROLE who will receive this policy (default: PUBLIC)",
						},
						schemaPolicyUsageAttr: {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "If true, allow the specified ROLEs to use objects within the schema(s)",
						},
						schemaPolicyUsageWithGrantAttr: {
							Type:        schema.TypeBool,
							Optional:    true,
							Default:     false,
							Description: "If true, allow the specified ROLEs to use objects within the schema(s) and GRANT the same USAGE privilege to different ROLEs",
						},
					},
				},
			},
		},
	}
}

func resourcePostgreSQLSchemaCreate(db *DBConnection, d *schema.ResourceData) error {
	database := getDatabase(d, db.client.databaseName)
	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	// If the authenticated user is not a superuser (e.g. on AWS RDS)
	// we'll need to temporarily grant it membership in the following roles:
	//  * the owner of the db (to have the permissions to create the schema)
	//  * the owner of the schema, if it has one (in order to change its owner)
	var rolesToGrant []string

	dbOwner, err := getDatabaseOwner(txn, database)
	if err != nil {
		return err
	}
	rolesToGrant = append(rolesToGrant, dbOwner)

	schemaOwner := d.Get("owner").(string)
	if schemaOwner != "" && schemaOwner != dbOwner {
		rolesToGrant = append(rolesToGrant, schemaOwner)

	}

	if err := withRolesGranted(txn, rolesToGrant, func() error {
		return createSchema(db, txn, d)
	}); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("error committing schema: %w", err)
	}

	d.SetId(generateSchemaID(d, database))

	return resourcePostgreSQLSchemaReadImpl(db, d)
}

func createSchema(db *DBConnection, txn *sql.Tx, d *schema.ResourceData) error {
	schemaName := d.Get(schemaNameAttr).(string)

	// Check if previous tasks haven't already create schema
	var foundSchema bool
	err := txn.QueryRow(`SELECT TRUE FROM pg_catalog.pg_namespace WHERE nspname = $1`, schemaName).Scan(&foundSchema)

	queries := []string{}
	switch {
	// With if_not_exists = false an existing schema is not adopted: CREATE SCHEMA
	// is sent without IF NOT EXISTS and PostgreSQL refuses it ("already exists").
	case err == sql.ErrNoRows, err == nil && !d.Get(schemaIfNotExists).(bool):
		b := bytes.NewBufferString("CREATE SCHEMA ")
		if db.featureSupported(featureSchemaCreateIfNotExist) {
			if v := d.Get(schemaIfNotExists); v.(bool) {
				fmt.Fprint(b, "IF NOT EXISTS ")
			}
		}
		fmt.Fprint(b, pq.QuoteIdentifier(schemaName))

		switch v, ok := d.GetOk(schemaOwnerAttr); {
		case ok:
			fmt.Fprint(b, " AUTHORIZATION ", pq.QuoteIdentifier(v.(string)))
		}
		queries = append(queries, b.String())

	case err != nil:
		return fmt.Errorf("error looking for schema: %w", err)

	default:
		// The schema already exists, we just set the owner.
		if err := setSchemaOwner(txn, d); err != nil {
			return err
		}
	}

	// ACL objects that can generate the necessary SQL
	type RoleKey string
	var schemaPolicies map[RoleKey]acl.Schema

	if policiesRaw, ok := d.GetOk(schemaPolicyAttr); ok {
		policiesList := policiesRaw.(*schema.Set).List()

		// NOTE: len(policiesList) doesn't take into account multiple
		// roles per policy.
		schemaPolicies = make(map[RoleKey]acl.Schema, len(policiesList))

		for _, policyRaw := range policiesList {
			policyMap := policyRaw.(map[string]any)
			rolePolicy := schemaPolicyToACL(policyMap)

			roleKey := RoleKey(strings.ToLower(rolePolicy.Role))
			if existingRolePolicy, ok := schemaPolicies[roleKey]; ok {
				schemaPolicies[roleKey] = existingRolePolicy.Merge(rolePolicy)
			} else {
				schemaPolicies[roleKey] = rolePolicy
			}
		}
	}

	for _, policy := range schemaPolicies {
		queries = append(queries, policy.Grants(schemaName)...)
	}

	for _, query := range queries {
		if _, err = txn.Exec(query); err != nil {
			return fmt.Errorf("error creating schema %s: %w", schemaName, err)
		}
	}

	return nil
}

func resourcePostgreSQLSchemaDelete(db *DBConnection, d *schema.ResourceData) error {
	database := getDatabase(d, db.client.databaseName)

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	schemaName := d.Get(schemaNameAttr).(string)

	exists, err := schemaExists(txn, schemaName)
	if err != nil {
		return err
	}
	if !exists {
		d.SetId("")
		return nil
	}

	owner := d.Get("owner").(string)

	if err = withRolesGranted(txn, []string{owner}, func() error {
		dropMode := "RESTRICT"
		if d.Get(schemaDropCascade).(bool) {
			dropMode = "CASCADE"
		}

		sql := fmt.Sprintf("DROP SCHEMA %s %s", pq.QuoteIdentifier(schemaName), dropMode)
		if _, err = txn.Exec(sql); err != nil {
			return fmt.Errorf("error deleting schema: %w", err)
		}

		return nil
	}); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("error committing schema: %w", err)
	}

	d.SetId("")

	return nil
}

func resourcePostgreSQLSchemaExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	database, schemaName, err := getDBSchemaName(d, db.client.databaseName)
	if err != nil {
		return false, err
	}

	// Check if the database exists
	exists, err := dbExists(db, database)
	if err != nil || !exists {
		return false, err
	}

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return false, err
	}
	defer deferredRollback(txn)

	err = txn.QueryRow("SELECT n.nspname FROM pg_catalog.pg_namespace n WHERE n.nspname=$1", schemaName).Scan(&schemaName)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading schema: %w", err)
	}

	return true, nil
}

func resourcePostgreSQLSchemaRead(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLSchemaReadImpl(db, d)
}

func resourcePostgreSQLSchemaReadImpl(db *DBConnection, d *schema.ResourceData) error {
	database, schemaName, err := getDBSchemaName(d, db.client.databaseName)
	if err != nil {
		return err
	}

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	var schemaOwner string
	var schemaACLs []string
	err = txn.QueryRow("SELECT pg_catalog.pg_get_userbyid(n.nspowner), COALESCE(n.nspacl, '{}'::aclitem[])::TEXT[] FROM pg_catalog.pg_namespace n WHERE n.nspname=$1", schemaName).Scan(&schemaOwner, pq.Array(&schemaACLs))
	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL schema (%s) not found in database %s", schemaName, database)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading schema: %w", err)
	default:
		actualPolicies := make(map[string]acl.Schema, len(schemaACLs))
		for _, aclStr := range schemaACLs {
			aclItem, err := acl.Parse(aclStr)
			if err != nil {
				return fmt.Errorf("error parsing aclitem: %w", err)
			}

			schemaACL, err := acl.NewSchema(aclItem)
			if err != nil {
				return fmt.Errorf("invalid perms for schema: %w", err)
			}

			actualPolicies[schemaACL.Role] = actualPolicies[schemaACL.Role].Merge(schemaACL)
		}

		if err := d.Set(schemaPolicyAttr, readSchemaPolicies(d, schemaOwner, actualPolicies)); err != nil {
			return fmt.Errorf("error setting schema policies: %w", err)
		}

		d.Set(schemaNameAttr, schemaName)
		d.Set(schemaOwnerAttr, schemaOwner)
		d.Set(schemaDatabaseAttr, database)
		d.SetId(generateSchemaID(d, database))

		return nil
	}
}

func resourcePostgreSQLSchemaUpdate(db *DBConnection, d *schema.ResourceData) error {
	databaseName := getDatabase(d, db.client.databaseName)

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	// If the authenticated user is not a superuser, it needs to be a member of
	// the old and the new owners to change the owner of the schema (see
	// resourcePostgreSQLSchemaCreate).
	var rolesToGrant []string
	if d.HasChange(schemaOwnerAttr) {
		oldOwner, newOwner := d.GetChange(schemaOwnerAttr)
		if newOwner.(string) != "" {
			rolesToGrant = append(rolesToGrant, newOwner.(string))
		}
		if oldOwner.(string) != "" && oldOwner.(string) != newOwner.(string) {
			rolesToGrant = append(rolesToGrant, oldOwner.(string))
		}
	}

	if err := withRolesGranted(txn, rolesToGrant, func() error {
		if err := setSchemaName(txn, d, databaseName); err != nil {
			return err
		}

		return setSchemaOwner(txn, d)
	}); err != nil {
		return err
	}

	if err := setSchemaPolicy(txn, d); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return fmt.Errorf("error committing schema: %w", err)
	}

	return resourcePostgreSQLSchemaReadImpl(db, d)
}

func setSchemaName(txn *sql.Tx, d *schema.ResourceData, databaseName string) error {
	if !d.HasChange(schemaNameAttr) {
		return nil
	}

	oraw, nraw := d.GetChange(schemaNameAttr)
	o := oraw.(string)
	n := nraw.(string)
	if n == "" {
		return errors.New("error setting schema name to an empty string")
	}

	sql := fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s", pq.QuoteIdentifier(o), pq.QuoteIdentifier(n))
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating schema NAME: %w", err)
	}
	d.SetId(generateSchemaID(d, databaseName))

	return nil
}

func setSchemaOwner(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(schemaOwnerAttr) {
		return nil
	}

	schemaName := d.Get(schemaNameAttr).(string)
	schemaOwner := d.Get(schemaOwnerAttr).(string)

	if schemaOwner == "" {
		return errors.New("error setting schema owner to an empty string")
	}

	sql := fmt.Sprintf("ALTER SCHEMA %s OWNER TO %s", pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(schemaOwner))
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating schema OWNER: %w", err)
	}

	return nil
}

func setSchemaPolicy(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(schemaPolicyAttr) {
		return nil
	}

	schemaName := d.Get(schemaNameAttr).(string)
	owner := d.Get(schemaOwnerAttr).(string)

	oraw, nraw := d.GetChange(schemaPolicyAttr)
	oldPolicies := mergeSchemaPolicies(oraw.(*schema.Set).List())
	newPolicies := mergeSchemaPolicies(nraw.(*schema.Set).List())
	queries := make([]string, 0, len(oldPolicies)+len(newPolicies))

	for key, rolePolicy := range oldPolicies {
		if newPolicy, ok := newPolicies[key]; ok {
			if newPolicy.Privileges == rolePolicy.Privileges && newPolicy.GrantOptions == rolePolicy.GrantOptions {
				continue
			}
			// Updated: remove the old privileges completely, then grant the new ones.
			queries = append(queries, revokeAllSchemaPolicy(rolePolicy, schemaName)...)
			queries = append(queries, newPolicy.Grants(schemaName)...)
			continue
		}

		// Dropped. The PUBLIC role can not be DROP'ed, therefore we do not need
		// to prevent revoking against it not existing.
		if rolePolicy.Role != "" {
			var foundUser bool
			err := txn.QueryRow(`SELECT TRUE FROM pg_catalog.pg_roles WHERE rolname = $1`, rolePolicy.Role).Scan(&foundUser)
			switch {
			case err == sql.ErrNoRows:
				// Don't execute this role's REVOKEs because the role
				// was dropped first and therefore doesn't exist.
				continue
			case err != nil:
				return fmt.Errorf("error reading schema: %w", err)
			}
		}
		queries = append(queries, revokeAllSchemaPolicy(rolePolicy, schemaName)...)
	}

	for key, rolePolicy := range newPolicies {
		if _, ok := oldPolicies[key]; !ok {
			queries = append(queries, rolePolicy.Grants(schemaName)...)
		}
	}

	rolesToGrant := []string{}
	if owner != "" {
		rolesToGrant = append(rolesToGrant, owner)
	}

	return withRolesGranted(txn, rolesToGrant, func() error {
		for _, query := range queries {
			if _, err := txn.Exec(query); err != nil {
				return fmt.Errorf("error updating schema DCL: %w", err)
			}
		}
		return nil
	})
}

// revokeAllSchemaPolicy returns the queries which remove the privileges of the
// policy completely. Revokes alone would only remove the grant option of the
// privileges granted WITH GRANT OPTION (REVOKE GRANT OPTION FOR).
func revokeAllSchemaPolicy(rolePolicy acl.Schema, schemaName string) []string {
	rolePolicy.GrantOptions = 0
	return rolePolicy.Revokes(schemaName)
}

// readSchemaPolicies returns the policy blocks to store in the state: the inverse
// of what Create and Update apply. Only the roles (and PUBLIC, the empty role) which
// already have a policy block are looked at, so that privileges given to other roles
// (or by postgresql_grant) never appear as drift, and so is the owner, whose
// privileges are implicit (a new schema has a NULL nspacl). For each role, the blocks
// of the state are kept as long as their cumulated privileges are the ones of the
// catalog; otherwise they are replaced by one block which describes the catalog.
func readSchemaPolicies(d *schema.ResourceData, owner string, actual map[string]acl.Schema) []any {
	current, ok := d.Get(schemaPolicyAttr).(*schema.Set)
	if !ok {
		return nil
	}

	roles := []string{}
	blocks := map[string][]any{}
	declared := map[string]acl.Schema{}
	for _, raw := range current.List() {
		block := raw.(map[string]any)
		rolePolicy := schemaPolicyToACL(block)
		role := rolePolicy.Role
		if _, seen := declared[role]; !seen {
			roles = append(roles, role)
		}
		declared[role] = declared[role].Merge(rolePolicy)
		blocks[role] = append(blocks[role], block)
	}

	result := make([]any, 0, len(current.List()))
	for _, role := range roles {
		have := actual[role]
		if role == owner || (declared[role].Privileges == have.Privileges && declared[role].GrantOptions == have.GrantOptions) {
			result = append(result, blocks[role]...)
			continue
		}
		result = append(result, map[string]any{
			schemaPolicyRoleAttr:            role,
			schemaPolicyCreateAttr:          have.GetPrivilege(acl.Create) && !have.GetGrantOption(acl.Create),
			schemaPolicyCreateWithGrantAttr: have.GetGrantOption(acl.Create),
			schemaPolicyUsageAttr:           have.GetPrivilege(acl.Usage) && !have.GetGrantOption(acl.Usage),
			schemaPolicyUsageWithGrantAttr:  have.GetGrantOption(acl.Usage),
		})
	}

	return result
}

// mergeSchemaPolicies cumulates the policy blocks of each role (see the NOTE on
// policy in the documentation), keyed by lowercase role like Create does.
func mergeSchemaPolicies(policies []any) map[string]acl.Schema {
	merged := make(map[string]acl.Schema, len(policies))
	for _, v := range policies {
		rolePolicy := schemaPolicyToACL(v.(map[string]any))
		key := strings.ToLower(rolePolicy.Role)
		merged[key] = merged[key].Merge(rolePolicy)
	}
	return merged
}

func schemaPolicyToACL(policyMap map[string]any) acl.Schema {
	var rolePolicy acl.Schema

	if policyMap[schemaPolicyCreateAttr].(bool) {
		rolePolicy.Privileges |= acl.Create
	}

	if policyMap[schemaPolicyCreateWithGrantAttr].(bool) {
		rolePolicy.Privileges |= acl.Create
		rolePolicy.GrantOptions |= acl.Create
	}

	if policyMap[schemaPolicyUsageAttr].(bool) {
		rolePolicy.Privileges |= acl.Usage
	}

	if policyMap[schemaPolicyUsageWithGrantAttr].(bool) {
		rolePolicy.Privileges |= acl.Usage
		rolePolicy.GrantOptions |= acl.Usage
	}

	if roleRaw, ok := policyMap[schemaPolicyRoleAttr]; ok {
		rolePolicy.Role = roleRaw.(string)
	}

	return rolePolicy
}

func generateSchemaID(d *schema.ResourceData, databaseName string) string {
	SchemaID := strings.Join([]string{
		getDatabase(d, databaseName),
		d.Get(schemaNameAttr).(string),
	}, ".")

	return SchemaID
}

func getDBSchemaName(d *schema.ResourceData, databaseName string) (string, string, error) {
	database := getDatabase(d, databaseName)
	schemaName := d.Get(schemaNameAttr).(string)

	// When importing, we have to parse the ID to find schema and database names.
	if schemaName == "" {
		parsed := strings.Split(d.Id(), ".")
		if len(parsed) != 2 {
			return "", "", fmt.Errorf("schema ID %s has not the expected format 'database.schema': %v", d.Id(), parsed)
		}
		database = parsed[0]
		schemaName = parsed[1]
	}
	return database, schemaName, nil
}
