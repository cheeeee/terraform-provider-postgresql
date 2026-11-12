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
				),
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
				),
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

	return &subTest{t: t, dbPub: dbNamePub, dbSub: dbNameSub, conninfo: getConnInfo(t, dbNamePub)}
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
	}
	resource "postgresql_replication_slot" "test_slot2" {
		name     = "test_slot2"
		database = "%[1]s"
		plugin   = "pgoutput"
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
	db, err := sql.Open("postgres", config.connStr(dbName))
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

func (s *subTest) insert(table, val string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		config := getTestConfig(s.t)
		dbExecute(s.t, config.connStr(s.dbPub), fmt.Sprintf("INSERT INTO %s (val) VALUES ('%s')", table, val))
		return nil
	}
}

// importStep checks the import of the subscription. The options of the creation are
// not in the catalog, and the connection string is only readable by the superusers.
func (s *subTest) importStep() resource.TestStep {
	ignore := []string{"create_slot", "connect", "copy_data"}
	if got, err := s.query(s.dbSub, "SELECT has_column_privilege('pg_catalog.pg_subscription', 'subconninfo', 'SELECT')::text"); err != nil || got != "true" {
		ignore = append(ignore, "conninfo")
	}
	return resource.TestStep{
		ResourceName:            "postgresql_subscription.test_sub",
		ImportState:             true,
		ImportStateVerify:       true,
		ImportStateVerifyIgnore: ignore,
	}
}

const subRes = "postgresql_subscription.test_sub"

// serverVersionNum returns the version of the server as server_version_num
func testServerVersionNum(t *testing.T) int {
	config := getTestConfig(t)
	db, err := sql.Open("postgres", config.connStr("postgres"))
	if err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer closeDB(t, db)
	var v int
	if err := db.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&v); err != nil {
		t.Fatalf("could not get the version of the server: %v", err)
	}
	return v
}

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
	// the connection string is not readable from the catalog by every user
	checkConninfo := func(sub, contains string) resource.TestCheckFunc {
		return func(st *terraform.State) error {
			if got, err := s.query(s.dbSub, "SELECT has_column_privilege('pg_catalog.pg_subscription', 'subconninfo', 'SELECT')::text"); err != nil || got != "true" {
				return nil
			}
			return s.catalog(sub, "subconninfo LIKE '%"+contains+"%'", "true")(st)
		}
	}
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
					checkConninfo(name, "application_name=tf_sub_alter"),
					s.sameOID(name, &oid),
				),
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

	var slotCreated bool
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			s.preCheck()()
			dbExecute(t, s.dsn(s.dbPub), "SELECT pg_create_logical_replication_slot('tf_kept_slot', 'pgoutput')")
			slotCreated = true
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
	if slotCreated {
		dbExecute(t, s.dsn(s.dbPub), "SELECT pg_drop_replication_slot('tf_kept_slot')")
	}
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
				Config: s.config(name, []string{"pub1", "pub2"}, options+"\nenabled = true"),
				Check: resource.ComposeTestCheckFunc(
					s.catalog(name, "subenabled", "true"),
					s.replicated("test_schema.test_table_1", 3),
				),
			},
		},
	})
}

// The owner of the subscription is altered in place.
func TestAccPostgresqlSubscription_Owner(t *testing.T) {
	s := newSubTest(t)
	const name = "tf_sub_owner"
	roleName := "tf_sub_owner_" + strconv.Itoa(int(time.Now().UnixNano()))
	var oid string

	setup := func() {
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
	}
	createRole := func() {
		config := getTestConfig(t)
		dbExecute(t, config.connStr("postgres"), fmt.Sprintf("CREATE ROLE %s", roleName))
		if testServerVersionNum(t) >= 160000 {
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("GRANT pg_create_subscription TO %s", roleName))
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("GRANT CREATE ON DATABASE %s TO %s", s.dbSub, roleName))
		} else {
			dbExecute(t, config.connStr("postgres"), fmt.Sprintf("ALTER ROLE %s SUPERUSER", roleName))
		}
	}
	t.Cleanup(func() {
		config := getTestConfig(t)
		// the role has privileges on the database, which is dropped after
		dbExecute(t, config.connStr("postgres"), fmt.Sprintf(
			`DO $$ BEGIN IF EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN DROP OWNED BY %[1]s; DROP ROLE %[1]s; END IF; END $$`, roleName))
	})
	// a non superuser owner is disabled, its apply worker could not connect without a password
	disabled := "enabled = false"

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			setup()
			createRole()
		},
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

// A subscription which connects to the server it lives on cannot create its replication slot
// (the creation of the slot waits for the transaction of CREATE SUBSCRIPTION). To test
// create_slot = true, the publisher has to be another server:
//
//	TEST_REMOTE_PUBLISHER_DSN       how the test reaches it, e.g. postgres://postgres:postgres@localhost:25449/postgres?sslmode=disable
//	TEST_REMOTE_PUBLISHER_CONNINFO  how the subscriber reaches it, without dbname, e.g. host=publisher port=5432 user=postgres password=postgres
//
// The test is skipped when they are not set.
func TestAccPostgresqlSubscription_CreateSlotOnRemotePublisher(t *testing.T) {
	skipIfNotAcc(t)
	remoteDSN := os.Getenv("TEST_REMOTE_PUBLISHER_DSN")
	remoteConninfo := os.Getenv("TEST_REMOTE_PUBLISHER_CONNINFO")
	if remoteDSN == "" || remoteConninfo == "" {
		t.Skip("Skip test: TEST_REMOTE_PUBLISHER_DSN and TEST_REMOTE_PUBLISHER_CONNINFO are not set")
	}
	dsnFor := func(dbName string) string {
		u, err := url.Parse(remoteDSN)
		if err != nil {
			t.Fatalf("could not parse TEST_REMOTE_PUBLISHER_DSN: %v", err)
		}
		u.Path = "/" + dbName
		return u.String()
	}
	remoteQuery := func(dbName, query string) string {
		db, err := sql.Open("postgres", dsnFor(dbName))
		if err != nil {
			t.Fatalf("could not connect to the remote publisher: %v", err)
		}
		defer closeDB(t, db)
		var result sql.NullString
		if err := db.QueryRow(query).Scan(&result); err != nil {
			t.Fatalf("could not execute %s on the remote publisher: %v", query, err)
		}
		return result.String
	}

	dbSuffixSub, teardownSub := setupTestDatabase(t, true, true)
	t.Cleanup(teardownSub)
	t.Cleanup(func() { coolDown() })
	createTestTables(t, dbSuffixSub, []string{"test_schema.test_table_1"}, "")
	dbNameSub, _ := getTestDBNames(dbSuffixSub)
	remoteDB := "tf_remote_pub_" + strconv.Itoa(int(time.Now().UnixNano()))
	dbExecute(t, dsnFor("postgres"), "CREATE DATABASE "+remoteDB)
	t.Cleanup(func() { dbExecute(t, dsnFor("postgres"), "DROP DATABASE IF EXISTS "+remoteDB+"") })
	dbExecute(t, dsnFor(remoteDB), "CREATE SCHEMA test_schema")
	dbExecute(t, dsnFor(remoteDB), "CREATE TABLE test_schema.test_table_1 (val text, test_column_one text, test_column_two text)")
	dbExecute(t, dsnFor(remoteDB), "CREATE PUBLICATION tf_remote_publication FOR TABLE test_schema.test_table_1")

	s := &subTest{t: t, dbSub: dbNameSub, conninfo: remoteConninfo + " dbname=" + remoteDB}
	slots := func(expected string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			var got string
			for i := 0; i < 30; i++ {
				got = remoteQuery(remoteDB, "SELECT COALESCE(string_agg(slot_name || ':' || active::text, ',' ORDER BY slot_name), '') FROM pg_replication_slots WHERE database = current_database()")
				if got == expected {
					return nil
				}
				time.Sleep(time.Second)
			}
			return fmt.Errorf("slots of the remote publisher are %q, expected %q", got, expected)
		}
	}
	config := func(extra string) string {
		return fmt.Sprintf(`
		resource "postgresql_subscription" "test_sub" {
			name         = "tf_sub_remote"
			database     = "%s"
			conninfo     = "%s"
			publications = ["tf_remote_publication"]
			%s
		}`, dbNameSub, s.conninfo, extra)
	}
	const name = "tf_sub_remote"
	var oid string

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			testCheckCanCreateSubscription(t, s.conninfo)
		},
		Providers: testAccProviders,
		CheckDestroy: func(st *terraform.State) error {
			if err := testAccCheckPostgresqlSubscriptionDestroy(st); err != nil {
				return err
			}
			// the slot which the subscription created is dropped with it: nothing leaks on the publisher
			return slots("")(st)
		},
		Steps: []resource.TestStep{
			{
				Config: config(""),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(subRes, "create_slot", "true"),
					resource.TestCheckResourceAttr(subRes, "slot_name", name),
					resource.TestCheckResourceAttr(subRes, "enabled", "true"),
					s.catalog(name, "subslotname", name),
					slots(name+":true"),
					s.rememberOID(name, &oid),
					func(_ *terraform.State) error {
						dbExecute(t, dsnFor(remoteDB), "INSERT INTO test_schema.test_table_1 (val) VALUES ('remote')")
						return s.waitFor(dbNameSub, "SELECT count(*)::text FROM test_schema.test_table_1", "1")
					},
				),
			},
			{
				// create_slot = true is created only once: no diff, no replacement
				Config: config("create_slot = false"),
				Check:  s.sameOID(name, &oid),
			},
			{
				Config:   config("create_slot = false"),
				PlanOnly: true,
			},
			{
				// renamed: the slot keeps its name
				Config: strings.Replace(config("create_slot = false"), `name         = "tf_sub_remote"`, `name         = "tf_sub_remote_2"`, 1),
				Check: resource.ComposeTestCheckFunc(
					s.catalog("tf_sub_remote_2", "subslotname", name),
					s.sameOID("tf_sub_remote_2", &oid),
					slots(name+":true"),
				),
			},
		},
	})
}
