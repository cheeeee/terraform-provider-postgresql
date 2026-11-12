package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/lib/pq"
)

type subscriptionOptionKind int

const (
	subscriptionBool subscriptionOptionKind = iota
	subscriptionString
	subscriptionInt
)

// subscriptionOption describes an option of CREATE SUBSCRIPTION ... WITH (...) which is
// stored in the catalog pg_subscription: it is created with the subscription, read back
// from the catalog and altered with ALTER SUBSCRIPTION ... SET (...).
type subscriptionOption struct {
	name   string
	kind   subscriptionOptionKind
	column string
	// feature is the flag of the version which introduced the option (since is its
	// number, for the error message). Options available in every version have no flag.
	feature *featureName
	since   string
	// needsDisabled is set when the server only alters the option while the
	// subscription is disabled.
	needsDisabled bool
	schema        *schema.Schema
}

func subFeature(f featureName) *featureName { return &f }

// subscriptionOptions are the options of the subscriptions, except slot_name, which has
// its own handling, and enabled, which is altered by ENABLE and DISABLE.
var subscriptionOptions = []subscriptionOption{
	{
		name: "synchronous_commit", kind: subscriptionString, column: "subsynccommit",
		schema: &schema.Schema{
			Type: schema.TypeString, Optional: true, Computed: true,
			Description:  "The value of synchronous_commit used by the apply worker of the subscription: local, remote_write, remote_apply, on or off. The server default is off",
			ValidateFunc: validation.StringInSlice([]string{"local", "remote_write", "remote_apply", "on", "off"}, false),
		},
	},
	{
		name: "binary", kind: subscriptionBool, column: "subbinary", feature: subFeature(featureSubscriptionBinary), since: "14",
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the publisher sends the data in binary format. The server default is false. Requires PostgreSQL 14 or later",
		},
	},
	{
		name: "streaming", kind: subscriptionString, column: "substream", feature: subFeature(featureSubscriptionBinary), since: "14",
		schema: &schema.Schema{
			Type: schema.TypeString, Optional: true, Computed: true,
			Description:  "Whether the publisher streams the in-progress transactions: off, on or parallel (parallel requires PostgreSQL 16 or later). The server default is off. Requires PostgreSQL 14 or later",
			ValidateFunc: validation.StringInSlice([]string{"off", "on", "parallel"}, false),
		},
	},
	{
		name: "two_phase", kind: subscriptionBool, column: "subtwophasestate", feature: subFeature(featureSubscriptionTwoPhase), since: "15",
		needsDisabled: true,
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether two-phase commit is enabled for the subscription. The server default is false. Requires PostgreSQL 15 or later. Before PostgreSQL 18 it can only be set when the subscription is created: changing it replaces the subscription",
		},
	},
	{
		name: "disable_on_error", kind: subscriptionBool, column: "subdisableonerr", feature: subFeature(featureSubscriptionTwoPhase), since: "15",
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the subscription is disabled when an error occurs in the apply worker. The server default is false. Requires PostgreSQL 15 or later",
		},
	},
	{
		name: "password_required", kind: subscriptionBool, column: "subpasswordrequired", feature: subFeature(featureSubscriptionRunAsOwner), since: "16",
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the connection to the publisher must use a password. The server default is true. Requires PostgreSQL 16 or later",
		},
	},
	{
		name: "run_as_owner", kind: subscriptionBool, column: "subrunasowner", feature: subFeature(featureSubscriptionRunAsOwner), since: "16",
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the changes are applied with the privileges of the owner of the subscription instead of the owner of the tables. The server default is false. Requires PostgreSQL 16 or later",
		},
	},
	{
		name: "origin", kind: subscriptionString, column: "suborigin", feature: subFeature(featureSubscriptionRunAsOwner), since: "16",
		schema: &schema.Schema{
			Type: schema.TypeString, Optional: true, Computed: true,
			Description:  "Whether the publisher sends the changes which have an origin (none) or all of them (any). The server default is any. Requires PostgreSQL 16 or later",
			ValidateFunc: validation.StringInSlice([]string{"any", "none"}, false),
		},
	},
	{
		name: "failover", kind: subscriptionBool, column: "subfailover", feature: subFeature(featureSubscriptionFailover), since: "17",
		needsDisabled: true,
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the replication slot of the subscription is synchronized to the standbys of the publisher. The server default is false. Requires PostgreSQL 17 or later. The provider disables the subscription while it alters the option",
		},
	},
	{
		name: "retain_dead_tuples", kind: subscriptionBool, column: "subretaindeadtuples", feature: subFeature(featureSubscriptionRetainDeadTuples), since: "18",
		needsDisabled: true,
		schema: &schema.Schema{
			Type: schema.TypeBool, Optional: true, Computed: true,
			Description: "Whether the information needed to detect conflicts is retained on the subscriber. The server default is false. Requires PostgreSQL 18 or later. The provider disables the subscription while it alters the option",
		},
	},
	{
		name: "max_retention_duration", kind: subscriptionInt, column: "submaxretention", feature: subFeature(featureSubscriptionWalReceiverTimeout), since: "19",
		schema: &schema.Schema{
			Type: schema.TypeInt, Optional: true, Computed: true,
			Description:  "The maximum time in milliseconds the dead tuples are retained for retain_dead_tuples, 0 for no limit. The server default is 0. Requires PostgreSQL 19 or later",
			ValidateFunc: validation.IntAtLeast(0),
		},
	},
	{
		name: "wal_receiver_timeout", kind: subscriptionString, column: "subwalrcvtimeout", feature: subFeature(featureSubscriptionWalReceiverTimeout), since: "19",
		schema: &schema.Schema{
			Type: schema.TypeString, Optional: true, Computed: true,
			Description:  "The wal_receiver_timeout of the apply worker of the subscription (e.g. 30s). The server default is -1, which uses the setting of the server. Requires PostgreSQL 19 or later",
			ValidateFunc: validation.StringIsNotEmpty,
		},
	},
}

func resourcePostgreSQLSubscription() *schema.Resource {
	s := map[string]*schema.Schema{
		"name": {
			Type:         schema.TypeString,
			Required:     true,
			Description:  "The name of the subscription. Changing it renames the subscription (the replication slot is not renamed)",
			ValidateFunc: validation.StringIsNotEmpty,
		},
		"database": {
			Type:        schema.TypeString,
			Optional:    true,
			Computed:    true,
			ForceNew:    true,
			Description: "Sets the database to add the subscription for",
		},
		"conninfo": {
			Type:         schema.TypeString,
			Required:     true,
			Sensitive:    true,
			Description:  "The connection string to the publisher. It should follow the keyword/value format (https://www.postgresql.org/docs/current/libpq-connect.html#LIBPQ-CONNSTRING)",
			ValidateFunc: validation.StringIsNotEmpty,
		},
		"publications": {
			Type:        schema.TypeSet,
			Required:    true,
			Elem:        &schema.Schema{Type: schema.TypeString},
			Description: "Names of the publications on the publisher to subscribe to",
		},
		"enabled": {
			Type:        schema.TypeBool,
			Optional:    true,
			Computed:    true,
			Description: "Whether the subscription is enabled, that is whether it replicates. The server default is true",
		},
		"owner": {
			Type:        schema.TypeString,
			Optional:    true,
			Computed:    true,
			Description: "The owner of the subscription",
		},
		"slot_name": {
			Type:         schema.TypeString,
			Optional:     true,
			Computed:     true,
			Description:  "Name of the replication slot to use, or NONE for no slot. The default behavior is to use the name of the subscription for the slot name",
			ValidateFunc: validation.StringIsNotEmpty,
			DiffSuppressFunc: func(_, old, new string, _ *schema.ResourceData) bool {
				return strings.EqualFold(old, "none") && strings.EqualFold(new, "none")
			},
		},
		"create_slot": {
			Type:             schema.TypeBool,
			Optional:         true,
			Default:          true,
			DiffSuppressFunc: subscriptionCreationOnly,
			Description:      "Specifies whether the command should create the replication slot on the publisher. It only matters when the subscription is created and is not read back: changing it afterwards has no effect on the subscription, but the subscription which did not create its slot keeps it when it is destroyed",
		},
		"connect": {
			Type:             schema.TypeBool,
			Optional:         true,
			DiffSuppressFunc: subscriptionCreationOnly,
			Description:      "Specifies whether the command should connect to the publisher. It only matters when the subscription is created and is not read back. With false, enabled, create_slot and copy_data cannot be true, and the subscription is created without a connection to the publisher: the server default is true",
		},
		"copy_data": {
			Type:             schema.TypeBool,
			Optional:         true,
			DiffSuppressFunc: subscriptionCreationOnly,
			Description:      "Specifies whether the existing data of the publications should be copied when the replication starts. It only matters when the subscription is created and is not read back. The server default is true",
		},
	}
	for _, option := range subscriptionOptions {
		s[option.name] = option.schema
	}

	return &schema.Resource{
		Create:        PGResourceFunc(resourcePostgreSQLSubscriptionCreate),
		Read:          PGResourceFunc(resourcePostgreSQLSubscriptionRead),
		Update:        PGResourceFunc(resourcePostgreSQLSubscriptionUpdate),
		Delete:        PGResourceFunc(resourcePostgreSQLSubscriptionDelete),
		Exists:        PGResourceExistsFunc(resourcePostgreSQLSubscriptionExists),
		CustomizeDiff: resourcePostgreSQLSubscriptionCustomizeDiff,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: s,
	}
}

// subscriptionCreationOnly suppresses the changes of the options which are only used
// to create the subscription.
func subscriptionCreationOnly(_, _, _ string, d *schema.ResourceData) bool {
	return d.Id() != ""
}

// resourcePostgreSQLSubscriptionCustomizeDiff replaces the subscription when two_phase
// changes on a server which cannot alter it (before PostgreSQL 18).
func resourcePostgreSQLSubscriptionCustomizeDiff(_ context.Context, d *schema.ResourceDiff, meta any) error {
	if d.Id() == "" || !d.HasChange("two_phase") || d.GetRawConfig().GetAttr("two_phase").IsNull() {
		return nil
	}
	db, err := meta.(*Client).Connect()
	if err != nil {
		return nil // the error will be reported when the change is applied
	}
	if !db.featureSupported(featureSubscriptionRetainDeadTuples) {
		return d.ForceNew("two_phase")
	}
	return nil
}

func subscriptionConfigured(d *schema.ResourceData, name string) bool {
	raw, diags := d.GetRawConfigAt(cty.GetAttrPath(name))
	return !diags.HasError() && !raw.IsNull() && raw.IsKnown()
}

func subscriptionOptionSQL(d *schema.ResourceData, option subscriptionOption) string {
	switch option.kind {
	case subscriptionBool:
		return fmt.Sprintf("%s = %t", option.name, d.Get(option.name).(bool))
	case subscriptionInt:
		return fmt.Sprintf("%s = %d", option.name, d.Get(option.name).(int))
	default:
		return fmt.Sprintf("%s = %s", option.name, pq.QuoteLiteral(d.Get(option.name).(string)))
	}
}

func subscriptionSlotSQL(slotName string) string {
	if strings.EqualFold(slotName, "none") {
		return "slot_name = NONE"
	}
	return "slot_name = " + pq.QuoteLiteral(slotName)
}

// checkSubscriptionOption refuses the options which the server does not know.
func checkSubscriptionOption(db *DBConnection, d *schema.ResourceData, option subscriptionOption) error {
	if option.feature != nil && !db.featureSupported(*option.feature) {
		return fmt.Errorf("attribute %s requires PostgreSQL %s or later (server is %s)", option.name, option.since, db.version)
	}
	if option.name == "streaming" && d.Get("streaming").(string) == "parallel" && !db.featureSupported(featureSubscriptionRunAsOwner) {
		return fmt.Errorf("attribute streaming = parallel requires PostgreSQL 16 or later (server is %s)", db.version)
	}
	return nil
}

// subscriptionClient returns the connection to the database of the subscription. The
// statements on subscriptions cannot all run in a transaction block, so they are sent
// one by one, outside of any transaction.
func subscriptionClient(db *DBConnection, databaseName string) (*DBConnection, error) {
	conn, err := db.client.config.NewClient(databaseName).Connect()
	if err != nil {
		return nil, fmt.Errorf("could not establish database connection: %w", err)
	}
	return conn, nil
}

func resourcePostgreSQLSubscriptionCreate(db *DBConnection, d *schema.ResourceData) error {
	subName := d.Get("name").(string)
	databaseName := getDatabaseForSubscription(d, db.client.databaseName)

	publications, err := getPublicationsForSubscription(d)
	if err != nil {
		return fmt.Errorf("could not get publications: %w", err)
	}
	connInfo, err := getConnInfoForSubscription(d)
	if err != nil {
		return fmt.Errorf("could not get conninfo: %w", err)
	}

	optionalParams, err := getOptionalParameters(db, d)
	if err != nil {
		return err
	}

	conn, err := subscriptionClient(db, databaseName)
	if err != nil {
		return err
	}

	sql := fmt.Sprintf("CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s %s;",
		pq.QuoteIdentifier(subName),
		pq.QuoteLiteral(connInfo),
		publications,
		optionalParams,
	)
	if _, err := conn.Exec(sql); err != nil {
		return fmt.Errorf("could not execute sql: %w", err)
	}

	d.SetId(generateSubscriptionID(d, databaseName))

	if subscriptionConfigured(d, "owner") {
		sql := fmt.Sprintf("ALTER SUBSCRIPTION %s OWNER TO %s", pq.QuoteIdentifier(subName), pq.QuoteIdentifier(d.Get("owner").(string)))
		if _, err := conn.Exec(sql); err != nil {
			return fmt.Errorf("could not execute sql: %w", err)
		}
	}

	return resourcePostgreSQLSubscriptionReadImpl(db, d)
}

func resourcePostgreSQLSubscriptionRead(db *DBConnection, d *schema.ResourceData) error {
	return resourcePostgreSQLSubscriptionReadImpl(db, d)
}

func resourcePostgreSQLSubscriptionReadImpl(db *DBConnection, d *schema.ResourceData) error {
	databaseName, subName, err := getDBSubscriptionName(d, db.client)
	if err != nil {
		return fmt.Errorf("could not get subscription name: %w", err)
	}

	txn, err := startTransaction(db.client, databaseName)
	if err != nil {
		return fmt.Errorf("could not start transaction: %w", err)
	}
	defer deferredRollback(txn)

	// pg_subscription is only partly readable by the users which are not superusers
	// (subconninfo is not, nor, before PostgreSQL 15, subsynccommit): what we are not
	// allowed to read, or what the version does not know, stays as it is in the state.
	readable := map[string]bool{}
	rows, err := txn.Query(`SELECT attname::text FROM pg_catalog.pg_attribute
		WHERE attrelid = 'pg_catalog.pg_subscription'::regclass AND attnum > 0 AND NOT attisdropped
		AND has_column_privilege(attrelid, attnum, 'SELECT')`)
	if err != nil {
		return fmt.Errorf("could not list the columns of pg_subscription: %w", err)
	}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			_ = rows.Close()
			return fmt.Errorf("could not list the columns of pg_subscription: %w", err)
		}
		readable[column] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not list the columns of pg_subscription: %w", err)
	}
	_ = rows.Close()

	// Each value is selected as text, then decoded.
	var owner, enabled sql.NullString
	var publications []string
	columns := []string{"pg_catalog.pg_get_userbyid(subowner)::text", "subenabled::text"}
	dests := []any{&owner, &enabled}
	catalogValues := map[string]*sql.NullString{}
	add := func(column string) {
		if readable[column] {
			v := &sql.NullString{}
			catalogValues[column] = v
			columns = append(columns, column+"::text")
			dests = append(dests, v)
		}
	}
	add("subconninfo")
	add("subslotname")
	for _, option := range subscriptionOptions {
		add(option.column)
	}
	if readable["subpublications"] {
		columns = append(columns, "subpublications")
		dests = append(dests, pq.Array(&publications))
	}

	query := fmt.Sprintf(`SELECT %s FROM pg_catalog.pg_subscription
		WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())`,
		strings.Join(columns, ", "))
	err = txn.QueryRow(query, subName).Scan(dests...)
	switch {
	case err == sql.ErrNoRows:
		log.Printf("[WARN] PostgreSQL Subscription (%s) not found for database %s", subName, databaseName)
		d.SetId("")
		return nil
	case err != nil:
		return fmt.Errorf("could not read subscription: %w", err)
	}

	d.Set("name", subName)
	d.Set("database", databaseName)
	d.SetId(generateSubscriptionID(d, databaseName))
	d.Set("owner", owner.String)
	d.Set("enabled", subscriptionTextIsTrue(enabled.String))
	if v := catalogValues["subconninfo"]; v != nil {
		d.Set("conninfo", v.String)
	}
	if readable["subpublications"] {
		d.Set("publications", publications)
	}
	if v := catalogValues["subslotname"]; v != nil {
		if v.Valid {
			d.Set("slot_name", v.String)
		} else {
			d.Set("slot_name", "NONE")
		}
	}
	for _, option := range subscriptionOptions {
		v := catalogValues[option.column]
		if v == nil || !v.Valid {
			continue
		}
		switch {
		case option.name == "two_phase":
			d.Set(option.name, v.String != "d")
		case option.name == "streaming":
			d.Set(option.name, subscriptionStreamingName(v.String))
		case option.kind == subscriptionBool:
			d.Set(option.name, subscriptionTextIsTrue(v.String))
		case option.kind == subscriptionInt:
			n, _ := strconv.Atoi(v.String)
			d.Set(option.name, n)
		default:
			d.Set(option.name, v.String)
		}
	}

	// The options of the creation are not in the catalog: keep what the state has. A
	// subscription which has been imported is supposed to have created its slot
	// (the default), so that destroying it drops the slot.
	if _, ok := d.GetOkExists("create_slot"); !ok { //nolint:staticcheck
		d.Set("create_slot", true)
	}

	return nil
}

func subscriptionTextIsTrue(s string) bool {
	return s == "t" || s == "true"
}

// subscriptionStreamingName converts substream (a boolean before PostgreSQL 16, then a
// char) to the value of the attribute.
func subscriptionStreamingName(s string) string {
	switch s {
	case "t", "true":
		return "on"
	case "p":
		return "parallel"
	default:
		return "off"
	}
}

func resourcePostgreSQLSubscriptionUpdate(db *DBConnection, d *schema.ResourceData) error {
	oldName, newName := d.GetChange("name")
	subName := oldName.(string)
	databaseName := getDatabaseForSubscription(d, db.client.databaseName)
	quotedName := pq.QuoteIdentifier(subName)

	conn, err := subscriptionClient(db, databaseName)
	if err != nil {
		return err
	}
	exec := func(format string, args ...any) error {
		sql := fmt.Sprintf(format, args...)
		if _, err := conn.Exec(sql); err != nil {
			return fmt.Errorf("could not execute sql: %w", err)
		}
		return nil
	}

	// What has been done is saved even if a later statement fails
	d.Partial(true)

	wantEnabled := d.Get("enabled").(bool)
	enabledNow := wantEnabled
	if d.HasChange("enabled") {
		old, _ := d.GetChange("enabled")
		enabledNow = old.(bool)
	}

	// SET ( option = value ) clauses
	var sets []string
	needsDisabled := false
	for _, option := range subscriptionOptions {
		if !d.HasChange(option.name) || !subscriptionConfigured(d, option.name) {
			continue
		}
		if err := checkSubscriptionOption(db, d, option); err != nil {
			return err
		}
		sets = append(sets, subscriptionOptionSQL(d, option))
		if option.needsDisabled {
			needsDisabled = true
		}
	}
	if d.HasChange("slot_name") && subscriptionConfigured(d, "slot_name") {
		slotName := d.Get("slot_name").(string)
		sets = append(sets, subscriptionSlotSQL(slotName))
		// a subscription cannot lose its slot name while it is enabled
		if strings.EqualFold(slotName, "none") {
			needsDisabled = true
		}
	}

	if d.HasChange("conninfo") {
		if err := exec("ALTER SUBSCRIPTION %s CONNECTION %s", quotedName, pq.QuoteLiteral(d.Get("conninfo").(string))); err != nil {
			return err
		}
	}

	if needsDisabled && enabledNow {
		if err := exec("ALTER SUBSCRIPTION %s DISABLE", quotedName); err != nil {
			return err
		}
		enabledNow = false
	}
	if len(sets) > 0 {
		if err := exec("ALTER SUBSCRIPTION %s SET (%s)", quotedName, strings.Join(sets, ", ")); err != nil {
			return err
		}
	}
	// the subscription goes back to its desired state (also after a temporary disabling)
	if wantEnabled != enabledNow {
		verb := "DISABLE"
		if wantEnabled {
			verb = "ENABLE"
		}
		if err := exec("ALTER SUBSCRIPTION %s %s", quotedName, verb); err != nil {
			return err
		}
		enabledNow = wantEnabled
	}

	// The server refreshes the list of the tables to replicate (and copies the data of
	// the new ones) only for an enabled subscription.
	if d.HasChange("publications") {
		publications, err := getPublicationsForSubscription(d)
		if err != nil {
			return fmt.Errorf("could not get publications: %w", err)
		}
		if err := exec("ALTER SUBSCRIPTION %s SET PUBLICATION %s WITH (refresh = %t)", quotedName, publications, enabledNow); err != nil {
			return err
		}
	}

	if d.HasChange("owner") && subscriptionConfigured(d, "owner") {
		if err := exec("ALTER SUBSCRIPTION %s OWNER TO %s", quotedName, pq.QuoteIdentifier(d.Get("owner").(string))); err != nil {
			return err
		}
	}

	if newName.(string) != subName {
		if err := exec("ALTER SUBSCRIPTION %s RENAME TO %s", quotedName, pq.QuoteIdentifier(newName.(string))); err != nil {
			return err
		}
		d.SetId(generateSubscriptionID(d, databaseName))
	}

	d.Partial(false)

	return resourcePostgreSQLSubscriptionReadImpl(db, d)
}

func resourcePostgreSQLSubscriptionDelete(db *DBConnection, d *schema.ResourceData) error {
	subName := d.Get("name").(string)
	createSlot := d.Get("create_slot").(bool)
	connect, hasConnect := d.GetOkExists("connect") //nolint:staticcheck

	databaseName := getDatabaseForSubscription(d, db.client.databaseName)

	// Dropping a subscription can not be done in a transaction
	conn, err := subscriptionClient(db, databaseName)
	if err != nil {
		return err
	}

	// disable subscription and unset the slot before dropping in order to keep the replication
	// slot, which the subscription did not create, or to not look for a slot which was never
	// created on the publisher (connect = false)
	if !createSlot || (hasConnect && !connect.(bool)) {
		sql := fmt.Sprintf("ALTER SUBSCRIPTION %s DISABLE", pq.QuoteIdentifier(subName))
		if _, err := conn.Exec(sql); err != nil {
			return fmt.Errorf("could not execute sql: %w", err)
		}
		sql = fmt.Sprintf("ALTER SUBSCRIPTION %s SET (slot_name = NONE)", pq.QuoteIdentifier(subName))
		if _, err := conn.Exec(sql); err != nil {
			return fmt.Errorf("could not execute sql: %w", err)
		}
	}

	sql := fmt.Sprintf("DROP SUBSCRIPTION %s", pq.QuoteIdentifier(subName))

	if _, err := conn.Exec(sql); err != nil {
		return fmt.Errorf("could not execute sql: %w", err)
	}

	d.SetId("")

	return nil
}

func resourcePostgreSQLSubscriptionExists(db *DBConnection, d *schema.ResourceData) (bool, error) {
	var subName string

	database, subName, err := getDBSubscriptionName(d, db.client)
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

	query := `SELECT subname FROM pg_catalog.pg_subscription
		WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())`
	err = txn.QueryRow(query, subName).Scan(&subName)

	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, err
	}

	return true, nil
}

func getPublicationsForSubscription(d *schema.ResourceData) (string, error) {
	var publicationsString string
	setPublications, ok := d.GetOk("publications")

	if !ok {
		return publicationsString, fmt.Errorf("attribute publications is not set")
	}

	publications := setPublications.(*schema.Set).List()
	var plist []string
	if elem, ok := isUniqueArr(publications); !ok {
		return publicationsString, fmt.Errorf("'%s' is duplicated for attribute publications", elem.(string))
	}
	for _, p := range publications {
		plist = append(plist, pq.QuoteIdentifier(p.(string)))
	}

	return strings.Join(plist, ", "), nil
}

func getConnInfoForSubscription(d *schema.ResourceData) (string, error) {
	var connInfo string
	setConnInfo, ok := d.GetOk("conninfo")
	if !ok {
		return connInfo, fmt.Errorf("attribute conninfo is not set")
	}
	return setConnInfo.(string), nil
}

func generateSubscriptionID(d *schema.ResourceData, databaseName string) string {
	return strings.Join([]string{databaseName, d.Get("name").(string)}, ".")
}

func getDatabaseForSubscription(d *schema.ResourceData, databaseName string) string {
	if v, ok := d.GetOk("database"); ok {
		databaseName = v.(string)
	}

	return databaseName
}

// getDBSubscriptionName returns database and subscription name. If we are importing this
// resource, they will be parsed from the resource ID (it will return an error if parsing failed)
// otherwise they will be simply get from the state.
func getDBSubscriptionName(d *schema.ResourceData, client *Client) (string, string, error) {
	database := getDatabaseForSubscription(d, client.databaseName)
	subName := d.Get("name").(string)

	// When importing, we have to parse the ID to find subscription and database names.
	if subName == "" {
		parsed := strings.Split(d.Id(), ".")
		if len(parsed) != 2 {
			return "", "", fmt.Errorf("subscription ID %s has not the expected format 'database.subscriptionName': %v", d.Id(), parsed)
		}
		database = parsed[0]
		subName = parsed[1]
	}

	return database, subName, nil
}

// getOptionalParameters returns the WITH ( ... ) clause of CREATE SUBSCRIPTION: the
// options of the creation and the options which the configuration sets.
func getOptionalParameters(db *DBConnection, d *schema.ResourceData) (string, error) {
	var params []string

	connect, hasConnect := d.GetOkExists("connect") //nolint:staticcheck
	connectFalse := hasConnect && !connect.(bool) && subscriptionConfigured(d, "connect")
	createSlot := d.Get("create_slot").(bool)

	if connectFalse {
		// The server refuses to create the slot, to enable the subscription and to copy
		// the data when it does not connect to the publisher, and sets them to false.
		if subscriptionConfigured(d, "create_slot") && createSlot {
			return "", fmt.Errorf("connect = false and create_slot = true are mutually exclusive: set create_slot = false")
		}
		if subscriptionConfigured(d, "enabled") && d.Get("enabled").(bool) {
			return "", fmt.Errorf("connect = false and enabled = true are mutually exclusive: set enabled = false")
		}
		if subscriptionConfigured(d, "copy_data") && d.Get("copy_data").(bool) {
			return "", fmt.Errorf("connect = false and copy_data = true are mutually exclusive: set copy_data = false")
		}
		createSlot = false
	}
	params = append(params, fmt.Sprintf("create_slot = %t", createSlot))
	if connectFalse {
		params = append(params, "connect = false")
	}
	if subscriptionConfigured(d, "copy_data") {
		params = append(params, fmt.Sprintf("copy_data = %t", d.Get("copy_data").(bool)))
	}
	if subscriptionConfigured(d, "enabled") {
		params = append(params, fmt.Sprintf("enabled = %t", d.Get("enabled").(bool)))
	}
	if slotName, ok := d.GetOk("slot_name"); ok {
		params = append(params, subscriptionSlotSQL(slotName.(string)))
	}
	for _, option := range subscriptionOptions {
		if !subscriptionConfigured(d, option.name) {
			continue
		}
		if err := checkSubscriptionOption(db, d, option); err != nil {
			return "", err
		}
		params = append(params, subscriptionOptionSQL(d, option))
	}

	return fmt.Sprintf("WITH (%s)", strings.Join(params, ", ")), nil
}

func getSubscriptionNameFromID(ID string) string {
	splitted := strings.SplitN(ID, ".", 2)
	return splitted[len(splitted)-1]
}
