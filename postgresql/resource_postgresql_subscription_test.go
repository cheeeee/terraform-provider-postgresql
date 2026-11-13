package postgresql

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
	"github.com/lib/pq"
)

func testAccCheckPostgresqlSubscriptionDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(*Client)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "postgresql_subscription" {
			continue
		}

		databaseName, ok := rs.Primary.Attributes["database"]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}
		txn, err := startTransaction(client, databaseName)
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSubscriptionExists(txn, getSubscriptionNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking subscription %s", err)
		}

		if exists {
			return fmt.Errorf("Subscription still exists after destroy")
		}

		streams, err := checkSubscriptionStreams(txn, getSubscriptionNameFromID(rs.Primary.ID))

		if err != nil {
			return fmt.Errorf("error checking subscription %s", err)
		}

		if streams {
			return fmt.Errorf("Subscription still streams after destroy")
		}
	}

	return nil
}

func checkSubscriptionExists(txn *sql.Tx, subName string) (bool, error) {
	var _rez bool
	err := txn.QueryRow("SELECT TRUE from pg_catalog.pg_subscription WHERE subname=$1", subName).Scan(&_rez)

	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about subscription: %s", err)
	}

	return true, nil
}

func checkSubscriptionStreams(txn *sql.Tx, subName string) (bool, error) {
	var _rez bool
	// The apply worker of the subscription runs: pg_stat_replication hides the sessions to
	// the users which are not superusers, pg_stat_subscription does not.
	err := txn.QueryRow("SELECT TRUE from pg_catalog.pg_stat_subscription WHERE subname=$1 AND relid IS NULL AND pid IS NOT NULL", subName).Scan(&_rez)

	switch {
	case err == sql.ErrNoRows:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("error reading info about subscription: %s", err)
	}

	return true, nil
}

func testAccCheckPostgresqlSubscriptionExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("No ID is set")
		}

		databaseName, ok := rs.Primary.Attributes["database"]
		if !ok {
			return fmt.Errorf("No Attribute for database is set")
		}

		subName, ok := rs.Primary.Attributes["name"]
		if !ok {
			return fmt.Errorf("No Attribute for subscription name is set")
		}

		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, databaseName)

		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSubscriptionExists(txn, subName)

		if err != nil {
			return fmt.Errorf("error checking subscription %s", err)
		}

		if !exists {
			return fmt.Errorf("Subscription not found")
		}

		// the walsender of the publisher takes a moment to start streaming
		var streams bool
		for i := 0; i < 30; i++ {
			streams, err = checkSubscriptionStreams(txn, subName)
			if err != nil {
				return fmt.Errorf("error checking subscription %s", err)
			}
			if streams {
				break
			}
			time.Sleep(time.Second)
			if err := txn.Commit(); err != nil {
				return err
			}
			if txn, err = startTransaction(client, databaseName); err != nil {
				return err
			}
			defer deferredRollback(txn)
		}
		if !streams {
			return fmt.Errorf("Subscription not streaming")
		}

		return nil
	}
}

func getConnInfo(t *testing.T, dbName string) string {
	dbConfig := getTestConfig(t)

	// The publisher is the server itself. TEST_PUBLISHER_HOST is the address it
	// can use to connect to itself, when it is not the one used by the tests.
	host := dbConfig.Host
	if v := os.Getenv("TEST_PUBLISHER_HOST"); v != "" {
		host = v
	}

	return fmt.Sprintf(
		`host=%s port=%d dbname=%s user=%s password=%s`,
		host,
		5432,
		dbName,
		dbConfig.Username,
		dbConfig.Password,
	)
}

// The database seems to take a few second to cleanup everything
func coolDown() {
	time.Sleep(5 * time.Second)
}

func TestAccPostgresqlSubscription_Basic(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffixPub, teardownPub := setupTestDatabase(t, true, true)
	dbSuffixSub, teardownSub := setupTestDatabase(t, true, true)

	defer teardownPub()
	defer teardownSub()
	testTables := []string{"test_schema.test_table_1"}
	createTestTables(t, dbSuffixPub, testTables, "")
	createTestTables(t, dbSuffixSub, testTables, "")

	dbNamePub, _ := getTestDBNames(dbSuffixPub)
	dbNameSub, _ := getTestDBNames(dbSuffixSub)

	conninfo := getConnInfo(t, dbNamePub)
	config := getTestConfig(t)

	subName := "subscription"
	testAccPostgresqlSubscriptionDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test_pub" {
		name     	= "test_publication"
		database	= "%s"
		tables		= ["test_schema.test_table_1"]
	}
	resource "postgresql_replication_slot" "test_replication_slot" {
		name		= "%s"
		database	= "%s"
		plugin		= "pgoutput"
	}
	resource "postgresql_subscription" "test_sub" {
		name     		= postgresql_replication_slot.test_replication_slot.name
		database 		= "%s"
		conninfo 		= "%s"
		publications	= [ postgresql_publication.test_pub.name ]
		create_slot		= false
	}
	`, dbNamePub, subName, dbNamePub, dbNameSub, conninfo)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateSubscription(t, conninfo)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSubscriptionDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(
						"postgresql_subscription.test_sub"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"name",
						subName),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"database",
						dbNameSub),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"conninfo",
						conninfo),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						fmt.Sprintf("%s.#", "publications"),
						"1"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						fmt.Sprintf("%s.0", "publications"),
						"test_publication"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"create_slot",
						"false"),
					testAccCheckSubscriptionConninfo("postgresql_subscription.test_sub", config.connStr(dbNameSub), subName, conninfo),
				),
			},
			{
				// nothing to change
				Config:   testAccPostgresqlSubscriptionDatabaseConfig,
				PlanOnly: true,
			},
		},
	},
	)
	coolDown()
}

func TestAccPostgresqlSubscription_CustomSlotName(t *testing.T) {
	skipIfNotAcc(t)

	dbSuffixPub, teardownPub := setupTestDatabase(t, true, true)
	dbSuffixSub, teardownSub := setupTestDatabase(t, true, true)

	defer teardownPub()
	defer teardownSub()

	dbNamePub, _ := getTestDBNames(dbSuffixPub)
	dbNameSub, _ := getTestDBNames(dbSuffixSub)

	conninfo := getConnInfo(t, dbNamePub)
	config := getTestConfig(t)

	subName := "subscription"
	testAccPostgresqlSubscriptionDatabaseConfig := fmt.Sprintf(`
	resource "postgresql_publication" "test_pub" {
		name		= "test_publication"
		database	= "%s"
	}
	resource "postgresql_replication_slot" "test_replication_slot" {
		name		= "custom_slot_name"
		plugin		= "pgoutput"
		database	= "%s"
	}
	resource "postgresql_subscription" "test_sub" {
		name     		= "%s"
		database 		= "%s"
		conninfo 		= "%s"
		publications	= [ postgresql_publication.test_pub.name ]
		create_slot		= false
		slot_name		= "custom_slot_name"

		depends_on 		= [ postgresql_replication_slot.test_replication_slot ]
	}
	`, dbNamePub, dbNamePub, subName, dbNameSub, conninfo)
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateSubscription(t, conninfo)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPostgresqlSubscriptionDatabaseConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(
						"postgresql_subscription.test_sub"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"name",
						subName),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"database",
						dbNameSub),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"conninfo",
						conninfo),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						fmt.Sprintf("%s.#", "publications"),
						"1"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						fmt.Sprintf("%s.0", "publications"),
						"test_publication"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"create_slot",
						"false"),
					resource.TestCheckResourceAttr(
						"postgresql_subscription.test_sub",
						"slot_name",
						"custom_slot_name"),
					testAccCheckSubscriptionConninfo("postgresql_subscription.test_sub", config.connStr(dbNameSub), subName, conninfo),
				),
			},
			{
				// nothing to change
				Config:   testAccPostgresqlSubscriptionDatabaseConfig,
				PlanOnly: true,
			},
		},
	},
	)
	coolDown()
}

// subTest is the environment of a test of the subscriptions: a publisher and a
// subscriber (two databases of the server), both with the tables test_schema.test_table_1
// and test_schema.test_table_2.
type subTest struct {
	t        *testing.T
	dbPub    string
	dbSub    string
	conninfo string
}

func newSubTest(t *testing.T) *subTest {
	skipIfNotAcc(t)

	dbSuffixPub, teardownPub := setupTestDatabase(t, true, true)
	dbSuffixSub, teardownSub := setupTestDatabase(t, true, true)
	t.Cleanup(teardownPub)
	t.Cleanup(teardownSub)
	// The database seems to take a few second to cleanup everything
	t.Cleanup(func() { coolDown() })

	tables := []string{"test_schema.test_table_1", "test_schema.test_table_2"}
	createTestTables(t, dbSuffixPub, tables, "")
	createTestTables(t, dbSuffixSub, tables, "")

	dbNamePub, _ := getTestDBNames(dbSuffixPub)
	dbNameSub, _ := getTestDBNames(dbSuffixSub)

	s := &subTest{t: t, dbPub: dbNamePub, dbSub: dbNameSub, conninfo: getConnInfo(t, dbNamePub)}
	// registered last, so it runs first: before the databases are dropped
	t.Cleanup(s.cleanLeftovers)
	return s
}

// dropSubscriptions drops the subscriptions of the database dbName without connecting to
// their publisher: the slot is unset first. Best effort: it is a cleanup.
func dropSubscriptions(t *testing.T, dbName string) {
	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr(dbName))
	if err != nil {
		t.Logf("cleanup: could not connect to %s: %v", dbName, err)
		return
	}
	defer closeDB(t, db)
	rows, err := db.Query("SELECT subname FROM pg_catalog.pg_subscription WHERE subdbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())")
	if err != nil {
		t.Logf("cleanup: could not list the subscriptions of %s: %v", dbName, err)
		return
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names = append(names, name)
		}
	}
	_ = rows.Close()
	for _, name := range names {
		for _, statement := range []string{"ALTER SUBSCRIPTION %[1]s DISABLE", "ALTER SUBSCRIPTION %[1]s SET (slot_name = NONE)", "DROP SUBSCRIPTION %[1]s"} {
			if _, err := db.Exec(fmt.Sprintf(statement, pq.QuoteIdentifier(name))); err != nil {
				t.Logf("cleanup: %s: %v", fmt.Sprintf(statement, name), err)
			}
		}
	}
}

// dropSlots drops the logical replication slots of the database of a server (the publisher):
// a slot which is left blocks the DROP DATABASE. Best effort, it waits for the walsenders to stop.
func dropSlots(t *testing.T, dsn string) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Logf("cleanup: could not connect to the publisher: %v", err)
		return
	}
	defer closeDB(t, db)
	for i := 0; i < 15; i++ {
		var remaining int
		err := db.QueryRow(`SELECT count(*) FROM (SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots
			WHERE database = current_database() AND active_pid IS NOT NULL) AS t`).Scan(&remaining)
		_, errDrop := db.Exec("SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE database = current_database() AND NOT active")
		if err == nil && errDrop == nil {
			if err := db.QueryRow("SELECT count(*) FROM pg_replication_slots WHERE database = current_database()").Scan(&remaining); err == nil && remaining == 0 {
				return
			}
		}
		time.Sleep(time.Second)
	}
	t.Logf("cleanup: slots left on the publisher")
}

// cleanLeftovers removes what a failed test leaves behind: the subscriptions, and the slots
// of the publisher which block the DROP DATABASE of the teardown.
func (s *subTest) cleanLeftovers() {
	dropSubscriptions(s.t, s.dbSub)
	dropSlots(s.t, s.dsn(s.dbPub))
}

func (s *subTest) preCheck(features ...featureName) func() {
	return func() {
		testAccPreCheck(s.t)
		testCheckCanCreateSubscription(s.t, s.conninfo)
		for _, feature := range features {
			testCheckCompatibleVersion(s.t, feature)
		}
	}
}

// config returns the configuration of a subscription which uses a slot created by
// the test: the publisher is the server itself, where the subscription could not create it.
// publications are the names of the resources postgresql_publication (pub1, pub2).
func (s *subTest) config(name string, publications []string, extra string) string {
	return s.configFull(name, "test_slot", "", publications, extra)
}

// configFull is config for a slot (the resource test_slot or test_slot2) and a suffix of the connection string.
func (s *subTest) configFull(name, slotResource, conninfoSuffix string, publications []string, extra string) string {
	var pubs []string
	for _, p := range publications {
		pubs = append(pubs, fmt.Sprintf("postgresql_publication.%s.name", p))
	}
	return fmt.Sprintf(`
	resource "postgresql_publication" "pub1" {
		name     = "test_publication_1"
		database = "%[1]s"
		tables   = ["test_schema.test_table_1"]
	}
	resource "postgresql_publication" "pub2" {
		name     = "test_publication_2"
		database = "%[1]s"
		tables   = ["test_schema.test_table_2"]
	}
	resource "postgresql_replication_slot" "test_slot" {
		name     = "test_slot"
		database = "%[1]s"
		plugin   = "pgoutput"
		lifecycle {
			// a subscription with two_phase = true turns the slot to two-phase decoding,
			// and with failover = true to a slot which is synchronized to the standbys
			ignore_changes = [two_phase, failover]
		}
	}
	resource "postgresql_replication_slot" "test_slot2" {
		name     = "test_slot2"
		database = "%[1]s"
		plugin   = "pgoutput"
		lifecycle {
			// a subscription with two_phase = true turns the slot to two-phase decoding,
			// and with failover = true to a slot which is synchronized to the standbys
			ignore_changes = [two_phase, failover]
		}
	}
	resource "postgresql_subscription" "test_sub" {
		name         = "%[2]s"
		database     = "%[3]s"
		conninfo     = "%[4]s%[5]s"
		publications = [%[6]s]
		create_slot  = false
		slot_name    = postgresql_replication_slot.%[7]s.name
		depends_on   = [postgresql_replication_slot.test_slot, postgresql_replication_slot.test_slot2]
		%[8]s
	}
	`, s.dbPub, name, s.dbSub, s.conninfo, conninfoSuffix, strings.Join(pubs, ", "), slotResource, extra)
}

func (s *subTest) dsn(dbName string) string {
	config := getTestConfig(s.t)
	return config.connStr(dbName)
}

func (s *subTest) query(dbName, query string, args ...any) (string, error) {
	config := getTestConfig(s.t)
	return queryDB(config.connStr(dbName), query, args...)
}

func queryDB(dsn, query string, args ...any) (string, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var result sql.NullString
	if err := db.QueryRow(query, args...).Scan(&result); err != nil {
		return "", err
	}
	return result.String, nil
}

// catalog checks the value of an expression on the row of pg_subscription of the subscription
func (s *subTest) catalog(subName, expr, expected string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := s.query(s.dbSub, fmt.Sprintf(`SELECT (%s)::text FROM pg_catalog.pg_subscription
			WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())`, expr), subName)
		if err != nil {
			return fmt.Errorf("could not read %s of subscription %s: %w", expr, subName, err)
		}
		if got != expected {
			return fmt.Errorf("subscription %s: %s is %q, expected %q", subName, expr, got, expected)
		}
		return nil
	}
}

func (s *subTest) publications(subName, expected string) resource.TestCheckFunc {
	return s.catalog(subName, "array_to_string(ARRAY(SELECT unnest(subpublications) ORDER BY 1), ',')", expected)
}

// slotNames checks the names of the replication slots of the publisher
func (s *subTest) slots(expected string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := s.query(s.dbPub, "SELECT COALESCE(string_agg(slot_name, ',' ORDER BY slot_name), '') FROM pg_replication_slots WHERE database = current_database()")
		if err != nil {
			return err
		}
		if got != expected {
			return fmt.Errorf("replication slots of the publisher are %q, expected %q", got, expected)
		}
		return nil
	}
}

// subscriptionOID keeps the oid of the subscription to prove that it is not replaced.
func (s *subTest) rememberOID(name string, oid *string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := s.query(s.dbSub, "SELECT oid::text FROM pg_catalog.pg_subscription WHERE subname = $1", name)
		if err != nil {
			return err
		}
		*oid = got
		return nil
	}
}

func (s *subTest) sameOID(name string, oid *string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := s.query(s.dbSub, "SELECT oid::text FROM pg_catalog.pg_subscription WHERE subname = $1", name)
		if err != nil {
			return err
		}
		if got != *oid {
			return fmt.Errorf("subscription %s has been replaced (oid %s, was %s)", name, got, *oid)
		}
		return nil
	}
}

func (s *subTest) differentOID(name string, oid *string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		got, err := s.query(s.dbSub, "SELECT oid::text FROM pg_catalog.pg_subscription WHERE subname = $1", name)
		if err != nil {
			return err
		}
		if got == *oid {
			return fmt.Errorf("subscription %s has not been replaced (oid %s)", name, got)
		}
		return nil
	}
}

// outside returns a PreConfig which alters the subscription outside of Terraform.
func (s *subTest) outside(statements ...string) func() {
	return func() {
		config := getTestConfig(s.t)
		for _, statement := range statements {
			dbExecute(s.t, config.connStr(s.dbSub), statement)
		}
	}
}

// waitFor waits until the query on the database returns the expected value.
func (s *subTest) waitFor(dbName, query, expected string) error {
	var got string
	for i := 0; i < 60; i++ {
		var err error
		got, err = s.query(dbName, query)
		if err == nil && got == expected {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s returned %q after 60 seconds, expected %q", query, got, expected)
}

func (s *subTest) replicated(table string, count int) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		return s.waitFor(s.dbSub, "SELECT count(*)::text FROM "+table, strconv.Itoa(count))
	}
}

// synced waits until the tables of the subscription are ready (the initial copy is done)
func (s *subTest) synced() resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		return s.waitFor(s.dbSub, "SELECT count(*)::text FROM pg_catalog.pg_subscription_rel WHERE srsubstate <> 'r'", "0")
	}
}

func (s *subTest) insert(table, val string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		config := getTestConfig(s.t)
		dbExecute(s.t, config.connStr(s.dbPub), fmt.Sprintf("INSERT INTO %s (val) VALUES ('%s')", table, val))
		return nil
	}
}

// testAccCheckSubscriptionConninfo checks the connection string of the subscription subName of the
// database dbName (the dsn of the subscriber is dsn). The state keeps the configured value. Where
// the connected user can read pg_subscription.subconninfo (the superusers) the catalog holds it too;
// where PostgreSQL hides it from the user (it does from every non superuser), the column is refused:
// the provider can only keep the configured value in the state.
func testAccCheckSubscriptionConninfo(resourceName, dsn, subName, expected string) resource.TestCheckFunc {
	return func(st *terraform.State) error {
		rs, ok := st.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("Resource not found: %s", resourceName)
		}
		if got := rs.Primary.Attributes["conninfo"]; got != expected {
			return fmt.Errorf("conninfo in the state is %q, expected %q", got, expected)
		}
		got, err := queryDB(dsn, "SELECT subconninfo FROM pg_catalog.pg_subscription WHERE subname = $1", subName)
		if subconninfoReadable(dsn) {
			if err != nil {
				return fmt.Errorf("could not read subconninfo of subscription %s: %w", subName, err)
			}
			if got != expected {
				return fmt.Errorf("subconninfo of subscription %s is %q, expected %q", subName, got, expected)
			}
			return nil
		}
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			return fmt.Errorf("subconninfo should be hidden from this user: got %q, error %v", got, err)
		}
		return nil
	}
}

func subconninfoReadable(dsn string) bool {
	got, err := queryDB(dsn, "SELECT has_column_privilege('pg_catalog.pg_subscription', 'subconninfo', 'SELECT')::text")
	return err == nil && got == "true"
}

func (s *subTest) conninfoReadable() bool {
	return subconninfoReadable(s.dsn(s.dbSub))
}

// checkConninfo checks the connection string of the subscription (see testAccCheckSubscriptionConninfo).
func (s *subTest) checkConninfo(subName, expected string) resource.TestCheckFunc {
	return testAccCheckSubscriptionConninfo(subRes, s.dsn(s.dbSub), subName, expected)
}

// importStep checks the import of the subscription. The options of the creation are
// not in the catalog, and the connection string is only readable by the superusers.
func (s *subTest) importStep() resource.TestStep {
	ignore := []string{"create_slot", "connect", "copy_data"}
	step := resource.TestStep{
		ResourceName:      "postgresql_subscription.test_sub",
		ImportState:       true,
		ImportStateVerify: true,
	}
	if !s.conninfoReadable() {
		// the import cannot read it: it is left empty (the verification of the other attributes goes on)
		ignore = append(ignore, "conninfo")
		step.ImportStateCheck = func(states []*terraform.InstanceState) error {
			if len(states) != 1 {
				return fmt.Errorf("expected one imported resource, got %d", len(states))
			}
			if got := states[0].Attributes["conninfo"]; got != "" {
				return fmt.Errorf("conninfo of the imported subscription is %q, expected it to be empty (hidden by PostgreSQL)", got)
			}
			return nil
		}
	}
	step.ImportStateVerifyIgnore = ignore
	return step
}

const subRes = "postgresql_subscription.test_sub"

// The synchronous_commit and the enabled options exist in every version.
func TestAccPostgresqlSubscription_EnabledSynchronousCommit(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_options"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				// created disabled, with its own commit mode
				Config: s.config(name, []string{"pub1"}, `enabled = false
				synchronous_commit = "remote_write"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExistsInCatalog(subRes),
					resource.TestCheckResourceAttr(subRes, "enabled", "false"),
					resource.TestCheckResourceAttr(subRes, "synchronous_commit", "remote_write"),
					resource.TestCheckResourceAttr(subRes, "slot_name", "test_slot"),
					s.catalog(name, "subenabled", "false"),
					s.catalog(name, "subsynccommit", "remote_write"),
					s.catalog(name, "subslotname", "test_slot"),
					s.rememberOID(name, &oid),
				),
			},
			{
				// enabled and altered in place
				Config: s.config(name, []string{"pub1"}, `enabled = true
				synchronous_commit = "local"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "enabled", "true"),
					s.catalog(name, "subenabled", "true"),
					s.catalog(name, "subsynccommit", "local"),
					s.sameOID(name, &oid),
				),
			},
			s.importStep(),
			{
				// made outside of Terraform
				PreConfig: s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s DISABLE", name), fmt.Sprintf("ALTER SUBSCRIPTION %s SET (synchronous_commit = 'on')", name)),
				Config: s.config(name, []string{"pub1"}, `enabled = true
				synchronous_commit = "local"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// and corrected
				Config: s.config(name, []string{"pub1"}, `enabled = true
				synchronous_commit = "local"`),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "true"),
					s.catalog(name, "subsynccommit", "local"),
					s.sameOID(name, &oid),
				),
			},
			{
				// the options which are no more in the configuration are left as they are
				Config: s.config(name, []string{"pub1"}, ``),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "true"),
					s.catalog(name, "subsynccommit", "local"),
				),
			},
		},
	})
}

func testAccCheckPostgresqlSubscriptionExistsInCatalog(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Resource not found: %s", n)
		}
		client := testAccProvider.Meta().(*Client)
		txn, err := startTransaction(client, rs.Primary.Attributes["database"])
		if err != nil {
			return err
		}
		defer deferredRollback(txn)

		exists, err := checkSubscriptionExists(txn, rs.Primary.Attributes["name"])
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("Subscription not found")
		}
		return nil
	}
}

// binary and streaming (on and off) exist since PostgreSQL 14.
func TestAccPostgresqlSubscription_BinaryStreaming(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_binary"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionBinary),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `binary = true
				streaming = "on"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "binary", "true"),
					resource.TestCheckResourceAttr(subRes, "streaming", "on"),
					s.catalog(name, "subbinary", "true"),
					s.catalog(name, "substream::text IN ('t', 'true')", "true"),
					s.rememberOID(name, &oid),
					// the data is replicated with the options
					s.insert("test_schema.test_table_1", "binary"),
					s.replicated("test_schema.test_table_1", 1),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `binary = false
				streaming = "off"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "binary", "false"),
					resource.TestCheckResourceAttr(subRes, "streaming", "off"),
					s.catalog(name, "subbinary", "false"),
					s.catalog(name, "substream::text IN ('f', 'false')", "true"),
					s.sameOID(name, &oid),
					s.insert("test_schema.test_table_1", "text"),
					s.replicated("test_schema.test_table_1", 2),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s SET (binary = true)", name)),
				Config:             s.config(name, []string{"pub1"}, "binary = false\nstreaming = \"off\""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "binary = true\nstreaming = \"off\""),
				Check:  s.catalog(name, "subbinary", "true"),
			},
			{
				// the option which is no more in the configuration is left as it is: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check:  s.catalog(name, "subbinary", "true"),
			},
		},
	})
}

// two_phase and disable_on_error exist since PostgreSQL 15. two_phase can be altered
// since PostgreSQL 18: before, the subscription is replaced.
func TestAccPostgresqlSubscription_TwoPhaseDisableOnError(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_two_phase"
	var oid string
	alterable := testServerVersionNum(t) >= 180000
	check := func(f func(string, *string) resource.TestCheckFunc) resource.TestCheckFunc {
		if alterable {
			return s.sameOID(name, &oid)
		}
		return f(name, &oid)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionTwoPhase),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `two_phase = false
				disable_on_error = true`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "two_phase", "false"),
					resource.TestCheckResourceAttr(subRes, "disable_on_error", "true"),
					s.catalog(name, "subtwophasestate", "d"),
					s.catalog(name, "subdisableonerr", "true"),
					s.rememberOID(name, &oid),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `two_phase = false
				disable_on_error = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "disable_on_error", "false"),
					s.catalog(name, "subdisableonerr", "false"),
					s.sameOID(name, &oid),
				),
			},
			{
				// in place since PostgreSQL 18, a replacement before
				Config: s.config(name, []string{"pub1"}, `two_phase = true
				disable_on_error = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "two_phase", "true"),
					s.catalog(name, "subtwophasestate <> 'd'", "true"),
					check(s.differentOID),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s SET (disable_on_error = true)", name)),
				Config:             s.config(name, []string{"pub1"}, "two_phase = true\ndisable_on_error = false"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "two_phase = true\ndisable_on_error = true"),
				Check:  s.catalog(name, "subdisableonerr", "true"),
			},
			{
				// the options which are no more in the configuration are left as they are: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subtwophasestate <> 'd'", "true"),
					s.catalog(name, "subdisableonerr", "true"),
				),
			},
		},
	})
}

// password_required, run_as_owner and origin exist since PostgreSQL 16, streaming = parallel too.
// Only a superuser can set password_required to false.
func TestAccPostgresqlSubscription_RunAsOwnerOrigin(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_origin"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionRunAsOwner),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `run_as_owner = true
				origin = "none"
				password_required = true
				streaming = "parallel"`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "run_as_owner", "true"),
					resource.TestCheckResourceAttr(subRes, "origin", "none"),
					resource.TestCheckResourceAttr(subRes, "password_required", "true"),
					resource.TestCheckResourceAttr(subRes, "streaming", "parallel"),
					s.catalog(name, "subrunasowner", "true"),
					s.catalog(name, "suborigin", "none"),
					s.catalog(name, "subpasswordrequired", "true"),
					s.catalog(name, "substream", "p"),
					s.rememberOID(name, &oid),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `run_as_owner = false
				origin = "any"
				streaming = "off"`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "run_as_owner", "false"),
					resource.TestCheckResourceAttr(subRes, "origin", "any"),
					s.catalog(name, "subrunasowner", "false"),
					s.catalog(name, "suborigin", "any"),
					s.catalog(name, "substream", "f"),
					s.sameOID(name, &oid),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s SET (origin = none)", name)),
				Config:             s.config(name, []string{"pub1"}, "run_as_owner = false\norigin = \"any\"\nstreaming = \"off\""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "run_as_owner = true\norigin = \"any\"\nstreaming = \"off\""),
				Check:  s.catalog(name, "subrunasowner", "true"),
			},
			{
				// the option which is no more in the configuration is left as it is: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check:  s.catalog(name, "subrunasowner", "true"),
			},
		},
	})
}

func TestAccPostgresqlSubscription_PasswordRequired(t *testing.T) {
	skipIfNotSuperuser(t)
	s := newSubTest(t)
	const name = "tf_sub_password"

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			s.preCheck(featureSubscriptionRunAsOwner)()
			testSuperuserPreCheck(t)
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `password_required = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "password_required", "false"),
					s.catalog(name, "subpasswordrequired", "false"),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `password_required = true`),
				Check:  s.catalog(name, "subpasswordrequired", "true"),
			},
			s.importStep(),
		},
	})
}

// failover exists since PostgreSQL 17. The server only alters it when the subscription is
// disabled: the provider disables the subscription and enables it again.
func TestAccPostgresqlSubscription_Failover(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_failover"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionFailover),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `failover = true`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "failover", "true"),
					s.catalog(name, "subfailover", "true"),
					s.rememberOID(name, &oid),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `failover = false`),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "enabled", "true"),
					s.catalog(name, "subfailover", "false"),
					s.catalog(name, "subenabled", "true"),
					s.sameOID(name, &oid),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s DISABLE", name), fmt.Sprintf("ALTER SUBSCRIPTION %s SET (failover = true)", name)),
				Config:             s.config(name, []string{"pub1"}, "enabled = true\nfailover = false"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "enabled = true\nfailover = false"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subfailover", "false"),
					s.catalog(name, "subenabled", "true"),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, "failover = true"),
				Check:  s.catalog(name, "subfailover", "true"),
			},
			{
				// the option which is no more in the configuration is left as it is: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check:  s.catalog(name, "subfailover", "true"),
			},
		},
	})
}

// retain_dead_tuples, max_retention_duration and wal_receiver_timeout exist since PostgreSQL 19.
func TestAccPostgresqlSubscription_RetainDeadTuples(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_retain"

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionRetainDeadTuples),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `retain_dead_tuples = true`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "retain_dead_tuples", "true"),
					s.catalog(name, "subretaindeadtuples", "true"),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `retain_dead_tuples = false`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "retain_dead_tuples", "false"),
					s.catalog(name, "subretaindeadtuples", "false"),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s DISABLE", name), fmt.Sprintf("ALTER SUBSCRIPTION %s SET (retain_dead_tuples = true)", name)),
				Config:             s.config(name, []string{"pub1"}, "enabled = true\nretain_dead_tuples = false"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "enabled = true\nretain_dead_tuples = true"),
				Check:  s.catalog(name, "subretaindeadtuples", "true"),
			},
			{
				// the option which is no more in the configuration is left as it is: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check:  s.catalog(name, "subretaindeadtuples", "true"),
			},
		},
	})
}

func TestAccPostgresqlSubscription_WalReceiverTimeout(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_timeout"

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(featureSubscriptionRetainDeadTuples),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, `wal_receiver_timeout = "30s"
				max_retention_duration = 0`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "wal_receiver_timeout", "30s"),
					resource.TestCheckResourceAttr(subRes, "max_retention_duration", "0"),
					s.catalog(name, "subwalrcvtimeout", "30s"),
					s.catalog(name, "submaxretention", "0"),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, `wal_receiver_timeout = "1min"
				retain_dead_tuples = true
				max_retention_duration = 60000`),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subwalrcvtimeout", "1min"),
					s.catalog(name, "submaxretention", "60000"),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s SET (wal_receiver_timeout = '5s')", name)),
				Config:             s.config(name, []string{"pub1"}, "wal_receiver_timeout = \"1min\"\nretain_dead_tuples = true\nmax_retention_duration = 60000"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, "wal_receiver_timeout = \"1min\"\nretain_dead_tuples = true\nmax_retention_duration = 60000"),
				Check:  s.catalog(name, "subwalrcvtimeout", "1min"),
			},
			{
				// the options which are no more in the configuration are left as they are: no diff
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "submaxretention", "60000"),
					s.catalog(name, "subwalrcvtimeout", "1min"),
				),
			},
		},
	})
}

// The options which the server does not know are refused with a clear error.
func TestAccPostgresqlSubscription_UnsupportedOptions(t *testing.T) {
	s := newSubTest(t)
	version := testServerVersionNum(t)

	unsupported := []struct {
		line, since string
		minimum     int
	}{
		{`binary = true`, "14", 140000},
		{`streaming = "on"`, "14", 140000},
		{`streaming = "parallel"`, "16", 160000},
		{`two_phase = true`, "15", 150000},
		{`disable_on_error = true`, "15", 150000},
		{`password_required = true`, "16", 160000},
		{`run_as_owner = true`, "16", 160000},
		{`origin = "none"`, "16", 160000},
		{`failover = true`, "17", 170000},
		{`retain_dead_tuples = true`, "19", 190000},
		{`max_retention_duration = 10`, "19", 190000},
		{`wal_receiver_timeout = "10s"`, "19", 190000},
	}
	var steps []resource.TestStep
	for _, u := range unsupported {
		// before 14 the server does not know streaming at all, which the previous step covers
		if version >= u.minimum || (u.since == "16" && strings.HasPrefix(u.line, "streaming") && version < 140000) {
			continue
		}
		steps = append(steps, resource.TestStep{
			Config:      s.config("tf_sub_unsupported", []string{"pub1"}, u.line),
			ExpectError: regexp.MustCompile(fmt.Sprintf("requires PostgreSQL %s or later", u.since)),
		})
	}
	if len(steps) == 0 {
		t.Skip("Skip test: the server knows every option")
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps:        steps,
	})
}

// The connection, the publications, the name and the slot are altered in place.
func TestAccPostgresqlSubscription_AlterInPlace(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_alter"
	const renamed = "tf_sub_alter_renamed"
	var oid string
	const application = " application_name=tf_sub_alter"

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.publications(name, "test_publication_1"),
					s.rememberOID(name, &oid),
				),
			},
			{
				// more publications: the tables of the new one are copied
				PreConfig: func() {
					dbExecute(s.t, s.dsn(s.dbPub), "INSERT INTO test_schema.test_table_2 (val) VALUES ('before')")
				},
				Config: s.config(name, []string{"pub1", "pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "publications.#", "2"),
					s.publications(name, "test_publication_1,test_publication_2"),
					s.sameOID(name, &oid),
					s.replicated("test_schema.test_table_2", 1),
					s.insert("test_schema.test_table_2", "after"),
					s.replicated("test_schema.test_table_2", 2),
				),
			},
			{
				// another connection string
				Config: s.configFull(name, "test_slot", application, []string{"pub1", "pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					s.checkConninfo(name, s.conninfo+application),
					s.sameOID(name, &oid),
				),
			},
			{
				Config:   s.configFull(name, "test_slot", application, []string{"pub1", "pub2"}, ""),
				PlanOnly: true,
			},
			{
				// fewer publications
				Config: s.configFull(name, "test_slot", application, []string{"pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					s.publications(name, "test_publication_2"),
					s.sameOID(name, &oid),
				),
			},
			{
				// another name: the subscription is renamed, the slot is the same
				Config: s.configFull(renamed, "test_slot", application, []string{"pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "name", renamed),
					resource.TestCheckResourceAttr(subRes, "id", s.dbSub+"."+renamed),
					s.catalog(renamed, "subslotname", "test_slot"),
					s.sameOID(renamed, &oid),
				),
			},
			{
				// another slot: the subscription does not need to be disabled
				Config: s.configFull(renamed, "test_slot2", application, []string{"pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "slot_name", "test_slot2"),
					s.catalog(renamed, "subslotname", "test_slot2"),
					s.sameOID(renamed, &oid),
				),
			},
			s.importStep(),
			{
				// the publications and the slot changed outside of Terraform
				PreConfig: s.outside(
					fmt.Sprintf("ALTER SUBSCRIPTION %s SET PUBLICATION test_publication_1 WITH (refresh = false)", renamed),
					fmt.Sprintf("ALTER SUBSCRIPTION %s SET (slot_name = test_slot)", renamed)),
				Config:             s.configFull(renamed, "test_slot2", application, []string{"pub2"}, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.configFull(renamed, "test_slot2", application, []string{"pub2"}, ""),
				Check: resource.ComposeTestCheckFunc(
					s.publications(renamed, "test_publication_2"),
					s.catalog(renamed, "subslotname", "test_slot2"),
					s.sameOID(renamed, &oid),
				),
			},
		},
	})
}

// The subscription removed outside of Terraform is created again.
func TestAccPostgresqlSubscription_RemovedOutside(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_removed"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.rememberOID(name, &oid),
				),
			},
			{
				PreConfig: s.outside(
					fmt.Sprintf("ALTER SUBSCRIPTION %s DISABLE", name),
					fmt.Sprintf("ALTER SUBSCRIPTION %s SET (slot_name = NONE)", name),
					fmt.Sprintf("DROP SUBSCRIPTION %s", name)),
				Config:             s.config(name, []string{"pub1"}, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.differentOID(name, &oid),
				),
			},
		},
	})
}

// The options of the creation are not in the catalog: changing them afterwards neither
// replaces the subscription nor leaves a diff. connect = false creates a subscription which does
// not connect to the publisher (so it is disabled and has no slot on the publisher).
func TestAccPostgresqlSubscription_CreationOptions(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_creation"
	var oid string
	config := func(extra string) string {
		return strings.Replace(s.config(name, []string{"pub1"}, extra), "create_slot  = false", "", 1)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("connect = false\ncreate_slot = false\ncopy_data = false"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "connect", "false"),
					resource.TestCheckResourceAttr(subRes, "create_slot", "false"),
					resource.TestCheckResourceAttr(subRes, "copy_data", "false"),
					resource.TestCheckResourceAttr(subRes, "enabled", "false"),
					s.catalog(name, "subenabled", "false"),
					s.catalog(name, "subslotname", "test_slot"),
					s.rememberOID(name, &oid),
				),
			},
			{
				// nothing to change
				Config:   config("connect = false\ncreate_slot = false\ncopy_data = false"),
				PlanOnly: true,
			},
			{
				// changing the options of the creation does not do anything
				Config: config("connect = true\ncreate_slot = true\ncopy_data = true"),
				Check: resource.ComposeTestCheckFunc(
					s.sameOID(name, &oid),
					s.catalog(name, "subenabled", "false"),
				),
			},
			{
				Config:   config("connect = true\ncreate_slot = true\ncopy_data = true"),
				PlanOnly: true,
			},
			{
				// removing them neither
				Config:   config(""),
				PlanOnly: true,
			},
			{
				// the subscription is enabled afterwards
				Config: config("enabled = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.catalog(name, "subenabled", "true"),
					s.sameOID(name, &oid),
				),
			},
		},
	})
}

// The combinations of the creation options which the server refuses
func TestAccPostgresqlSubscription_CreationOptionsRefused(t *testing.T) {
	s := newSubTest(t)

	resource.Test(t, resource.TestCase{
		PreCheck:  s.preCheck(),
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      strings.Replace(s.config("tf_sub_refused", []string{"pub1"}, "connect = false\ncreate_slot = true"), "create_slot  = false", "", 1),
				ExpectError: regexp.MustCompile("connect = false and create_slot = true are mutually exclusive"),
			},
			{
				Config:      s.config("tf_sub_refused", []string{"pub1"}, "connect = false\nenabled = true"),
				ExpectError: regexp.MustCompile("connect = false and enabled = true are mutually exclusive"),
			},
			{
				Config:      s.config("tf_sub_refused", []string{"pub1"}, "connect = false\ncopy_data = true"),
				ExpectError: regexp.MustCompile("connect = false and copy_data = true are mutually exclusive"),
			},
		},
	})
}

// A subscription created with create_slot = false keeps the slot when it is destroyed;
// connect = false subscriptions can be destroyed although there is no slot on the publisher.
func TestAccPostgresqlSubscription_DestroyKeepsSlot(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_keep"
	config := fmt.Sprintf(`
	resource "postgresql_publication" "pub1" {
		name     = "test_publication_1"
		database = "%[1]s"
		tables   = ["test_schema.test_table_1"]
	}
	resource "postgresql_subscription" "test_sub" {
		name         = "%[2]s"
		database     = "%[3]s"
		conninfo     = "%[4]s"
		publications = [postgresql_publication.pub1.name]
		create_slot  = false
		slot_name    = "tf_kept_slot"
	}
	`, s.dbPub, name, s.dbSub, s.conninfo)

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			s.preCheck()()
			dbExecute(t, s.dsn(s.dbPub), "SELECT pg_create_logical_replication_slot('tf_kept_slot', 'pgoutput')")
			// The slot is kept by the destroy: it is dropped when the test ends, also when it fails
			// (the subscription which uses it goes first).
			t.Cleanup(s.cleanLeftovers)
		},
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			// the slot is still there and nobody uses it
			return s.slotState("tf_kept_slot", "false")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.slotState("tf_kept_slot", "true"),
				),
			},
		},
	})
}

// slotState checks that the slot of the publisher exists and whether it is active.
func (s *subTest) slotState(slot, active string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		var err error
		var got string
		for i := 0; i < 30; i++ {
			got, err = s.query(s.dbPub, "SELECT active::text FROM pg_replication_slots WHERE slot_name = $1", slot)
			if err == nil && got == active {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("slot %s: active = %q (%v), expected %q", slot, got, err, active)
	}
}

func TestAccPostgresqlSubscription_ConnectFalseDestroy(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_noconnect"
	config := strings.Replace(s.config(name, []string{"pub1"}, "connect = false\ncreate_slot = false"), "create_slot  = false", "", 1)

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: strings.Replace(config, `slot_name    = postgresql_replication_slot.test_slot.name`, ``, 1),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "false"),
					// the slot has not been created by the subscription
					s.catalog(name, "subslotname", name),
					s.slots("test_slot,test_slot2"),
				),
			},
		},
	})
}

// The slot of the subscription is set to NONE and back.
func TestAccPostgresqlSubscription_SlotNone(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_none"
	var oid string
	noSlot := strings.Replace(s.config(name, []string{"pub1"}, "enabled = false"), `slot_name    = postgresql_replication_slot.test_slot.name`, `slot_name    = "none"`, 1)

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.rememberOID(name, &oid),
				),
			},
			{
				// the subscription is disabled to lose its slot
				Config: noSlot,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "slot_name", "NONE"),
					resource.TestCheckResourceAttr(subRes, "enabled", "false"),
					s.catalog(name, "subslotname IS NULL", "true"),
					s.catalog(name, "subenabled", "false"),
					s.sameOID(name, &oid),
				),
			},
			{
				Config:   noSlot,
				PlanOnly: true,
			},
			s.importStep(),
			{
				Config: s.config(name, []string{"pub1"}, "enabled = true"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.catalog(name, "subslotname", "test_slot"),
					s.sameOID(name, &oid),
				),
			},
		},
	})
}

// The data of the publisher is replicated with the options of the subscription, also the data
// of the tables which a new publication adds.
func TestAccPostgresqlSubscription_Replication(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_replication"
	options := `synchronous_commit = "remote_apply"`
	if testServerVersionNum(t) >= 140000 {
		options += "\nbinary = true\nstreaming = \"on\""
	}
	if testServerVersionNum(t) >= 160000 {
		options += "\norigin = \"any\""
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, options),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					s.insert("test_schema.test_table_1", "one"),
					s.replicated("test_schema.test_table_1", 1),
				),
			},
			{
				PreConfig: func() {
					dbExecute(t, s.dsn(s.dbPub), "INSERT INTO test_schema.test_table_2 (val) VALUES ('existing')")
				},
				Config: s.config(name, []string{"pub1", "pub2"}, options),
				Check: resource.ComposeTestCheckFunc(
					// copied by the refresh
					s.replicated("test_schema.test_table_2", 1),
					s.insert("test_schema.test_table_2", "two"),
					s.replicated("test_schema.test_table_2", 2),
					s.insert("test_schema.test_table_1", "three"),
					s.replicated("test_schema.test_table_1", 2),
				),
			},
			{
				// disabled: the changes of the publisher wait
				Config: s.config(name, []string{"pub1", "pub2"}, options+"\nenabled = false"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "false"),
					s.insert("test_schema.test_table_1", "four"),
				),
			},
			{
				// the publications of a disabled subscription change without refresh (the server refuses it)
				Config: s.config(name, []string{"pub1"}, options+"\nenabled = false"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "false"),
					s.publications(name, "test_publication_1"),
				),
			},
			{
				// enabled again, with the two publications
				Config: s.config(name, []string{"pub1", "pub2"}, options+"\nenabled = true"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "true"),
					s.publications(name, "test_publication_1,test_publication_2"),
					s.replicated("test_schema.test_table_1", 3),
				),
			},
		},
	})
}

// ownerRole returns the name of a role which can own a subscription and the PreCheck which skips
// the test when the connected user cannot create it, and creates it. The cleanup drops the role:
// DROP OWNED BY revokes its privilege on the database of the subscriber (a shared object, revoked
// from any database), without which the role could not be dropped, whenever the database is dropped.
func (s *subTest) ownerRole() (string, func()) {
	t := s.t
	roleName := "tf_sub_owner_" + strconv.Itoa(int(time.Now().UnixNano()))
	return roleName, func() {
		testAccPreCheck(t)
		testCheckCanExecute(t, "create a role which can own a subscription",
			"CREATE ROLE tf_probe_owner",
			`DO $$ BEGIN
				IF current_setting('server_version_num')::int >= 160000 THEN
					EXECUTE 'GRANT pg_create_subscription TO tf_probe_owner';
				ELSE
					EXECUTE 'ALTER ROLE tf_probe_owner SUPERUSER';
				END IF;
			END $$`,
		)
		s.preCheck()()
		config := getTestConfig(t)
		t.Cleanup(func() {
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf(
				// (a user who is not a superuser needs the privileges of the role to drop what it owns)
				`DO $$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN GRANT %[1]s TO CURRENT_USER; DROP OWNED BY %[1]s; DROP ROLE %[1]s; END IF; END $$`, roleName))
		})
		dbExecute(t, config.connStr("postgres"), fmt.Sprintf("CREATE ROLE %s", roleName))
		if testServerVersionNum(t) >= 160000 {
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("GRANT pg_create_subscription TO %s", roleName))
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("GRANT CREATE ON DATABASE %s TO %s", s.dbSub, roleName))
		} else {
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("ALTER ROLE %s SUPERUSER", roleName))
		}
	}
}

// The owner of the subscription is altered in place.
func TestAccPostgresqlSubscription_Owner(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_owner"
	roleName, preCheck := s.ownerRole()
	var oid string
	// a non superuser owner is disabled, its apply worker could not connect without a password
	disabled := "enabled = false"

	resource.Test(t, resource.TestCase{
		PreCheck:     preCheck,
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, disabled),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet(subRes, "owner"),
					s.rememberOID(name, &oid),
				),
			},
			{
				Config: s.config(name, []string{"pub1"}, disabled+"\nowner = \""+roleName+"\""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "owner", roleName),
					s.catalog(name, "pg_get_userbyid(subowner)", roleName),
					s.sameOID(name, &oid),
				),
			},
			s.importStep(),
			{
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s OWNER TO CURRENT_USER", name)),
				Config:             s.config(name, []string{"pub1"}, disabled+"\nowner = \""+roleName+"\""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// the subscription goes back to its first owner: destroying a subscription needs a
				// connection to the publisher, which a non superuser can only make with a password
				Config: s.config(name, []string{"pub1"}, disabled+"\nowner = \""+getTestConfig(t).Username+"\""),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "pg_get_userbyid(subowner)", getTestConfig(t).Username),
					s.sameOID(name, &oid),
				),
			},
		},
	})
}

// The owner is set when the subscription is created (CREATE, then OWNER TO).
func TestAccPostgresqlSubscription_OwnerAtCreate(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_owner_create"
	roleName, preCheck := s.ownerRole()
	config := s.config(name, []string{"pub1"}, "enabled = false\nowner = \""+roleName+"\"")

	resource.Test(t, resource.TestCase{
		PreCheck:     preCheck,
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "owner", roleName),
					s.catalog(name, "pg_get_userbyid(subowner)", roleName),
					s.catalog(name, "subenabled", "false"),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
			s.importStep(),
			{
				// back to the first owner, which can drop the subscription without a password
				Config: s.config(name, []string{"pub1"}, "enabled = false\nowner = \""+getTestConfig(t).Username+"\""),
				Check:  s.catalog(name, "pg_get_userbyid(subowner)", getTestConfig(t).Username),
			},
		},
	})
}

// slot_name = NONE at the creation: no slot, so the subscription has to be disabled and not to create one.
func TestAccPostgresqlSubscription_SlotNoneAtCreate(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_none_create"
	config := strings.Replace(s.config(name, []string{"pub1"}, "enabled = false"), `slot_name    = postgresql_replication_slot.test_slot.name`, `slot_name    = "none"`, 1)

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "slot_name", "NONE"),
					resource.TestCheckResourceAttr(subRes, "enabled", "false"),
					s.catalog(name, "subslotname IS NULL", "true"),
					s.catalog(name, "subenabled", "false"),
					// the subscription did not create a slot, and does not use the ones of the test
					s.slots("test_slot,test_slot2"),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
			s.importStep(),
		},
	})
}

// Several publications at the creation, and the data of the tables of both is copied.
func TestAccPostgresqlSubscription_TwoPublicationsAtCreate(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_two_pubs"
	config := s.config(name, []string{"pub1", "pub2"}, "")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			s.preCheck()()
			dbExecute(t, s.dsn(s.dbPub), "INSERT INTO test_schema.test_table_1 (val) VALUES ('first')")
			dbExecute(t, s.dsn(s.dbPub), "INSERT INTO test_schema.test_table_2 (val) VALUES ('second')")
		},
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPostgresqlSubscriptionExists(subRes),
					resource.TestCheckResourceAttr(subRes, "publications.#", "2"),
					s.publications(name, "test_publication_1,test_publication_2"),
					s.replicated("test_schema.test_table_1", 1),
					s.replicated("test_schema.test_table_2", 1),
					s.insert("test_schema.test_table_1", "third"),
					s.insert("test_schema.test_table_2", "fourth"),
					s.replicated("test_schema.test_table_1", 2),
					s.replicated("test_schema.test_table_2", 2),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
			s.importStep(),
			{
				// the list changed outside of Terraform
				PreConfig:          s.outside(fmt.Sprintf("ALTER SUBSCRIPTION %s SET PUBLICATION test_publication_1 WITH (refresh = false)", name)),
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// copy_data at the creation: the rows which exist on the publisher are copied or not.
func TestAccPostgresqlSubscription_CopyDataAtCreate(t *testing.T) {
	for _, copyData := range []bool{true, false} {
		t.Run(fmt.Sprintf("copy_data=%t", copyData), func(t *testing.T) {
			s := newSubTest(t)
			const name = "tf_sub_copy"
			config := s.config(name, []string{"pub1"}, fmt.Sprintf("copy_data = %t", copyData))
			existing, total := 2, 3
			if !copyData {
				existing, total = 0, 1
			}

			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					s.preCheck()()
					dbExecute(t, s.dsn(s.dbPub), "INSERT INTO test_schema.test_table_1 (val) VALUES ('one'), ('two')")
				},
				Providers:    testAccProviders,
				CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
				Steps: []resource.TestStep{
					{
						Config: config,
						Check: resource.ComposeTestCheckFunc(
							testAccCheckPostgresqlSubscriptionExists(subRes),
							resource.TestCheckResourceAttr(subRes, "copy_data", strconv.FormatBool(copyData)),
							s.synced(),
							s.replicated("test_schema.test_table_1", existing),
							// the changes which follow are replicated in both cases
							s.insert("test_schema.test_table_1", "three"),
							s.replicated("test_schema.test_table_1", total),
						),
					},
					{
						Config:   config,
						PlanOnly: true,
					},
				},
			})
		})
	}
}

// A subscription which connects to the server it lives on cannot create its replication slot
// (the creation of the slot waits for the transaction of CREATE SUBSCRIPTION). To test
// create_slot = true, the publisher has to be another server: the service "publisher" of
// tests/docker-compose.yml, which the scripts tests/switch_*.sh declare with
//
//	TEST_REMOTE_PUBLISHER_DSN       how the test reaches it, e.g. postgres://postgres:postgres@localhost:25433/postgres?sslmode=disable
//	TEST_REMOTE_PUBLISHER_CONNINFO  how the subscriber reaches it, without dbname, e.g. host=publisher port=5432 user=postgres password=postgres
//
// The tests which use it are skipped when they are not set (e.g. against a managed service).
type remoteTest struct {
	*subTest
	dsn string // of the server of the remote publisher
	db  string // database of the remote publisher
}

func newRemoteTest(t *testing.T) *remoteTest {
	skipIfNotAcc(t)
	remoteDSN := os.Getenv("TEST_REMOTE_PUBLISHER_DSN")
	remoteConninfo := os.Getenv("TEST_REMOTE_PUBLISHER_CONNINFO")
	if remoteDSN == "" || remoteConninfo == "" {
		t.Skip("Skip test: TEST_REMOTE_PUBLISHER_DSN and TEST_REMOTE_PUBLISHER_CONNINFO are not set")
	}
	dbSuffixSub, teardownSub := setupTestDatabase(t, true, true)
	t.Cleanup(teardownSub)
	t.Cleanup(func() { coolDown() })
	createTestTables(t, dbSuffixSub, []string{"test_schema.test_table_1", "test_schema.test_table_2"}, "")
	dbNameSub, _ := getTestDBNames(dbSuffixSub)

	r := &remoteTest{dsn: remoteDSN, db: "tf_remote_pub_" + strconv.Itoa(int(time.Now().UnixNano()))}
	r.subTest = &subTest{t: t, dbSub: dbNameSub, conninfo: remoteConninfo + " dbname=" + r.db}
	r.exec("postgres", "CREATE DATABASE "+r.db)
	// What a failed test leaves: the subscription goes first, then its slot, then the database
	// (the slots of the publisher are dropped by the subscription, or here).
	t.Cleanup(func() {
		dropSubscriptions(t, dbNameSub)
		dropSlots(t, r.dsnFor(r.db))
		for i := 0; i < 10; i++ {
			if err := execDB(r.dsnFor("postgres"), "DROP DATABASE IF EXISTS "+r.db); err == nil {
				return
			}
			time.Sleep(time.Second)
		}
		t.Logf("cleanup: could not drop the database %s of the remote publisher", r.db)
	})
	r.exec(r.db, "CREATE SCHEMA test_schema")
	r.exec(r.db, "CREATE TABLE test_schema.test_table_1 (val text, test_column_one text, test_column_two text)")
	r.exec(r.db, "CREATE TABLE test_schema.test_table_2 (val text, test_column_one text, test_column_two text)")
	r.exec(r.db, "CREATE PUBLICATION tf_remote_publication FOR TABLE test_schema.test_table_1")
	return r
}

func execDB(dsn, query string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(query)
	return err
}

func (r *remoteTest) dsnFor(dbName string) string {
	u, err := url.Parse(r.dsn)
	if err != nil {
		r.t.Fatalf("could not parse TEST_REMOTE_PUBLISHER_DSN: %v", err)
	}
	u.Path = "/" + dbName
	return u.String()
}

func (r *remoteTest) exec(dbName, query string) {
	if err := execDB(r.dsnFor(dbName), query); err != nil {
		r.t.Fatalf("could not execute %s on the remote publisher: %v", query, err)
	}
}

// slots checks the slots of the remote publisher ("name:active" separated by commas)
func (r *remoteTest) slots(expected string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		var got string
		var err error
		for i := 0; i < 30; i++ {
			got, err = queryDB(r.dsnFor(r.db), "SELECT COALESCE(string_agg(slot_name || ':' || active::text, ',' ORDER BY slot_name), '') FROM pg_replication_slots WHERE database = current_database()")
			if err == nil && got == expected {
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("slots of the remote publisher are %q (%v), expected %q", got, err, expected)
	}
}

func (r *remoteTest) insert(table, val string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		r.exec(r.db, fmt.Sprintf("INSERT INTO %s (val) VALUES ('%s')", table, val))
		return nil
	}
}

func (r *remoteTest) preCheck() {
	testAccPreCheck(r.t)
	testCheckCanCreateSubscription(r.t, r.conninfo)
}

// config is a subscription which creates its slot (create_slot is true unless the extra lines say it).
func (r *remoteTest) config(name, extra string) string {
	return r.configNamed(name, "", extra)
}

func (r *remoteTest) configNamed(name, conninfoSuffix, extra string) string {
	return fmt.Sprintf(`
	resource "postgresql_subscription" "test_sub" {
		name         = "%s"
		database     = "%s"
		conninfo     = "%s%s"
		publications = ["tf_remote_publication"]
		%s
	}`, name, r.dbSub, r.conninfo, conninfoSuffix, extra)
}

// create_slot = true (the default): the subscription creates its slot on the publisher, the
// data replicates, the slot is dropped with the subscription.
func TestAccPostgresqlSubscription_CreateSlotOnRemotePublisher(t *testing.T) {
	r := newRemoteTest(t)
	s := r.subTest
	const name = "tf_sub_remote"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:  r.preCheck,
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			// the slot which the subscription created is dropped with it: nothing leaks on the publisher
			return r.slots("")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: r.config(name, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "create_slot", "true"),
					resource.TestCheckResourceAttr(subRes, "slot_name", name),
					resource.TestCheckResourceAttr(subRes, "enabled", "true"),
					s.catalog(name, "subslotname", name),
					s.catalog(name, "subenabled", "true"),
					// the slot has been created on the publisher, not on the subscriber, and is in use
					r.slots(name+":true"),
					s.checkConninfo(name, r.conninfo),
					s.rememberOID(name, &oid),
					r.insert("test_schema.test_table_1", "remote"),
					s.replicated("test_schema.test_table_1", 1),
				),
			},
			s.importStep(),
			{
				// an imported subscription is supposed to have created its slot (create_slot = true)
				ResourceName: subRes,
				ImportState:  true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected one imported resource, got %d", len(states))
					}
					if got := states[0].Attributes["create_slot"]; got != "true" {
						return fmt.Errorf("create_slot of the imported subscription is %q, expected true", got)
					}
					return nil
				},
			},
			{
				// create_slot = true is used only at the creation: no diff, no replacement
				Config: r.config(name, "create_slot = false"),
				Check:  s.sameOID(name, &oid),
			},
			{
				Config:   r.config(name, "create_slot = false"),
				PlanOnly: true,
			},
			{
				// renamed: the slot keeps its name
				Config: strings.Replace(r.config(name, "create_slot = false"), `name         = "tf_sub_remote"`, `name         = "tf_sub_remote_2"`, 1),
				Check: resource.ComposeTestCheckFunc(
					s.catalog("tf_sub_remote_2", "subslotname", name),
					s.sameOID("tf_sub_remote_2", &oid),
					r.slots(name+":true"),
					r.insert("test_schema.test_table_1", "after the rename"),
					s.replicated("test_schema.test_table_1", 2),
				),
			},
		},
	})
}

// slot_name with create_slot = true: the slot of the publisher has this name, not the one of the subscription.
func TestAccPostgresqlSubscription_CreateSlotCustomName(t *testing.T) {
	r := newRemoteTest(t)
	s := r.subTest
	const name = "tf_sub_remote_custom"
	const slot = "tf_custom_remote_slot"
	config := r.config(name, `slot_name = "`+slot+`"`)

	resource.Test(t, resource.TestCase{
		PreCheck:  r.preCheck,
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			return r.slots("")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "slot_name", slot),
					s.catalog(name, "subslotname", slot),
					r.slots(slot+":true"),
					r.insert("test_schema.test_table_1", "custom"),
					s.replicated("test_schema.test_table_1", 1),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
			s.importStep(),
		},
	})
}

// copy_data = true at the creation: the rows which exist on the publisher are copied.
func TestAccPostgresqlSubscription_CreateSlotCopyData(t *testing.T) {
	r := newRemoteTest(t)
	s := r.subTest
	const name = "tf_sub_remote_copy"
	config := r.config(name, "copy_data = true")

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			r.preCheck()
			r.exec(r.db, "INSERT INTO test_schema.test_table_1 (val) VALUES ('one'), ('two')")
		},
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			return r.slots("")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "copy_data", "true"),
					s.synced(),
					s.replicated("test_schema.test_table_1", 2),
					r.insert("test_schema.test_table_1", "three"),
					s.replicated("test_schema.test_table_1", 3),
					r.slots(name+":true"),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}

// enabled = false with create_slot = true: the slot is created on the publisher but nobody uses it,
// and nothing is replicated until the subscription is enabled.
func TestAccPostgresqlSubscription_CreateSlotDisabled(t *testing.T) {
	r := newRemoteTest(t)
	s := r.subTest
	const name = "tf_sub_remote_disabled"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			r.preCheck()
			r.exec(r.db, "INSERT INTO test_schema.test_table_1 (val) VALUES ('before')")
		},
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			return r.slots("")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: r.config(name, "enabled = false"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "enabled", "false"),
					s.catalog(name, "subenabled", "false"),
					s.catalog(name, "subslotname", name),
					r.slots(name+":false"),
					r.insert("test_schema.test_table_1", "while disabled"),
					func(_ *terraform.State) error {
						time.Sleep(3 * time.Second)
						return s.waitFor(s.dbSub, "SELECT count(*)::text FROM test_schema.test_table_1", "0")
					},
					s.rememberOID(name, &oid),
				),
			},
			{
				Config:   r.config(name, "enabled = false"),
				PlanOnly: true,
			},
			{
				// enabled: the slot is used, the existing rows are copied
				Config: r.config(name, "enabled = true"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "true"),
					r.slots(name+":true"),
					s.replicated("test_schema.test_table_1", 2),
					s.sameOID(name, &oid),
				),
			},
		},
	})
}

// The values of the attributes are case sensitive: the server and the state use these spellings.
func TestAccPostgresqlSubscription_InvalidValues(t *testing.T) {
	s := newSubTest(t)

	resource.Test(t, resource.TestCase{
		PreCheck:  s.preCheck(),
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config:      s.config("tf_sub_invalid", []string{"pub1"}, `synchronous_commit = "ON"`),
				ExpectError: regexp.MustCompile(`expected synchronous_commit to be one of`),
			},
			{
				Config:      s.config("tf_sub_invalid", []string{"pub1"}, `origin = "ANY"`),
				ExpectError: regexp.MustCompile(`expected origin to be one of`),
			},
		},
	})
}

// The connection string holds a password: it is hidden from the output of Terraform.
func TestSubscriptionSchemaConninfoIsSensitive(t *testing.T) {
	if !resourcePostgreSQLSubscription().Schema["conninfo"].Sensitive {
		t.Fatal("conninfo of postgresql_subscription must be sensitive")
	}
}

// database cannot be altered (the subscription is created again in the other database) and, when it
// is left out, is the one of the provider and stays what it is.
func TestAccPostgresqlSubscription_Database(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_database"
	suffix2, teardown2 := setupTestDatabase(t, true, true)
	t.Cleanup(teardown2)
	t.Cleanup(func() { coolDown() })
	createTestTables(t, suffix2, []string{"test_schema.test_table_1", "test_schema.test_table_2"}, "")
	db2, _ := getTestDBNames(suffix2)
	t.Cleanup(func() { dropSubscriptions(t, db2) })

	inSub := s.config(name, []string{"pub1"}, "")
	inDB2 := strings.Replace(inSub, `database     = "`+s.dbSub+`"`, `database     = "`+db2+`"`, 1)
	left := strings.Replace(inDB2, `database     = "`+db2+`"`, ``, 1)
	count := func(db, expected string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			got, err := s.query(db, "SELECT count(*)::text FROM pg_catalog.pg_subscription WHERE subname = $1 AND subdbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = $2)", name, db)
			if err != nil {
				return err
			}
			if got != expected {
				return fmt.Errorf("%s subscriptions %s in %s, expected %s", got, name, db, expected)
			}
			return nil
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: inSub,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "database", s.dbSub),
					count(s.dbSub, "1"), count(db2, "0"),
				),
			},
			{
				// replaced: dropped in the first database and created in the other
				Config: inDB2,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "database", db2),
					count(s.dbSub, "0"), count(db2, "1"),
				),
			},
			{
				Config:   left,
				PlanOnly: true,
			},
		},
	})
}

// subscriptionWorker is the process id of the apply worker of the subscription.
func (s *subTest) workerPID(name string) (string, error) {
	var got string
	for i := 0; i < 60; i++ {
		var err error
		got, err = s.query(s.dbSub, "SELECT pid::text FROM pg_catalog.pg_stat_subscription WHERE subname = $1 AND relid IS NULL AND pid IS NOT NULL", name)
		if err == nil && got != "" {
			return got, nil
		}
		time.Sleep(time.Second)
	}
	return "", fmt.Errorf("no apply worker for subscription %s", name)
}

func (s *subTest) rememberWorker(name string, pid *string) resource.TestCheckFunc {
	return func(_ *terraform.State) (err error) {
		*pid, err = s.workerPID(name)
		return err
	}
}

// sameWorker checks that the apply worker has not been stopped, which disabling the
// subscription does.
func (s *subTest) sameWorker(name string, pid *string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		time.Sleep(3 * time.Second)
		got, err := s.workerPID(name)
		if err != nil {
			return err
		}
		if got != *pid {
			return fmt.Errorf("the apply worker of %s has been restarted (pid %s, was %s): the subscription has been disabled", name, got, *pid)
		}
		return nil
	}
}

// An option which the server alters in an enabled subscription (synchronous_commit) is altered without disabling
// the subscription, whatever the other options of the configuration are (those which need it are unchanged).
func TestAccPostgresqlSubscription_AlterWithoutDisabling(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_keep_worker"
	version := testServerVersionNum(t)
	var pid string
	options := func(commit string) string {
		o := fmt.Sprintf("synchronous_commit = %q", commit)
		if version >= 140000 {
			o += "\nbinary = false"
		}
		if version >= 150000 {
			o += "\ntwo_phase = false\ndisable_on_error = false"
		}
		if version >= 170000 {
			o += "\nfailover = false"
		}
		if version >= 190000 {
			o += "\nretain_dead_tuples = false"
		}
		return o
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, options("local")),
				Check:  s.rememberWorker(name, &pid),
			},
			{
				Config: s.config(name, []string{"pub1"}, options("remote_write")),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subsynccommit", "remote_write"),
					s.catalog(name, "subenabled", "true"),
					s.sameWorker(name, &pid),
				),
			},
			{
				// the publications as well
				Config: s.config(name, []string{"pub1", "pub2"}, options("remote_write")),
				Check: resource.ComposeTestCheckFunc(
					s.publications(name, "test_publication_1,test_publication_2"),
					s.catalog(name, "subenabled", "true"),
				),
			},
		},
	})
}

// A failed update keeps the state it had: the next plan renames the subscription (the statements
// which did not run are not recorded as done).
func TestAccPostgresqlSubscription_FailedUpdate(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_failed"
	const renamed = "tf_sub_failed_renamed"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, ""),
				Check:  s.rememberOID(name, &oid),
			},
			{
				// the owner fails before the rename
				Config:      s.config(renamed, []string{"pub1"}, `owner = "tf_no_such_role"`),
				ExpectError: regexp.MustCompile(`tf_no_such_role`),
			},
			{
				Config: s.config(renamed, []string{"pub1"}, ""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "name", renamed),
					s.catalog(renamed, "subname", renamed),
					s.sameOID(renamed, &oid),
				),
			},
		},
	})
}

// connect = true is the default: the subscription connects to the publisher, so it is enabled.
func TestAccPostgresqlSubscription_ConnectTrue(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_connect"

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: s.config(name, []string{"pub1"}, "connect = true"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "connect", "true"),
					resource.TestCheckResourceAttr(subRes, "enabled", "true"),
					s.catalog(name, "subenabled", "true"),
					s.insert("test_schema.test_table_1", "connected"),
					s.replicated("test_schema.test_table_1", 1),
				),
			},
		},
	})
}

// connect = false with enabled = false, and create_slot left to its default (true): the subscription
// is created without a slot, and destroyed without looking for the slot on the publisher.
func TestAccPostgresqlSubscription_ConnectFalseDefaultCreateSlot(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_noconnect_default"
	config := strings.Replace(s.config(name, []string{"pub1"}, "connect = false\nenabled = false"), "create_slot  = false", "", 1)
	config = strings.Replace(config, `slot_name    = postgresql_replication_slot.test_slot.name`, ``, 1)

	resource.Test(t, resource.TestCase{
		PreCheck:     s.preCheck(),
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckPostgresqlSubscriptionDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "create_slot", "true"),
					s.catalog(name, "subenabled", "false"),
					s.slots("test_slot,test_slot2"),
				),
			},
		},
	})
}
