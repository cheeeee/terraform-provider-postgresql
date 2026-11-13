package postgresql

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/lib/pq"
)

const (
	pubNameAttr                    = "name"
	pubOwnerAttr                   = "owner"
	pubDatabaseAttr                = "database"
	pubAllTablesAttr               = "all_tables"
	pubTablesAttr                  = "tables"
	pubDropCascadeAttr             = "drop_cascade"
	pubPublishAttr                 = "publish_param"
	pubPublishViaPartitionRootAttr = "publish_via_partition_root_param"
	pubGeneratedColumnsAttr        = "publish_generated_columns"
	pubTableAttr                   = "table"
	pubSchemasAttr                 = "schemas"
	pubAllSequencesAttr            = "all_sequences"
	pubExceptTablesAttr            = "except_tables"

	// attributes of the blocks `table`
	pubTableNameAttr          = "name"
	pubTableOnlyAttr          = "only"
	pubTableColumnsAttr       = "columns"
	pubTableRowFilterAttr     = "row_filter"
	pubTableRowFilterNormAttr = "row_filter_normalized"
)

func resourcePostgreSQLPublication() *schema.Resource {
	return &schema.Resource{
		Create: PGResourceFunc(resourcePostgreSQLPublicationCreate),
		Read:   PGResourceFunc(resourcePostgreSQLPublicationRead),
		Delete: PGResourceFunc(resourcePostgreSQLPublicationDelete),
		Update: PGResourceFunc(resourcePostgreSQLPublicationUpdate),
		Exists: PGResourceExistsFunc(resourcePostgreSQLPublicationExists),
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			pubNameAttr: {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     false,
				ValidateFunc: validation.StringIsNotEmpty,
			},
			pubDatabaseAttr: {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "Sets the database to add the publication for",
			},
			pubOwnerAttr: {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ForceNew:     false,
				Description:  "Sets the owner of the publication",
				ValidateFunc: validation.StringIsNotEmpty,
			},
			pubTablesAttr: {
				Type:          schema.TypeSet,
				Optional:      true,
				Computed:      true,
				ForceNew:      false,
				Elem:          &schema.Schema{Type: schema.TypeString},
				Description:   "Sets the tables list to publish",
				ConflictsWith: []string{pubAllTablesAttr, pubAllSequencesAttr, pubTableAttr},
			},
			pubTableAttr: {
				Type:          schema.TypeSet,
				Optional:      true,
				Set:           pubTableHash,
				ConflictsWith: []string{pubAllTablesAttr, pubAllSequencesAttr, pubTablesAttr},
				Description:   "A table to publish, with its options (ONLY, column list, row filter)",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						pubTableNameAttr: {
							Type:         schema.TypeString,
							Required:     true,
							Description:  "Name of the table: <schema_name>.<table_name>",
							ValidateFunc: validation.StringIsNotEmpty,
						},
						pubTableOnlyAttr: {
							Type:        schema.TypeBool,
							Optional:    true,
							Description: "Publish only this table, not its descendants (ONLY). Otherwise the descendants are published too (*)",
						},
						pubTableColumnsAttr: {
							Type:        schema.TypeSet,
							Optional:    true,
							Elem:        &schema.Schema{Type: schema.TypeString},
							Description: "The columns to publish (PostgreSQL 15+). All the columns when empty",
						},
						pubTableRowFilterAttr: {
							Type:        schema.TypeString,
							Optional:    true,
							Description: "The expression which selects the rows to publish (PostgreSQL 15+)",
						},
						pubTableRowFilterNormAttr: {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "The row filter as PostgreSQL stores it (read only)",
						},
					},
				},
			},
			pubSchemasAttr: {
				Type:          schema.TypeSet,
				Optional:      true,
				Elem:          &schema.Schema{Type: schema.TypeString},
				ConflictsWith: []string{pubAllTablesAttr, pubAllSequencesAttr},
				Description:   "The schemas whose tables are published (FOR TABLES IN SCHEMA, PostgreSQL 15+)",
			},
			pubAllSequencesAttr: {
				Type:          schema.TypeBool,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{pubTablesAttr, pubTableAttr, pubSchemasAttr},
				Description:   "Publish all the sequences (FOR ALL SEQUENCES, PostgreSQL 19+)",
			},
			pubExceptTablesAttr: {
				Type:         schema.TypeSet,
				Optional:     true,
				Elem:         &schema.Schema{Type: schema.TypeString},
				RequiredWith: []string{pubAllTablesAttr},
				Description:  "The tables which are not published by all_tables (ALL TABLES EXCEPT, PostgreSQL 19+)",
			},
			pubAllTablesAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "Sets the tables list to publish to ALL tables",
			},
			pubPublishAttr: {
				Type:        schema.TypeList,
				Optional:    true,
				Computed:    true,
				MinItems:    1,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Sets which DML operations will be published",
			},
			pubPublishViaPartitionRootAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				ForceNew:    false,
				Description: "Sets whether changes in a partitioned table using the identity and schema of the partitioned table",
			},
			pubGeneratedColumnsAttr: {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validation.StringInSlice([]string{"none", "stored"}, false),
				Description:  "Whether the generated columns are published: none or stored (PostgreSQL 18+)",
			},
			pubDropCascadeAttr: {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "When true, will also drop all the objects that depend on the publication, and in turn all objects that depend on those objects",
			},
		},
	}
}

func resourcePostgreSQLPublicationUpdate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePublication) {
		return fmt.Errorf(
			"postgresql_publication resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	database := getDatabaseForPublication(d, db.client.databaseName)
	txn, err := startTransaction(db.client, database)
	if err != nil {
		return fmt.Errorf("could not start transaction: %w", err)
	}

	defer deferredRollback(txn)

	// The publication is altered under its current name, then renamed, then given
	// to its new owner: once it is not owned by the old owner anymore the connected
	// user may not be allowed to alter it.
	oldName, _ := d.GetChange(pubNameAttr)
	pubName := oldName.(string)
	err = withPublicationOwnerGranted(txn, pubName, func() error {
		// the schemas are dropped before and added after the tables: a publication
		// of a schema can not have a column list
		if err := setPubSchemas(txn, db, d, pubName, true); err != nil {
			return fmt.Errorf("could not update publication schemas: %w", err)
		}

		if err := setPubTables(txn, db, d, pubName); err != nil {
			return fmt.Errorf("could not update publication tables: %w", err)
		}

		if err := setPubSchemas(txn, db, d, pubName, false); err != nil {
			return fmt.Errorf("could not update publication schemas: %w", err)
		}

		if err := setPubExceptTables(txn, db, d, pubName); err != nil {
			return fmt.Errorf("could not update publication excluded tables: %w", err)
		}

		if err := setPubParams(txn, db, d, pubName); err != nil {
			return fmt.Errorf("could not update publication tables: %w", err)
		}

		if err := setPubName(txn, d); err != nil {
			return fmt.Errorf("could not update publication name: %w", err)
		}

		if err := setPubOwner(txn, d); err != nil {
			return fmt.Errorf("could not update publication owner: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("error updating publication: %w", err)
	}
	return resourcePostgreSQLPublicationReadImpl(db, d)
}

// withPublicationOwnerGranted executes fn with the owner of the publication
// granted to the connected user: a publication can only be altered or dropped
// by its owner (or by a superuser).
func withPublicationOwnerGranted(txn *sql.Tx, pubName string, fn func() error) error {
	var owner string
	err := txn.QueryRow(
		"SELECT r.rolname FROM pg_catalog.pg_publication AS p JOIN pg_catalog.pg_roles AS r ON p.pubowner = r.oid WHERE p.pubname = $1",
		pubName,
	).Scan(&owner)
	switch {
	case err == sql.ErrNoRows:
		return fn()
	case err != nil:
		return fmt.Errorf("could not get the owner of the publication %s: %w", pubName, err)
	}

	return withRolesGranted(txn, []string{owner}, fn)
}

func setPubName(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(pubNameAttr) {
		return nil
	}
	oraw, nraw := d.GetChange(pubNameAttr)
	o := oraw.(string)
	n := nraw.(string)
	database := d.Get(pubDatabaseAttr).(string)
	sql := fmt.Sprintf("ALTER PUBLICATION %s RENAME TO %s", pq.QuoteIdentifier(o), pq.QuoteIdentifier(n))
	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error updating publication name: %w", err)
	}
	d.SetId(generatePublicationID(d, database))
	return nil
}

func setPubOwner(txn *sql.Tx, d *schema.ResourceData) error {
	if !d.HasChange(pubOwnerAttr) {
		return nil
	}

	_, nraw := d.GetChange(pubOwnerAttr)
	n := nraw.(string)
	pubName := d.Get(pubNameAttr).(string)

	// A user which is not a superuser has to be a member of the new owner
	// to give it the publication, the role is granted during the change.
	err := withRolesGranted(txn, []string{n}, func() error {
		sql := fmt.Sprintf("ALTER PUBLICATION %s OWNER TO %s", pq.QuoteIdentifier(pubName), pq.QuoteIdentifier(n))
		_, err := txn.Exec(sql)
		return err
	})
	if err != nil {
		return fmt.Errorf("error updating publication owner: %w", err)
	}
	return nil
}

// pubTable is a table of a publication with its options.
type pubTable struct {
	oid  int64
	name string
	// only is true for ONLY (the descendants are not published), star is true to
	// write the descendants explicitly (*)
	only, star bool
	columns    []string
	filter     string
	filterNorm string
}

// clause returns the table as PostgreSQL expects it after TABLE.
func (t pubTable) clause() string {
	clause := quoteTableName(t.name)
	switch {
	case t.only:
		clause = "ONLY " + clause
	case t.star:
		clause += " *"
	}
	return clause + t.options()
}

// options returns the column list and the row filter of the table.
func (t pubTable) options() string {
	clause := ""
	if len(t.columns) > 0 {
		quoted := make([]string, len(t.columns))
		for i, c := range t.columns {
			quoted[i] = pq.QuoteIdentifier(c)
		}
		clause += " (" + strings.Join(quoted, ", ") + ")"
	}
	if t.filter != "" {
		clause += " WHERE (" + t.filter + ")"
	}
	return clause
}

func pubTableHash(v any) int {
	m := v.(map[string]any)
	columns := publicationSetToStrings(m[pubTableColumnsAttr])
	sort.Strings(columns)
	return schema.HashString(fmt.Sprintf(
		"%s|%t|%s|%s", m[pubTableNameAttr], m[pubTableOnlyAttr], strings.Join(columns, ","), m[pubTableRowFilterAttr],
	))
}

func publicationSetToStrings(v any) []string {
	set, ok := v.(*schema.Set)
	if !ok || set == nil {
		return nil
	}
	var out []string
	for _, e := range set.List() {
		out = append(out, e.(string))
	}
	return out
}

// pubTablesFromBlocks returns the tables of the blocks `table`.
func pubTablesFromBlocks(v any) []pubTable {
	var tables []pubTable
	for _, e := range v.(*schema.Set).List() {
		m := e.(map[string]any)
		columns := publicationSetToStrings(m[pubTableColumnsAttr])
		sort.Strings(columns)
		only, _ := m[pubTableOnlyAttr].(bool)
		filter, _ := m[pubTableRowFilterAttr].(string)
		norm, _ := m[pubTableRowFilterNormAttr].(string)
		tables = append(tables, pubTable{
			name:       m[pubTableNameAttr].(string),
			only:       only,
			star:       !only,
			columns:    columns,
			filter:     filter,
			filterNorm: norm,
		})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].name < tables[j].name })
	return tables
}

// getDesiredPubTables returns the tables of the configuration, from the blocks
// `table` or from the attribute `tables` (a set: its elements are unique).
func getDesiredPubTables(d *schema.ResourceData) ([]pubTable, error) {
	if v := d.Get(pubTableAttr); v.(*schema.Set).Len() > 0 {
		tables := pubTablesFromBlocks(v)
		seen := map[string]bool{}
		for _, t := range tables {
			if seen[t.name] {
				return nil, fmt.Errorf("'%s' is duplicated for attribute `%s`", t.name, pubTableAttr)
			}
			seen[t.name] = true
		}
		return tables, nil
	}

	var tables []pubTable
	for _, t := range d.Get(pubTablesAttr).(*schema.Set).List() {
		tables = append(tables, pubTable{name: t.(string)})
	}
	sort.Slice(tables, func(i, j int) bool { return tables[i].name < tables[j].name })
	return tables, nil
}

// checkPubFeatures refuses the options of the tables and the schemas which the server does not have.
func checkPubFeatures(db *DBConnection, tables []pubTable, schemas []string) error {
	for _, t := range tables {
		if len(t.columns) > 0 && !db.featureSupported(featurePubColumnList) {
			return fmt.Errorf("a column list (`columns` of `%s`) is supported only for postgres version 15 and above (%s)", pubTableAttr, db.version)
		}
		if t.filter != "" && !db.featureSupported(featurePubRowFilter) {
			return fmt.Errorf("a row filter (`row_filter` of `%s`) is supported only for postgres version 15 and above (%s)", pubTableAttr, db.version)
		}
	}
	if len(schemas) > 0 && !db.featureSupported(featurePubSchema) {
		return fmt.Errorf("`%s` is supported only for postgres version 15 and above (%s)", pubSchemasAttr, db.version)
	}
	return nil
}

func checkPubAllFeatures(db *DBConnection, d *schema.ResourceData) error {
	if db.featureSupported(featurePubAllSequences) {
		return nil
	}
	if d.Get(pubAllSequencesAttr).(bool) {
		return fmt.Errorf("`%s` is supported only for postgres version 19 and above (%s)", pubAllSequencesAttr, db.version)
	}
	if d.Get(pubExceptTablesAttr).(*schema.Set).Len() > 0 {
		return fmt.Errorf("`%s` is supported only for postgres version 19 and above (%s)", pubExceptTablesAttr, db.version)
	}
	return nil
}

func resolvePubTable(txn *sql.Tx, name string) (int64, error) {
	var oid sql.NullInt64
	if err := txn.QueryRow("SELECT pg_catalog.to_regclass($1)::oid::bigint", quoteTableName(name)).Scan(&oid); err != nil {
		return 0, fmt.Errorf("could not find the table %s: %w", name, err)
	}
	if !oid.Valid {
		return 0, fmt.Errorf("the table %s does not exist", name)
	}
	return oid.Int64, nil
}

// pubRel is a row of pg_publication_rel.
type pubRel struct {
	oid     int64
	schema  string
	name    string
	kind    string
	columns []string
	filter  string
}

func getPublicationRels(txn *sql.Tx, db *DBConnection, pubName string, except bool) ([]pubRel, error) {
	columns := "'{}'::text[], ''::text"
	if db.featureSupported(featurePubColumnList) {
		columns = "COALESCE((SELECT array_agg(a.attname::text ORDER BY a.attnum) FROM pg_catalog.pg_attribute a " +
			"WHERE a.attrelid = r.prrelid AND a.attnum = ANY(r.prattrs::int2[])), '{}'::text[]), " +
			"COALESCE(pg_catalog.pg_get_expr(r.prqual, r.prrelid), '')"
	}
	where := ""
	if db.featureSupported(featurePubAllSequences) {
		where = fmt.Sprintf(" AND r.prexcept = %t", except)
	} else if except {
		return nil, nil
	}
	query := "SELECT r.prrelid::bigint, n.nspname, c.relname, c.relkind::text, " + columns +
		" FROM pg_catalog.pg_publication_rel r" +
		" JOIN pg_catalog.pg_publication p ON p.oid = r.prpubid" +
		" JOIN pg_catalog.pg_class c ON c.oid = r.prrelid" +
		" JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace" +
		" WHERE p.pubname = $1" + where + " ORDER BY n.nspname, c.relname"
	rows, err := txn.Query(query, pubName)
	if err != nil {
		return nil, fmt.Errorf("could not get the tables of the publication: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rels []pubRel
	for rows.Next() {
		var r pubRel
		if err := rows.Scan(&r.oid, &r.schema, &r.name, &r.kind, pq.Array(&r.columns), &r.filter); err != nil {
			return nil, fmt.Errorf("could not get the tables of the publication: %w", err)
		}
		rels = append(rels, r)
	}
	return rels, rows.Err()
}

func inheritanceDescendants(txn *sql.Tx, oid int64) ([]int64, error) {
	rows, err := txn.Query(
		"WITH RECURSIVE d(oid) AS (SELECT inhrelid FROM pg_catalog.pg_inherits WHERE inhparent = $1::oid "+
			"UNION SELECT i.inhrelid FROM pg_catalog.pg_inherits i JOIN d ON i.inhparent = d.oid) SELECT oid::bigint FROM d",
		oid,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var oids []int64
	for rows.Next() {
		var o int64
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		oids = append(oids, o)
	}
	return oids, rows.Err()
}

// pubTableDescendants returns the descendants of a table which PostgreSQL publishes with it: the tables
// which inherit from it (the partitions of a partitioned table are not rows of a publication).
func pubTableDescendants(txn *sql.Tx, oid int64) ([]pubRel, error) {
	rows, err := txn.Query(
		"WITH RECURSIVE d(oid) AS (SELECT i.inhrelid FROM pg_catalog.pg_inherits i JOIN pg_catalog.pg_class p ON p.oid = i.inhparent "+
			"WHERE i.inhparent = $1::oid AND p.relkind = 'r' "+
			"UNION SELECT i.inhrelid FROM pg_catalog.pg_inherits i JOIN d ON i.inhparent = d.oid) "+
			"SELECT c.oid::bigint, n.nspname, c.relname FROM d JOIN pg_catalog.pg_class c ON c.oid = d.oid "+
			"JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace ORDER BY n.nspname, c.relname",
		oid,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rels []pubRel
	for rows.Next() {
		var r pubRel
		if err := rows.Scan(&r.oid, &r.schema, &r.name); err != nil {
			return nil, err
		}
		rels = append(rels, r)
	}
	return rels, rows.Err()
}

// pubTablesFromRels groups the rows of a publication in tables: a descendant of
// another table of the publication stands for the descendants of this table
// (they are added by PostgreSQL), unless it is in explicit (it has been asked).
//
// The rows do not tell why a descendant is published. priorOnly gives the tables of explicit
// which are ONLY in the configuration: it is kept as long as the rows agree with it, which is
// when their descendants in the publication are the ones of another table of the configuration.
func pubTablesFromRels(txn *sql.Tx, rels []pubRel, explicit, priorOnly map[int64]bool) ([]pubTable, error) {
	inPub := map[int64]bool{}
	for _, r := range rels {
		inPub[r.oid] = true
	}
	descendants := map[int64][]int64{}
	collapsed := map[int64]bool{}
	// publishers: the tables of the configuration which publish a relation as one of their descendants
	publishers := map[int64][]int64{}
	for _, r := range rels {
		if r.kind != "r" {
			continue
		}
		desc, err := inheritanceDescendants(txn, r.oid)
		if err != nil {
			return nil, fmt.Errorf("could not get the descendants of %s.%s: %w", r.schema, r.name, err)
		}
		descendants[r.oid] = desc
		for _, o := range desc {
			if inPub[o] && !explicit[o] {
				collapsed[o] = true
			}
			if explicit[r.oid] && !priorOnly[r.oid] {
				publishers[o] = append(publishers[o], r.oid)
			}
		}
	}

	var tables []pubTable
	for _, r := range rels {
		if collapsed[r.oid] {
			continue
		}
		t := pubTable{
			oid:     r.oid,
			name:    r.schema + "." + r.name,
			columns: r.columns,
			filter:  r.filter,
		}
		sort.Strings(t.columns)
		// The descendants which are not tables of the configuration themselves tell ONLY from the default.
		// Without any (no descendants, a partitioned table), ONLY is what the configuration says.
		missing, unexplained := 0, 0
		for _, o := range descendants[r.oid] {
			if explicit[o] {
				continue
			}
			if !inPub[o] {
				missing++
			} else if len(publishers[o]) == 0 {
				unexplained++
			}
		}
		t.only = missing > 0 || (priorOnly[r.oid] && unexplained == 0)
		tables = append(tables, t)
	}
	return tables, nil
}

// pubRelEqual tells whether a row of the publication has the options of the table which publishes it.
// old are the tables of the state of Terraform, by oid: a row filter is not compared
// as a text, because PostgreSQL stores a normalized form of it.
func pubRelEqual(want pubTable, have pubRel, old map[int64]pubTable) bool {
	columns := append([]string{}, have.columns...)
	sort.Strings(columns)
	if strings.Join(want.columns, ",") != strings.Join(columns, ",") {
		return false
	}
	switch {
	case want.filter == "" && have.filter == "":
		return true
	case want.filter == "" || have.filter == "":
		return false
	}
	o, ok := old[want.oid]
	return ok && o.filter == want.filter && o.filterNorm == have.filter
}

func setPubTables(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string) error {
	if d.Get(pubAllTablesAttr).(bool) || (!d.HasChange(pubTablesAttr) && !d.HasChange(pubTableAttr)) {
		return nil
	}

	want, err := getDesiredPubTables(d)
	if err != nil {
		return err
	}
	if err := checkPubFeatures(db, want, nil); err != nil {
		return err
	}

	// The rows the publication must have: the tables asked and, unless ONLY, their descendants with the
	// options of the table. A descendant asked for itself has its own options.
	type row struct {
		name  string
		table pubTable
	}
	rowOf := map[int64]row{}
	var order []int64
	for i := range want {
		if want[i].oid, err = resolvePubTable(txn, want[i].name); err != nil {
			return err
		}
		if _, dup := rowOf[want[i].oid]; dup {
			return fmt.Errorf("'%s' is duplicated for attribute `%s`", want[i].name, pubTablesAttr)
		}
		rowOf[want[i].oid] = row{quoteTableName(want[i].name), want[i]}
		order = append(order, want[i].oid)
	}
	for _, w := range want {
		if w.only {
			continue
		}
		descendants, err := pubTableDescendants(txn, w.oid)
		if err != nil {
			return fmt.Errorf("could not get the descendants of %s: %w", w.name, err)
		}
		for _, r := range descendants {
			if _, ok := rowOf[r.oid]; !ok {
				rowOf[r.oid] = row{pq.QuoteIdentifier(r.schema) + "." + pq.QuoteIdentifier(r.name), w}
				order = append(order, r.oid)
			}
		}
	}

	// the tables of the state, to compare the row filters
	old := map[int64]pubTable{}
	oldBlocks, _ := d.GetChange(pubTableAttr)
	for _, t := range pubTablesFromBlocks(oldBlocks) {
		if oid, err := resolvePubTable(txn, t.name); err == nil {
			t.oid = oid
			old[oid] = t
		}
	}

	rels, err := getPublicationRels(txn, db, pubName, false)
	if err != nil {
		return err
	}
	// Row by row: a table which does not change is never dropped, and a descendant which another table
	// still publishes stays in the publication.
	upToDate := map[int64]bool{}
	for _, r := range rels {
		if wanted, ok := rowOf[r.oid]; ok && pubRelEqual(wanted.table, r, old) {
			upToDate[r.oid] = true
			continue
		}
		name := pq.QuoteIdentifier(r.schema) + "." + pq.QuoteIdentifier(r.name)
		if _, err := txn.Exec(fmt.Sprintf("ALTER PUBLICATION %s DROP TABLE ONLY %s", pq.QuoteIdentifier(pubName), name)); err != nil {
			return fmt.Errorf("could not drop a table from the publication: %w", err)
		}
	}
	for _, oid := range order {
		if upToDate[oid] {
			continue
		}
		wanted := rowOf[oid]
		if _, err := txn.Exec(fmt.Sprintf(
			"ALTER PUBLICATION %s ADD TABLE ONLY %s%s", pq.QuoteIdentifier(pubName), wanted.name, wanted.table.options(),
		)); err != nil {
			return fmt.Errorf("could not add a table to the publication: %w", err)
		}
	}
	return nil
}

// setPubSchemas drops (drop) or adds (otherwise) the schemas of the publication.
func setPubSchemas(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string, drop bool) error {
	if !d.HasChange(pubSchemasAttr) {
		return nil
	}

	oraw, nraw := d.GetChange(pubSchemasAttr)
	added := arrayDifference(nraw.(*schema.Set).List(), oraw.(*schema.Set).List())
	dropped := arrayDifference(oraw.(*schema.Set).List(), nraw.(*schema.Set).List())
	if len(added) > 0 && !db.featureSupported(featurePubSchema) {
		return checkPubFeatures(db, nil, []string{added[0].(string)})
	}

	if !drop {
		dropped = nil
	} else {
		added = nil
	}
	for _, s := range dropped {
		if _, err := txn.Exec(fmt.Sprintf("ALTER PUBLICATION %s DROP TABLES IN SCHEMA %s", pq.QuoteIdentifier(pubName), pq.QuoteIdentifier(s.(string)))); err != nil {
			return fmt.Errorf("could not drop a schema from the publication: %w", err)
		}
	}
	for _, s := range added {
		if _, err := txn.Exec(fmt.Sprintf("ALTER PUBLICATION %s ADD TABLES IN SCHEMA %s", pq.QuoteIdentifier(pubName), pq.QuoteIdentifier(s.(string)))); err != nil {
			return fmt.Errorf("could not add a schema to the publication: %w", err)
		}
	}
	return nil
}

// pubAllClause returns the clause which publishes all the tables and / or all the sequences.
func pubAllClause(d *schema.ResourceData) string {
	var parts []string
	if d.Get(pubAllTablesAttr).(bool) {
		part := "ALL TABLES"
		var except []string
		for _, t := range publicationSetToStrings(d.Get(pubExceptTablesAttr)) {
			except = append(except, "TABLE "+quoteTableName(t))
		}
		sort.Strings(except)
		if len(except) > 0 {
			part += " EXCEPT (" + strings.Join(except, ", ") + ")"
		}
		parts = append(parts, part)
	}
	if d.Get(pubAllSequencesAttr).(bool) {
		parts = append(parts, "ALL SEQUENCES")
	}
	return strings.Join(parts, ", ")
}

func setPubExceptTables(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string) error {
	if !d.HasChange(pubExceptTablesAttr) {
		return nil
	}
	if err := checkPubAllFeatures(db, d); err != nil {
		return err
	}
	// SET replaces everything, the sequences are kept if the publication has them
	if _, err := txn.Exec(fmt.Sprintf("ALTER PUBLICATION %s SET %s", pq.QuoteIdentifier(pubName), pubAllClause(d))); err != nil {
		return fmt.Errorf("could not change the excluded tables: %w", err)
	}
	return nil
}

func setPubParams(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string) error {
	paramAlterTemplate := "ALTER PUBLICATION %s %s"
	publicationParametersString, err := getPublicationParameters(d, db)
	if err != nil {
		return fmt.Errorf("error getting publication parameters: %w", err)
	}
	if publicationParametersString != "" {
		sql := fmt.Sprintf(paramAlterTemplate, pq.QuoteIdentifier(pubName), publicationParametersString)
		if _, err := txn.Exec(sql); err != nil {
			return fmt.Errorf("error updating publication parameters: %w", err)
		}
	}
	return nil
}

func resourcePostgreSQLPublicationCreate(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePublication) {
		return fmt.Errorf(
			"postgresql_publication resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	name := d.Get(pubNameAttr).(string)
	databaseName := getDatabaseForPublication(d, db.client.databaseName)
	tables, err := getTablesForPublication(db, d)
	if err != nil {
		return fmt.Errorf("could not get tables for publication: %w", err)
	}
	publicationParameters, err := getPublicationParameters(d, db)
	if err != nil {
		return fmt.Errorf("could not get publication parameters: %w", err)
	}
	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return fmt.Errorf("could not start transaction: %w", err)
	}
	defer deferredRollback(txn)

	sql := fmt.Sprintf("CREATE PUBLICATION %s %s %s", pq.QuoteIdentifier(name), tables, publicationParameters)

	if _, err := txn.Exec(sql); err != nil {
		return fmt.Errorf("error creating Publication: %w", err)
	}
	if err := setPubOwner(txn, d); err != nil {
		return fmt.Errorf("could not set publication owner during creation: %w", err)
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("error creating Publication: %w", err)
	}

	d.SetId(generatePublicationID(d, databaseName))

	return resourcePostgreSQLPublicationReadImpl(db, d)
}

func resourcePostgreSQLPublicationExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	if !db.featureSupported(featurePublication) {
		return false, fmt.Errorf(
			"postgresql_publication resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	var PublicationName string

	database, PublicationName, err := getDBPublicationName(d, db.client)
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

	query := "SELECT pubname FROM pg_catalog.pg_publication WHERE pubname = $1"
	err = txn.QueryRow(query, PublicationName).Scan(&PublicationName)
	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}

	return true, nil
}

func resourcePostgreSQLPublicationRead(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLPublicationReadImpl(db, d)
}

func resourcePostgreSQLPublicationReadImpl(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePublication) {
		return fmt.Errorf(
			"postgresql_publication resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	database, PublicationName, err := getDBPublicationName(d, db.client)
	if err != nil {
		return fmt.Errorf("could not get publication name: %w", err)
	}

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return fmt.Errorf("could not start transaction: %w", err)
	}
	defer deferredRollback(txn)

	var publishParams []string
	var puballtables, puballsequences, pubinsert, pubupdate, pubdelete, pubtruncate, pubviaroot bool
	var pubgencols string
	var pubowner string
	columns := []string{"puballtables", "pubinsert", "pubupdate", "pubdelete", "r.rolname as pubownername"}
	values := []any{
		&puballtables,
		&pubinsert,
		&pubupdate,
		&pubdelete,
		&pubowner,
	}

	if db.featureSupported(featurePublishViaRoot) {
		columns = append(columns, "pubviaroot")
		values = append(values, &pubviaroot)
	}
	if db.featureSupported(featurePubTruncate) {
		columns = append(columns, "pubtruncate")
		values = append(values, &pubtruncate)
	}
	if db.featureSupported(featurePubGeneratedColumns) {
		columns = append(columns, "pubgencols::text")
		values = append(values, &pubgencols)
	}
	if db.featureSupported(featurePubAllSequences) {
		columns = append(columns, "puballsequences")
		values = append(values, &puballsequences)
	}

	query := fmt.Sprintf("SELECT %s FROM pg_catalog.pg_publication as p join pg_catalog.pg_roles as r on p.pubowner = r.oid WHERE pubname = $1", strings.Join(columns, ", "))
	err = txn.QueryRow(query, PublicationName).Scan(values...)

	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL Publication (%s) not found for database %s", PublicationName, database)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("error reading publication info: %w", err)
	}

	if pubinsert {
		publishParams = append(publishParams, "insert")
	}
	if pubupdate {
		publishParams = append(publishParams, "update")
	}
	if pubdelete {
		publishParams = append(publishParams, "delete")
	}
	if pubtruncate {
		publishParams = append(publishParams, "truncate")
	}

	if puballtables {
		if err := readPubAllTables(txn, db, d, PublicationName); err != nil {
			return err
		}
	} else {
		if err := readPubTables(txn, db, d, PublicationName); err != nil {
			return err
		}
	}

	if db.featureSupported(featurePubSchema) {
		var schemas []string
		rows, err := txn.Query(
			"SELECT n.nspname FROM pg_catalog.pg_publication_namespace pn "+
				"JOIN pg_catalog.pg_publication p ON p.oid = pn.pnpubid "+
				"JOIN pg_catalog.pg_namespace n ON n.oid = pn.pnnspid WHERE p.pubname = $1 ORDER BY 1",
			PublicationName,
		)
		if err != nil {
			return fmt.Errorf("could not get publication schemas: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var schemaName string
			if err := rows.Scan(&schemaName); err != nil {
				return fmt.Errorf("could not get schemas: %w", err)
			}
			schemas = append(schemas, schemaName)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("got rows.Err: %w", err)
		}
		d.Set(pubSchemasAttr, schemas)
	}

	// the name is set before the ID is built from it (it is empty when importing)
	d.Set(pubNameAttr, PublicationName)
	d.Set(pubDatabaseAttr, database)
	d.SetId(generatePublicationID(d, database))
	d.Set(pubOwnerAttr, pubowner)
	d.Set(pubAllTablesAttr, puballtables)
	d.Set(pubPublishAttr, publishParams)
	if sliceContainsStr(columns, "pubviaroot") {
		d.Set(pubPublishViaPartitionRootAttr, pubviaroot)
	}
	if db.featureSupported(featurePubGeneratedColumns) {
		generated := map[string]string{"n": "none", "s": "stored", "v": "virtual"}[pubgencols]
		if generated == "" {
			generated = pubgencols
		}
		d.Set(pubGeneratedColumnsAttr, generated)
	}
	if db.featureSupported(featurePubAllSequences) {
		d.Set(pubAllSequencesAttr, puballsequences)
	}
	return nil
}

// readPubAllTables reads the tables of a publication FOR ALL TABLES: all the tables
// except the ones which are excluded (PostgreSQL 19+).
func readPubAllTables(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string) error {
	rows, err := txn.Query(
		"SELECT CONCAT(schemaname,'.',tablename) as fulltablename FROM pg_catalog.pg_publication_tables WHERE pubname = $1",
		pubName,
	)
	if err != nil {
		return fmt.Errorf("could not get publication tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return fmt.Errorf("could not get tables: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("got rows.Err: %w", err)
	}
	d.Set(pubTablesAttr, tables)
	d.Set(pubTableAttr, nil)

	if db.featureSupported(featurePubAllSequences) {
		rels, err := getPublicationRels(txn, db, pubName, true)
		if err != nil {
			return err
		}
		explicit := map[int64]bool{}
		for _, name := range publicationSetToStrings(d.Get(pubExceptTablesAttr)) {
			if oid, err := resolvePubTable(txn, name); err == nil {
				explicit[oid] = true
			}
		}
		except, err := pubTablesFromRels(txn, rels, explicit, nil)
		if err != nil {
			return err
		}
		var names []string
		for _, t := range except {
			names = append(names, pubTableDisplayName(txn, d, t))
		}
		d.Set(pubExceptTablesAttr, names)
	}
	return nil
}

// pubTableDisplayName returns the name of the table as it is written in the
// configuration, when it is the same table, otherwise <schema>.<table>.
func pubTableDisplayName(txn *sql.Tx, d *schema.ResourceData, t pubTable) string {
	var names []string
	names = append(names, publicationSetToStrings(d.Get(pubTablesAttr))...)
	names = append(names, publicationSetToStrings(d.Get(pubExceptTablesAttr))...)
	for _, b := range pubTablesFromBlocks(d.Get(pubTableAttr)) {
		names = append(names, b.name)
	}
	for _, name := range names {
		if oid, err := resolvePubTable(txn, name); err == nil && oid == t.oid {
			return name
		}
	}
	return t.name
}

// readPubTables reads the tables of a publication which is not FOR ALL TABLES, from
// pg_publication_rel: the tables of the schemas of the publication are not listed.
func readPubTables(txn *sql.Tx, db *DBConnection, d *schema.ResourceData, pubName string) error {
	blocksMode := d.Get(pubTableAttr).(*schema.Set).Len() > 0

	// What the configuration says about the tables: the descendants asked explicitly,
	// the name of the table, ONLY as long as the rows agree with it, the row filter as written.
	explicit := map[int64]bool{}
	priorOnly := map[int64]bool{}
	prior := map[int64]pubTable{}
	// (`tables` is Computed: it keeps the tables of the state when the blocks are used)
	if !blocksMode {
		for _, name := range publicationSetToStrings(d.Get(pubTablesAttr)) {
			if oid, err := resolvePubTable(txn, name); err == nil {
				explicit[oid] = true
			}
		}
	}
	for _, t := range pubTablesFromBlocks(d.Get(pubTableAttr)) {
		if oid, err := resolvePubTable(txn, t.name); err == nil {
			t.oid = oid
			prior[oid] = t
			explicit[oid] = true
			priorOnly[oid] = t.only
		}
	}

	rels, err := getPublicationRels(txn, db, pubName, false)
	if err != nil {
		return err
	}
	tables, err := pubTablesFromRels(txn, rels, explicit, priorOnly)
	if err != nil {
		return err
	}

	var names []string
	var blocks []any
	for _, t := range tables {
		name := pubTableDisplayName(txn, d, t)
		names = append(names, name)

		filter := t.filter
		if p, ok := prior[t.oid]; ok {
			// the row filter as written, as long as PostgreSQL has the form it had given for it
			if p.filter != "" && (p.filterNorm == "" || p.filterNorm == t.filter) {
				filter = p.filter
			}
		}
		// without blocks in the configuration only the tables with options are listed in blocks
		if !blocksMode && len(t.columns) == 0 && t.filter == "" && !t.only {
			continue
		}
		columns := make([]any, len(t.columns))
		for i, c := range t.columns {
			columns[i] = c
		}
		blocks = append(blocks, map[string]any{
			pubTableNameAttr:          name,
			pubTableOnlyAttr:          t.only,
			pubTableColumnsAttr:       columns,
			pubTableRowFilterAttr:     filter,
			pubTableRowFilterNormAttr: t.filter,
		})
	}
	d.Set(pubTablesAttr, names)
	d.Set(pubTableAttr, blocks)
	return nil
}

func resourcePostgreSQLPublicationDelete(db *DBConnection, d *schema.ResourceData) error {
	if !db.featureSupported(featurePublication) {
		return fmt.Errorf(
			"postgresql_publication resource is not supported for this Postgres version (%s)",
			db.version,
		)
	}

	publicationName := d.Get(pubNameAttr).(string)
	database := getDatabaseForPublication(d, db.client.databaseName)

	txn, err := startTransaction(db.client, database)
	if err != nil {
		return fmt.Errorf("could not start transaction: %w", err)
	}
	defer deferredRollback(txn)
	dropMode := "RESTRICT"
	if d.Get(pubDropCascadeAttr).(bool) {
		dropMode = "CASCADE"
	}

	err = withPublicationOwnerGranted(txn, publicationName, func() error {
		sql := fmt.Sprintf("DROP PUBLICATION %s %s", pq.QuoteIdentifier(publicationName), dropMode)
		_, err := txn.Exec(sql)
		return err
	})
	if err != nil {
		return fmt.Errorf("could not execute sql: %w", err)
	}

	if err = txn.Commit(); err != nil {
		return fmt.Errorf("error deleting Publication: %w", err)
	}
	d.SetId("")

	return nil
}

func getDatabaseForPublication(d *schema.ResourceData, databaseName string) string {
	if v, ok := d.GetOk(pubDatabaseAttr); ok {
		databaseName = v.(string)
	}

	return databaseName
}

// getTablesForPublication returns the FOR clause of CREATE PUBLICATION.
func getTablesForPublication(db *DBConnection, d *schema.ResourceData) (string, error) {
	if err := checkPubAllFeatures(db, d); err != nil {
		return "", err
	}
	if all := pubAllClause(d); all != "" {
		return "FOR " + all, nil
	}

	tables, err := getDesiredPubTables(d)
	if err != nil {
		return "", err
	}
	schemas := publicationSetToStrings(d.Get(pubSchemasAttr))
	sort.Strings(schemas)
	if err := checkPubFeatures(db, tables, schemas); err != nil {
		return "", err
	}

	var objects []string
	for _, t := range tables {
		objects = append(objects, t.clause())
	}
	if len(schemas) == 0 {
		if len(objects) == 0 {
			return "", nil
		}
		return "FOR TABLE " + strings.Join(objects, ", "), nil
	}

	// with the schemas every object has its own keyword
	for i := range objects {
		objects[i] = "TABLE " + objects[i]
	}
	for _, s := range schemas {
		objects = append(objects, "TABLES IN SCHEMA "+pq.QuoteIdentifier(s))
	}
	return "FOR " + strings.Join(objects, ", "), nil
}

func validatedPublicationPublishParams(paramList []any) ([]string, error) {
	var attrs []string
	if elem, ok := isUniqueArr(paramList); !ok {
		return make([]string, 0), fmt.Errorf("'%s' is duplicated for attribute `%s`", elem.(string), pubTablesAttr)
	}

	validation := []string{"insert", "update", "delete", "truncate"}
	for _, attr := range paramList {
		if !sliceContainsStr(validation, attr.(string)) {
			return make([]string, 0), fmt.Errorf("invalid value of `%s`: %s. Should be at least one of '%s'", pubPublishAttr, attr, strings.Join(validation, ", "))
		}
		attrs = append(attrs, attr.(string))
	}

	return attrs, nil
}

func getPublicationParameters(d *schema.ResourceData, db *DBConnection) (string, error) {
	parameterSQLTemplate := ""
	returnValue := ""
	pubParams := make(map[string]string, 3)
	if d.IsNewResource() {
		if v, ok := d.GetOk(pubPublishViaPartitionRootAttr); ok {
			if !db.featureSupported(featurePublishViaRoot) {
				return "", fmt.Errorf(
					"publish_via_partition_root attribute is supported only for postgres version 13 and above",
				)
			}
			pubParams["publish_via_partition_root"] = fmt.Sprintf("%v", v.(bool))
		}

		if v, ok := d.GetOk(pubGeneratedColumnsAttr); ok {
			if !db.featureSupported(featurePubGeneratedColumns) {
				return "", fmt.Errorf(
					"%s attribute is supported only for postgres version 18 and above", pubGeneratedColumnsAttr,
				)
			}
			pubParams["publish_generated_columns"] = pq.QuoteLiteral(v.(string))
		}

		if v, ok := d.GetOk(pubPublishAttr); ok {
			if paramsList, err := validatedPublicationPublishParams(v.([]any)); err != nil {
				return "", err
			} else {
				pubParams["publish"] = fmt.Sprintf("'%s'", strings.Join(paramsList, ", "))
			}
		}

		parameterSQLTemplate = "WITH (%s)"

	} else {

		if d.HasChange(pubPublishViaPartitionRootAttr) {
			if !db.featureSupported(featurePublishViaRoot) {
				return "", fmt.Errorf(
					"publish_via_partition_root attribute is supported only for postgres version 13 and above",
				)
			}
			_, nraw := d.GetChange(pubPublishViaPartitionRootAttr)
			pubParams["publish_via_partition_root"] = fmt.Sprintf("%v", nraw.(bool))
		}

		if d.HasChange(pubGeneratedColumnsAttr) {
			if !db.featureSupported(featurePubGeneratedColumns) {
				return "", fmt.Errorf(
					"%s attribute is supported only for postgres version 18 and above", pubGeneratedColumnsAttr,
				)
			}
			_, nraw := d.GetChange(pubGeneratedColumnsAttr)
			pubParams["publish_generated_columns"] = pq.QuoteLiteral(nraw.(string))
		}

		if d.HasChange(pubPublishAttr) {
			_, nraw := d.GetChange(pubPublishAttr)
			if paramsList, err := validatedPublicationPublishParams(nraw.([]any)); err != nil {
				return "", err
			} else {
				pubParams["publish"] = fmt.Sprintf("'%s'", strings.Join(paramsList, ", "))
			}
		}
		parameterSQLTemplate = "SET (%s)"

	}
	var paramsList []string
	for k, v := range pubParams {
		paramsList = append(paramsList, fmt.Sprintf("%s = %s", k, v))
	}
	sort.Strings(paramsList)
	if len(paramsList) > 0 {
		returnValue = fmt.Sprintf(parameterSQLTemplate, strings.Join(paramsList, ","))
	}
	return returnValue, nil
}

func generatePublicationID(d *schema.ResourceData, databaseName string) string {
	return strings.Join([]string{
		databaseName,
		d.Get(pubNameAttr).(string),
	}, ".")
}

// getDBPublicationName returns database and publication name. If we are importing this
// resource, they will be parsed from the resource ID (it will return an error if parsing failed)
// otherwise they will be simply get from the state.
func getDBPublicationName(d *schema.ResourceData, client *Client) (string, string, error) {
	database := getDatabaseForPublication(d, client.databaseName)
	PublicationName := d.Get(pubNameAttr).(string)

	// When importing, we have to parse the ID to find publication and database names.
	if PublicationName == "" {
		parsed := strings.SplitN(d.Id(), ".", 2)
		if len(parsed) != 2 {
			return "", "", fmt.Errorf("publication ID %s has not the expected format 'database.publication_name': %v", d.Id(), parsed)
		}
		database = parsed[0]
		PublicationName = parsed[1]
	}
	return database, PublicationName, nil
}

// getPublicationNameFromID returns the name of the publication of an ID 'database.publication_name'.
func getPublicationNameFromID(ID string) string {
	_, name, _ := strings.Cut(ID, ".")
	return name
}
