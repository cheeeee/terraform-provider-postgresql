package postgresql

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func TestParseConfigParameters(t *testing.T) {
	reserved := map[string]string{"search_path": "search_path"}

	var tests = []struct {
		config   pq.StringArray
		expected map[string]any
	}{
		{nil, map[string]any{}},
		{
			pq.StringArray{"work_mem=64MB", "TimeZone=UTC", "pgaudit.log=ddl, write"},
			map[string]any{"work_mem": "64MB", "timezone": "UTC", "pgaudit.log": "ddl, write"},
		},
		// reserved parameters are ignored
		{
			pq.StringArray{`search_path="$user", public`, "work_mem=64MB"},
			map[string]any{"work_mem": "64MB"},
		},
		// entries which are not a parameter are ignored
		{
			pq.StringArray{"not a parameter", "work_mem=64MB"},
			map[string]any{"work_mem": "64MB"},
		},
		// only the first = is a separator
		{
			pq.StringArray{"application_name=a=b", "default_tablespace="},
			map[string]any{"application_name": "a=b", "default_tablespace": ""},
		},
	}

	for _, test := range tests {
		actual := parseConfigParameters(test.config, reserved)
		if !reflect.DeepEqual(actual, test.expected) {
			t.Errorf("parseConfigParameters(%s): expected %v, got %v", test.config, test.expected, actual)
		}
	}
}

func TestValidateConfigParameters(t *testing.T) {
	validate := validateConfigParameters(map[string]string{"search_path": "search_path"})

	var tests = []struct {
		name  string
		valid bool
	}{
		{"work_mem", true},
		{"pgaudit.log", true},
		{"_custom.my_param2", true},
		{"search_path", false},
		{"TimeZone", false},
		{"work_mem; DROP ROLE foo", false},
		{"pgaudit.", false},
		{"", false},
	}

	for _, test := range tests {
		_, errs := validate(map[string]any{test.name: "value"}, "parameters")
		if valid := len(errs) == 0; valid != test.valid {
			t.Errorf("validateConfigParameters(%q): expected valid=%t, got errors: %v", test.name, test.valid, errs)
		}
	}
}

func TestSplitListParameter(t *testing.T) {
	var tests = []struct {
		value    string
		expected []string
	}{
		{``, []string{}},
		{`  `, []string{}},
		{`""`, []string{}},
		{`public`, []string{"public"}},
		{`"$user", public`, []string{"$user", "public"}},
		{`$user,public`, []string{"$user", "public"}},
		{` foo ,  "Bar Baz"  , "with ""quotes"", and comma"`, []string{"foo", "Bar Baz", `with "quotes", and comma`}},
		{`a,,b`, []string{"a", "", "b"}},
		{`a,`, []string{"a", ""}},
		{`"unterminated`, []string{"unterminated"}},
	}

	for _, test := range tests {
		actual := splitListParameter(test.value)
		if !reflect.DeepEqual(actual, test.expected) {
			t.Errorf("splitListParameter(%q): expected %q, got %q", test.value, test.expected, actual)
		}
	}
}

func TestStoredAsSingleElement(t *testing.T) {
	var tests = []struct {
		stored   string
		value    string
		expected bool
	}{
		// the value is stored as it is
		{`64MB`, `64MB`, false},
		{`foo, bar`, `foo, bar`, false},
		{`"foo, bar"`, `"foo, bar"`, false},
		// a list stored as one single element
		{`"foo, bar"`, `foo, bar`, true},
		{`"foo,bar"`, `foo,bar`, true},
		{`"""$user"", public"`, `"$user", public`, true},
		// a quoted element stored with its quotes
		{`"""Foo"""`, `"Foo"`, true},
		// a single element which only had to be quoted
		{`"Foo"`, `Foo`, false},
		{`"foo bar"`, `foo bar`, false},
		// a list correctly stored
		{`foo, bar`, `foo,bar`, false},
		{`"$user", public`, `$user,public`, false},
		// unrelated values
		{`"foo"`, `bar`, false},
		{`"foo", "bar"`, `foo`, false},
	}

	for _, test := range tests {
		actual := storedAsSingleElement(test.stored, test.value)
		if actual != test.expected {
			t.Errorf("storedAsSingleElement(%q, %q): expected %t, got %t", test.stored, test.value, test.expected, actual)
		}
	}
}

func TestConfigParameterValue(t *testing.T) {
	var tests = []struct {
		name     string
		value    string
		expected string
	}{
		{"work_mem", "64MB", `'64MB'`},
		{"pgaudit.log", "ddl, write", `'ddl, write'`},
		{"application_name", `it's \ me`, `E'it''s \\ me'`},
		{"search_path", `"$user", public`, `'$user', 'public'`},
		{"search_path", `it's`, `'it''s'`},
		{"temp_tablespaces", ``, `''`},
	}

	for _, test := range tests {
		actual := configParameterValue(test.name, test.value)
		// lib/pq adds a space before the E of an escaped string
		if strings.TrimSpace(actual) != test.expected {
			t.Errorf("configParameterValue(%q, %q): expected %s, got %s", test.name, test.value, test.expected, actual)
		}
	}
}

// Test that a list parameter which is not in listQuoteParameters
// (e.g.: one added by a newer version of PostgreSQL) is detected and correctly set.
func TestAccConfigParameters_UnknownListParameter(t *testing.T) {
	skipIfNotAcc(t)

	delete(listQuoteParameters, "search_path")
	delete(listQuoteParameters, "temp_tablespaces")
	defer func() {
		listQuoteParameters["search_path"] = true
		listQuoteParameters["temp_tablespaces"] = true
	}()

	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	defer closeDB(t, db)

	txn, err := db.Begin()
	if err != nil {
		t.Fatalf("could not start transaction: %v", err)
	}
	defer func() {
		_ = txn.Rollback()
	}()
	if _, err := txn.Exec("CREATE ROLE unknown_list_role"); err != nil {
		t.Fatalf("could not create role: %v", err)
	}

	var tests = []struct {
		value    string
		expected string
	}{
		{`"$user", public`, `"$user", public`},
		{`foo,bar`, `foo, bar`},
		// a single element is only quoted
		{`Foo`, `"Foo"`},
		{`public`, `public`},
	}
	for _, test := range tests {
		err := alterConfigParameters(txn, "ROLE", "unknown_list_role", map[string]any{}, map[string]any{"search_path": test.value})
		if err != nil {
			t.Fatalf("could not set search_path to %q: %v", test.value, err)
		}
		stored, err := readConfigParameters(txn, "ROLE", "unknown_list_role")
		if err != nil {
			t.Fatal(err)
		}
		if stored["search_path"] != test.expected {
			t.Errorf("search_path set to %q: expected %q to be stored, got %q", test.value, test.expected, stored["search_path"])
		}
	}

	// Several list parameters set at the same time
	err = alterConfigParameters(txn, "ROLE", "unknown_list_role", map[string]any{}, map[string]any{
		"search_path":      "foo,bar",
		"temp_tablespaces": "pg_default,pg_default",
		"work_mem":         "64MB",
	})
	if err != nil {
		t.Fatalf("could not set the parameters: %v", err)
	}
	stored, err := readConfigParameters(txn, "ROLE", "unknown_list_role")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"search_path": "foo, bar", "temp_tablespaces": "pg_default, pg_default", "work_mem": "64MB"}
	if !reflect.DeepEqual(stored, expected) {
		t.Errorf("expected %v to be stored, got %v", expected, stored)
	}
}

// testAccCheckConfigParameters checks that the setconfig array returned by query
// (called with name as argument) contains exactly the expected entries.
func testAccCheckConfigParameters(query, name string, expected []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		var config pq.StringArray
		if err := db.QueryRow(query, name).Scan(&config); err != nil {
			return fmt.Errorf("could not read parameters of %s: %w", name, err)
		}

		actual := []string(config)
		sort.Strings(actual)
		sort.Strings(expected)
		if len(actual) == 0 && len(expected) == 0 {
			return nil
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("parameters of %s: expected %v, got %v", name, expected, actual)
		}
		return nil
	}
}

const testRoleConfigQuery = `SELECT COALESCE(rolconfig, '{}'::text[]) FROM pg_catalog.pg_roles WHERE rolname = $1`

const testDatabaseConfigQuery = `SELECT COALESCE((
	SELECT s.setconfig FROM pg_catalog.pg_db_role_setting AS s
	JOIN pg_catalog.pg_database AS d ON d.oid = s.setdatabase
	WHERE d.datname = $1 AND s.setrole = 0
), '{}'::text[])`

// testCheckCanSetParameter skips the test if the connected user is not allowed to set
// the parameter for a role (e.g.: the ones reserved to a superuser, or to a member
// of rds_superuser on AWS RDS).
func testCheckCanSetParameter(t *testing.T, name, value string) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}

	txn, err := db.Begin()
	if err != nil {
		t.Fatalf("could not start transaction: %v", err)
	}
	defer func() {
		_ = txn.Rollback()
	}()
	if _, err := txn.Exec("CREATE ROLE params_probe"); err != nil {
		t.Fatalf("could not create role: %v", err)
	}
	if _, err := txn.Exec(fmt.Sprintf("ALTER ROLE params_probe SET %s TO %s", name, configParameterValue(name, value))); err != nil {
		t.Skipf("Skip test: the connected user cannot set %s: %v", name, err)
	}
}

// testCheckPgauditLoaded skips the test if pgaudit is not in shared_preload_libraries.
func testCheckPgauditLoaded(t *testing.T) {
	client := testAccProvider.Meta().(*Client)
	db, err := client.Connect()
	if err != nil {
		t.Fatalf("could not connect to database: %v", err)
	}

	var loaded bool
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_settings WHERE name = 'pgaudit.log')").Scan(&loaded); err != nil {
		t.Fatalf("could not check if pgaudit is loaded: %v", err)
	}
	if !loaded {
		t.Skip("Skip test: pgaudit is not loaded")
	}
}

// testAccCheckSessionParameter checks the value of a parameter
// in a new session opened by role on the database dbName.
func testAccCheckSessionParameter(t *testing.T, role, password, dbName, name, expected string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		config := getTestConfig(t)
		config.Username = role
		config.Password = password
		db, err := sql.Open("postgres", config.connStr(dbName))
		if err != nil {
			return fmt.Errorf("could not open SQL connection: %w", err)
		}
		defer closeDB(t, db)

		var actual string
		if err := db.QueryRow("SELECT current_setting($1)", name).Scan(&actual); err != nil {
			return fmt.Errorf("could not read %s as %s: %w", name, role, err)
		}
		if actual != expected {
			return fmt.Errorf("%s for %s on %s: expected %q, got %q", name, role, dbName, expected, actual)
		}
		return nil
	}
}

// testSettableParameters returns the values to test for every configuration parameter
// which is accepted by PostgreSQL in an ALTER ROLE ... SET executed by the connected user,
// so the list depends on the version of PostgreSQL and on the privileges of the user.
// The values come from pg_settings: both values of a boolean, every value of an enum,
// the minimum, the maximum and the default value (with its unit) of a number.
func testSettableParameters(t *testing.T, reserved map[string]string) map[string][]string {
	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not open SQL connection: %v", err)
	}
	defer closeDB(t, db)

	rows, err := db.Query(`
SELECT lower(name), vartype, COALESCE(unit, ''), COALESCE(reset_val, ''), COALESCE(enumvals, '{}'),
       COALESCE(min_val, ''), COALESCE(max_val, '')
FROM pg_catalog.pg_settings
WHERE context NOT IN ('internal', 'postmaster', 'sighup', 'backend', 'superuser-backend')
ORDER BY name`)
	if err != nil {
		t.Fatalf("could not read pg_settings: %v", err)
	}

	candidates := map[string][]string{}
	for rows.Next() {
		var name, vartype, unit, value, minVal, maxVal string
		var enumvals pq.StringArray
		if err := rows.Scan(&name, &vartype, &unit, &value, &enumvals, &minVal, &maxVal); err != nil {
			t.Fatalf("could not read pg_settings: %v", err)
		}
		if _, ok := reserved[name]; ok {
			continue
		}

		var values []string
		switch vartype {
		case "bool":
			// the value which is not the default one first
			values = []string{"on", "off"}
			if value == "on" {
				values = []string{"off", "on"}
			}
		case "enum":
			for _, enumval := range enumvals {
				if enumval != value {
					values = append(values, enumval)
				}
			}
			values = append(values, value)
		case "integer", "real":
			// The default value with its unit if it has a simple one
			withUnit := value
			if number, err := strconv.ParseFloat(value, 64); err == nil && number > 0 {
				switch unit {
				case "kB", "MB", "ms", "s", "min":
					withUnit += unit
				}
			}
			values = []string{withUnit, minVal, maxVal}
		default:
			values = []string{value}
		}
		candidates[name] = values
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("could not read pg_settings: %v", err)
	}

	// Keep only the values PostgreSQL allows to set and to reset.
	txn, err := db.Begin()
	if err != nil {
		t.Fatalf("could not start transaction: %v", err)
	}
	defer func() {
		_ = txn.Rollback()
	}()
	if _, err := txn.Exec("CREATE ROLE params_probe"); err != nil {
		t.Fatalf("could not create role: %v", err)
	}

	parameters := map[string][]string{}
	nbValues := 0
	for name, values := range candidates {
		seen := map[string]bool{}
		for _, value := range values {
			if seen[value] {
				continue
			}
			seen[value] = true

			if _, err := txn.Exec("SAVEPOINT probe"); err != nil {
				t.Fatalf("could not create savepoint: %v", err)
			}
			_, err := txn.Exec(fmt.Sprintf("ALTER ROLE params_probe SET %s TO %s", name, configParameterValue(name, value)))
			if err == nil {
				_, err = txn.Exec(fmt.Sprintf("ALTER ROLE params_probe RESET %s", name))
			}
			if err != nil {
				t.Logf("value %q of parameter %s is skipped: %v", value, name, err)
				if _, err := txn.Exec("ROLLBACK TO SAVEPOINT probe"); err != nil {
					t.Fatalf("could not rollback to savepoint: %v", err)
				}
				continue
			}
			parameters[name] = append(parameters[name], value)
			nbValues++
		}
	}

	if len(parameters) == 0 {
		t.Fatal("no settable parameter found")
	}
	t.Logf("%d settable parameters found, %d values to test", len(parameters), nbValues)

	return parameters
}

// testParametersRounds distributes the values to test in several sets of parameters:
// every set contains all the parameters, and every value is in at least one set.
func testParametersRounds(values map[string][]string) []map[string]string {
	nbRounds := 0
	for _, v := range values {
		if len(v) > nbRounds {
			nbRounds = len(v)
		}
	}

	rounds := make([]map[string]string, nbRounds)
	for i := range rounds {
		rounds[i] = make(map[string]string, len(values))
		for name, v := range values {
			rounds[i][name] = v[i%len(v)]
		}
	}
	return rounds
}

// testParametersHCL returns the HCL representation of a map of parameters.
func testParametersHCL(parameters map[string]string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)

	// Escape the template sequences of HCL
	replacer := strings.NewReplacer("${", "$${", "%{", "%%{")

	var b strings.Builder
	b.WriteString("{\n")
	for _, name := range names {
		fmt.Fprintf(&b, "    %s = %s\n", strconv.Quote(name), replacer.Replace(strconv.Quote(parameters[name])))
	}
	b.WriteString("  }")
	return b.String()
}

// testAccCheckAllConfigParameters checks that the parameters returned by query
// (called with name as argument) are exactly the expected ones.
func testAccCheckAllConfigParameters(query, name string, reserved map[string]string, expected map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := testAccProvider.Meta().(*Client)
		db, err := client.Connect()
		if err != nil {
			return err
		}

		var config pq.StringArray
		if err := db.QueryRow(query, name).Scan(&config); err != nil {
			return fmt.Errorf("could not read parameters of %s: %w", name, err)
		}

		actual := parseConfigParameters(config, reserved)
		for paramName, value := range expected {
			stored, ok := actual[paramName]
			if !ok {
				return fmt.Errorf("parameter %s of %s is not set", paramName, name)
			}
			equal := stored == value
			if listQuoteParameters[paramName] {
				// PostgreSQL normalizes the lists (e.g.: an empty one is stored as "")
				equal = reflect.DeepEqual(splitListParameter(stored.(string)), splitListParameter(value))
			}
			if !equal {
				return fmt.Errorf("parameter %s of %s: expected %q, got %q", paramName, name, value, stored)
			}
		}
		if len(actual) != len(expected) {
			return fmt.Errorf("parameters of %s: expected %d parameters, got %d", name, len(expected), len(actual))
		}
		return nil
	}
}
