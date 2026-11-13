package postgresql

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	// Use Postgres as SQL driver
	"github.com/lib/pq"
)

var allowedObjectTypes = []string{
	"database",
	"function",
	"procedure",
	"routine",
	"schema",
	"sequence",
	"table",
	"foreign_data_wrapper",
	"foreign_server",
	"column",
	"domain",
	"type",
	"language",
	"large_object",
	"parameter",
	"tablespace",
}

// objectTypesWithoutSchema are the object types which do not belong to a schema: the statement
// names them directly (databases, foreign data wrappers, servers, languages, tablespaces, parameters)
// or by their OID (large objects).
var objectTypesWithoutSchema = []string{"database", "foreign_data_wrapper", "foreign_server", "language", "large_object", "parameter", "tablespace"}

// namedObjectsKeywords are the object types which have no ALL ... IN SCHEMA form: their objects
// have to be listed in `objects`, and the keyword of the statement is given here.
var namedObjectsKeywords = map[string]string{
	"domain":       "DOMAIN",
	"language":     "LANGUAGE",
	"large_object": "LARGE OBJECT",
	"parameter":    "PARAMETER",
	"tablespace":   "TABLESPACE",
	"type":         "TYPE",
}

var objectTypes = map[string]string{
	"table":    "r",
	"sequence": "S",
	"function": "f",
	"routine":  "f",
	"schema":   "n",
}

type ResourceSchemeGetter func(string) any

func resourcePostgreSQLGrant() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLGrantCreate),
		Update: PGResourceFunc(resourcePostgreSQLGrantUpdate),
		Read:   PGResourceFunc(resourcePostgreSQLGrantRead),
		Delete: PGResourceFunc(resourcePostgreSQLGrantDelete),

		Schema: map[string]*schema.Schema{
			"role": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the role to grant privileges on",
			},
			"database": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The database to grant privileges on for this role",
			},
			"schema": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "The database schema to grant privileges on for this role",
			},
			"object_type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(allowedObjectTypes, false),
				Description:  "The PostgreSQL object type to grant the privileges on (one of: " + strings.Join(allowedObjectTypes, ", ") + ")",
			},
			"objects": {
				Type:        schema.TypeSet,
				Optional:    true,
				ForceNew:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Set:         schema.HashString,
				Description: "The specific objects to grant privileges on for this role (empty means all objects of the requested type)",
			},
			"columns": {
				Type:        schema.TypeSet,
				Optional:    true,
				ForceNew:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Set:         schema.HashString,
				Description: "The specific columns to grant privileges on for this role",
			},
			"privileges": {
				Type:        schema.TypeSet,
				Required:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Set:         schema.HashString,
				Description: "The list of privileges to grant",
			},
			"granted_by": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "The role recorded as the grantor of the privileges (PostgreSQL 19 or above); only the privileges granted by this role are managed",
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

func resourcePostgreSQLGrantRead(db *DBConnection, d *schema.ResourceData) error {
	if err := validateFeatureSupport(db, d); err != nil {
		return fmt.Errorf("feature is not supported: %v", err)
	}

	exists, err := checkRoleDBSchemaExists(db, d)
	if err != nil {
		return err
	}
	if !exists {
		d.SetId("")
		return nil
	}
	d.SetId(generateGrantID(d))

	txn, err := startTransaction(db.client, d.Get("database").(string))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	return readRolePrivileges(txn, db, d)
}

func resourcePostgreSQLGrantCreate(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLGrantCreateOrUpdate(db, d, false)
}

func resourcePostgreSQLGrantUpdate(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLGrantCreateOrUpdate(db, d, true)
}

func resourcePostgreSQLGrantCreateOrUpdate(db *DBConnection, d *schema.ResourceData, usePrevious bool) error {
	if err := validateFeatureSupport(db, d); err != nil {
		return fmt.Errorf("feature is not supported: %v", err)
	}

	// Validate parameters.
	objectType := d.Get("object_type").(string)
	if d.Get("schema").(string) == "" && !sliceContainsStr(objectTypesWithoutSchema, objectType) {
		return fmt.Errorf("parameter 'schema' is mandatory for postgresql_grant resource")
	}
	if _, named := namedObjectsKeywords[objectType]; named && d.Get("objects").(*schema.Set).Len() == 0 {
		return fmt.Errorf("must specify `objects` when `object_type` is `%s`: PostgreSQL has no ALL form for this object type", objectType)
	}
	if objectType == "large_object" {
		for _, oid := range d.Get("objects").(*schema.Set).List() {
			if _, err := strconv.ParseUint(oid.(string), 10, 32); err != nil {
				return fmt.Errorf("the `objects` of `object_type` `large_object` must be large object OIDs, not %q", oid)
			}
		}
	}
	if d.Get("objects").(*schema.Set).Len() > 0 && (objectType == "database" || objectType == "schema") {
		return fmt.Errorf("cannot specify `objects` when `object_type` is `database` or `schema`")
	}
	if d.Get("columns").(*schema.Set).Len() > 0 && (objectType != "column") {
		return fmt.Errorf("cannot specify `columns` when `object_type` is not `column`")
	}
	if d.Get("columns").(*schema.Set).Len() == 0 && (objectType == "column") {
		return fmt.Errorf("must specify `columns` when `object_type` is `column`")
	}
	if d.Get("privileges").(*schema.Set).Len() != 1 && (objectType == "column") {
		return fmt.Errorf("must specify exactly 1 `privileges` when `object_type` is `column`")
	}
	if (d.Get("objects").(*schema.Set).Len() != 1) && (objectType == "column") {
		return fmt.Errorf("must specify exactly 1 table in the `objects` field when `object_type` is `column`")
	}
	if d.Get("objects").(*schema.Set).Len() != 1 && (objectType == "foreign_data_wrapper" || objectType == "foreign_server") {
		return fmt.Errorf("one element must be specified in `objects` when `object_type` is `foreign_data_wrapper` or `foreign_server`")
	}
	if err := validatePrivileges(db, d); err != nil {
		return err
	}
	if d.Get("role").(string) == publicRole && d.Get("with_grant_option").(bool) {
		return fmt.Errorf("with_grant_option cannot be used with role %s: PostgreSQL does not allow to grant options to PUBLIC", publicRole)
	}

	database := d.Get("database").(string)

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	role := d.Get("role").(string)
	if err := pgLockRole(txn, role); err != nil {
		return err
	}

	// The privileges of an object are stored in the row of the object: granting
	// privileges on the same object to several roles at the same time fails with
	// "tuple concurrently updated", so the grants of a database are serialized.
	if err := pgLockDatabase(txn, database); err != nil {
		return err
	}

	owners, err := getRolesToGrant(txn, d)
	if err != nil {
		return err
	}
	if err := withRolesGranted(txn, owners, func() error {
		// Revoke all privileges before granting otherwise reducing privileges will not work.
		// We just have to revoke them in the same transaction so the role will not lose its
		// privileges between the revoke and grant statements.
		if err := revokeRolePrivileges(txn, d, usePrevious); err != nil {
			return err
		}
		if err := grantRolePrivileges(txn, d); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	d.SetId(generateGrantID(d))

	txn, err = startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	return readRolePrivileges(txn, db, d)
}

func resourcePostgreSQLGrantDelete(db *DBConnection, d *schema.ResourceData) error {
	if err := validateFeatureSupport(db, d); err != nil {
		return fmt.Errorf("feature is not supported: %v", err)
	}

	database := d.Get("database").(string)
	txn, err := startTransaction(db.client, database)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	role := d.Get("role").(string)
	if err := pgLockRole(txn, role); err != nil {
		return err
	}

	// See the creation: the grants of a database are serialized.
	if err := pgLockDatabase(txn, database); err != nil {
		return err
	}

	owners, err := getRolesToGrant(txn, d)
	if err != nil {
		return err
	}

	if err := withRolesGranted(txn, owners, func() error {
		return revokeRolePrivileges(txn, d, false)
	}); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("could not commit transaction: %w", err)
	}

	return nil
}

func readDatabaseRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, roleOID uint32) error {
	dbName := d.Get("database").(string)
	query := `
SELECT array_agg(privilege_type)
FROM (
	SELECT (aclexplode(datacl)).* FROM pg_database WHERE datname=$1
) as privileges
WHERE grantee = $2` + grantorClause(d, "") + `
`

	var privileges pq.ByteaArray
	if err := txn.QueryRow(query, dbName, roleOID).Scan(&privileges); err != nil {
		return fmt.Errorf("could not read privileges for database %s: %w", dbName, err)
	}
	granted := pgArrayToSet(privileges)
	if !resourcePrivilegesEqual(granted, db, d) {
		return d.Set("privileges", granted)
	}
	return nil
}

func readSchemaRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, roleOID uint32) error {
	dbName := d.Get("schema").(string)
	query := `
SELECT array_agg(privilege_type)
FROM (
	SELECT (aclexplode(nspacl)).* FROM pg_namespace WHERE nspname=$1
) as privileges
WHERE grantee = $2` + grantorClause(d, "") + `
`

	var privileges pq.ByteaArray
	if err := txn.QueryRow(query, dbName, roleOID).Scan(&privileges); err != nil {
		return fmt.Errorf("could not read privileges for schema %s: %w", dbName, err)
	}

	granted := pgArrayToSet(privileges)
	if !resourcePrivilegesEqual(granted, db, d) {
		return d.Set("privileges", granted)
	}
	return nil
}

func readForeignDataWrapperRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, roleOID uint32) error {
	objects := d.Get("objects").(*schema.Set).List()
	fdwName := objects[0].(string)
	query := `
SELECT pg_catalog.array_agg(privilege_type)
FROM (
	SELECT (pg_catalog.aclexplode(fdwacl)).* FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname=$1
) as privileges
WHERE grantee = $2` + grantorClause(d, "") + `
`

	var privileges pq.ByteaArray
	if err := txn.QueryRow(query, fdwName, roleOID).Scan(&privileges); err != nil {
		return fmt.Errorf("could not read privileges for foreign data wrapper %s: %w", fdwName, err)
	}

	granted := pgArrayToSet(privileges)
	if !resourcePrivilegesEqual(granted, db, d) {
		return d.Set("privileges", granted)
	}
	return nil
}

func readForeignServerRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, roleOID uint32) error {
	objects := d.Get("objects").(*schema.Set).List()
	srvName := objects[0].(string)
	query := `
SELECT pg_catalog.array_agg(privilege_type)
FROM (
	SELECT (pg_catalog.aclexplode(srvacl)).* FROM pg_catalog.pg_foreign_server WHERE srvname=$1
) as privileges
WHERE grantee = $2` + grantorClause(d, "") + `
`

	var privileges pq.ByteaArray
	if err := txn.QueryRow(query, srvName, roleOID).Scan(&privileges); err != nil {
		return fmt.Errorf("could not read privileges for foreign server %s: %w", srvName, err)
	}

	granted := pgArrayToSet(privileges)
	if !resourcePrivilegesEqual(granted, db, d) {
		return d.Set("privileges", granted)
	}
	return nil
}

func readColumnRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData) error {
	objects := d.Get("objects").(*schema.Set)

	missingColumns := d.Get("columns").(*schema.Set) // Getting columns from state.
	// If the query returns a column, it is a removed from the missingColumns.

	var rows *sql.Rows

	// The attacl column of pg_attribute contains information only about explicit column grants
	query := `
SELECT relname AS table_name, attname AS column_name, array_agg(privilege_type) AS column_privileges
FROM (SELECT relname, attname, (aclexplode(attacl)).*
      FROM pg_class
               JOIN pg_namespace ON pg_class.relnamespace = pg_namespace.oid
               JOIN pg_attribute ON pg_class.oid = attrelid
      WHERE nspname = $2
        AND relname = $3
        AND relkind = $4)
         AS col_privs
         LEFT JOIN pg_roles ON pg_roles.oid = col_privs.grantee
WHERE COALESCE(rolname, 'public') = $1` + grantorClause(d, "col_privs.") + `
GROUP BY col_privs.relname, col_privs.attname
ORDER BY col_privs.attname
;`
	rows, err := txn.Query(
		query, d.Get("role").(string), d.Get("schema"), objects.List()[0], objectTypes["table"],
	)

	if err != nil {
		return err
	}

	for rows.Next() {
		var objName string
		var colName string
		var privileges pq.ByteaArray

		if err := rows.Scan(&objName, &colName, &privileges); err != nil {
			return err
		}

		if objects.Len() > 0 && !objects.Contains(objName) {
			continue
		}

		if missingColumns.Contains(colName) {
			missingColumns.Remove(colName)
		}

		privilegesSet := pgArrayToSet(privileges)

		if !resourcePrivilegesEqual(privilegesSet, db, d) {
			// If any object doesn't have the same privileges as saved in the state,
			// we return its privileges to force an update.
			log.Printf(
				"[DEBUG] %s %s has not the expected privileges %v for role %s",
				strings.ToTitle("column"), objName, privileges, d.Get("role"),
			)
			d.Set("privileges", privilegesSet)
			break
		}
	}

	if missingColumns.Len() > 0 {
		// If missingColumns is not empty by the end of the result processing loop
		// it means that a column is missing
		remainingColumns := d.Get("columns").(*schema.Set).Difference(missingColumns)
		log.Printf(
			"[DEBUG] Role %s does not have the expected privileges on columns",
			d.Get("role"),
		)
		d.Set("columns", remainingColumns)
	}

	return nil
}

func readRolePrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData) error {
	role := d.Get("role").(string)
	objectType := d.Get("object_type").(string)
	objects := d.Get("objects").(*schema.Set)

	roleOID, err := getRoleOID(txn, role)
	if err != nil {
		return err
	}

	var query string
	var rows *sql.Rows

	switch objectType {
	case "database":
		return readDatabaseRolePrivileges(txn, db, d, roleOID)

	case "schema":
		return readSchemaRolePrivileges(txn, db, d, roleOID)

	case "foreign_data_wrapper":
		return readForeignDataWrapperRolePrivileges(txn, db, d, roleOID)

	case "foreign_server":
		return readForeignServerRolePrivileges(txn, db, d, roleOID)

	case "function", "procedure", "routine":
		// The objects given with their argument types are read one by one, by their OID.
		if drifted, err := readFunctionSignaturesPrivileges(txn, db, d, roleOID); err != nil || drifted {
			return err
		}
		query = `
SELECT pg_proc.proname, array_remove(array_agg(privilege_type), NULL)
FROM pg_proc
JOIN pg_namespace ON pg_namespace.oid = pg_proc.pronamespace
LEFT JOIN (
    select acls.*
    from (
             SELECT proname, pronamespace, (aclexplode(proacl)).* FROM pg_proc
         ) acls
    WHERE grantee = $1` + grantorClause(d, "") + `
) privs
USING (proname, pronamespace)
      WHERE nspname = $2
GROUP BY pg_proc.proname
`
		rows, err = txn.Query(
			query, roleOID, d.Get("schema"),
		)

	case "column":
		return readColumnRolePrivileges(txn, db, d)

	case "type", "domain":
		query = `
SELECT pg_type.typname, array_remove(array_agg(privs.privilege_type), NULL)
FROM pg_type
JOIN pg_namespace ON pg_namespace.oid = pg_type.typnamespace
LEFT JOIN (SELECT oid, (aclexplode(typacl)).* FROM pg_type) privs
	ON privs.oid = pg_type.oid AND privs.grantee = $1` + grantorClause(d, "privs.") + `
WHERE pg_namespace.nspname = $2 AND pg_type.typname = ANY($3)
GROUP BY pg_type.oid, pg_type.typname
`
		rows, err = txn.Query(query, roleOID, d.Get("schema"), pq.StringArray(setToStrings(objects)))

	case "language":
		query = `
SELECT pg_language.lanname, array_remove(array_agg(privs.privilege_type), NULL)
FROM pg_language
LEFT JOIN (SELECT lanname, (aclexplode(lanacl)).* FROM pg_language) privs
	ON privs.lanname = pg_language.lanname AND privs.grantee = $1` + grantorClause(d, "privs.") + `
WHERE pg_language.lanname = ANY($2)
GROUP BY pg_language.oid, pg_language.lanname
`
		rows, err = txn.Query(query, roleOID, pq.StringArray(setToStrings(objects)))

	case "tablespace":
		query = `
SELECT pg_tablespace.spcname, array_remove(array_agg(privs.privilege_type), NULL)
FROM pg_tablespace
LEFT JOIN (SELECT spcname, (aclexplode(spcacl)).* FROM pg_tablespace) privs
	ON privs.spcname = pg_tablespace.spcname AND privs.grantee = $1` + grantorClause(d, "privs.") + `
WHERE pg_tablespace.spcname = ANY($2)
GROUP BY pg_tablespace.oid, pg_tablespace.spcname
`
		rows, err = txn.Query(query, roleOID, pq.StringArray(setToStrings(objects)))

	case "large_object":
		query = `
SELECT pg_largeobject_metadata.oid::text, array_remove(array_agg(privs.privilege_type), NULL)
FROM pg_largeobject_metadata
LEFT JOIN (SELECT oid, (aclexplode(lomacl)).* FROM pg_largeobject_metadata) privs
	ON privs.oid = pg_largeobject_metadata.oid AND privs.grantee = $1` + grantorClause(d, "privs.") + `
WHERE pg_largeobject_metadata.oid::text = ANY($2)
GROUP BY pg_largeobject_metadata.oid
`
		rows, err = txn.Query(query, roleOID, pq.StringArray(setToStrings(objects)))

	case "parameter":
		// A parameter without privileges has no row in pg_parameter_acl: the parameters are read from the objects.
		query = `
SELECT params.name, array_remove(array_agg(privs.privilege_type), NULL)
FROM unnest($2::text[]) AS params(name)
LEFT JOIN (SELECT parname, (aclexplode(paracl)).* FROM pg_parameter_acl) privs
	ON privs.parname = params.name AND privs.grantee = $1` + grantorClause(d, "privs.") + `
GROUP BY params.name
`
		rows, err = txn.Query(query, roleOID, pq.StringArray(setToStrings(objects)))

	default:
		query = `
SELECT pg_class.relname, array_remove(array_agg(privilege_type), NULL)
FROM pg_class
JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
LEFT JOIN (
    SELECT acls.* FROM (
        SELECT relname, relnamespace, relkind, (aclexplode(relacl)).* FROM pg_class c
    ) as acls
    WHERE grantee=$1` + grantorClause(d, "") + `
) privs
USING (relname, relnamespace, relkind)
WHERE nspname = $2 AND relkind = $3
GROUP BY pg_class.relname
`
		rows, err = txn.Query(
			query, roleOID, d.Get("schema"), objectTypes[objectType],
		)
	}

	// This returns, for the specified role (rolname),
	// the list of all object of the specified type (relkind) in the specified schema (namespace)
	// with the list of the currently applied privileges (aggregation of privilege_type)
	//
	// Our goal is to check that every object has the same privileges as saved in the state.
	if err != nil {
		return err
	}

	for rows.Next() {
		var objName string
		var privileges pq.ByteaArray

		if err := rows.Scan(&objName, &privileges); err != nil {
			return err
		}

		if objects.Len() > 0 && !objects.Contains(objName) {
			continue
		}

		privilegesSet := pgArrayToSet(privileges)
		if !resourcePrivilegesEqual(privilegesSet, db, d) {
			// If any object doesn't have the same privileges as saved in the state,
			// we return its privileges to force an update.
			log.Printf(
				"[DEBUG] %s %s has not the expected privileges %v for role %s",
				strings.ToTitle(objectType), objName, privileges, d.Get("role"),
			)
			d.Set("privileges", privilegesSet)
			break
		}
	}

	return nil
}

// grantorClause restricts the ACL entries of a query to the ones of the grantor `granted_by`, when it is set:
// the entries recorded for another grantor are not managed by the resource. prefix is the alias of the exploded ACL.
func grantorClause(d *schema.ResourceData, prefix string) string {
	grantedBy := d.Get("granted_by").(string)
	if grantedBy == "" {
		return ""
	}
	return fmt.Sprintf(" AND %sgrantor = (SELECT oid FROM pg_roles WHERE rolname = %s)", prefix, pq.QuoteLiteral(grantedBy))
}

// readFunctionSignaturesPrivileges compares the privileges of the objects of `objects` which are given with
// their argument types, e.g. f(integer), with the ones of the state. The catalog only knows the bare name of a
// function, which cannot tell two overloads apart, so each signature is resolved to the OID of exactly one
// function and the ACL of this function is read. It returns true when the privileges of an object differ,
// after having set them in the state. The objects given by their name only are read by readRolePrivileges.
func readFunctionSignaturesPrivileges(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, roleOID uint32) (bool, error) {
	query := `
SELECT array_remove(array_agg(privilege_type), NULL)
FROM (SELECT (aclexplode(proacl)).* FROM pg_proc WHERE oid = $1::oid) AS privileges
WHERE grantee = $2` + grantorClause(d, "") + `
`
	for _, object := range d.Get("objects").(*schema.Set).List() {
		signature := object.(string)
		if !strings.Contains(signature, "(") {
			continue
		}
		oid, err := resolveFunctionSignature(txn, d.Get("schema").(string), d.Get("object_type").(string), signature)
		if err != nil {
			return false, err
		}
		if oid == "" {
			log.Printf("[DEBUG] %s %s of schema %s does not exist or could not be resolved", d.Get("object_type"), signature, d.Get("schema"))
			continue
		}

		var privileges pq.ByteaArray
		if err := txn.QueryRow(query, oid, roleOID).Scan(&privileges); err != nil {
			return false, fmt.Errorf("could not read privileges for %s %s: %w", d.Get("object_type"), signature, err)
		}
		granted := pgArrayToSet(privileges)
		if !resourcePrivilegesEqual(granted, db, d) {
			log.Printf("[DEBUG] %s %s has not the expected privileges %v for role %s", d.Get("object_type"), signature, privileges, d.Get("role"))
			return true, d.Set("privileges", granted)
		}
	}
	return false, nil
}

// resolveFunctionSignature returns the OID (as a string) of the function, procedure or routine written
// `name(argument list)` in the schema, or "" when there is none. The argument list is what a GRANT accepts:
// argument modes and names are dropped and PostgreSQL resolves the types, ignoring their modifiers.
func resolveFunctionSignature(txn *sql.Tx, schemaName, objectType, signature string) (string, error) {
	signature = strings.TrimSpace(signature)
	open := strings.Index(signature, "(")
	if open < 0 || !strings.HasSuffix(signature, ")") {
		return "", nil
	}
	name := signature[:open]

	// The OUT arguments are dropped, for a function and for a procedure: GRANT ON PROCEDURE p(IN a int, OUT b int)
	// designates the procedure of the input arguments (a int), not a procedure p(int, int) which could exist too.
	types := []string{}
	outArguments := false
	for _, arg := range splitTopLevel(signature[open+1 : len(signature)-1]) {
		mode, argType, err := parseFunctionArgument(txn, arg)
		if err != nil {
			return "", err
		}
		if argType == "" {
			continue
		}
		if mode == "OUT" {
			outArguments = true
			continue
		}
		types = append(types, argType)
	}

	qualified := fmt.Sprintf("%s.%s(%s)", pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(name), strings.Join(types, ","))
	oid, ok, err := softQueryString(txn, "SELECT to_regprocedure($1)::oid::text", qualified)
	if err != nil || ok {
		return oid, err
	}
	if objectType == "function" || outArguments {
		return "", nil
	}
	// GRANT ON PROCEDURE p(int, int) and GRANT ON ROUTINE p(int, int) also designate p(a int, OUT b int):
	// a list of types without OUT is matched with all the arguments too (PostgreSQL 14 and above).
	allTypes, err := pq.Array(types).Value()
	if err != nil {
		return "", err
	}
	oid, _, err = softQueryString(txn, routineOfAllArgumentsQuery(schemaName, name, objectType), allTypes.(string))
	return oid, err
}

// routineOfAllArgumentsQuery returns the query which finds the procedure (or the routine) whose arguments of
// every mode have the types given as its parameter, an array of names of types.
func routineOfAllArgumentsQuery(schemaName, name, objectType string) string {
	kinds := "'p'"
	if objectType == "routine" {
		kinds = "'p', 'f'"
	}
	return fmt.Sprintf(`
SELECT p.oid::text
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = %s AND p.proname = %s AND p.prokind IN (%s) AND p.proallargtypes IS NOT NULL
  AND ARRAY(
    SELECT a.argtype FROM unnest(p.proallargtypes, p.proargmodes) WITH ORDINALITY AS a(argtype, argmode, position)
    WHERE a.argmode <> 't' ORDER BY a.position
  )::oid[] = $1::regtype[]::oid[]`, pq.QuoteLiteral(schemaName), pq.QuoteLiteral(name), kinds)
}

// splitTopLevel splits a list at the commas which are neither in parentheses or brackets nor in a quoted identifier.
func splitTopLevel(list string) []string {
	var parts []string
	depth, start, quoted := 0, 0, false
	for i, c := range list {
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, list[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(list[start:]) != "" || len(parts) > 0 {
		parts = append(parts, list[start:])
	}
	return parts
}

// firstTokenEnd returns the index of the end of the first word of s, which may be a quoted identifier or
// contain parentheses.
func firstTokenEnd(s string) int {
	depth, quoted := 0, false
	for i, c := range s {
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case (c == ' ' || c == '\t' || c == '\n') && depth == 0:
			return i
		}
	}
	return len(s)
}

// parseFunctionArgument splits `[mode] [name] type` into the upper-cased mode (empty for none) and the type.
func parseFunctionArgument(txn *sql.Tx, arg string) (mode, argType string, err error) {
	arg = strings.TrimSpace(arg)
	if end := firstTokenEnd(arg); end < len(arg) {
		switch word := strings.ToUpper(arg[:end]); word {
		case "IN", "OUT", "INOUT", "VARIADIC":
			mode, arg = word, strings.TrimSpace(arg[end:])
		}
	}
	end := firstTokenEnd(arg)
	if end == len(arg) {
		return mode, arg, nil
	}
	// Two words: either a name and a type, or a type of several words (double precision...).
	if _, ok, err := softQueryString(txn, "SELECT to_regtype($1)::oid::text", arg); err != nil || ok {
		return mode, arg, err
	}
	return mode, strings.TrimSpace(arg[end:]), nil
}

// softQueryString runs a query with one parameter which returns a string, inside a savepoint: ok is
// false when the statement fails, which does not abort the transaction (to_regtype and to_regprocedure raise an
// error for a malformed name).
func softQueryString(txn *sql.Tx, query, arg string) (value string, ok bool, err error) {
	if _, err := txn.Exec("SAVEPOINT tfpg_resolve"); err != nil {
		return "", false, err
	}
	var result sql.NullString
	if qerr := txn.QueryRow(query, arg).Scan(&result); qerr != nil {
		if _, err := txn.Exec("ROLLBACK TO SAVEPOINT tfpg_resolve"); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if _, err := txn.Exec("RELEASE SAVEPOINT tfpg_resolve"); err != nil {
		return "", false, err
	}
	return result.String, result.Valid, nil
}

// namedObjectsClause returns the keyword and the objects of a GRANT or REVOKE statement for the
// object types of namedObjectsKeywords, e.g. TYPE "sch"."t1","sch"."t2" or LARGE OBJECT 16384.
func namedObjectsClause(objectType, schemaName string, objects *schema.Set) string {
	keyword := namedObjectsKeywords[objectType]
	switch objectType {
	case "type", "domain":
		return keyword + " " + setToPgIdentList(schemaName, objects)
	case "large_object":
		// Large objects are identified by their OID, which is a number, not an identifier.
		return keyword + " " + strings.Join(setToStrings(objects), ",")
	default:
		return keyword + " " + setToPgIdentListWithoutSchema(objects)
	}
}

func setToStrings(set *schema.Set) []string {
	values := []string{}
	for _, v := range set.List() {
		values = append(values, v.(string))
	}
	return values
}

func createGrantQuery(d *schema.ResourceData, privileges []string) string {
	var query string

	switch strings.ToUpper(d.Get("object_type").(string)) {
	case "DATABASE":
		query = fmt.Sprintf(
			"GRANT %s ON DATABASE %s TO %s",
			strings.Join(privileges, ","),
			pq.QuoteIdentifier(d.Get("database").(string)),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "SCHEMA":
		query = fmt.Sprintf(
			"GRANT %s ON SCHEMA %s TO %s",
			strings.Join(privileges, ","),
			pq.QuoteIdentifier(d.Get("schema").(string)),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "FOREIGN_DATA_WRAPPER":
		fdwName := d.Get("objects").(*schema.Set).List()[0]
		query = fmt.Sprintf(
			"GRANT %s ON FOREIGN DATA WRAPPER %s TO %s",
			strings.Join(privileges, ","),
			pq.QuoteIdentifier(fdwName.(string)),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "FOREIGN_SERVER":
		srvName := d.Get("objects").(*schema.Set).List()[0]
		query = fmt.Sprintf(
			"GRANT %s ON FOREIGN SERVER %s TO %s",
			strings.Join(privileges, ","),
			pq.QuoteIdentifier(srvName.(string)),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "COLUMN":
		objects := d.Get("objects").(*schema.Set)
		query = fmt.Sprintf(
			"GRANT %s (%s) ON TABLE %s TO %s",
			strings.Join(privileges, ","),
			setToPgIdentListWithoutSchema(d.Get("columns").(*schema.Set)),
			setToPgIdentList(d.Get("schema").(string), objects),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "TYPE", "DOMAIN", "LANGUAGE", "LARGE_OBJECT", "PARAMETER", "TABLESPACE":
		query = fmt.Sprintf(
			"GRANT %s ON %s TO %s",
			strings.Join(privileges, ","),
			namedObjectsClause(d.Get("object_type").(string), d.Get("schema").(string), d.Get("objects").(*schema.Set)),
			pq.QuoteIdentifier(d.Get("role").(string)),
		)
	case "TABLE", "SEQUENCE", "FUNCTION", "PROCEDURE", "ROUTINE":
		objects := d.Get("objects").(*schema.Set)
		if objects.Len() > 0 {
			query = fmt.Sprintf(
				"GRANT %s ON %s %s TO %s",
				strings.Join(privileges, ","),
				strings.ToUpper(d.Get("object_type").(string)),
				setToPgIdentList(d.Get("schema").(string), objects),
				pq.QuoteIdentifier(d.Get("role").(string)),
			)
		} else {
			query = fmt.Sprintf(
				"GRANT %s ON ALL %sS IN SCHEMA %s TO %s",
				strings.Join(privileges, ","),
				strings.ToUpper(d.Get("object_type").(string)),
				pq.QuoteIdentifier(d.Get("schema").(string)),
				pq.QuoteIdentifier(d.Get("role").(string)),
			)
		}
	}

	if d.Get("with_grant_option").(bool) {
		query = query + " WITH GRANT OPTION"
	}
	if grantedBy := d.Get("granted_by").(string); grantedBy != "" {
		query = query + " GRANTED BY " + pq.QuoteIdentifier(grantedBy)
	}

	return query
}

func createRevokeQuery(getter ResourceSchemeGetter) string {
	var query string

	switch strings.ToUpper(getter("object_type").(string)) {
	case "DATABASE":
		query = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON DATABASE %s FROM %s",
			pq.QuoteIdentifier(getter("database").(string)),
			pq.QuoteIdentifier(getter("role").(string)),
		)
	case "SCHEMA":
		query = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON SCHEMA %s FROM %s",
			pq.QuoteIdentifier(getter("schema").(string)),
			pq.QuoteIdentifier(getter("role").(string)),
		)
	case "FOREIGN_DATA_WRAPPER":
		fdwName := getter("objects").(*schema.Set).List()[0]
		query = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON FOREIGN DATA WRAPPER %s FROM %s",
			pq.QuoteIdentifier(fdwName.(string)),
			pq.QuoteIdentifier(getter("role").(string)),
		)
	case "FOREIGN_SERVER":
		srvName := getter("objects").(*schema.Set).List()[0]
		query = fmt.Sprintf(
			"REVOKE ALL PRIVILEGES ON FOREIGN SERVER %s FROM %s",
			pq.QuoteIdentifier(srvName.(string)),
			pq.QuoteIdentifier(getter("role").(string)),
		)
	case "COLUMN":
		objects := getter("objects").(*schema.Set)
		columns := getter("columns").(*schema.Set)
		privileges := getter("privileges").(*schema.Set)
		if privileges.Len() == 0 || columns.Len() == 0 {
			// No privileges to revoke, so don't revoke anything
			query = "SELECT NULL"
		} else {
			query = fmt.Sprintf(
				"REVOKE %s (%s) ON TABLE %s FROM %s",
				setToPgIdentSimpleList(privileges),
				setToPgIdentListWithoutSchema(columns),
				setToPgIdentList(getter("schema").(string), objects),
				pq.QuoteIdentifier(getter("role").(string)),
			)
		}
	case "TYPE", "DOMAIN", "LANGUAGE", "LARGE_OBJECT", "PARAMETER", "TABLESPACE":
		objectsClause := namedObjectsClause(getter("object_type").(string), getter("schema").(string), getter("objects").(*schema.Set))
		privileges := getter("privileges").(*schema.Set)
		if privileges.Len() > 0 {
			query = fmt.Sprintf(
				"REVOKE %s ON %s FROM %s",
				setToPgIdentSimpleList(privileges),
				objectsClause,
				pq.QuoteIdentifier(getter("role").(string)),
			)
		} else {
			query = fmt.Sprintf(
				"REVOKE ALL PRIVILEGES ON %s FROM %s",
				objectsClause,
				pq.QuoteIdentifier(getter("role").(string)),
			)
		}
	case "TABLE", "SEQUENCE", "FUNCTION", "PROCEDURE", "ROUTINE":
		objects := getter("objects").(*schema.Set)
		privileges := getter("privileges").(*schema.Set)
		if objects.Len() > 0 {
			if privileges.Len() > 0 {
				// Revoking specific privileges instead of all privileges
				// to avoid messing with column level grants
				query = fmt.Sprintf(
					"REVOKE %s ON %s %s FROM %s",
					setToPgIdentSimpleList(privileges),
					strings.ToUpper(getter("object_type").(string)),
					setToPgIdentList(getter("schema").(string), objects),
					pq.QuoteIdentifier(getter("role").(string)),
				)
			} else {
				query = fmt.Sprintf(
					"REVOKE ALL PRIVILEGES ON %s %s FROM %s",
					strings.ToUpper(getter("object_type").(string)),
					setToPgIdentList(getter("schema").(string), objects),
					pq.QuoteIdentifier(getter("role").(string)),
				)
			}
		} else {
			query = fmt.Sprintf(
				"REVOKE ALL PRIVILEGES ON ALL %sS IN SCHEMA %s FROM %s",
				strings.ToUpper(getter("object_type").(string)),
				pq.QuoteIdentifier(getter("schema").(string)),
				pq.QuoteIdentifier(getter("role").(string)),
			)
		}
	}

	if grantedBy := getter("granted_by").(string); grantedBy != "" && query != "SELECT NULL" {
		query = query + " GRANTED BY " + pq.QuoteIdentifier(grantedBy)
	}

	return query
}

func grantRolePrivileges(txn *sql.Tx, d *schema.ResourceData) error {
	privileges := []string{}
	for _, priv := range d.Get("privileges").(*schema.Set).List() {
		privileges = append(privileges, priv.(string))
	}

	if len(privileges) == 0 {
		log.Printf("[DEBUG] no privileges to grant for role %s in database: %s,", d.Get("role").(string), d.Get("database"))
		return nil
	}

	query := createGrantQuery(d, privileges)

	_, err := txn.Exec(query)
	return err
}

func revokeRolePrivileges(txn *sql.Tx, d *schema.ResourceData, usePrevious bool) error {
	getter := d.Get

	if usePrevious {
		getter = func(name string) any {
			if d.HasChange(name) {
				old, _ := d.GetChange(name)
				return old
			}

			return d.Get(name)
		}
	}

	query := createRevokeQuery(getter)
	if len(query) == 0 {
		// Query is empty, don't run anything
		return nil
	}
	if _, err := txn.Exec(query); err != nil {
		return fmt.Errorf("could not execute revoke query: %w", err)
	}
	return nil
}

// getNamedObjectsOwners returns the owners of the named objects, which are granted to the connected
// user during the change when it is not a superuser (see withRolesGranted).
func getNamedObjectsOwners(txn *sql.Tx, objectType, schemaName string, objects *schema.Set) ([]string, error) {
	names := pq.StringArray(setToStrings(objects))

	var query string
	var args []any
	switch objectType {
	case "type", "domain":
		query = `
SELECT DISTINCT pg_roles.rolname
FROM pg_type
JOIN pg_namespace ON pg_namespace.oid = pg_type.typnamespace
JOIN pg_roles ON pg_roles.oid = pg_type.typowner
WHERE pg_namespace.nspname = $1 AND pg_type.typname = ANY($2)`
		args = []any{schemaName, names}
	case "language":
		query = `
SELECT DISTINCT pg_roles.rolname
FROM pg_language
JOIN pg_roles ON pg_roles.oid = pg_language.lanowner
WHERE pg_language.lanname = ANY($1)`
		args = []any{names}
	case "tablespace":
		query = `
SELECT DISTINCT pg_roles.rolname
FROM pg_tablespace
JOIN pg_roles ON pg_roles.oid = pg_tablespace.spcowner
WHERE pg_tablespace.spcname = ANY($1)`
		args = []any{names}
	case "large_object":
		query = `
SELECT DISTINCT pg_roles.rolname
FROM pg_largeobject_metadata
JOIN pg_roles ON pg_roles.oid = pg_largeobject_metadata.lomowner
WHERE pg_largeobject_metadata.oid::text = ANY($1)`
		args = []any{names}
	default:
		// Parameters are owned by the bootstrap superuser: there is nothing to grant.
		return []string{}, nil
	}

	rows, err := txn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("error while looking for the owners of %s objects: %w", objectType, err)
	}
	defer func() { _ = rows.Close() }()

	owners := []string{}
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, fmt.Errorf("could not scan the owner of a %s object: %w", objectType, err)
		}
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}

// getRelationsOrFunctionsOwners returns the owners of the named tables, sequences (and the table of column
// grants), functions, procedures and routines: they are not always the owners of the tables of the schema, which
// are the only ones looked for otherwise. A function given with its argument types is resolved to its OID.
func getRelationsOrFunctionsOwners(txn *sql.Tx, objectType, schemaName string, objects *schema.Set) ([]string, error) {
	owners := []string{}
	switch objectType {
	case "table", "sequence", "column":
		rows, err := txn.Query(`
SELECT DISTINCT pg_roles.rolname
FROM pg_class
JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
JOIN pg_roles ON pg_roles.oid = pg_class.relowner
WHERE pg_namespace.nspname = $1 AND pg_class.relname = ANY($2)`, schemaName, pq.StringArray(setToStrings(objects)))
		if err != nil {
			return nil, fmt.Errorf("error while looking for the owners of %s objects: %w", objectType, err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var owner string
			if err := rows.Scan(&owner); err != nil {
				return nil, fmt.Errorf("could not scan the owner of a %s object: %w", objectType, err)
			}
			owners = append(owners, owner)
		}
		return owners, rows.Err()
	case "function", "procedure", "routine":
		for _, object := range objects.List() {
			name := object.(string)
			query := `
SELECT rolname FROM pg_proc
JOIN pg_namespace ON pg_namespace.oid = pronamespace
JOIN pg_roles ON pg_roles.oid = proowner
WHERE nspname = $1 AND proname = $2`
			args := []any{schemaName, name}
			if strings.Contains(name, "(") {
				oid, err := resolveFunctionSignature(txn, schemaName, objectType, name)
				if err != nil {
					return nil, err
				}
				if oid == "" {
					continue
				}
				query = "SELECT rolname FROM pg_proc JOIN pg_roles ON pg_roles.oid = proowner WHERE pg_proc.oid = $1::oid"
				args = []any{oid}
			}
			rows, err := txn.Query(query, args...)
			if err != nil {
				return nil, fmt.Errorf("error while looking for the owner of %s %s: %w", objectType, name, err)
			}
			for rows.Next() {
				var owner string
				if err := rows.Scan(&owner); err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("could not scan the owner of %s %s: %w", objectType, name, err)
				}
				if !sliceContainsStr(owners, owner) {
					owners = append(owners, owner)
				}
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	return owners, nil
}

func checkRoleDBSchemaExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	// Check the database exists
	database := d.Get("database").(string)
	exists, err := dbExists(db, database)
	if err != nil {
		return false, err
	}
	if !exists {
		log.Printf("[DEBUG] database %s does not exists", database)
		return false, nil
	}

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return false, err
	}
	defer deferredRollback(txn)

	// Check the role exists
	role := d.Get("role").(string)
	if role != publicRole {
		exists, err := roleExists(txn, role)
		if err != nil {
			return false, err
		}
		if !exists {
			log.Printf("[DEBUG] role %s does not exists", role)
			return false, nil
		}
	}

	// Check the schema exists (the SQL connection needs to be on the right database)
	pgSchema := d.Get("schema").(string)
	if !sliceContainsStr(objectTypesWithoutSchema, d.Get("object_type").(string)) && pgSchema != "" {
		exists, err = schemaExists(txn, pgSchema)
		if err != nil {
			return false, err
		}
		if !exists {
			log.Printf("[DEBUG] schema %s does not exists", pgSchema)
			return false, nil
		}
	}

	return true, nil
}

func generateGrantID(d *schema.ResourceData) string {
	parts := []string{d.Get("role").(string), d.Get("database").(string)}

	objectType := d.Get("object_type").(string)
	if !sliceContainsStr(objectTypesWithoutSchema, objectType) {
		parts = append(parts, d.Get("schema").(string))
	}
	parts = append(parts, objectType)

	for _, object := range d.Get("objects").(*schema.Set).List() {
		parts = append(parts, object.(string))
	}

	for _, column := range d.Get("columns").(*schema.Set).List() {
		parts = append(parts, column.(string))
	}

	return strings.Join(parts, "_")
}

func getRolesToGrant(txn *sql.Tx, d *schema.ResourceData) ([]string, error) {
	// If user we use for Terraform is not a superuser (e.g.: in RDS)
	// we need to grant owner of the schema and owners of tables in the schema
	// in order to change theirs permissions.
	owners := []string{}
	objectType := d.Get("object_type").(string)

	schemaName := d.Get("schema").(string)

	if objectType == "language" || objectType == "large_object" || objectType == "tablespace" {
		return getNamedObjectsOwners(txn, objectType, schemaName, d.Get("objects").(*schema.Set))
	}

	if sliceContainsStr(objectTypesWithoutSchema, objectType) {
		return owners, nil
	}

	if objectType != "schema" {
		var err error
		owners, err = getTablesOwner(txn, schemaName)
		if err != nil {
			return nil, err
		}
	}

	if objects := d.Get("objects").(*schema.Set); objects.Len() > 0 {
		objectOwners, err := getRelationsOrFunctionsOwners(txn, objectType, schemaName, objects)
		if err != nil {
			return nil, err
		}
		for _, owner := range objectOwners {
			if !sliceContainsStr(owners, owner) {
				owners = append(owners, owner)
			}
		}
	}

	if objectType == "type" || objectType == "domain" {
		typeOwners, err := getNamedObjectsOwners(txn, objectType, schemaName, d.Get("objects").(*schema.Set))
		if err != nil {
			return nil, err
		}
		owners = append(owners, typeOwners...)
	}

	schemaOwner, err := getSchemaOwner(txn, schemaName)
	if err != nil {
		return nil, err
	}
	if !sliceContainsStr(owners, schemaOwner) {
		owners = append(owners, schemaOwner)
	}

	owners, err = resolveOwners(txn, owners)
	if err != nil {
		return nil, err
	}

	return owners, nil
}

func validateFeatureSupport(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePrivileges) {
		return fmt.Errorf(
			"postgresql_grant resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}
	if d.Get("object_type") == "procedure" && !db.featureSupported(featureProcedure) {
		return fmt.Errorf(
			"object type PROCEDURE is not supported for this Postgres version (%s)",
			db.version,
		)
	}
	if d.Get("object_type") == "routine" && !db.featureSupported(featureRoutine) {
		return fmt.Errorf(
			"object type ROUTINE is not supported for this Postgres version (%s)",
			db.version,
		)
	}
	if d.Get("granted_by").(string) != "" && !db.featureSupported(featureGrantedBy) {
		return fmt.Errorf(
			"granted_by is not supported for this Postgres version (%s): GRANTED BY needs PostgreSQL 19 or above",
			db.version,
		)
	}
	if d.Get("object_type") == "parameter" && !db.featureSupported(featureParameterPrivilege) {
		return fmt.Errorf(
			"object type PARAMETER is not supported for this Postgres version (%s)",
			db.version,
		)
	}
	return nil
}
