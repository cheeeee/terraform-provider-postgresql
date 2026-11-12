package postgresql

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/lib/pq"
)

const (
	funcNameAttr            = "name"
	funcSchemaAttr          = "schema"
	funcBodyAttr            = "body"
	funcArgAttr             = "arg"
	funcLanguageAttr        = "language"
	funcReturnsAttr         = "returns"
	funcDropCascadeAttr     = "drop_cascade"
	funcDatabaseAttr        = "database"
	funcParallelAttr        = "parallel"
	funcSecurityDefinerAttr = "security_definer"
	funcStrictAttr          = "strict"
	funcVolatilityAttr      = "volatility"

	funcSQLBodyAttr             = "sql_body"
	funcSQLBodyDeparsedAttr     = "sql_body_deparsed"
	funcObjFileAttr             = "obj_file"
	funcLeakproofAttr           = "leakproof"
	funcWindowAttr              = "window"
	funcCostAttr                = "cost"
	funcRowsAttr                = "rows"
	funcSupportAttr             = "support"
	funcTransformAttr           = "transform"
	funcParametersAttr          = "parameters"
	funcOwnerAttr               = "owner"
	funcDependsOnExtensionsAttr = "depends_on_extensions"

	funcArgTypeAttr    = "type"
	funcArgNameAttr    = "name"
	funcArgModeAttr    = "mode"
	funcArgDefaultAttr = "default"

	defaultFunctionVolatility = "VOLATILE"
	defaultFunctionParallel   = "UNSAFE"

	// default COST of the functions written in C and internal, and of the other languages
	defaultFunctionCostCompiled = 1
	defaultFunctionCost         = 100
	// default ROWS of the functions which return a set
	defaultFunctionRows = 1000
)

func resourcePostgreSQLFunction() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLFunctionCreate),
		Read:   PGResourceFunc(resourcePostgreSQLFunctionRead),
		Update: PGResourceFunc(resourcePostgreSQLFunctionUpdate),
		Delete: PGResourceFunc(resourcePostgreSQLFunctionDelete),
		Exists: PGResourceExistsFunc(resourcePostgreSQLFunctionExists),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		CustomizeDiff: customizeFunctionDiff,

		Schema: map[string]*schema.Schema{
			funcSchemaAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "Schema where the function is located. If not specified, the provider default schema is used. Changing it moves the function (ALTER FUNCTION ... SET SCHEMA).",

				DiffSuppressFunc: defaultDiffSuppressFunc,
			},
			funcNameAttr: {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Name of the function. Changing it renames the function (ALTER FUNCTION ... RENAME TO).",
			},
			funcArgAttr: {
				Type: schema.TypeList,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						funcArgTypeAttr: {
							Type:        schema.TypeString,
							Description: "The argument type.",
							Required:    true,
							ForceNew:    true,
						},
						funcArgNameAttr: {
							Type:        schema.TypeString,
							Description: "The argument name. The name may be required for some languages or depending on the argument mode.",
							Optional:    true,
							ForceNew:    true,
						},
						funcArgModeAttr: {
							Type:        schema.TypeString,
							Description: "The argument mode. One of: IN, OUT, INOUT, or VARIADIC",
							Optional:    true,
							Default:     "IN",
							ForceNew:    true,

							ValidateFunc: validation.StringInSlice([]string{"IN", "OUT", "INOUT", "VARIADIC"}, false),

							DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
								if (new == "" && old == "IN") || (new == "IN" && old == "") {
									return true
								}
								return old == new
							},
						},
						funcArgDefaultAttr: {
							Type:        schema.TypeString,
							Description: "An expression to be used as default value if the parameter is not specified.",
							Optional:    true,
						},
					},
				},
				Optional:    true,
				ForceNew:    true,
				Description: "Function argument definitions.",
			},
			funcLanguageAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "plpgsql",
				Description: "Language of the function. One of: internal, sql, c, plpgsql",

				DiffSuppressFunc: defaultDiffSuppressFunc,
			},
			funcReturnsAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Computed:    true,
				Description: "Function return type, including SETOF type and TABLE(column type, ...). If not specified, it will be calculated based on the output arguments. PostgreSQL normalizes the type names, write them the way pg_get_functiondef prints them (e.g.: TABLE(id integer, label text)).",

				DiffSuppressFunc: defaultDiffSuppressFunc,
			},
			funcBodyAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "Body of the function. Required, except with sql_body. For the C language, it is the link symbol (it defaults to the name of the function).",

				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return normalizeFunctionBody(new) == old
				},
				StateFunc: func(val any) string {
					return normalizeFunctionBody(val.(string))
				},
			},
			funcDropCascadeAttr: {
				Type:        schema.TypeBool,
				Description: "Automatically drop objects that depend on the function (such as operators or triggers), and in turn all objects that depend on those objects.",
				Optional:    true,
				Default:     false,
			},
			funcParallelAttr: {
				Type:             schema.TypeString,
				Description:      "If the function can be executed in parallel for a single query execution. One of: UNSAFE, RESTRICTED, SAFE",
				Optional:         true,
				Default:          defaultFunctionParallel,
				DiffSuppressFunc: defaultDiffSuppressFunc,
				ValidateFunc:     validation.StringInSlice([]string{"UNSAFE", "RESTRICTED", "SAFE"}, false),
			},
			funcSecurityDefinerAttr: {
				Type:        schema.TypeBool,
				Description: "If the function should execute with the permissions of the function owner instead of the permissions of the caller.",
				Optional:    true,
				Default:     false,
			},
			funcStrictAttr: {
				Type:        schema.TypeBool,
				Description: "If the function should always return NULL if any of it's inputs is NULL.",
				Optional:    true,
				Default:     false,
			},
			funcVolatilityAttr: {
				Type:             schema.TypeString,
				Description:      "Volatility of the function. One of: VOLATILE, STABLE, IMMUTABLE.",
				Optional:         true,
				Default:          defaultFunctionVolatility,
				DiffSuppressFunc: defaultDiffSuppressFunc,
				ValidateFunc:     validation.StringInSlice([]string{"VOLATILE", "STABLE", "IMMUTABLE"}, false),
			},
			funcSQLBodyAttr: {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{funcBodyAttr},
				Description:   "SQL-standard body of a SQL function (PostgreSQL 14+): RETURN expression, or BEGIN ATOMIC ... END. PostgreSQL stores it parsed and returns it deparsed, so the value of the configuration is kept in the state as long as the stored body is the one which has been applied.",
			},
			funcSQLBodyDeparsedAttr: {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The SQL-standard body as PostgreSQL returns it (read only).",
			},
			funcObjFileAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Object file (shared library) of a function written in C: AS 'obj_file', 'link_symbol' (the link symbol is body). Reserved to superusers.",
			},
			funcLeakproofAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "If the function has no side effects and reveals nothing about its arguments except by its result (LEAKPROOF). Reserved to superusers.",
			},
			funcWindowAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				ForceNew:    true,
				Description: "If the function is a window function (WINDOW). Only useful for the functions written in C or internal.",
			},
			funcCostAttr: {
				Type:         schema.TypeFloat,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validateFunctionPositive,
				Description:  "Estimated execution cost of the function, in cost units (COST). PostgreSQL chooses 100 (1 for C and internal functions) if it is not specified.",
			},
			funcRowsAttr: {
				Type:         schema.TypeFloat,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validateFunctionPositive,
				Description:  "Estimated number of rows returned by a function which returns a set (ROWS). PostgreSQL chooses 1000 if it is not specified.",
			},
			funcSupportAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Planner support function of the function (SUPPORT), schema qualified (e.g.: pg_catalog.generate_series_int4_support). Reserved to superusers.",
			},
			funcTransformAttr: {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Types for which the function applies a transform (TRANSFORM FOR TYPE). Write the type names the way PostgreSQL prints them (e.g.: integer, not int4).",
			},
			funcParametersAttr: {
				Type:             schema.TypeMap,
				Optional:         true,
				Elem:             &schema.Schema{Type: schema.TypeString},
				ValidateFunc:     validateConfigParameters(nil),
				DiffSuppressFunc: suppressConfigParametersDiff,
				Description:      "Configuration parameters set while the function runs (SET configuration_parameter TO value). The names must be lowercase. The value of a list parameter such as search_path is a comma separated list.",
			},
			funcOwnerAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				Description: "Role which owns the function (ALTER FUNCTION ... OWNER TO). It is the role of the connection when the function is created and the attribute is not specified.",
			},
			funcDependsOnExtensionsAttr: {
				Type:        schema.TypeSet,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Extensions the function depends on: the function is dropped when one of them is dropped (DEPENDS ON EXTENSION). Removing an extension requires PostgreSQL 13 or later (NO DEPENDS ON EXTENSION).",
			},
			funcDatabaseAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "The database where the function is located. If not specified, the provider default database is used.",

				DiffSuppressFunc: defaultDiffSuppressFunc,
			},
		},
	}
}

// validateFunctionPositive refuses the values which PostgreSQL refuses for COST and ROWS ("must be
// positive"). Zero is refused too: the provider uses 0 for "not set", so a 0 would never reach the catalog.
func validateFunctionPositive(v any, k string) ([]string, []error) {
	if f := v.(float64); f <= 0 {
		return nil, []error{fmt.Errorf("%s must be positive, got %v", k, f)}
	}
	return nil, nil
}

func resourcePostgreSQLFunctionCreate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featureFunction) {
		return fmt.Errorf(
			"postgresql_function resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	var pgFunction PGFunction
	if err := pgFunction.FromResourceData(d); err != nil {
		return err
	}
	if err := checkFunctionFeatures(db, &pgFunction); err != nil {
		return err
	}

	txn, err := startTransaction(db.client, d.Get(funcDatabaseAttr).(string))
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if _, err := txn.Exec(createFunctionSQL(&pgFunction, false)); err != nil {
		return err
	}

	functionID, err := generateFunctionID(db, d)
	if err != nil {
		return err
	}
	_, signature, err := expandFunctionID(functionID, d, db)
	if err != nil {
		return err
	}

	if pgFunction.Owner != "" {
		if err := alterFunction(txn, signature, "OWNER TO "+pq.QuoteIdentifier(pgFunction.Owner)); err != nil {
			return err
		}
	}
	if err := syncFunctionExtensions(db, txn, signature, nil, pgFunction.Extensions); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	return resourcePostgreSQLFunctionReadImpl(db, d, true)
}

// checkFunctionFeatures refuses the clauses which are not supported by the version of the server.
func checkFunctionFeatures(db *DBConnection, pgFunction *PGFunction) error {
	if pgFunction.SQLBody != "" && !db.featureSupported(featureFunctionSQLBody) {
		return fmt.Errorf("the %s attribute (SQL-standard function body) requires PostgreSQL 14 or later (%s)", funcSQLBodyAttr, db.version)
	}
	return nil
}

func resourcePostgreSQLFunctionExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	if !db.featureSupported(featureFunction) {
		return false, fmt.Errorf(
			"postgresql_function resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	functionId := d.Id()

	databaseName, functionSignature, expandErr := expandFunctionID(functionId, d, db)
	if expandErr != nil {
		return false, expandErr
	}

	var functionExists bool

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return false, err
	}
	defer deferredRollback(txn)

	query := fmt.Sprintf("SELECT to_regprocedure('%s') IS NOT NULL AS functionExists", functionSignature)

	if err := txn.QueryRow(query).Scan(&functionExists); err != nil {
		return false, err
	}

	if err := txn.Commit(); err != nil {
		return false, err
	}

	return functionExists, nil
}

func resourcePostgreSQLFunctionRead(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featureFunction) {
		return fmt.Errorf(
			"postgresql_function resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	return resourcePostgreSQLFunctionReadImpl(db, d, false)
}

// functionHeaderEndRegexp finds the line where the definition of a function ends and
// its body begins: AS '...', BEGIN ATOMIC ... END or RETURN expression.
var functionHeaderEndRegexp = regexp.MustCompile(`(?m)^ (?:AS |BEGIN ATOMIC|RETURN )`)

// functionCatalog is what pg_proc says about a function, as opposed to what its definition says.
type functionCatalog struct {
	Definition string
	Window     bool
	Leakproof  bool
	Cost       string
	Rows       string
	Volatility string
	Strict     bool
	Security   bool
	Parallel   string
	Owner      string
	Config     pq.StringArray
	Source     string
	ObjFile    string
	Support    string
	Transform  pq.StringArray
	Extensions pq.StringArray
	ArgNames   pq.StringArray
	SQLBody    string
}

const functionCatalogQuery = `SELECT pg_get_functiondef(p.oid), p.prokind = 'w', p.proleakproof, p.procost::text, p.prorows::text, ` +
	`p.provolatile, p.proisstrict, p.prosecdef, p.proparallel, pg_get_userbyid(p.proowner), ` +
	`COALESCE(p.proconfig, '{}'::text[]), p.prosrc, COALESCE(p.probin, ''), ` +
	`COALESCE((SELECT quote_ident(n.nspname) || '.' || quote_ident(s.proname) FROM pg_proc s JOIN pg_namespace n ON n.oid = s.pronamespace WHERE s.oid = p.prosupport), ''), ` +
	`COALESCE(ARRAY(SELECT format_type(t.oid, NULL) FROM unnest(p.protrftypes) WITH ORDINALITY AS t(oid, n) ORDER BY t.n), '{}'::text[]), ` +
	`COALESCE(ARRAY(SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid = d.refobjid ` +
	`WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'x' ORDER BY e.extname), '{}'::text[]), ` +
	`ARRAY(SELECT COALESCE(p.proargnames[i], '') FROM generate_series(1, COALESCE(array_length(p.proallargtypes, 1), p.pronargs)) AS i ` +
	`WHERE COALESCE(p.proargmodes[i], 'i') <> 't' ORDER BY i), ` +
	`%s ` +
	`FROM pg_proc p WHERE p.oid = to_regprocedure($1)`

func readFunctionCatalog(txn *sql.Tx, db *DBConnection, signature string) (*functionCatalog, error) {
	sqlBody := "''::text"
	if db.featureSupported(featureFunctionSQLBody) {
		sqlBody = "COALESCE(pg_get_function_sqlbody(p.oid), '')"
	}

	c := &functionCatalog{}
	err := txn.QueryRow(fmt.Sprintf(functionCatalogQuery, sqlBody), signature).Scan(
		&c.Definition, &c.Window, &c.Leakproof, &c.Cost, &c.Rows,
		&c.Volatility, &c.Strict, &c.Security, &c.Parallel, &c.Owner,
		&c.Config, &c.Source, &c.ObjFile, &c.Support, &c.Transform,
		&c.Extensions, &c.ArgNames, &c.SQLBody,
	)
	return c, err
}

func resourcePostgreSQLFunctionReadImpl(db *DBConnection, d *schema.ResourceData, applied bool) error {
	functionId := d.Id()

	if functionId == "" {
		// Generate during creation
		generatedFunctionId, err := generateFunctionID(db, d)
		if err != nil {
			return err
		}
		functionId = generatedFunctionId
	}

	databaseName, functionSignature, expandErr := expandFunctionID(functionId, d, db)
	if expandErr != nil {
		return expandErr
	}

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	catalog, err := readFunctionCatalog(txn, db, functionSignature)
	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL function: %s", functionId)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading function: %w", err)
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	// The body is the one of pg_proc: the header of the definition is enough to find the rest
	header := catalog.Definition
	if loc := functionHeaderEndRegexp.FindStringIndex(header); loc != nil {
		header = header[:loc[0]]
	}

	var pgFunction PGFunction

	err = pgFunction.Parse(header + "\n AS $function$$function$")
	if err != nil {
		return err
	}

	var args []map[string]any

	definitions := functionArgDefinitions(header)
	for i, a := range pgFunction.Args {
		if i < len(catalog.ArgNames) && catalog.ArgNames[i] == "" && i < len(definitions) {
			a = parseUnnamedArg(definitions[i], a.Default)
		}
		args = append(args, map[string]any{
			funcArgTypeAttr:    a.Type,
			funcArgNameAttr:    a.Name,
			funcArgModeAttr:    a.Mode,
			funcArgDefaultAttr: a.Default,
		})
	}

	cost, err := strconv.ParseFloat(catalog.Cost, 64)
	if err != nil {
		return fmt.Errorf("could not read the cost of the function: %w", err)
	}
	rows, err := strconv.ParseFloat(catalog.Rows, 64)
	if err != nil {
		return fmt.Errorf("could not read the rows of the function: %w", err)
	}

	volatility := map[string]string{"i": "IMMUTABLE", "s": "STABLE", "v": "VOLATILE"}[catalog.Volatility]
	parallel := map[string]string{"s": "SAFE", "r": "RESTRICTED", "u": "UNSAFE"}[catalog.Parallel]

	d.Set(funcDatabaseAttr, databaseName)
	d.Set(funcNameAttr, pgFunction.Name)
	d.Set(funcSchemaAttr, pgFunction.Schema)
	d.Set(funcLanguageAttr, pgFunction.Language)
	d.Set(funcReturnsAttr, pgFunction.Returns)
	d.Set(funcSecurityDefinerAttr, catalog.Security)
	d.Set(funcStrictAttr, catalog.Strict)
	d.Set(funcParallelAttr, parallel)
	d.Set(funcVolatilityAttr, volatility)
	d.Set(funcArgAttr, args)
	d.Set(funcLeakproofAttr, catalog.Leakproof)
	d.Set(funcWindowAttr, catalog.Window)
	d.Set(funcCostAttr, cost)
	d.Set(funcRowsAttr, rows)
	d.Set(funcSupportAttr, catalog.Support)
	d.Set(funcTransformAttr, []string(catalog.Transform))
	d.Set(funcParametersAttr, parseConfigParameters(catalog.Config, nil))
	d.Set(funcOwnerAttr, catalog.Owner)
	d.Set(funcDependsOnExtensionsAttr, []string(catalog.Extensions))
	d.Set(funcObjFileAttr, catalog.ObjFile)

	if catalog.SQLBody != "" {
		// PostgreSQL returns the body deparsed: the one of the configuration is kept
		// as long as the body has not been changed since it was applied.
		if !applied && catalog.SQLBody != d.Get(funcSQLBodyDeparsedAttr).(string) {
			d.Set(funcSQLBodyAttr, catalog.SQLBody)
		}
		d.Set(funcSQLBodyDeparsedAttr, catalog.SQLBody)
		d.Set(funcBodyAttr, "")
	} else {
		d.Set(funcSQLBodyAttr, "")
		d.Set(funcSQLBodyDeparsedAttr, "")
		d.Set(funcBodyAttr, catalog.Source)
	}

	d.SetId(functionId)

	return nil
}

func resourcePostgreSQLFunctionDelete(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featureFunction) {
		return fmt.Errorf(
			"postgresql_function resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	databaseName, functionSignature, err := expandFunctionID(d.Id(), d, db)
	if err != nil {
		return err
	}

	dropMode := "RESTRICT"
	if v, ok := d.GetOk(funcDropCascadeAttr); ok && v.(bool) {
		dropMode = "CASCADE"
	}

	sql := fmt.Sprintf("DROP FUNCTION IF EXISTS %s %s", functionSignature, dropMode)

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	if _, err := txn.Exec(sql); err != nil {
		return err
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	d.SetId("")

	return nil
}

func resourcePostgreSQLFunctionUpdate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featureFunction) {
		return fmt.Errorf(
			"postgresql_function resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	var pgFunction PGFunction
	if err := pgFunction.FromResourceData(d); err != nil {
		return err
	}
	if err := checkFunctionFeatures(db, &pgFunction); err != nil {
		return err
	}

	databaseName, signature, err := expandFunctionID(d.Id(), d, db)
	if err != nil {
		return err
	}

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return err
	}
	defer deferredRollback(txn)

	// The function is renamed and moved first: the other statements use its new signature
	oldSchema, newSchema := d.GetChange(funcSchemaAttr)
	_, newName := d.GetChange(funcNameAttr)
	if d.HasChange(funcNameAttr) {
		if err := alterFunction(txn, signature, "RENAME TO "+pq.QuoteIdentifier(newName.(string))); err != nil {
			return err
		}
		signature = functionSignature(databaseName, oldSchema.(string), newName.(string), pgFunction.Args)
	}
	if d.HasChange(funcSchemaAttr) {
		if err := alterFunction(txn, signature, "SET SCHEMA "+pq.QuoteIdentifier(newSchema.(string))); err != nil {
			return err
		}
	}
	functionID := buildFunctionID(databaseName, pgFunction.Schema, pgFunction.Name, pgFunction.Args)
	_, signature, err = expandFunctionID(functionID, d, db)
	if err != nil {
		return err
	}

	// What CREATE OR REPLACE FUNCTION is needed for: there is no ALTER FUNCTION for them
	replace := d.HasChange(funcBodyAttr) && pgFunction.SQLBody == "" ||
		d.HasChanges(funcSQLBodyAttr, funcObjFileAttr, funcTransformAttr, funcArgAttr)
	if d.HasChange(funcSupportAttr) {
		// there is no way to remove the support function with ALTER FUNCTION
		if _, v := d.GetChange(funcSupportAttr); v.(string) == "" {
			replace = true
		}
	}

	if replace {
		// it sets all the attributes of the function
		if _, err := txn.Exec(createFunctionSQL(&pgFunction, true)); err != nil {
			return err
		}
	} else if actions := alterFunctionActions(d, &pgFunction); len(actions) > 0 {
		if err := alterFunction(txn, signature, strings.Join(actions, " ")); err != nil {
			return err
		}
	}

	if d.HasChange(funcOwnerAttr) && pgFunction.Owner != "" {
		if err := alterFunction(txn, signature, "OWNER TO "+pq.QuoteIdentifier(pgFunction.Owner)); err != nil {
			return err
		}
	}

	if replace || d.HasChange(funcDependsOnExtensionsAttr) {
		// CREATE OR REPLACE FUNCTION drops the dependencies of the function: the current ones are read
		current, err := readFunctionExtensions(txn, signature)
		if err != nil {
			return err
		}
		if err := syncFunctionExtensions(db, txn, signature, current, pgFunction.Extensions); err != nil {
			return err
		}
	}

	if err := txn.Commit(); err != nil {
		return err
	}

	d.SetId(functionID)

	return resourcePostgreSQLFunctionReadImpl(db, d, true)
}

// alterFunction executes ALTER FUNCTION <signature> <action>
func alterFunction(db QueryAble, signature, action string) error {
	query := fmt.Sprintf("ALTER FUNCTION %s %s", signature, action)
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("could not %s: %w", strings.ToLower(query), err)
	}
	return nil
}

// alterFunctionActions returns the actions of ALTER FUNCTION which go from the state to the configuration
func alterFunctionActions(d *schema.ResourceData, pgFunction *PGFunction) []string {
	var actions []string

	if d.HasChange(funcVolatilityAttr) {
		actions = append(actions, pgFunction.Volatility)
	}
	if d.HasChange(funcStrictAttr) {
		if pgFunction.Strict {
			actions = append(actions, "STRICT")
		} else {
			actions = append(actions, "CALLED ON NULL INPUT")
		}
	}
	if d.HasChange(funcSecurityDefinerAttr) {
		if pgFunction.SecurityDefiner {
			actions = append(actions, "SECURITY DEFINER")
		} else {
			actions = append(actions, "SECURITY INVOKER")
		}
	}
	if d.HasChange(funcParallelAttr) {
		actions = append(actions, "PARALLEL "+pgFunction.Parallel)
	}
	if d.HasChange(funcLeakproofAttr) {
		if pgFunction.Leakproof {
			actions = append(actions, "LEAKPROOF")
		} else {
			actions = append(actions, "NOT LEAKPROOF")
		}
	}
	if d.HasChange(funcCostAttr) {
		actions = append(actions, "COST "+formatFunctionNumber(pgFunction.Cost))
	}
	if d.HasChange(funcRowsAttr) {
		actions = append(actions, "ROWS "+formatFunctionNumber(pgFunction.Rows))
	}
	if d.HasChange(funcSupportAttr) {
		actions = append(actions, "SUPPORT "+pgFunction.Support)
	}
	if d.HasChange(funcParametersAttr) {
		o, n := d.GetChange(funcParametersAttr)
		oldParams, newParams := o.(map[string]any), n.(map[string]any)
		var toReset, toSet []string
		for name := range oldParams {
			if _, ok := newParams[name]; !ok {
				toReset = append(toReset, name)
			}
		}
		for name, value := range newParams {
			if oldParams[name] != value {
				toSet = append(toSet, name)
			}
		}
		sort.Strings(toReset)
		sort.Strings(toSet)
		for _, name := range toReset {
			actions = append(actions, "RESET "+name)
		}
		for _, name := range toSet {
			actions = append(actions, fmt.Sprintf("SET %s TO %s", name, configParameterValue(name, newParams[name].(string))))
		}
	}

	return actions
}

func formatFunctionNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// readFunctionExtensions returns the extensions that a function depends on (DEPENDS ON EXTENSION).
func readFunctionExtensions(txn *sql.Tx, signature string) ([]string, error) {
	var extensions pq.StringArray
	err := txn.QueryRow(
		`SELECT COALESCE(ARRAY(SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid = d.refobjid `+
			`WHERE d.classid = 'pg_proc'::regclass AND d.deptype = 'x' AND d.objid = to_regprocedure($1)), '{}'::text[])`,
		signature,
	).Scan(&extensions)
	return extensions, err
}

// syncFunctionExtensions makes the function depend on the extensions wanted, and only on them.
func syncFunctionExtensions(db *DBConnection, txn QueryAble, signature string, current, wanted []string) error {
	has := func(list []string, name string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, name := range current {
		if has(wanted, name) {
			continue
		}
		if !db.featureSupported(featureFunctionNoDependsOnExtension) {
			return fmt.Errorf("removing the dependency on the extension %s requires PostgreSQL 13 or later (%s)", name, db.version)
		}
		if err := alterFunction(txn, signature, "NO DEPENDS ON EXTENSION "+pq.QuoteIdentifier(name)); err != nil {
			return err
		}
	}
	for _, name := range wanted {
		if has(current, name) {
			continue
		}
		if err := alterFunction(txn, signature, "DEPENDS ON EXTENSION "+pq.QuoteIdentifier(name)); err != nil {
			return err
		}
	}
	return nil
}

// customizeFunctionDiff checks the combinations of attributes which depend on each other.
func customizeFunctionDiff(_ context.Context, d *schema.ResourceDiff, _ any) error {
	language := d.Get(funcLanguageAttr).(string)
	if !d.NewValueKnown(funcLanguageAttr) {
		return nil
	}

	rawBody, _ := d.GetRawConfigAt(cty.GetAttrPath(funcBodyAttr))
	rawSQLBody, _ := d.GetRawConfigAt(cty.GetAttrPath(funcSQLBodyAttr))
	hasBody, hasSQLBody := !rawBody.IsNull(), !rawSQLBody.IsNull()

	switch {
	case hasSQLBody && language != "sql":
		return fmt.Errorf("%s is only for the sql language, not %s", funcSQLBodyAttr, language)
	case !hasBody && !hasSQLBody && language != "c":
		return fmt.Errorf("one of %s or %s is required", funcBodyAttr, funcSQLBodyAttr)
	case language == "c" && d.Get(funcObjFileAttr).(string) == "":
		return fmt.Errorf("%s is required for the c language", funcObjFileAttr)
	case language != "c" && d.Get(funcObjFileAttr).(string) != "":
		return fmt.Errorf("%s is only for the c language, not %s", funcObjFileAttr, language)
	}

	if d.Id() != "" && d.HasChange(funcSQLBodyAttr) {
		return d.SetNewComputed(funcSQLBodyDeparsedAttr)
	}
	return nil
}

// createFunctionSQL returns the CREATE [OR REPLACE] FUNCTION statement of a function
func createFunctionSQL(pgFunction *PGFunction, replace bool) string {
	b := bytes.NewBufferString("CREATE ")

	if replace {
		b.WriteString(" OR REPLACE ")
	}

	b.WriteString("FUNCTION ")

	fmt.Fprint(b, pq.QuoteIdentifier(pgFunction.Schema), ".")

	fmt.Fprint(b, pq.QuoteIdentifier(pgFunction.Name), " (")

	for i, arg := range pgFunction.Args {
		if i > 0 {
			b.WriteRune(',')
		}

		b.WriteString("\n    ")

		if arg.Mode != "" {
			fmt.Fprint(b, arg.Mode, " ")
		}

		if arg.Name != "" {
			fmt.Fprint(b, arg.Name, " ")
		}

		b.WriteString(arg.Type)

		if arg.Default != "" {
			fmt.Fprint(b, " DEFAULT ", arg.Default)
		}
	}

	if len(pgFunction.Args) > 0 {
		b.WriteRune('\n')
	}

	b.WriteString(")")

	fmt.Fprint(b, "\nRETURNS ", pgFunction.Returns)
	fmt.Fprint(b, "\nLANGUAGE ", pgFunction.Language)
	for i, t := range pgFunction.Transform {
		if i == 0 {
			fmt.Fprint(b, "\nTRANSFORM ")
		} else {
			b.WriteString(", ")
		}
		fmt.Fprint(b, "FOR TYPE ", t)
	}
	if pgFunction.Window {
		fmt.Fprint(b, "\nWINDOW")
	}
	if pgFunction.Volatility != defaultFunctionVolatility {
		fmt.Fprint(b, "\n", pgFunction.Volatility)
	}
	if pgFunction.Leakproof {
		fmt.Fprint(b, "\nLEAKPROOF")
	}
	if pgFunction.SecurityDefiner {
		fmt.Fprint(b, "\nSECURITY DEFINER")
	}
	if pgFunction.Parallel != defaultFunctionParallel {
		fmt.Fprint(b, "\nPARALLEL ", pgFunction.Parallel)
	}
	if pgFunction.Strict {
		fmt.Fprint(b, "\nSTRICT")
	}
	defaultCost := float64(defaultFunctionCost)
	if pgFunction.Language == "c" || pgFunction.Language == "internal" {
		defaultCost = defaultFunctionCostCompiled
	}
	if pgFunction.Cost > 0 && pgFunction.Cost != defaultCost {
		fmt.Fprint(b, "\nCOST ", formatFunctionNumber(pgFunction.Cost))
	}
	if pgFunction.Rows > 0 && pgFunction.Rows != defaultFunctionRows {
		fmt.Fprint(b, "\nROWS ", formatFunctionNumber(pgFunction.Rows))
	}
	if pgFunction.Support != "" {
		fmt.Fprint(b, "\nSUPPORT ", pgFunction.Support)
	}
	names := make([]string, 0, len(pgFunction.Parameters))
	for name := range pgFunction.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(b, "\nSET %s TO %s", name, configParameterValue(name, pgFunction.Parameters[name].(string)))
	}

	switch {
	case pgFunction.SQLBody != "":
		fmt.Fprint(b, "\n", pgFunction.SQLBody, ";")
	case pgFunction.ObjFile != "":
		fmt.Fprint(b, "\nAS ", pq.QuoteLiteral(pgFunction.ObjFile))
		if pgFunction.Body != "" {
			fmt.Fprint(b, ", ", pq.QuoteLiteral(pgFunction.Body))
		}
		b.WriteString(";")
	default:
		fmt.Fprint(b, "\nAS $function$", pgFunction.Body, "$function$;")
	}

	return b.String()
}

func generateFunctionID(db *DBConnection, d *schema.ResourceData) (string, error) {
	databaseName := db.client.databaseName
	if dbAttr, ok := d.GetOk(funcDatabaseAttr); ok {
		databaseName = dbAttr.(string)
	}

	var pgFunction PGFunction
	err := pgFunction.FromResourceData(d)
	if err != nil {
		return "", err
	}

	return buildFunctionID(databaseName, pgFunction.Schema, pgFunction.Name, pgFunction.Args), nil
}

// buildFunctionID returns the identifier of a function: database.schema.name(types of the arguments which are not OUT)
func buildFunctionID(databaseName, schemaName, name string, args []PGFunctionArg) string {
	b := bytes.NewBufferString("")

	fmt.Fprint(b, databaseName, ".", schemaName, ".", name, "(")

	argCount := 0

	for _, arg := range args {
		mode := "IN"
		if arg.Mode != "" {
			mode = arg.Mode
		}

		if mode != "OUT" {
			if argCount > 0 {
				b.WriteRune(',')
			}

			b.WriteString(arg.Type)
			argCount += 1
		}
	}

	b.WriteRune(')')

	return b.String()
}

// functionSignature returns the quoted signature (schema.name(types)) of a function
func functionSignature(databaseName, schemaName, name string, args []PGFunctionArg) string {
	_, signature, _ := expandFunctionID(buildFunctionID(databaseName, schemaName, name, args), nil, nil)
	return signature
}

func expandFunctionID(functionId string, d *schema.ResourceData, db *DBConnection) (databaseName string, functionSignature string, err error) {

	partsCount := strings.Count(functionId, ".") + 1

	if partsCount == 2 {
		clientDatabaseName := "postgres"
		if db != nil {
			clientDatabaseName = db.client.databaseName
		}

		signature, err := quoteSignature(functionId)
		if err != nil {
			return "", "", err
		}

		return getDatabase(d, clientDatabaseName), signature, nil
	}

	if partsCount == 3 {
		functionIdParts := strings.Split(functionId, ".")
		signature, err := quoteSignature(strings.Join(functionIdParts[1:], "."))
		if err != nil {
			return "", "", err
		}
		return functionIdParts[0], signature, nil
	}

	return "", "", fmt.Errorf("function ID %s has not the expected format 'database.schema.function_name(arguments)'", functionId)
}

func quoteSignature(s string) (signature string, err error) {

	signatureData := findStringSubmatchMap(`(?si)(?P<Schema>[^\.]+)\.(?P<Name>[^(]+)\((?P<Args>.*)\)`, s)

	schemaName, schemaFound := signatureData["Schema"]
	name, nameFound := signatureData["Name"]
	args, argsFound := signatureData["Args"]
	if schemaFound && nameFound && argsFound {
		return fmt.Sprintf("%s.%s(%s)", pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(name), args), nil
	}

	return "", fmt.Errorf("incorrect signature format \"%s\". The expected format is schema.function_name(arguments)", s)
}
