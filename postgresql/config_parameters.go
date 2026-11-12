package postgresql

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/lib/pq"
)

// Helpers to manage configuration parameters stored in pg_db_role_setting,
// i.e. the ones defined with ALTER ROLE ... SET or ALTER DATABASE ... SET.

// Parameter names are interpolated unquoted in the ALTER statements so they
// are restricted to (optionally dot-qualified) lowercase identifiers.
// PostgreSQL matches parameter names case-insensitively, forcing lowercase
// here avoids a permanent diff with the names read back from the catalog.
var configParameterNameRegexp = regexp.MustCompile(`^[a-z_][a-z0-9_]*(\.[a-z_][a-z0-9_$]*)*$`)

// listQuoteParameters are the parameters whose value is a list of identifiers
// (GUC_LIST_QUOTE in PostgreSQL). Each element has to be sent separately,
// otherwise the whole value is stored as one single quoted identifier.
// This is the list used by variable_is_guc_list_quote() in pg_dump, the ones
// which are not in this list are detected when they are set (see alterConfigParameters)
// but the formatting of their value is not normalized in the plan.
var listQuoteParameters = map[string]bool{
	"local_preload_libraries":   true,
	"output_plugin_libraries":   true,
	"search_path":               true,
	"session_preload_libraries": true,
	"shared_preload_libraries":  true,
	"temp_tablespaces":          true,
	"unix_socket_directories":   true,
}

// splitListParameter splits the value of a list parameter into its elements.
// The elements are separated by commas and can be double-quoted
// (this is how PostgreSQL stores the names that need it), e.g.: "$user", public
func splitListParameter(value string) []string {
	elements := []string{}
	rest := strings.TrimSpace(value)
	for rest != "" {
		var element string
		if strings.HasPrefix(rest, `"`) {
			rest = rest[1:]
			var b strings.Builder
			for {
				i := strings.Index(rest, `"`)
				if i < 0 {
					// unterminated quote, take the rest as it is
					b.WriteString(rest)
					rest = ""
					break
				}
				b.WriteString(rest[:i])
				rest = rest[i+1:]
				if !strings.HasPrefix(rest, `"`) {
					break
				}
				// doubled quote
				b.WriteString(`"`)
				rest = rest[1:]
			}
			element = b.String()
			if i := strings.Index(rest, ","); i >= 0 {
				rest = rest[i:]
			} else {
				rest = ""
			}
		} else {
			i := strings.Index(rest, ",")
			if i < 0 {
				i = len(rest)
			}
			element = strings.TrimSpace(rest[:i])
			rest = rest[i:]
		}
		elements = append(elements, element)

		if !strings.HasPrefix(rest, ",") {
			break
		}
		rest = strings.TrimSpace(rest[1:])
		if rest == "" {
			// trailing comma
			elements = append(elements, "")
		}
	}

	// An empty list is stored as one empty element
	if len(elements) == 1 && elements[0] == "" {
		return []string{}
	}
	return elements
}

// configParameterValue returns the SQL representation of the value of a parameter.
func configParameterValue(name, value string) string {
	if !listQuoteParameters[name] {
		return pq.QuoteLiteral(value)
	}
	return listParameterValue(value)
}

// listParameterValue returns the SQL representation of the value of a list parameter.
func listParameterValue(value string) string {
	elements := splitListParameter(value)
	if len(elements) == 0 {
		return "''"
	}
	quoted := make([]string, len(elements))
	for i, element := range elements {
		quoted[i] = pq.QuoteLiteral(element)
	}
	return strings.Join(quoted, ", ")
}

// suppressConfigParametersDiff is a schema.SchemaDiffSuppressFunc for a map of
// configuration parameters. For the list parameters, PostgreSQL adds or removes
// the quotes of each element and normalizes the separator, so it ignores
// the differences of formatting.
func suppressConfigParametersDiff(k, old, new string, d *schema.ResourceData) bool {
	attr, name, _ := strings.Cut(k, ".")
	if !listQuoteParameters[name] {
		return false
	}

	oldElements, newElements := splitListParameter(old), splitListParameter(new)
	if len(oldElements) != len(newElements) {
		return false
	}
	for i := range oldElements {
		if oldElements[i] != newElements[i] {
			return false
		}
	}

	// The lists are the same. They can also be both empty because the parameter
	// is not set in the database, or not defined in the configuration, which is
	// a real difference: the diff is ignored only if the parameter is on both sides.
	_, defined := d.Get(attr).(map[string]any)[name]
	return defined && old != ""
}

// validateConfigParameters returns a schema.SchemaValidateFunc for a map of
// configuration parameters.
// reserved maps the parameters that cannot be set with this map to the
// attribute which has to be used instead.
func validateConfigParameters(reserved map[string]string) func(any, string) ([]string, []error) {
	return func(v any, k string) ([]string, []error) {
		var errs []error
		for name := range v.(map[string]any) {
			if attr, ok := reserved[name]; ok {
				errs = append(errs, fmt.Errorf("%s: %q cannot be set here, use the %q attribute instead", k, name, attr))
			} else if !configParameterNameRegexp.MatchString(name) {
				errs = append(errs, fmt.Errorf("%s: %q is not a valid parameter name (it must be lowercase)", k, name))
			}
		}
		return nil, errs
	}
}

// parseConfigParameters converts a setconfig array (as stored in
// pg_roles.rolconfig or pg_db_role_setting.setconfig) to a map.
// Parameters listed in reserved are ignored.
func parseConfigParameters(config pq.StringArray, reserved map[string]string) map[string]any {
	parameters := make(map[string]any, len(config))
	for _, v := range config {
		name, value, found := strings.Cut(v, "=")
		if !found {
			continue
		}
		// Some parameters have a mixed case canonical name (e.g.: TimeZone)
		name = strings.ToLower(name)
		if _, ok := reserved[name]; ok {
			continue
		}
		parameters[name] = value
	}
	return parameters
}

// alterConfigParameters executes the ALTER <objectType> <objectName> SET / RESET
// statements needed to go from the oldParams parameters to the newParams ones.
// objectType is either ROLE or DATABASE.
func alterConfigParameters(db QueryAble, objectType, objectName string, oldParams, newParams map[string]any) error {
	return alterConfigParametersOn(
		db,
		fmt.Sprintf("%s %s", objectType, pq.QuoteIdentifier(objectName)),
		fmt.Sprintf("%s %s", strings.ToLower(objectType), objectName),
		func() (map[string]any, error) { return readConfigParameters(db, objectType, objectName) },
		oldParams, newParams,
	)
}

// alterConfigParametersOn is alterConfigParameters for any target of an
// ALTER ... SET / RESET statement: target is what follows ALTER in the SQL
// (e.g.: ROLE "foo" IN DATABASE "bar"), description is used in the errors and
// read returns the parameters which are stored for the target.
func alterConfigParametersOn(db QueryAble, target, description string, read func() (map[string]any, error), oldParams, newParams map[string]any) error {
	var toReset, toSet []string
	for name := range oldParams {
		if _, ok := newParams[name]; !ok {
			toReset = append(toReset, name)
		}
	}
	for name, value := range newParams {
		// a parameter which is not in oldParams is nil, so it's different too
		if oldParams[name] != value {
			toSet = append(toSet, name)
		}
	}
	sort.Strings(toReset)
	sort.Strings(toSet)

	for _, name := range toReset {
		query := fmt.Sprintf("ALTER %s RESET %s", target, name)
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("could not reset parameter %s for %s: %w", name, description, err)
		}
	}
	set := func(name, value string) error {
		query := fmt.Sprintf("ALTER %s SET %s TO %s", target, name, value)
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("could not set parameter %s for %s: %w", name, description, err)
		}
		return nil
	}
	for _, name := range toSet {
		if err := set(name, configParameterValue(name, newParams[name].(string))); err != nil {
			return err
		}
	}

	// PostgreSQL stores the value as it is, except for the list parameters.
	// If one which is not in listQuoteParameters has been stored as one single
	// quoted element, its value has to be set again element by element.
	stored, err := read()
	if err != nil {
		return err
	}
	for _, name := range toSet {
		value := newParams[name].(string)
		storedValue, _ := stored[name].(string)
		if storedAsSingleElement(storedValue, value) {
			if err := set(name, listParameterValue(value)); err != nil {
				return err
			}
		}
	}
	return nil
}

// storedAsSingleElement returns true if stored is value quoted by PostgreSQL as
// one single element of a list, whereas value is not written as a single element.
// e.g.: foo, bar stored as "foo, bar"
func storedAsSingleElement(stored, value string) bool {
	if stored == value {
		return false
	}
	if elements := splitListParameter(stored); len(elements) != 1 || elements[0] != value {
		return false
	}
	// A value which is already one single element only had to be quoted (e.g.: Foo stored as "Foo")
	elements := splitListParameter(value)
	return len(elements) != 1 || elements[0] != value
}

// readConfigParameters returns all the configuration parameters set for a role or a database.
func readConfigParameters(db QueryAble, objectType, objectName string) (map[string]any, error) {
	query := `SELECT COALESCE(rolconfig, '{}'::text[]) FROM pg_catalog.pg_roles WHERE rolname = $1`
	if objectType == "DATABASE" {
		query = `SELECT COALESCE((` +
			`SELECT s.setconfig FROM pg_catalog.pg_db_role_setting AS s ` +
			`JOIN pg_catalog.pg_database AS d ON d.oid = s.setdatabase ` +
			`WHERE d.datname = $1 AND s.setrole = 0` +
			`), '{}'::text[])`
	}

	var config pq.StringArray
	if err := db.QueryRow(query, objectName).Scan(&config); err != nil {
		return nil, fmt.Errorf("error reading parameters for %s %s: %w", strings.ToLower(objectType), objectName, err)
	}
	return parseConfigParameters(config, nil), nil
}
