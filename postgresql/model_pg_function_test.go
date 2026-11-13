package postgresql

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/stretchr/testify/assert"
)

func TestFromResourceData(t *testing.T) {
	d := mockFunctionResourceData(t, PGFunction{
		Name: "increment",
		Body: "BEGIN result = i + 1; END;",
		Args: []PGFunctionArg{
			{
				Name: "i",
				Type: "integer",
			},
			{
				Name: "result",
				Type: "integer",
				Mode: "OUT",
			},
		},
	})

	var pgFunction PGFunction

	err := pgFunction.FromResourceData(d)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, pgFunction, PGFunction{
		Schema:          "public",
		Name:            "increment",
		Returns:         "integer",
		Language:        "plpgsql",
		Parallel:        defaultFunctionParallel,
		Strict:          false,
		SecurityDefiner: false,
		Volatility:      defaultFunctionVolatility,
		Body:            "BEGIN result = i + 1; END;",
		Args: []PGFunctionArg{
			{
				Name: "i",
				Type: "integer",
			},
			{
				Name: "result",
				Type: "integer",
				Mode: "OUT",
			},
		},
	})
}

func TestFromResourceDataWithArguments(t *testing.T) {
	d := mockFunctionResourceData(t, PGFunction{
		Name: "increment",
		Body: "BEGIN result = i + 1; END;",
		Args: []PGFunctionArg{
			{
				Name: "i",
				Type: "integer",
			},
			{
				Name: "result",
				Type: "integer",
				Mode: "OUT",
			},
		},
		Parallel:        "SAFE",
		Strict:          true,
		SecurityDefiner: true,
		Volatility:      "IMMUTABLE",
	})

	var pgFunction PGFunction

	err := pgFunction.FromResourceData(d)
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, pgFunction, PGFunction{
		Schema:          "public",
		Name:            "increment",
		Returns:         "integer",
		Language:        "plpgsql",
		Parallel:        "SAFE",
		Strict:          true,
		SecurityDefiner: true,
		Volatility:      "IMMUTABLE",
		Body:            "BEGIN result = i + 1; END;",
		Args: []PGFunctionArg{
			{
				Name: "i",
				Type: "integer",
			},
			{
				Name: "result",
				Type: "integer",
				Mode: "OUT",
			},
		},
	})
}

func TestPGFunctionParseWithArguments(t *testing.T) {

	var functionDefinition = `
CREATE OR REPLACE FUNCTION public.pg_func_test(showtext boolean, OUT userid oid, default_null integer DEFAULT NULL::integer, simple_default integer DEFAULT 42, long_default character varying DEFAULT 'foo'::character varying)
RETURNS SETOF record
LANGUAGE c
STABLE PARALLEL SAFE STRICT SECURITY DEFINER
AS $function$pg_func_test_body$function$
	`

	var pgFunction PGFunction

	err := pgFunction.Parse(functionDefinition)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, pgFunction, PGFunction{
		Name:            "pg_func_test",
		Schema:          "public",
		Returns:         "SETOF record",
		Language:        "c",
		Parallel:        "SAFE",
		SecurityDefiner: true,
		Strict:          true,
		Volatility:      "STABLE",
		Body:            "pg_func_test_body",
		Args: []PGFunctionArg{
			{
				Mode: "IN",
				Name: "showtext",
				Type: "boolean",
			},
			{
				Mode: "OUT",
				Name: "userid",
				Type: "oid",
			},
			{
				Mode:    "IN",
				Name:    "default_null",
				Type:    "integer",
				Default: "NULL::integer",
			},
			{
				Mode:    "IN",
				Name:    "simple_default",
				Type:    "integer",
				Default: "42",
			},
			{
				Mode:    "IN",
				Name:    "long_default",
				Type:    "character varying",
				Default: "'foo'::character varying",
			},
		},
	})
}

func TestPGFunctionParseWithoutArguments(t *testing.T) {

	var functionDefinition = `
CREATE OR REPLACE FUNCTION public.pg_func_test()
RETURNS SETOF record
LANGUAGE plpgsql
AS $function$
MultiLine Function
$function$
	`

	var pgFunction PGFunction

	err := pgFunction.Parse(functionDefinition)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, pgFunction, PGFunction{
		Name:            "pg_func_test",
		Schema:          "public",
		Returns:         "SETOF record",
		Language:        "plpgsql",
		Parallel:        "UNSAFE",
		SecurityDefiner: false,
		Strict:          false,
		Volatility:      "VOLATILE",
		Body: `
MultiLine Function
`,
		Args: []PGFunctionArg{},
	})
}

func TestPGFunctionArgParseWithDefault(t *testing.T) {

	var functionArgDefinition = `default_null integer DEFAULT NULL::integer`

	var pgFunctionArg PGFunctionArg

	err := pgFunctionArg.Parse(functionArgDefinition)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, pgFunctionArg, PGFunctionArg{
		Mode:    "IN",
		Name:    "default_null",
		Type:    "integer",
		Default: "NULL::integer",
	})
}

func TestPGFunctionArgParseWithoutDefault(t *testing.T) {

	var functionArgDefinition = `num integer`

	var pgFunctionArg PGFunctionArg

	err := pgFunctionArg.Parse(functionArgDefinition)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, pgFunctionArg, PGFunctionArg{
		Mode: "IN",
		Name: "num",
		Type: "integer",
	})
}

func mockFunctionResourceData(t *testing.T, obj PGFunction) *schema.ResourceData {

	state := terraform.InstanceState{}

	state.ID = ""
	// Build the attribute map from ForemanModel
	attributes := make(map[string]any)

	attributes["name"] = obj.Name
	attributes["returns"] = obj.Returns
	attributes["language"] = obj.Language
	attributes["body"] = obj.Body
	attributes["strict"] = obj.Strict
	attributes["security_definer"] = obj.SecurityDefiner
	attributes["parallel"] = obj.Parallel
	attributes["volatility"] = obj.Volatility

	var args []any

	for _, a := range obj.Args {
		args = append(args, map[string]any{
			"type":    a.Type,
			"name":    a.Name,
			"mode":    a.Mode,
			"default": a.Default,
		})
	}

	attributes["arg"] = args

	return schema.TestResourceDataRaw(t, resourcePostgreSQLFunction().Schema, attributes)
}

func TestSplitFunctionArgs(t *testing.T) {
	cases := map[string][]string{
		"a integer, b text": {"a integer", "b text"},
		"a numeric(10,2) DEFAULT round(1.5, 1), b text DEFAULT 'x,y'::text": {"a numeric(10,2) DEFAULT round(1.5, 1)", "b text DEFAULT 'x,y'::text"},
		`"a,b" integer[], c integer`:                                        {`"a,b" integer[]`, "c integer"},
		"integer":                                                           {"integer"},
	}
	for input, expected := range cases {
		assert.Equal(t, expected, splitFunctionArgs(input), input)
	}
}

func TestParseUnnamedArg(t *testing.T) {
	assert.Equal(t, PGFunctionArg{Mode: "IN", Type: "integer"}, parseUnnamedArg("integer", ""))
	assert.Equal(t, PGFunctionArg{Mode: "OUT", Type: "double precision"}, parseUnnamedArg("OUT double precision", ""))
	assert.Equal(t, PGFunctionArg{Mode: "VARIADIC", Type: "integer[]"}, parseUnnamedArg("VARIADIC integer[]", ""))
	assert.Equal(t, PGFunctionArg{Mode: "INOUT", Type: "text", Default: "'a'"}, parseUnnamedArg("INOUT text", "'a'"))
}

func TestFunctionArgDefinitions(t *testing.T) {
	definition := "CREATE OR REPLACE FUNCTION public.f(integer, OUT double precision, a text DEFAULT 'x'::text)\n RETURNS record\n LANGUAGE sql\n"
	assert.Equal(t, []string{"integer", "OUT double precision", "a text"}, functionArgDefinitions(definition))
}

func TestCreateFunctionSQL(t *testing.T) {
	f := PGFunction{
		Schema:     "public",
		Name:       "My Func",
		Returns:    "TABLE(a integer)",
		Language:   "sql",
		Body:       "SELECT 1",
		Volatility: "STABLE",
		Parallel:   "SAFE",
		Strict:     true,
		Leakproof:  true,
		Cost:       5,
		Rows:       10,
		Support:    "pg_catalog.s",
		Transform:  []string{"integer", "text"},
		Parameters: map[string]any{"work_mem": "1MB", "search_path": "a, b"},
	}
	assert.Equal(t, `CREATE FUNCTION "public"."My Func" ()
RETURNS TABLE(a integer)
LANGUAGE sql
TRANSFORM FOR TYPE integer, FOR TYPE text
STABLE
LEAKPROOF
PARALLEL SAFE
STRICT
COST 5
ROWS 10
SUPPORT pg_catalog.s
SET search_path TO 'a', 'b'
SET work_mem TO '1MB'
AS $function$SELECT 1$function$;`, createFunctionSQL(&f, false))

	// a function which does not use the new attributes has the same statement as before
	f = PGFunction{Schema: "public", Name: "f", Returns: "integer", Language: "plpgsql", Body: "BEGIN RETURN 1; END;", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: 100, Rows: 0}
	assert.Equal(t, "CREATE  OR REPLACE FUNCTION \"public\".\"f\" ()\nRETURNS integer\nLANGUAGE plpgsql\nAS $function$BEGIN RETURN 1; END;$function$;", createFunctionSQL(&f, true))

	f = PGFunction{Schema: "public", Name: "f", Returns: "boolean", Language: "c", ObjFile: "$libdir/citext", Body: "citext_eq", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: 1}
	assert.Contains(t, createFunctionSQL(&f, false), "\nAS '$libdir/citext', 'citext_eq';")

	f = PGFunction{Schema: "public", Name: "f", Returns: "integer", Language: "sql", SQLBody: "RETURN 1", Volatility: "VOLATILE", Parallel: "UNSAFE"}
	assert.Contains(t, createFunctionSQL(&f, false), "\nRETURN 1;")

	// COST is left out when it is the default of the language: 1 for c and internal, 100 for the others
	f = PGFunction{Schema: "public", Name: "f", Returns: "boolean", Language: "c", ObjFile: "$libdir/citext", Body: "citext_eq", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: 1}
	assert.NotContains(t, createFunctionSQL(&f, false), "COST")
	f.Cost = 100
	assert.Contains(t, createFunctionSQL(&f, false), "\nCOST 100\n")
	f = PGFunction{Schema: "public", Name: "f", Returns: "integer", Language: "internal", Body: "int4pl", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: 1}
	assert.NotContains(t, createFunctionSQL(&f, false), "COST")
	f = PGFunction{Schema: "public", Name: "f", Returns: "integer", Language: "plpgsql", Body: "BEGIN RETURN 1; END;", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: 1}
	assert.Contains(t, createFunctionSQL(&f, false), "\nCOST 1\n")
	f.Cost = 100
	assert.NotContains(t, createFunctionSQL(&f, false), "COST")
}
