package postgresql

import (
	"database/sql"
	"fmt"
	"github.com/lib/pq"
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

func TestAccPostgresqlFunction_Languages(t *testing.T) {
	config := func(volatility string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "sql" {
    name = "tf_lang_sql"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "sql"
    volatility = "%[1]s"
    body = "SELECT i * 2"
}

resource "postgresql_function" "plpgsql" {
    name = "tf_lang_plpgsql"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "plpgsql"
    volatility = "%[1]s"
    body = "BEGIN RETURN i * 3; END;"
}
`, volatility)
	}
	var oid string
	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccFunctionPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("IMMUTABLE"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.sql", &oid),
					testAccFunctionProp("postgresql_function.sql", "(SELECT lanname FROM pg_language WHERE oid = p.prolang)", "sql"),
					testAccFunctionProp("postgresql_function.plpgsql", "(SELECT lanname FROM pg_language WHERE oid = p.prolang)", "plpgsql"),
					testAccFunctionCall("SELECT public.tf_lang_sql(21)", "42"),
					testAccFunctionCall("SELECT public.tf_lang_plpgsql(14)", "42"),
				),
			},
			{
				Config: config("STABLE"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.sql", &oid),
					testAccFunctionProp("postgresql_function.sql", "p.provolatile", "s"),
				),
			},
			{
				ResourceName:            "postgresql_function.sql",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:            "postgresql_function.plpgsql",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "CREATE OR REPLACE FUNCTION public.tf_lang_plpgsql(i integer) RETURNS integer LANGUAGE sql STABLE AS 'SELECT i'")
				},
				Config:             config("STABLE"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// The internal and c languages are reserved to superusers.
func TestAccPostgresqlFunction_InternalAndC(t *testing.T) {
	config := func(volatility string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "internal" {
    name = "tf_lang_internal"
    arg {
        type = "integer"
    }
    arg {
        type = "integer"
    }
    returns = "integer"
    language = "internal"
    volatility = "IMMUTABLE"
    strict = true
    body = "int4pl"
}

resource "postgresql_function" "c" {
    name = "tf_lang_c"
    arg {
        type = "text"
    }
    arg {
        type = "text"
    }
    returns = "boolean"
    language = "c"
    obj_file = "$libdir/citext"
    body = "citext_eq"
    volatility = "%s"
    strict = true
}

# the library built with the test image, which has no SQL script in $libdir
resource "postgresql_function" "dummy" {
    name = "tf_lang_dummy"
    returns = "void"
    language = "c"
    obj_file = "/opt/dummy_seclabel/dummy_seclabel"
    body = "dummy_seclabel_dummy"
}
`, volatility)
	}
	var cOID string
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanExecute(
				t, "create functions in the internal and c languages",
				"CREATE FUNCTION public.tf_probe_internal(integer, integer) RETURNS integer LANGUAGE internal AS 'int4pl'",
				"CREATE FUNCTION public.tf_probe_c(text, text) RETURNS boolean LANGUAGE c AS '$libdir/citext', 'citext_eq'",
				"CREATE FUNCTION public.tf_probe_dummy() RETURNS void LANGUAGE c AS '/opt/dummy_seclabel/dummy_seclabel', 'dummy_seclabel_dummy'",
			)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("IMMUTABLE"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.c", &cOID),
					testAccFunctionProp("postgresql_function.internal", "p.prosrc || '/' || (SELECT lanname FROM pg_language WHERE oid = p.prolang)", "int4pl/internal"),
					resource.TestCheckResourceAttr("postgresql_function.internal", "body", "int4pl"),
					testAccFunctionCall("SELECT public.tf_lang_internal(40, 2)", "42"),
					testAccFunctionProp("postgresql_function.c", "p.probin || ',' || p.prosrc", "$libdir/citext,citext_eq"),
					resource.TestCheckResourceAttr("postgresql_function.c", "obj_file", "$libdir/citext"),
					resource.TestCheckResourceAttr("postgresql_function.c", "body", "citext_eq"),
					testAccFunctionCall("SELECT public.tf_lang_c('Abc', 'aBC')", "true"),
					testAccFunctionProp("postgresql_function.dummy", "p.probin || ',' || p.prosrc", "/opt/dummy_seclabel/dummy_seclabel,dummy_seclabel_dummy"),
					testAccFunctionCall("SELECT public.tf_lang_dummy() IS NULL", "false"),
				),
			},
			{
				// a change in place
				Config: config("STABLE"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.c", &cOID),
					testAccFunctionProp("postgresql_function.c", "p.provolatile", "s"),
				),
			},
			{
				ResourceName:            "postgresql_function.internal",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:            "postgresql_function.c",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				ResourceName:            "postgresql_function.dummy",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "CREATE OR REPLACE FUNCTION public.tf_lang_c(text, text) RETURNS boolean LANGUAGE c STABLE STRICT AS '$libdir/citext', 'citext_ne'")
				},
				Config:             config("STABLE"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config("STABLE"),
				Check:  testAccFunctionCall("SELECT public.tf_lang_c('Abc', 'aBC')", "true"),
			},
		},
	})
}

func TestAccPostgresqlFunction_SQLBody(t *testing.T) {
	config := func(body1, body2 string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "ret" {
    name = "tf_sqlbody_return"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "sql"
    sql_body = %q
}

resource "postgresql_function" "atomic" {
    name = "tf_sqlbody_atomic"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "SETOF integer"
    language = "sql"
    sql_body = %q
}
`, body1, body2)
	}
	// written as PostgreSQL deparses it
	deparsed1 := "RETURN (i + 1)"
	deparsed2 := "BEGIN ATOMIC\n SELECT i AS i;\n SELECT (i + 1);\nEND"
	var oid string
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCompatibleVersion(t, featureFunctionSQLBody)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				// not as PostgreSQL writes it: the plan is empty all the same
				Config: config("RETURN i+1", "BEGIN ATOMIC SELECT i; SELECT i+1; END"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.ret", &oid),
					testAccFunctionProp("postgresql_function.ret", "p.prosrc", ""),
					testAccFunctionProp("postgresql_function.ret", "p.prosqlbody IS NOT NULL", "true"),
					resource.TestCheckResourceAttr("postgresql_function.ret", "body", ""),
					resource.TestCheckResourceAttr("postgresql_function.ret", "sql_body_deparsed", deparsed1),
					testAccFunctionCall("SELECT public.tf_sqlbody_return(41)", "42"),
					testAccFunctionCall("SELECT sum(x) FROM public.tf_sqlbody_atomic(1) AS x", "2"),
				),
			},
			{
				Config:   config("RETURN i+1", "BEGIN ATOMIC SELECT i; SELECT i+1; END"),
				PlanOnly: true,
			},
			{
				Config: config("RETURN i + 2", "BEGIN ATOMIC SELECT i; SELECT i+1; END"),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID("postgresql_function.ret", &oid),
					resource.TestCheckResourceAttr("postgresql_function.ret", "sql_body_deparsed", "RETURN (i + 2)"),
					testAccFunctionCall("SELECT public.tf_sqlbody_return(40)", "42"),
				),
			},
			{
				Config:   config("RETURN i + 2", "BEGIN ATOMIC SELECT i; SELECT i+1; END"),
				PlanOnly: true,
			},
			{
				// imported: the body is the one of PostgreSQL
				Config: config(deparsed1, deparsed2),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionCall("SELECT public.tf_sqlbody_return(41)", "42"),
				),
			},
			{
				ResourceName:            "postgresql_function.atomic",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "CREATE OR REPLACE FUNCTION public.tf_sqlbody_return(i integer) RETURNS integer LANGUAGE sql RETURN i + 100")
				},
				Config:             config(deparsed1, deparsed2),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(deparsed1, deparsed2),
				Check:  testAccFunctionCall("SELECT public.tf_sqlbody_return(41)", "42"),
			},
			{
				PlanOnly: true,
				Config:   config(deparsed1, deparsed2),
			},
		},
	})
}

func TestAccPostgresqlFunction_SQLBodyOldVersion(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			client := testAccProvider.Meta().(*Client)
			db, err := client.Connect()
			if err != nil {
				t.Fatal(err)
			}
			if db.featureSupported(featureFunctionSQLBody) {
				t.Skipf("Skip: PostgreSQL %s has the SQL-standard function bodies", db.version)
			}
		},
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
resource "postgresql_function" "old" {
    name = "tf_sqlbody_old"
    returns = "integer"
    language = "sql"
    sql_body = "RETURN 1"
}`,
				ExpectError: regexp.MustCompile("requires PostgreSQL 14 or later"),
			},
		},
	})
}

func TestAccPostgresqlFunction_Transform(t *testing.T) {
	config := func(transform string) string {
		return fmt.Sprintf(`
resource "postgresql_function" "tr" {
    name = "tf_transform"
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "plpgsql"
    %s
    body = "BEGIN RETURN i; END;"
}
`, transform)
	}
	var oid string
	const res = "postgresql_function.tr"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanExecute(
				t, "create a transform",
				"CREATE TRANSFORM FOR integer LANGUAGE plpgsql (FROM SQL WITH FUNCTION pg_catalog.generate_series_int4_support(internal))",
			)
			testAccFunctionExec(t, "CREATE TRANSFORM FOR integer LANGUAGE plpgsql (FROM SQL WITH FUNCTION pg_catalog.generate_series_int4_support(internal))")
			t.Cleanup(func() { testAccFunctionExec(t, "DROP TRANSFORM IF EXISTS FOR integer LANGUAGE plpgsql CASCADE") })
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.protrftypes::text", ""),
				),
			},
			{
				// a change which needs CREATE OR REPLACE
				Config: config(`transform = ["integer"]`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.protrftypes::regtype[]::text", "{integer}"),
					resource.TestCheckResourceAttr(res, "transform.#", "1"),
					resource.TestCheckResourceAttr(res, "transform.0", "integer"),
					testAccFunctionCall("SELECT public.tf_transform(42)", "42"),
				),
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "CREATE OR REPLACE FUNCTION public.tf_transform(i integer) RETURNS integer LANGUAGE plpgsql AS $$ BEGIN RETURN i; END; $$")
				},
				Config:             config(`transform = ["integer"]`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: config(`transform = ["integer"]`),
				Check:  testAccFunctionProp(res, "p.protrftypes::regtype[]::text", "{integer}"),
			},
			{
				Config: config(``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.protrftypes::text", ""),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_RenameMoveOwner(t *testing.T) {
	config := func(schemaName, name, owner string) string {
		return fmt.Sprintf(`
resource "postgresql_schema" "one" {
    name = "tf_func_one"
}

resource "postgresql_schema" "two" {
    name = "tf_func_two"
}

resource "postgresql_grant" "create1" {
    database = "postgres"
    schema = postgresql_schema.one.name
    role = "tf_func_owner1"
    object_type = "schema"
    privileges = ["CREATE"]
}

resource "postgresql_grant" "create2" {
    database = "postgres"
    schema = postgresql_schema.one.name
    role = "tf_func_owner2"
    object_type = "schema"
    privileges = ["CREATE"]
}

resource "postgresql_function" "move" {
    schema = postgresql_schema.%[1]s.name
    name = "%[2]s"
    %[3]s
    depends_on = [postgresql_grant.create1, postgresql_grant.create2]
    arg {
        name = "i"
        type = "integer"
    }
    returns = "integer"
    language = "sql"
    body = "SELECT i + 1"
}
`, schemaName, name, owner)
	}
	var oid string
	const res = "postgresql_function.move"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			// the roles are members of the role of the connection, which can then give them the functions
			user := getTestConfig(t).Username
			for _, role := range []string{"tf_func_owner1", "tf_func_owner2"} {
				testAccFunctionExec(t, "CREATE ROLE "+role, fmt.Sprintf("GRANT %s TO %s", role, pq.QuoteIdentifier(user)))
				t.Cleanup(func() { testAccFunctionExec(t, "DROP OWNED BY "+role, "DROP ROLE "+role) })
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("one", "tf_move_a", ``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					resource.TestCheckResourceAttrSet(res, "owner"),
					testAccFunctionProp(res, "pg_get_userbyid(p.proowner) = current_user", "true"),
					testAccFunctionCall("SELECT tf_func_one.tf_move_a(1)", "2"),
				),
			},
			{
				// rename
				Config: config("one", "tf_move_b", ``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.proname", "tf_move_b"),
					resource.TestCheckResourceAttr(res, "id", testAccFunctionID("tf_func_one", "tf_move_b(integer)")),
					testAccFunctionCall("SELECT tf_func_one.tf_move_b(1)", "2"),
				),
			},
			{
				// move
				Config: config("two", "tf_move_b", ``),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.pronamespace::regnamespace::text", "tf_func_two"),
					resource.TestCheckResourceAttr(res, "id", testAccFunctionID("tf_func_two", "tf_move_b(integer)")),
					testAccFunctionCall("SELECT tf_func_two.tf_move_b(1)", "2"),
				),
			},
			{
				// both at once, and a new owner
				Config: config("one", "tf_move_c", `owner = "tf_func_owner1"`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "p.pronamespace::regnamespace::text || '.' || p.proname", "tf_func_one.tf_move_c"),
					testAccFunctionProp(res, "pg_get_userbyid(p.proowner)", "tf_func_owner1"),
					resource.TestCheckResourceAttr(res, "owner", "tf_func_owner1"),
				),
			},
			{
				Config: config("one", "tf_move_c", `owner = "tf_func_owner2"`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, "pg_get_userbyid(p.proowner)", "tf_func_owner2"),
				),
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION tf_func_one.tf_move_c(integer) OWNER TO tf_func_owner1")
				},
				Config:             config("one", "tf_move_c", `owner = "tf_func_owner2"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() {
					testAccFunctionExec(t, "ALTER FUNCTION tf_func_one.tf_move_c(integer) RENAME TO tf_moved_away")
				},
				Config:             config("one", "tf_move_c", `owner = "tf_func_owner2"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// the function which has been renamed is not found any more: it is created again
				PreConfig: func() {
					testAccFunctionExec(t, "DROP FUNCTION tf_func_one.tf_moved_away(integer)")
				},
				Config: config("one", "tf_move_c", `owner = "tf_func_owner2"`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionProp(res, "pg_get_userbyid(p.proowner)", "tf_func_owner2"),
					testAccFunctionCall("SELECT tf_func_one.tf_move_c(1)", "2"),
				),
			},
		},
	})
}

func testAccFunctionID(schemaName, signature string) string {
	return fmt.Sprintf("%s.%s.%s", "postgres", schemaName, signature)
}

func TestAccPostgresqlFunction_DependsOnExtension(t *testing.T) {
	config := func(extensions string) string {
		return fmt.Sprintf(`
resource "postgresql_extension" "trgm" {
    name = "pg_trgm"
}

resource "postgresql_extension" "citext" {
    name = "citext"
}

resource "postgresql_function" "dep" {
    name = "tf_depends"
    returns = "integer"
    language = "sql"
    depends_on_extensions = %s
    body = "SELECT 1"
}
`, extensions)
	}
	var oid string
	canRemove := true
	const res = "postgresql_function.dep"
	depends := "(SELECT string_agg(e.extname, ',' ORDER BY e.extname) FROM pg_depend d JOIN pg_extension e ON e.oid = d.refobjid WHERE d.objid = p.oid AND d.classid = 'pg_proc'::regclass AND d.deptype = 'x')"
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanCreateExtension(t, "pg_trgm", "citext")
			db, err := testAccProvider.Meta().(*Client).Connect()
			if err != nil {
				t.Fatal(err)
			}
			canRemove = db.featureSupported(featureFunctionNoDependsOnExtension)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(`[]`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, depends, ""),
				),
			},
			{
				Config: config(`[postgresql_extension.trgm.name]`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, depends, "pg_trgm"),
					resource.TestCheckResourceAttr(res, "depends_on_extensions.#", "1"),
				),
			},
			{
				Config: config(`[postgresql_extension.trgm.name, postgresql_extension.citext.name]`),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, depends, "citext,pg_trgm"),
				),
			},
			{
				ResourceName:            res,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"drop_cascade"},
			},
			{
				// the dependencies survive CREATE OR REPLACE FUNCTION
				Config: strings.Replace(config(`[postgresql_extension.trgm.name, postgresql_extension.citext.name]`), "SELECT 1", "SELECT 2", 1),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, depends, "citext,pg_trgm"),
					testAccFunctionCall("SELECT public.tf_depends()", "2"),
				),
			},
			{
				// changed outside of Terraform (NO DEPENDS ON EXTENSION does not exist before PostgreSQL 13)
				SkipFunc: func() (bool, error) { return !canRemove, nil },
				PreConfig: func() {
					if canRemove {
						testAccFunctionExec(t, "ALTER FUNCTION public.tf_depends() NO DEPENDS ON EXTENSION citext")
					}
				},
				Config:             strings.Replace(config(`[postgresql_extension.trgm.name, postgresql_extension.citext.name]`), "SELECT 1", "SELECT 2", 1),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				SkipFunc: func() (bool, error) { return !canRemove, nil },
				Config:   strings.Replace(config(`[postgresql_extension.citext.name]`), "SELECT 1", "SELECT 2", 1),
				Check: resource.ComposeTestCheckFunc(
					testAccFunctionOID(res, &oid),
					testAccFunctionProp(res, depends, "citext"),
				),
			},
		},
	})
}

func TestAccPostgresqlFunction_NoDependsOnExtensionOldVersion(t *testing.T) {
	config := func(extensions string) string {
		return fmt.Sprintf(`
resource "postgresql_extension" "trgm" {
    name = "pg_trgm"
}

resource "postgresql_function" "dep" {
    name = "tf_depends_old"
    returns = "integer"
    language = "sql"
    depends_on_extensions = %s
    body = "SELECT 1"
}
`, extensions)
	}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccFunctionPreCheck(t)
			testCheckCanCreateExtension(t, "pg_trgm")
			db, err := testAccProvider.Meta().(*Client).Connect()
			if err != nil {
				t.Fatal(err)
			}
			if db.featureSupported(featureFunctionNoDependsOnExtension) {
				t.Skipf("Skip: PostgreSQL %s has NO DEPENDS ON EXTENSION", db.version)
			}
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlFunctionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(`[postgresql_extension.trgm.name]`),
				Check: testAccFunctionProp("postgresql_function.dep",
					"(SELECT string_agg(e.extname, ',') FROM pg_depend d JOIN pg_extension e ON e.oid = d.refobjid WHERE d.objid = p.oid AND d.classid = 'pg_proc'::regclass AND d.deptype = 'x')", "pg_trgm"),
			},
			{
				Config:      config(`[]`),
				ExpectError: regexp.MustCompile("requires PostgreSQL 13 or later"),
			},
		},
	})
}

func TestAccPostgresqlFunction_DropCascade(t *testing.T) {
	config := `
resource "postgresql_function" "cascade" {
    name = "tf_cascade"
    returns = "integer"
    language = "sql"
    drop_cascade = true
    body = "SELECT 1"
}
`
	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccFunctionPreCheck(t) },
		Providers: testAccProviders,
		CheckDestroy: func(s *terraform.State) error {
			if err := testAccCheckPostgresqlFunctionDestroy(s); err != nil {
				return err
			}
			// the objects which depend on the function are dropped with it
			client := testAccProvider.Meta().(*Client)
			txn, err := startTransaction(client, "")
			if err != nil {
				return err
			}
			defer deferredRollback(txn)
			var exists bool
			if err := txn.QueryRow("SELECT to_regclass('public.tf_cascade_view') IS NOT NULL").Scan(&exists); err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("the view which depends on the function still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					testAccFunctionExec(t, "CREATE VIEW public.tf_cascade_view AS SELECT public.tf_cascade() AS v")
					return nil
				},
			},
		},
	})
}
