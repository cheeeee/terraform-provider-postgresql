package postgresql

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccPostgresqlFunction_Basic(t *testing.T) {
	config := `
resource "postgresql_function" "basic_function" {
    name = "basic_function"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.basic_function", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "name", "basic_function"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "language", "plpgsql"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_SpecificDatabase(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffix, teardown := setupTestDatabase(t, true, true)
	defer teardown()

	dbName, _ := getTestDBNames(dbSuffix)

	config := `
resource "postgresql_function" "basic_function" {
    name = "basic_function"
    database = "%s"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(config, dbName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.basic_function", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "name", "basic_function"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "database", dbName),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.basic_function", "language", "plpgsql"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_MultipleArgs(t *testing.T) {
	config := `
resource "postgresql_schema" "test" {
    name = "test"
}

resource "postgresql_function" "increment" {
    schema = postgresql_schema.test.name
    name = "increment"
    arg {
        name = "i"
        type = "integer"
        default = "7"
    }
    arg {
        name = "result"
        type = "integer"
        mode = "OUT"
    }
    language = "plpgsql"
    parallel = "RESTRICTED"
    strict = true
    security_definer = true
    volatility = "STABLE"
    body = <<-EOF
        BEGIN
            result = i + 1;
        END;
    EOF
}
`

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.increment", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "name", "increment"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "schema", "test"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "language", "plpgsql"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "strict", "true"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "parallel", "RESTRICTED"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "security_definer", "true"),
					resource.TestCheckResourceAttr(
						"postgresql_function.increment", "volatility", "STABLE"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Update(t *testing.T) {
	configCreate := `
resource "postgresql_function" "func" {
    name = "func"
    returns = "integer"
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            RETURN 1;
        END;
    EOF
}
`

	configUpdate := `
resource "postgresql_function" "func" {
    name = "func"
    returns = "integer"
    language = "plpgsql"
    volatility = "IMMUTABLE"
    body = <<-EOF
        BEGIN
            RETURN 2;
        END;
    EOF
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCompatibleVersion(t, featureFunction)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: configCreate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.func", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "name", "func"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "volatility", "VOLATILE"),
				),
			},
			{
				Config: configUpdate,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlFunctionExists("postgresql_function.func", ""),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "name", "func"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "schema", "public"),
					resource.TestCheckResourceAttr(
						"postgresql_function.func", "volatility", "IMMUTABLE"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlFunctionExists(n string, database string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		signature := rs.Primary.ID

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, database)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkFunctionExists(txn, signature)

		if err != nil {
			return fmt.Errorf("error checking function %s", err)
		}

		if !exists {
			return fmt.Errorf("Function not found")
		}

		return nil
	}
}

func testAccCheckPostgresqlFunctionDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_function" {
			continue
		}

		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		_, functionSignature, expandErr := expandFunctionID(rs.Primary.ID, nil, nil)

		if expandErr != nil {
			return fmt.Errorf("Incorrect resource Id %s", err)
		}

		exists, err := checkFunctionExists(txn, functionSignature)

		if err != nil {
			return fmt.Errorf("error checking function %s", err)
		}

		if exists {
			return fmt.Errorf("Function still exists after destroy")
		}
	}

	return nil
}

func checkFunctionExists(txn *sql.Tx, signature string) (bool, error) {
	var _rez bool
	err := txn.QueryRow(fmt.Sprintf("SELECT to_regprocedure('%s') IS NOT NULL", signature)).Scan(&_rez)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about function: %s", err)
	}

	return _rez, nil
}

// testAccFunctionProp checks an expression of pg_proc (p) for the function of a resource.
func testAccFunctionProp(n, expr, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		got, err := testAccFunctionQuery(s, n, fmt.Sprintf("SELECT (%s)::text FROM pg_proc p WHERE p.oid = to_regprocedure($1)", expr))
		if err != nil {
			return err
		}
		if got != expected {
			return fmt.Errorf("%s: %s is %q, expected %q", n, expr, got, expected)
		}
		return nil
	}
}

// testAccFunctionQuery runs a query with the signature of the function of a resource as parameter.
func testAccFunctionQuery(s *terraform.State, n, query string) (string, error) {
	rs, ok := s.RootModule().Resources[n]
	if !ok {
		return "", fmt.Errorf("Resource not found: %s", n)
	}
	database, signature, err := expandFunctionID(rs.Primary.ID, nil, nil)
	if err != nil {
		return "", err
	}
	if database == "" {
		database = "postgres"
	}
	client := testAccProvider.Meta().(*Client)
	txn, err := startTransaction(client, database)
	if err != nil {
		return "", err
	}
	defer deferredRollback(txn)

	var got sql.NullString
	if err := txn.QueryRow(query, signature).Scan(&got); err != nil {
		return "", fmt.Errorf("error running %s: %w", query, err)
	}
	return got.String, nil
}

// testAccFunctionCall runs a query (which calls a function) and checks its result.
func testAccFunctionCall(query, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, "")
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		var got sql.NullString
		if err := txn.QueryRow(query).Scan(&got); err != nil {
			return fmt.Errorf("error running %s: %w", query, err)
		}
		if got.String != expected {
			return fmt.Errorf("%s returned %q, expected %q", query, got.String, expected)
		}
		return nil
	}
}

// testAccFunctionOID records the oid of the function of a resource the first time it is called and
// checks that it is still the same ones later: the function has been altered, not replaced.
func testAccFunctionOID(n string, oid *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		got, err := testAccFunctionQuery(s, n, "SELECT p.oid::text FROM pg_proc p WHERE p.oid = to_regprocedure($1)")
		if err != nil {
			return err
		}
		if *oid == "" {
			*oid = got
		} else if *oid != got {
			return fmt.Errorf("%s has been replaced (oid %s, was %s)", n, got, *oid)
		}
		return nil
	}
}

// testAccFunctionExec runs statements in the database, outside of Terraform
func testAccFunctionExec(t *testing.T, statements ...string) {
	config := getTestConfig(t)
	for _, statement := range statements {
		dbExecute(t, config.connStr("postgres"), statement)
	}
}

func testCheckCanCreateSuperuserFunction(t *testing.T) {
	testCheckCanExecute(
		t, "create a leakproof function",
		"CREATE FUNCTION public.tf_probe_function() RETURNS integer LANGUAGE sql LEAKPROOF AS 'SELECT 1'",
	)
}

func testAccFunctionPreCheck(t *testing.T) {
	testAccPreCheck(t)
	testCheckCompatibleVersion(t, featureFunction)
}

func TestAccPostgresqlFunction_Attributes(t *testing.T) {
	config := func(attributes string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "attrs" {
    name = "tf_attrs"
    returns = "integer"
    arg {
        name = "i"
        type = "integer"
    }
    %s
    body = <<-EOF
        BEGIN
            RETURN i + 1;
        END;
    EOF
}
`, attributes)
	}
	const res = "postgresql_function.attrs"
	var oid string

	check := func(props string, volatility, parallel, secdef, strict string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			testAccFunctionOID(res, &oid),
			testAccFunctionProp(res, "concat_ws(',', p.provolatile, p.proparallel, p.prosecdef, p.proisstrict)", props),
			resource.TestCheckResourceAttr(res, "volatility", volatility),
			resource.TestCheckResourceAttr(res, "parallel", parallel),
			resource.TestCheckResourceAttr(res, "security_definer", secdef),
			resource.TestCheckResourceAttr(res, "strict", strict),
			testAccFunctionCall("SELECT public.tf_attrs(41)", "42"),
		)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(`volatility = "IMMUTABLE"
    parallel = "SAFE"
    security_definer = true
    strict = true`),
				Check: check("i,s,t,t", "IMMUTABLE", "SAFE", "true", "true"),
			},
			{
				Config: config(`volatility = "STABLE"
    parallel = "RESTRICTED"
    security_definer = false
    strict = false`),
				Check: check("s,r,f,f", "STABLE", "RESTRICTED", "false", "false"),
			},
			{
				Config: config(`volatility = "VOLATILE"
    parallel = "UNSAFE"
    security_definer = true
    strict = true`),
				Check: check("v,u,t,t", "VOLATILE", "UNSAFE", "true", "true"),
			},
			{
				// the defaults when the attributes are removed
				Config: config(``),
				Check:  check("v,u,f,f", "VOLATILE", "UNSAFE", "false", "false"),
			},
			{
				Config: config(`volatility = "IMMUTABLE"
    strict = true`),
				Check: check("i,u,f,t", "IMMUTABLE", "UNSAFE", "false", "true"),
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				// changed outside of Terraform
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION public.tf_attrs(integer) STABLE SECURITY DEFINER PARALLEL SAFE CALLED ON NULL INPUT")
				},
				Config: config(`volatility = "IMMUTABLE"
    strict = true`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// and brought back
				Config: config(`volatility = "IMMUTABLE"
    strict = true`),
				Check: check("i,u,f,t", "IMMUTABLE", "UNSAFE", "false", "true"),
			},
		},
	})
}

func TestAccPostgresqlFunction_ArgModes(t *testing.T) {
	config := `
resource "postgresql_function" "modes" {
    name = "tf_modes"
    arg {
        name = "b"
        type = "integer"
        mode = "INOUT"
    }
    arg {
        name = "a"
        type = "integer"
        default = "1"
    }
    arg {
        name = "c"
        type = "text"
        mode = "OUT"
    }
    arg {
        name = "d"
        type = "integer"
        mode = "OUT"
    }
    language = "plpgsql"
    body = <<-EOF
        BEGIN
            b := b + a;
            c := 'x' || b;
            d := b * 2;
        END;
    EOF
}

resource "postgresql_function" "variadic" {
    name = "tf_variadic"
    arg {
        name = "nums"
        type = "integer[]"
        mode = "VARIADIC"
    }
    returns = "integer"
    language = "sql"
    body = "SELECT sum(n)::integer FROM unnest(nums) AS n"
}

resource "postgresql_function" "unnamed" {
    name = "tf_unnamed"
    arg {
        type = "integer"
    }
    arg {
        type = "double precision"
    }
    arg {
        type = "integer"
        mode = "OUT"
    }
    arg {
        type = "text"
        default = "'a,b'::text"
    }
    language = "sql"
    body = "SELECT $1 + $2::integer"
}

resource "postgresql_function" "inout_only" {
    name = "tf_inout_only"
    arg {
        name = "v"
        type = "integer"
        mode = "INOUT"
    }
    language = "sql"
    body = "SELECT v + 1"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("postgresql_function.modes", "returns", "record"),
					testAccFunctionProp("postgresql_function.modes", "p.proargmodes::text", "{b,i,o,o}"),
					testAccFunctionProp("postgresql_function.modes", "p.pronargdefaults", "1"),
					testAccFunctionCall("SELECT (public.tf_modes(5, 1)).c", "x6"),
					testAccFunctionCall("SELECT (public.tf_modes(5)).d", "12"),
					resource.TestCheckResourceAttr("postgresql_function.variadic", "arg.0.mode", "VARIADIC"),
					testAccFunctionProp("postgresql_function.variadic", "p.provariadic::regtype", "integer"),
					testAccFunctionCall("SELECT public.tf_variadic(1, 2, 3)", "6"),
					resource.TestCheckResourceAttr("postgresql_function.unnamed", "arg.#", "4"),
					resource.TestCheckResourceAttr("postgresql_function.unnamed", "arg.1.type", "double precision"),
					resource.TestCheckResourceAttr("postgresql_function.unnamed", "arg.2.mode", "OUT"),
					resource.TestCheckResourceAttr("postgresql_function.unnamed", "arg.3.default", "'a,b'::text"),
					testAccFunctionCall("SELECT public.tf_unnamed(1, 2)", "3"),
					resource.TestCheckResourceAttr("postgresql_function.inout_only", "returns", "integer"),
					testAccFunctionCall("SELECT public.tf_inout_only(1)", "2"),
				),
			},
			{
				ResourceName:      "postgresql_function.modes",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:      "postgresql_function.variadic",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:      "postgresql_function.unnamed",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				// no change
				Config:   config,
				PlanOnly: true,
			},
			{
				// replaced outside of Terraform with another default
				PreConfig: func() {
					testAccFunctionExec(t, `CREATE OR REPLACE FUNCTION public.tf_modes(INOUT b integer, a integer DEFAULT 10, OUT c text, OUT d integer) LANGUAGE plpgsql AS $$ BEGIN b := b + a; c := 'x' || b; d := b * 2; END; $$`)
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config,
				Check:  testAccFunctionProp("postgresql_function.modes", "p.pronargdefaults", "1"),
			},
		},
	})
}

func TestAccPostgresqlFunction_ReturnsSet(t *testing.T) {
	config := func(rows string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "tbl" {
    name = "tf_table"
    arg {
        name = "n"
        type = "integer"
    }
    returns = "TABLE(id integer, label text)"
    language = "sql"
    %s
    body = "SELECT i, 'row' || i FROM generate_series(1, n) AS i"
}

resource "postgresql_function" "setof" {
    name = "tf_setof"
    arg {
        name = "n"
        type = "integer"
    }
    returns = "SETOF integer"
    language = "plpgsql"
    cost = 12.5
    %s
    body = <<-EOF
        BEGIN
            RETURN QUERY SELECT generate_series(1, n);
        END;
    EOF
}
`, rows, rows)
	}
	var tblOID, setofOID string
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.tbl", &tblOID),
					testAccFunctionOID("postgresql_function.setof", &setofOID),
					resource.TestCheckResourceAttr("postgresql_function.tbl", "returns", "TABLE(id integer, label text)"),
					resource.TestCheckResourceAttr("postgresql_function.setof", "returns", "SETOF integer"),
					testAccFunctionProp("postgresql_function.tbl", "p.proretset", "true"),
					testAccFunctionProp("postgresql_function.tbl", "p.prorows", "1000"),
					testAccFunctionProp("postgresql_function.setof", "p.procost", "12.5"),
					resource.TestCheckResourceAttr("postgresql_function.setof", "cost", "12.5"),
					testAccFunctionCall("SELECT count(*) FROM public.tf_table(3)", "3"),
					testAccFunctionCall("SELECT label FROM public.tf_table(3) WHERE id = 2", "row2"),
					testAccFunctionCall("SELECT sum(x) FROM public.tf_setof(4) AS x", "10"),
				),
			},
			{
				// ROWS is changed in place, and the cost is not touched
				Config: config(`rows = 42`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.tbl", &tblOID),
					testAccFunctionOID("postgresql_function.setof", &setofOID),
					testAccFunctionProp("postgresql_function.tbl", "p.prorows", "42"),
					testAccFunctionProp("postgresql_function.setof", "p.prorows", "42"),
					testAccFunctionProp("postgresql_function.setof", "p.procost", "12.5"),
					resource.TestCheckResourceAttr("postgresql_function.tbl", "rows", "42"),
				),
			},
			{
				ResourceName:      "postgresql_function.tbl",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:      "postgresql_function.setof",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION public.tf_table(integer) ROWS 7", "ALTER FUNCTION public.tf_setof(integer) COST 3")
				},
				Config:             config(`rows = 42`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`rows = 42`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionProp("postgresql_function.tbl", "p.prorows", "42"),
					testAccFunctionProp("postgresql_function.setof", "p.procost", "12.5"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Cost(t *testing.T) {
	config := func(cost string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "cost" {
    name = "tf_cost"
    returns = "integer"
    %s
    body = "BEGIN RETURN 1; END;"
}
`, cost)
	}
	var oid string
	const res = "postgresql_function.cost"
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.procost", "100"),
					resource.TestCheckResourceAttr(res, "cost", "100"),
				),
			},
			{
				Config: config(`cost = 0.0025`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.procost", "0.0025"),
				),
			},
			{
				Config: config(`cost = 5000`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.procost", "5000"),
				),
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION public.tf_cost() COST 9")
				},
				Config:             config(`cost = 5000`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// a body change keeps the cost
				Config: strings.Replace(config(`cost = 5000`), "RETURN 1", "RETURN 2", 1),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.procost", "5000"),
					testAccFunctionCall("SELECT public.tf_cost()", "2"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Parameters(t *testing.T) {
	config := func(parameters string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "params" {
    name = "tf_params"
    returns = "text"
    language = "plpgsql"
    %s
    body = "BEGIN RETURN current_setting('search_path') || '|' || current_setting('work_mem'); END;"
}
`, parameters)
	}
	var oid string
	const res = "postgresql_function.params"
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proconfig::text", ""),
					resource.TestCheckResourceAttr(res, "parameters.%", "0"),
				),
			},
			{
				Config: config(`parameters = {
        search_path = "pg_catalog, public"
        work_mem = "8MB"
    }`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proconfig::text", `{"search_path=pg_catalog, public",work_mem=8MB}`),
					resource.TestCheckResourceAttr(res, "parameters.search_path", "pg_catalog, public"),
					resource.TestCheckResourceAttr(res, "parameters.work_mem", "8MB"),
					testAccFunctionCall("SELECT public.tf_params()", "pg_catalog, public|8MB"),
				),
			},
			{
				// the quotes of a list parameter are not a difference
				Config: config(`parameters = {
        search_path = "\"pg_catalog\",public"
        work_mem = "8MB"
    }`),
				PlanOnly: true,
			},
			{
				// one changed, one removed, another one added
				Config: config(`parameters = {
        search_path = "\"$user\", public"
        statement_timeout = "5s"
    }`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proconfig::text", `{"search_path=\"$user\", public",statement_timeout=5s}`),
					resource.TestCheckResourceAttr(res, "parameters.%", "2"),
				),
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION public.tf_params() SET work_mem = '1MB'")
				},
				Config: config(`parameters = {
        search_path = "\"$user\", public"
        statement_timeout = "5s"
    }`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// a body change keeps the parameters
				Config: strings.Replace(config(`parameters = {
        search_path = "\"$user\", public"
        statement_timeout = "5s"
    }`), "current_setting('work_mem')", "current_setting('statement_timeout')", 1),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proconfig::text", `{"search_path=\"$user\", public",statement_timeout=5s}`),
					testAccFunctionCall("SELECT public.tf_params()", `"$user", public|5s`),
				),
			},
			{
				// all removed
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proconfig::text", ""),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_InvalidParameters(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccFunctionPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
    parameters = { Work_Mem = "1MB" }
    body = "BEGIN RETURN 1; END;"
}`,
				ExpectError: regexp.MustCompile("is not a valid parameter name"),
			},
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
    language = "sql"
    sql_body = "RETURN 1"
    body = "SELECT 1"
}`,
				ExpectError: regexp.MustCompile("conflicts with"),
			},
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
}`,
				ExpectError: regexp.MustCompile("one of body or sql_body is required"),
			},
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
    obj_file = "x"
    body = "BEGIN RETURN 1; END;"
}`,
				ExpectError: regexp.MustCompile("obj_file is only for the c language"),
			},
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
    language = "c"
    body = "tf_bad"
}`,
				ExpectError: regexp.MustCompile("obj_file is required for the c language"),
			},
			{
				Config: `
resource "postgresql_function" "bad" {
    name = "tf_bad"
    returns = "integer"
    arg {
        type = "integer"
        mode = "TABLE"
    }
    body = "BEGIN RETURN 1; END;"
}`,
				ExpectError: regexp.MustCompile("expected .* to be one of"),
			},
		},
	})
}

func TestAccPostgresqlFunction_Leakproof(t *testing.T) {
	config := func(leakproof string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "leak" {
    name = "tf_leak"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "boolean"
    language = "sql"
    %s
    volatility = "IMMUTABLE"
    body = "SELECT i > 0"
}
`, leakproof)
	}
	var oid string
	const res = "postgresql_function.leak"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanCreateSuperuserFunction(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proleakproof", "false"),
					resource.TestCheckResourceAttr(res, "leakproof", "false"),
				),
			},
			{
				Config: config(`leakproof = true`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proleakproof", "true"),
					testAccFunctionCall("SELECT public.tf_leak(1)", "true"),
				),
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION public.tf_leak(integer) NOT LEAKPROOF")
				},
				Config:             config(`leakproof = true`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`leakproof = false`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proleakproof", "false"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Support(t *testing.T) {
	config := func(support string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "support" {
    name = "tf_support"
    arg {
        name = "a"
        type = "integer"
    }
    arg {
        name = "b"
        type = "integer"
    }
    returns = "SETOF integer"
    language = "sql"
    %s
    volatility = "IMMUTABLE"
    body = "SELECT generate_series(a, b)"
}
`, support)
	}
	var oid string
	const res = "postgresql_function.support"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanExecute(
				t, "set a support function",
				"CREATE FUNCTION public.tf_probe_support() RETURNS SETOF integer LANGUAGE sql SUPPORT pg_catalog.generate_series_int4_support AS 'SELECT 1'",
			)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.prosupport::oid", "0"),
				),
			},
			{
				Config: config(`support = "pg_catalog.generate_series_int4_support"`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.prosupport::regproc", "generate_series_int4_support"),
					resource.TestCheckResourceAttr(res, "support", "pg_catalog.generate_series_int4_support"),
					testAccFunctionCall("SELECT count(*) FROM public.tf_support(1, 5)", "5"),
				),
			},
			{
				ResourceName:      res,
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "CREATE OR REPLACE FUNCTION public.tf_support(a integer, b integer) RETURNS SETOF integer LANGUAGE sql IMMUTABLE AS 'SELECT generate_series(a, b)'")
				},
				Config:             config(`support = "pg_catalog.generate_series_int4_support"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`support = "pg_catalog.generate_series_int4_support"`),
				Check:  testAccFunctionProp(res, "p.prosupport::regproc", "generate_series_int4_support"),
			},
			{
				// removed: the function has to be replaced, ALTER FUNCTION cannot do it
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.prosupport::oid", "0"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_Window(t *testing.T) {
	config := `
resource "postgresql_function" "window" {
    name = "tf_row_number"
    returns = "bigint"
    language = "internal"
    window = true
    volatility = "IMMUTABLE"
    parallel = "SAFE"
    body = "window_row_number"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanExecute(
				t, "create an internal window function",
				"CREATE FUNCTION public.tf_probe_window() RETURNS bigint LANGUAGE internal WINDOW AS 'window_row_number'",
			)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionProp("postgresql_function.window", "p.prokind", "w"),
					testAccFunctionProp("postgresql_function.window", "p.procost", "1"),
					resource.TestCheckResourceAttr("postgresql_function.window", "window", "true"),
					resource.TestCheckResourceAttr("postgresql_function.window", "body", "window_row_number"),
					testAccFunctionCall("SELECT string_agg(n::text, ',') FROM (SELECT public.tf_row_number() OVER (ORDER BY x) AS n FROM generate_series(1, 3) AS x) AS t", "1,2,3"),
				),
			},
			{
				ResourceName:      "postgresql_function.window",
				ImportState:       true,
				ImportStateVerify: true,
				// it is not read from the database
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "DROP FUNCTION public.tf_row_number()", "CREATE FUNCTION public.tf_row_number() RETURNS bigint LANGUAGE internal IMMUTABLE PARALLEL SAFE AS 'int8inc_any'")
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
