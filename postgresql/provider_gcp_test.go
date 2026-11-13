package postgresql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/postgres/gcppostgres"
)

// recordingCertSource is where the Cloud SQL proxy asks the certificates of an instance: it
// keeps the name of the instance and refuses, nothing of Google Cloud is called.
type recordingCertSource struct {
	lock      sync.Mutex
	instances []string
}

var errRecordingCertSource = errors.New("no certificate: the instance is only recorded")

func (s *recordingCertSource) record(instance string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.instances = append(s.instances, instance)
}

func (s *recordingCertSource) Local(instance string) (tls.Certificate, error) {
	s.record(instance)
	return tls.Certificate{}, errRecordingCertSource
}

func (s *recordingCertSource) Remote(instance string) (*x509.Certificate, string, string, string, error) {
	s.record(instance)
	return nil, "", "", "", errRecordingCertSource
}

// scheme = "gcppostgres": the connection string of the provider, with the host written as
// project/region/instance or as project:region:instance (the connection name given by
// google_sql_database_instance), is the instance and the database the Cloud SQL proxy connects to.
func TestGCPConnStrInstance(t *testing.T) {
	for _, host := range []string{"my-project/europe-west1/my-instance", "my-project:europe-west1:my-instance"} {
		t.Run(host, func(t *testing.T) {
			config := &Config{
				Scheme:          "gcppostgres",
				Host:            host,
				Port:            5432,
				Username:        "some user",
				Password:        "p@ss/word",
				SSLMode:         "require",
				ApplicationName: "Terraform provider",
			}
			dsn, err := url.Parse(config.connStr("my_database"))
			if err != nil {
				t.Fatalf("the connection string cannot be parsed: %v", err)
			}

			certs := &recordingCertSource{}
			opener := gcppostgres.URLOpener{CertSource: certs}
			db, err := opener.OpenPostgresURL(context.Background(), dsn)
			if err != nil {
				t.Fatalf("the connection string %s is refused: %v", dsn.Redacted(), err)
			}
			defer func() { _ = db.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := db.PingContext(ctx); err == nil {
				t.Fatal("a connection was opened without any certificate")
			}

			certs.lock.Lock()
			defer certs.lock.Unlock()
			if len(certs.instances) == 0 {
				t.Fatal("no certificate was asked: the proxy of Cloud SQL was not used")
			}
			for _, instance := range certs.instances {
				// The proxy reads the project, the region and the name and ignores what follows (the port)
				parts := strings.Split(instance, ":")
				if len(parts) < 3 || parts[0] != "my-project" || parts[1] != "europe-west1" || parts[2] != "my-instance" {
					t.Errorf("the instance asked to the proxy is %q", instance)
				}
			}
		})
	}
}
