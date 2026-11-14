package postgresql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"syscall"
	"time"

	"github.com/lib/pq"
	"golang.org/x/net/proxy"
)

const proxyDriverName = "postgresql-proxy"

type proxyDriver struct{}

// Open opens a connection. The provider keeps no idle connection, so one is opened for every
// transaction and every query: when the network or the server closes it while it is being opened
// (EOF, connection reset), nothing has been executed yet. It is reported as a bad connection, for
// which database/sql opens another one (it makes three attempts) instead of failing the operation.
//
// It is written in the log every time: a connection which is lost has a cause, which is not to
// be hidden. One which was found: a Mac which resets, by itself, 1 in 1024 of the connections
// whose SYN it had to send again.
func (d proxyDriver) Open(name string) (driver.Conn, error) {
	conn, err := pq.DialOpen(d, name)
	if err != nil && isLostConnectionError(err) {
		// (the error is the one of the network: it has no part of the connection string)
		log.Printf("[WARN] the connection to PostgreSQL was closed by the other side while it was being opened (%v): it is opened again, three attempts are made", err)
		return nil, fmt.Errorf("%w: %w", driver.ErrBadConn, err)
	}
	return conn, err
}

// isLostConnectionError tells whether the error is the one of a connection closed by the other side.
func isLostConnectionError(err error) bool {
	for _, lost := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.EPIPE} {
		if errors.Is(err, lost) {
			return true
		}
	}
	return false
}

func (d proxyDriver) Dial(network, address string) (net.Conn, error) {
	dialer := proxy.FromEnvironment()
	return dialer.Dial(network, address)
}

func (d proxyDriver) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()
	return proxy.Dial(ctx, network, address)
}

func init() {
	sql.Register(proxyDriverName, proxyDriver{})
}
