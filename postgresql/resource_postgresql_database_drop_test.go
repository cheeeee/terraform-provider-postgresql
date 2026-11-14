package postgresql

import (
	"database/sql"
	"testing"
	"time"
)

// A database is dropped while an autovacuum worker is in it. The worker has no role: a role
// which is not a superuser is not allowed to terminate it, neither with pg_terminate_backend
// nor with DROP DATABASE ... WITH (FORCE). The deletion of the database used to fail there
// ("must be a superuser to terminate superuser process"); PostgreSQL stops the worker itself
// when the database is dropped without FORCE.
func TestAccPostgresqlDatabase_DropWithAutovacuumWorker(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)

	const dbName = "tf_tests_db_autovacuum"
	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	dbExecute(t, dsn, "DROP DATABASE IF EXISTS "+dbName)
	dbExecute(t, dsn, "CREATE DATABASE "+dbName)
	t.Cleanup(func() {
		dbExecute(t, dsn, "DROP DATABASE IF EXISTS "+dbName)
	})

	// A table which autovacuum processes as soon as it can, and very slowly: its worker stays
	// in the database for minutes
	dbDSN := config.connStr(dbName)
	dbExecute(t, dbDSN, `CREATE TABLE autovacuum_slow (id int) WITH (
		autovacuum_vacuum_threshold = 0, autovacuum_vacuum_scale_factor = 0,
		autovacuum_vacuum_cost_delay = 100, autovacuum_vacuum_cost_limit = 1)`)
	dbExecute(t, dbDSN, "INSERT INTO autovacuum_slow SELECT generate_series(1, 300000)")
	dbExecute(t, dbDSN, "DELETE FROM autovacuum_slow")

	db, err := sql.Open(testDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(t, db)

	// The worker comes within autovacuum_naptime (a minute by default). It is the process of the
	// database which has no role: a role which is not a superuser does not see its type.
	workers := func() int {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND usesysid IS NULL", dbName).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	deadline := time.Now().Add(4 * time.Minute)
	for workers() == 0 {
		if time.Now().After(deadline) {
			t.Skip("no autovacuum worker came to the database: is autovacuum off?")
		}
		time.Sleep(2 * time.Second)
	}

	client := testAccProvider.Meta().(*Client)
	conn, err := client.Connect()
	if err != nil {
		t.Fatal(err)
	}
	d := resourcePostgreSQLDatabase().TestResourceData()
	d.SetId(dbName)
	if err := d.Set(dbNameAttr, dbName); err != nil {
		t.Fatal(err)
	}
	if err := resourcePostgreSQLDatabaseDelete(conn, d); err != nil {
		t.Fatalf("the database is not dropped while an autovacuum worker is in it: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM pg_database WHERE datname = $1", dbName).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("the database %s still exists", dbName)
	}
}
