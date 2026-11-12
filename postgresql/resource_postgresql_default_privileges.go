package postgresql

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	// Use Postgres as SQL driver
	"github.com/lib/pq"
)

func resourcePostgreSQLDefaultPrivileges() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLDefaultPrivilegesCreate),
		Update: PGResourceFunc(resourcePostgreSQLDefaultPrivilegesCreate),
		Read:   PGResourceFunc(resourcePostgreSQLDefaultPrivilegesRead),
		Delete: PGResourceFunc(resourcePostgreSQLDefaultPrivilegesDelete),

		Schema: map[string]*schema.Schema{
			"role": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the role to which grant default privileges on",
			},
			"database": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The database to grant default privileges for this role",
			},
			"owner": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Target role for which to alter default privileges.",
			},
			"schema": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "The database schema to set default privileges for this role",
			},
			"object_type": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					"table",
					"sequence",
					"function",
					"routine",
					"type",
					"schema",
					"large_object",
				}, false),
				Description: "The PostgreSQL object type to set the default privileges on (one of: table, sequence, function, routine, type, schema, large_object)",
			},
			"privileges": {
				Type:        schema.TypeSet,
				Required:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Set:         schema.HashString,
				Description: "The list of privileges to apply as default privileges",
			},
			"with_grant_option": {
				Type:        schema.TypeBool,
				Optional:    true,
				ForceNew:    true,
				Default:     false,
				Description: "Permit the grant recipient to grant it to others",
			},
		},
	}
}

// defaultPrivilegesObjectClasses maps object_type to the class keyword of
// ALTER DEFAULT PRIVILEGES and to the pg_default_acl.defaclobjtype code.
var defaultPrivilegesObjectClasses = map[string]struct {
	class  string
	defacl string
}{
	"table":        {"TABLES", "r"},
	"sequence":     {"SEQUENCES", "S"},
	"function":     {"FUNCTIONS", "f"},
	"routine":      {"ROUTINES", "f"},
	"type":         {"TYPES", "T"},
	"schema":       {"SCHEMAS", "n"},
	"large_object": {"LARGE OBJECTS", "L"},
}

// validateDefaultPrivilegesObjectType refuses the object types and schema
// combinations which this Postgres version or PostgreSQL itself does not support.
func validateDefaultPrivilegesObjectType(db *DBConnection, d *schema.ResourceData) error {
	pgSchema := d.Get("schema").(string)
	objectType := d.Get("object_type").(string)

	if objectType == "schema" && pgSchema != "" {
		if !db.featureSupported(featurePrivilegesOnSchemas) {
			return fmt.Errorf(
				"changing default privileges for schemas is not supported for this Postgres version (%s)",
				db.version,
			)
		}
		return fmt.Errorf("cannot specify `schema` when `object_type` is `schema`: PostgreSQL has no default privileges for schemas inside a schema")
	}

	if objectType == "routine" && !db.featureSupported(featureRoutine) {
		return fmt.Errorf(
			"object type ROUTINE is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	if objectType == "large_object" {
		if !db.featureSupported(featureDefaultPrivilegesLargeObjects) {
			return fmt.Errorf(
				"object type LARGE OBJECTS is not supported for this Postgres version (%s)",
				db.version,
			)
		}
		if pgSchema != "" {
			return fmt.Errorf("cannot specify `schema` when `object_type` is `large_object`: large objects have no schema")
		}
	}

	return nil
}

func resourcePostgreSQLDefaultPrivilegesRead(db *DBConnection, d *schema.ResourceData) error {
	if err := validateDefaultPrivilegesObjectType(db, d); err != nil {
		return err
	}

	exists, err := checkRoleDBSchemaExists(db, d)
	if err != nil {
		return err
	}
	if !exists {
		d.SetId("")
		return nil
	}

	txn, err := startTransaction(db.client, d.Get("database").(string))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	return readRoleDefaultPrivileges(txn, db, d)
}

func resourcePostgreSQLDefaultPrivilegesCreate(db *DBConnection, d *schema.ResourceData) error {
	if err := validateDefaultPrivilegesObjectType(db, d); err != nil {
		return err
	}

	if d.Get("with_grant_option").(bool) && strings.ToLower(d.Get("role").(string)) == "public" {
		return fmt.Errorf("with_grant_option cannot be true for role 'public'")
	}

	if err := validatePrivileges(db, d); err != nil {
		return err
	}

	database := d.Get("database").(string)
	owner := d.Get("owner").(string)

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if err := pgLockRole(txn, owner); err != nil {
		return err
	}

	// Needed in order to set the owner of the db if the connection user is not a superuser
	if err := withRolesGranted(txn, []string{owner}, func() error {

		if err = checkDefaultPrivilegesRevokeHasEffect(txn, d); err != nil {
			return err
		}

		// Revoke all privileges before granting otherwise reducing privileges will not work.
		// We just have to revoke them in the same transaction so role will not lose its privileges
		// between revoke and grant.
		if err = revokeRoleDefaultPrivileges(txn, d); err != nil {
			return err
		}

		if err = grantRoleDefaultPrivileges(txn, d); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	d.SetId(generateDefaultPrivilegesID(d))

	txn, err = startTransaction(db.client, d.Get("database").(string))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	return readRoleDefaultPrivileges(txn, db, d)
}

func resourcePostgreSQLDefaultPrivilegesDelete(db *DBConnection, d *schema.ResourceData) error {
	owner := d.Get("owner").(string)
	pgSchema := d.Get("schema").(string)
	objectType := d.Get("object_type").(string)

	if pgSchema != "" && objectType == "schema" && !db.featureSupported(featurePrivilegesOnSchemas) {
		return fmt.Errorf(
			"changing default privileges for schemas is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	txn, err := startTransaction(db.client, d.Get("database").(string))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if err := pgLockRole(txn, owner); err != nil {
		return err
	}

	// Needed in order to set the owner of the db if the connection user is not a superuser
	if err := withRolesGranted(txn, []string{owner}, func() error {
		return revokeRoleDefaultPrivileges(txn, d)
	}); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	return nil
}

func readRoleDefaultPrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData) error {
	role := d.Get("role").(string)
	owner := d.Get("owner").(string)
	pgSchema := d.Get("schema").(string)
	objectType := d.Get("object_type").(string)
	privilegesInput := d.Get("privileges").(*schema.Set).List()

	if err := pgLockRole(txn, owner); err != nil {
		return err
	}

	roleOID, err := getRoleOID(txn, role)
	if err != nil {
		return err
	}
	ownerOID, err := getRoleOID(txn, owner)
	if err != nil {
		return err
	}

	privileges, grantOption, err := readDefaultPrivilegesACL(txn, objectType, pgSchema, ownerOID, roleOID)
	if err != nil {
		return err
	}

	// We consider no privileges as "not exists" unless no privileges were provided as input
	if len(privileges) == 0 {
		log.Printf("[DEBUG] no default privileges for role %s in schema %s", role, pgSchema)
		if len(privilegesInput) != 0 {
			d.SetId("")
			return nil
		}
	}

	privilegesSet := pgArrayToSet(privileges)
	privilegesEqual := resourcePrivilegesEqual(privilegesSet, db, d)

	if !privilegesEqual {
		d.Set("privileges", privilegesSet)
	}
	if len(privileges) != 0 {
		d.Set("with_grant_option", grantOption.Bool)
	}
	d.SetId(generateDefaultPrivilegesID(d))

	return nil
}

// readDefaultPrivilegesACL returns the privileges granted by the owner to the role in one
// row of pg_default_acl, and whether all of them are WITH GRANT OPTION.
//
// Per-schema default privileges are ADDED to the global ones, they do not replace them
// (ALTER DEFAULT PRIVILEGES, Description). A schema row starts empty, so a schema-scoped
// resource reads only its own schema row, and nothing when there is none. The global row
// starts from the built-in default (acldefault) and PostgreSQL stores it only when it
// differs from it: a global resource reads the global row, or acldefault when there is none,
// so that the built-in privileges of PUBLIC (e.g. EXECUTE on functions) and of the owner are seen.
// The grant option is true only if every privilege is granted WITH GRANT OPTION.
func readDefaultPrivilegesACL(txn *sql.Tx, objectType, pgSchema string, ownerOID, roleOID uint32) (pq.ByteaArray, sql.NullBool, error) {
	defacl := defaultPrivilegesObjectClasses[objectType].defacl

	var query string
	args := []interface{}{defacl, ownerOID, roleOID}
	if pgSchema != "" {
		query = `SELECT array_agg(prtype), bool_and(grantable) FROM (
			SELECT (aclexplode(defaclacl)).* FROM pg_default_acl
			WHERE defaclrole = $2 AND defaclobjtype = $1
				AND defaclnamespace = (SELECT oid FROM pg_namespace WHERE nspname = $4)
		) AS t (grantor_oid, grantee_oid, prtype, grantable)
		WHERE grantee_oid = $3 AND grantor_oid = $2;`
		args = append(args, pgSchema)
	} else {
		query = `SELECT array_agg(prtype), bool_and(grantable) FROM (
			SELECT (aclexplode(COALESCE(
				(SELECT defaclacl FROM pg_default_acl WHERE defaclrole = $2 AND defaclobjtype = $1
					AND defaclnamespace = 0),
				acldefault($1::"char", $2)
			))).*
		) AS t (grantor_oid, grantee_oid, prtype, grantable)
		WHERE grantee_oid = $3 AND grantor_oid = $2;`
	}

	var privileges pq.ByteaArray
	var grantOption sql.NullBool
	if err := txn.QueryRow(query, args...).Scan(&privileges, &grantOption); err != nil {
		return nil, grantOption, fmt.Errorf("could not read default privileges: %w", err)
	}
	return privileges, grantOption, nil
}

// checkDefaultPrivilegesRevokeHasEffect refuses a schema-scoped resource with no privileges
// when the role has default privileges only globally or by built-in default: the REVOKE
// IN SCHEMA would be a silent no-op in PostgreSQL, which says in ALTER DEFAULT PRIVILEGES:
// "This means you cannot revoke privileges per-schema if they are granted globally (either by
// default or according to an ALTER DEFAULT PRIVILEGES command that does not specify a schema)."
func checkDefaultPrivilegesRevokeHasEffect(txn *sql.Tx, d *schema.ResourceData) error {
	pgSchema := d.Get("schema").(string)
	if pgSchema == "" || d.Get("privileges").(*schema.Set).Len() != 0 {
		return nil
	}
	objectType := d.Get("object_type").(string)

	roleOID, err := getRoleOID(txn, d.Get("role").(string))
	if err != nil {
		return err
	}
	ownerOID, err := getRoleOID(txn, d.Get("owner").(string))
	if err != nil {
		return err
	}

	inSchema, _, err := readDefaultPrivilegesACL(txn, objectType, pgSchema, ownerOID, roleOID)
	if err != nil {
		return err
	}
	if len(inSchema) != 0 {
		return nil
	}
	global, _, err := readDefaultPrivilegesACL(txn, objectType, "", ownerOID, roleOID)
	if err != nil {
		return err
	}
	if len(global) == 0 {
		return nil
	}

	return fmt.Errorf(
		"role %q has default privileges on %s created by %q which are not granted in schema %q "+
			"(they are granted globally or by the built-in default): a revoke IN SCHEMA has no effect on them, "+
			"because PostgreSQL adds the privileges of a schema to the global ones and \"cannot revoke privileges per-schema "+
			"if they are granted globally\". Use a postgresql_default_privileges resource without `schema` to change them",
		d.Get("role").(string), defaultPrivilegesObjectClasses[objectType].class, d.Get("owner").(string), pgSchema,
	)
}

func grantRoleDefaultPrivileges(txn *sql.Tx, d *schema.ResourceData) error {
	role := d.Get("role").(string)
	pgSchema := d.Get("schema").(string)

	privileges := []string{}
	for _, priv := range d.Get("privileges").(*schema.Set).List() {
		privileges = append(privileges, priv.(string))
	}

	if len(privileges) == 0 {
		log.Printf("[DEBUG] no default privileges to grant for role %s, owner %s in database: %s,", d.Get("role").(string), d.Get("owner").(string), d.Get("database").(string))
		return nil
	}

	var inSchema string

	// If a schema is specified we need to build the part of the query string to action this
	if pgSchema != "" {
		inSchema = fmt.Sprintf("IN SCHEMA %s", pq.QuoteIdentifier(pgSchema))
	}

	query := fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s %s GRANT %s ON %s TO %s",
		pq.QuoteIdentifier(d.Get("owner").(string)),
		inSchema,
		strings.Join(privileges, ","),
		defaultPrivilegesObjectClasses[d.Get("object_type").(string)].class,
		pq.QuoteIdentifier(role),
	)

	if d.Get("with_grant_option").(bool) {
		query = query + " WITH GRANT OPTION"
	}

	_, err := txn.Exec(
		query,
	)
	if err != nil {
		return fmt.Errorf("could not alter default privileges: %w", err)
	}

	return nil
}

func revokeRoleDefaultPrivileges(txn *sql.Tx, d *schema.ResourceData) error {
	pgSchema := d.Get("schema").(string)

	var inSchema string

	// If a schema is specified we need to build the part of the query string to action this
	if pgSchema != "" {
		inSchema = fmt.Sprintf("IN SCHEMA %s", pq.QuoteIdentifier(pgSchema))
	}
	query := fmt.Sprintf(
		"ALTER DEFAULT PRIVILEGES FOR ROLE %s %s REVOKE ALL ON %s FROM %s",
		pq.QuoteIdentifier(d.Get("owner").(string)),
		inSchema,
		defaultPrivilegesObjectClasses[d.Get("object_type").(string)].class,
		pq.QuoteIdentifier(d.Get("role").(string)),
	)

	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not revoke default privileges: %w", err)
	}
	return nil
}

func generateDefaultPrivilegesID(d *schema.ResourceData) string {
	pgSchema := d.Get("schema").(string)
	if pgSchema == "" {
		pgSchema = "noschema"
	}

	return strings.Join([]string{
		d.Get("role").(string), d.Get("database").(string), pgSchema,
		d.Get("owner").(string), d.Get("object_type").(string),
	}, "_")

}
