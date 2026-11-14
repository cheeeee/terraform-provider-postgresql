package postgresql

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/cloudsqlconn"
	"golang.org/x/oauth2"
)

// scheme = "gcppostgres": the connection string of the provider, with the host written as
// project/region/instance or as project:region:instance (the connection name given by
// google_sql_database_instance), names the instance the Cloud SQL connector asks the Cloud SQL
// Admin API for. The API is a local server which records what it is asked and refuses: nothing of
// Google Cloud is called. (The connection itself is tested on Cloud SQL: tests/e2e/gcp.sh.)
func TestGCPConnStrInstance(t *testing.T) {
	var lock sync.Mutex
	var asked []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		asked = append(asked, r.URL.Path)
		lock.Unlock()
		http.Error(w, `{"error": {"code": 404, "message": "the instance is only recorded"}}`, http.StatusNotFound)
	}))
	defer api.Close()

	// No credentials of Google Cloud, and the Admin API of the test
	tokenSource, dialerOptions := gcpTokenSource, gcpDialerOptions
	resetDialers := func() {
		gcpDialersLock.Lock()
		defer gcpDialersLock.Unlock()
		for _, dialer := range gcpDialers {
			_ = dialer.Close()
		}
		gcpDialers = map[string]*cloudsqlconn.Dialer{}
	}
	resetDialers()
	var impersonated []string
	gcpTokenSource = func(_ context.Context, account string) (oauth2.TokenSource, error) {
		impersonated = append(impersonated, account)
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token-of-the-test"}), nil
	}
	gcpDialerOptions = []cloudsqlconn.Option{cloudsqlconn.WithAdminAPIEndpoint(api.URL)}
	defer func() {
		resetDialers()
		gcpTokenSource, gcpDialerOptions = tokenSource, dialerOptions
	}()

	for _, test := range []struct {
		host, impersonate string
	}{
		{"my-project/europe-west1/my-instance", ""},
		{"my-project:europe-west1:my-instance", ""},
		{"my-project:europe-west1:my-instance", "account@my-project.iam.gserviceaccount.com"},
	} {
		t.Run(test.host+" "+test.impersonate, func(t *testing.T) {
			lock.Lock()
			asked = nil
			lock.Unlock()
			config := &Config{
				Scheme:          "gcppostgres",
				Host:            test.host,
				Port:            5432,
				Username:        "some user",
				Password:        "p@ss/word",
				SSLMode:         "require",
				ApplicationName: "Terraform provider",
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			db, err := openGCPDBConnection(ctx, config.connStr("my_database"), test.impersonate)
			if err != nil {
				t.Fatalf("the connection string is refused: %v", err)
			}
			defer func() { _ = db.Close() }()
			if err := db.PingContext(ctx); err == nil {
				t.Fatal("a connection was opened without any instance")
			}

			lock.Lock()
			defer lock.Unlock()
			if len(asked) == 0 {
				t.Fatal("the Cloud SQL Admin API was not asked for the instance")
			}
			for _, path := range asked {
				if !strings.Contains(path, "/projects/my-project/instances/my-instance/") &&
					!strings.Contains(path, "/projects/my-project/instances/my-instance:") {
					t.Errorf("the Cloud SQL Admin API was asked for %s", path)
				}
			}
			if impersonated[len(impersonated)-1] != test.impersonate {
				t.Errorf("the credentials are the ones of %q, not of %q", impersonated[len(impersonated)-1], test.impersonate)
			}
		})
	}
}
