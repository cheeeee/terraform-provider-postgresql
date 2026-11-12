package postgresql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blang/semver"
)

func TestConfigConnParams(t *testing.T) {
	var tests = []struct {
		input *Config
		want  []string
	}{
		{&Config{Scheme: "postgres", SSLMode: "require", ConnectTimeoutSec: 10}, []string{"connect_timeout=10", "sslmode=require"}},
		{&Config{Scheme: "postgres", SSLMode: "disable"}, []string{"connect_timeout=0", "sslmode=disable"}},
		{&Config{Scheme: "awspostgres", ConnectTimeoutSec: 10}, []string{}},
		{&Config{Scheme: "awspostgres", SSLMode: "disable"}, []string{}},
		{&Config{ExpectedVersion: semver.MustParse("9.0.0"), ApplicationName: "Terraform provider"}, []string{"fallback_application_name=Terraform+provider"}},
		{&Config{ExpectedVersion: semver.MustParse("8.0.0"), ApplicationName: "Terraform provider"}, []string{}},
		{&Config{SSLClientCert: &ClientCertificateConfig{CertificatePath: "/path/to/public-certificate.pem", KeyPath: "/path/to/private-key.pem"}}, []string{"sslcert=%2Fpath%2Fto%2Fpublic-certificate.pem", "sslkey=%2Fpath%2Fto%2Fprivate-key.pem"}},
		{&Config{SSLRootCertPath: "/path/to/root.pem"}, []string{"sslrootcert=%2Fpath%2Fto%2Froot.pem"}},
	}

	for _, test := range tests {

		connParams := test.input.connParams()

		sort.Strings(connParams)
		sort.Strings(test.want)

		if !reflect.DeepEqual(connParams, test.want) {
			t.Errorf("Config.connParams(%+v) returned %#v, want %#v", test.input, connParams, test.want)
		}

	}
}

func TestConfigConnStr(t *testing.T) {
	var tests = []struct {
		input        *Config
		wantDbURL    string
		wantDbParams []string
	}{
		{&Config{Scheme: "postgres", Host: "localhost", Port: 5432, Username: "postgres_user", Password: "postgres_password", SSLMode: "disable"}, "postgres://postgres_user:postgres_password@localhost:5432/postgres", []string{"connect_timeout=0", "sslmode=disable"}},
		{&Config{Scheme: "postgres", Host: "localhost", Port: 5432, Username: "spaced user", Password: "spaced password", SSLMode: "disable"}, "postgres://spaced%20user:spaced%20password@localhost:5432/postgres", []string{"connect_timeout=0", "sslmode=disable"}},
	}

	for _, test := range tests {

		connStr := test.input.connStr("postgres")

		splitConnStr := strings.Split(connStr, "?")

		if splitConnStr[0] != test.wantDbURL {
			t.Errorf("Config.connStr(%+v) returned %#v, want %#v", test.input, splitConnStr[0], test.wantDbURL)
		}

		connParams := strings.Split(splitConnStr[1], "&")

		sort.Strings(connParams)
		sort.Strings(test.wantDbParams)

		if !reflect.DeepEqual(connParams, test.wantDbParams) {
			t.Errorf("Config.connStr(%+v) returned %#v, want %#v", test.input, connParams, test.wantDbParams)
		}

	}
}

// flakyPingDriver is a fake database/sql/driver.Driver whose connections fail
// to Ping a configurable number of times before succeeding, used to exercise
// Client.connectWithRetry without a real PostgreSQL server.
type flakyPingDriver struct {
	failuresBeforeSuccess int32
	attempts              atomic.Int32
}

func (d *flakyPingDriver) Open(name string) (driver.Conn, error) {
	return &flakyPingConn{driver: d}, nil
}

type flakyPingConn struct {
	driver *flakyPingDriver
}

func (c *flakyPingConn) Prepare(query string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *flakyPingConn) Close() error                              { return nil }
func (c *flakyPingConn) Begin() (driver.Tx, error)                 { return nil, driver.ErrSkip }

func (c *flakyPingConn) Ping(ctx context.Context) error {
	attempt := c.driver.attempts.Add(1)
	if attempt <= c.driver.failuresBeforeSuccess {
		return errors.New("connection refused")
	}
	return nil
}

func newFlakyClient(driverName string, drv *flakyPingDriver, maxConnRetries, timeoutSeconds int) *Client {
	sql.Register(driverName, drv)
	return &Client{
		config: Config{
			Scheme:                        "postgres",
			MaxConnRetries:                maxConnRetries,
			ConnectionRetryTimeoutSeconds: timeoutSeconds,
		},
	}
}

func TestConnectWithRetrySucceedsAfterTransientFailures(t *testing.T) {
	drv := &flakyPingDriver{failuresBeforeSuccess: 2}
	c := newFlakyClient("flaky-retry-success", drv, 5, 5)

	db, err := c.connectWithRetry(context.Background(), "flaky-retry-success", "dsn")
	if err != nil {
		t.Fatalf("connectWithRetry() returned unexpected error: %v", err)
	}
	defer func() { _ = db.Close() }()

	if got := drv.attempts.Load(); got != 3 {
		t.Errorf("expected 3 connection attempts (2 failures + 1 success), got %d", got)
	}
}

func TestConnectWithRetryGivesUpAfterMaxConnRetries(t *testing.T) {
	drv := &flakyPingDriver{failuresBeforeSuccess: 100}
	c := newFlakyClient("flaky-retry-exhausted", drv, 3, 30)

	_, err := c.connectWithRetry(context.Background(), "flaky-retry-exhausted", "dsn")
	if err == nil {
		t.Fatal("connectWithRetry() expected an error, got nil")
	}

	if got := drv.attempts.Load(); got != 4 {
		t.Errorf("expected exactly 4 connection attempts (1 initial + MaxConnRetries), got %d", got)
	}
}

func TestParseServerVersion(t *testing.T) {
	var tests = []struct {
		pgVersion string
		expected  string
	}{
		{"PostgreSQL 9.2.21 on x86_64-apple-darwin16.5.0, compiled by Apple LLVM version 8.1.0 (clang-802.0.42), 64-bit", "9.2.21"},
		{"PostgreSQL 9.6.7, compiled by Visual C++ build 1800, 64-bit", "9.6.7"},
		{"PostgreSQL 17.11 (Debian 17.11-1.pgdg13+2) on aarch64-unknown-linux-gnu, compiled by gcc (Debian 14.2.0-19) 14.2.0, 64-bit", "17.11.0"},
		{"PostgreSQL 17.11 on aarch64-unknown-linux-gnu, compiled by aarch64-unknown-linux-gnu-gcc (GCC) 12.4.0, 64-bit", "17.11.0"},
		// pre-releases
		{"PostgreSQL 19rc1 (Debian 19~rc1-1.pgdg13+1) on aarch64-unknown-linux-gnu, compiled by gcc (Debian 14.2.0-19) 14.2.0, 64-bit", "19.0.0"},
		{"PostgreSQL 19beta3 on x86_64-pc-linux-gnu, compiled by gcc (GCC) 14.2.0, 64-bit", "19.0.0"},
		{"PostgreSQL 20devel on x86_64-pc-linux-gnu, compiled by gcc (GCC) 14.2.0, 64-bit", "20.0.0"},
		// not a version
		{"PostgreSQL", ""},
		{"PostgreSQL unknown on x86_64-pc-linux-gnu", ""},
		{"", ""},
	}

	// The error gives what could not be parsed
	if _, err := parseServerVersion("PostgreSQL unknown on x86_64-pc-linux-gnu"); err == nil || !strings.Contains(err.Error(), `"unknown"`) {
		t.Errorf(`parseServerVersion: expected an error about "unknown", got %v`, err)
	}

	for _, test := range tests {
		version, err := parseServerVersion(test.pgVersion)
		if test.expected == "" {
			if err == nil {
				t.Errorf("parseServerVersion(%q): expected an error, got %s", test.pgVersion, version)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseServerVersion(%q): %v", test.pgVersion, err)
			continue
		}
		if version.String() != test.expected {
			t.Errorf("parseServerVersion(%q): expected %s, got %s", test.pgVersion, test.expected, version)
		}
	}
}
