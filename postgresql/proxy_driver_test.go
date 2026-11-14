package postgresql

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// testClosingServer listens on a local port and closes every connection it accepts, like a
// network or a server which drops a connection while it is being opened. It returns the port
// and the counter of the connections.
func testClosingServer(t *testing.T) (int, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, &accepted
}

// A connection closed while it is opened is a bad connection: database/sql opens another one,
// and every lost connection is written in the log, without the password.
func TestProxyDriverLostConnection(t *testing.T) {
	port, accepted := testClosingServer(t)
	dsn := fmt.Sprintf("host=127.0.0.1 port=%d user=u password=tfpg-secret dbname=d sslmode=disable connect_timeout=5", port)

	var logged bytes.Buffer
	output, flags := log.Writer(), log.Flags()
	log.SetOutput(&logged)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(output)
		log.SetFlags(flags)
	}()
	warnings := func() int {
		return strings.Count(logged.String(), "[WARN] the connection to PostgreSQL was closed by the other side")
	}

	_, err := proxyDriver{}.Open(dsn)
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("expected a bad connection, got %v", err)
	}
	if !isLostConnectionError(err) {
		t.Errorf("the cause of the error is lost: %v", err)
	}
	if got := warnings(); got != 1 {
		t.Errorf("%d warnings for a lost connection, expected 1: %q", got, logged.String())
	}

	accepted.Store(0)
	db, err := sql.Open(proxyDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Begin(); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("expected a bad connection, got %v", err)
	}
	if got := accepted.Load(); got != 3 {
		t.Errorf("%d connections were opened for a transaction, expected 3 attempts", got)
	}
	accepted.Store(0)
	if err := db.QueryRow("SELECT 1").Scan(new(int)); !errors.Is(err, driver.ErrBadConn) {
		t.Errorf("expected a bad connection, got %v", err)
	}
	if got := accepted.Load(); got != 3 {
		t.Errorf("%d connections were opened for a query, expected 3 attempts", got)
	}
	// one for the direct call, three for the transaction, three for the query
	if got := warnings(); got != 7 {
		t.Errorf("%d warnings, expected one for each of the 7 lost connections: %q", got, logged.String())
	}
	if strings.Contains(logged.String(), "tfpg-secret") {
		t.Errorf("the password is in the log: %q", logged.String())
	}
}

// The other errors are returned as they are, at the first attempt.
func TestProxyDriverOtherErrors(t *testing.T) {
	// a port where nothing listens
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	_, err = proxyDriver{}.Open(fmt.Sprintf("host=127.0.0.1 port=%d user=u dbname=d sslmode=disable connect_timeout=5", port))
	if err == nil || errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("a refused connection is not a bad connection: %v", err)
	}

	for _, test := range []struct {
		err  error
		lost bool
	}{
		{errors.New("pq: password authentication failed for user \"u\""), false},
		{fmt.Errorf("read tcp: %w", net.ErrClosed), false},
		{fmt.Errorf("dial: %w", errors.New("connection refused")), false},
	} {
		if got := isLostConnectionError(test.err); got != test.lost {
			t.Errorf("isLostConnectionError(%v) = %t", test.err, got)
		}
	}
}
