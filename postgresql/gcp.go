package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"

	"cloud.google.com/go/cloudsqlconn"
	"gocloud.dev/gcp"
	"gocloud.dev/postgres/gcppostgres"
	"golang.org/x/oauth2"
	"google.golang.org/api/impersonate"
)

const (
	// The scope of the credentials which read the instance in the Cloud SQL Admin API
	gcpSQLAdminScope = "https://www.googleapis.com/auth/sqlservice.admin"
	// The scope of the token a service account logs in to the database with: it is sent to the
	// instance, it only allows to log in
	gcpSQLLoginScope = "https://www.googleapis.com/auth/sqlservice.login"
)

var (
	// gcpDialers are the connectors to Cloud SQL: one for the credentials of the environment (""),
	// one for each service account they impersonate. They are kept for the life of the provider.
	gcpDialers     = map[string]*cloudsqlconn.Dialer{}
	gcpDialersLock sync.Mutex

	// gcpTokenSource gives the credentials of Google Cloud: the ones of the environment, or the ones
	// of the service account they impersonate, with a scope. (A variable: the tests have no
	// credentials.)
	gcpTokenSource = func(ctx context.Context, impersonated string, scope string) (oauth2.TokenSource, error) {
		if impersonated == "" {
			credentials, err := gcp.DefaultCredentials(ctx)
			if err != nil {
				return nil, err
			}
			return credentials.TokenSource, nil
		}
		return impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: impersonated,
			Scopes:          []string{scope},
		})
	}

	// gcpDialerOptions are added to the options of the connectors (the tests give them another
	// address for the Cloud SQL Admin API).
	gcpDialerOptions []cloudsqlconn.Option
)

// gcpDialer returns the connector to Cloud SQL of the credentials of the environment, or of a
// service account they impersonate. With an impersonated service account the user of the database
// is the one of this account: it logs in with the IAM database authentication, without a password.
func gcpDialer(ctx context.Context, impersonated string) (*cloudsqlconn.Dialer, error) {
	gcpDialersLock.Lock()
	defer gcpDialersLock.Unlock()
	if dialer, ok := gcpDialers[impersonated]; ok {
		return dialer, nil
	}

	credentialsError := func(err error) error {
		if impersonated != "" {
			return fmt.Errorf("error creating token source with service account impersonation of %s: %w", impersonated, err)
		}
		return fmt.Errorf("error reading the credentials of Google Cloud: %w", err)
	}
	api, err := gcpTokenSource(ctx, impersonated, gcpSQLAdminScope)
	if err != nil {
		return nil, credentialsError(err)
	}
	options := []cloudsqlconn.Option{
		// A run of Terraform does not send the metrics of its connections to Cloud Monitoring
		cloudsqlconn.WithBuiltInMetrics(false),
	}
	if impersonated != "" {
		// The token which logs in to the database is not the one of the Admin API: it only
		// has the scope which allows to log in
		login, err := gcpTokenSource(ctx, impersonated, gcpSQLLoginScope)
		if err != nil {
			return nil, credentialsError(err)
		}
		options = append(options, cloudsqlconn.WithIAMAuthNTokenSources(api, login), cloudsqlconn.WithIAMAuthN())
	} else {
		options = append(options, cloudsqlconn.WithTokenSource(api))
	}
	options = append(options, gcpDialerOptions...)

	dialer, err := cloudsqlconn.NewDialer(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("error creating the connector to Cloud SQL: %w", err)
	}
	gcpDialers[impersonated] = dialer
	return dialer, nil
}

// openGCPDBConnection opens the connections of scheme = "gcppostgres": they go through the Cloud
// SQL connector, to the instance named by the host of the connection string
// (project/region/instance).
func openGCPDBConnection(ctx context.Context, dsn string, impersonated string) (*sql.DB, error) {
	dialer, err := gcpDialer(ctx, impersonated)
	if err != nil {
		return nil, err
	}
	dbURL, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("error parsing connection string: %w", err)
	}
	opener := gcppostgres.URLOpener{Dialer: dialer}
	return opener.OpenPostgresURL(ctx, dbURL)
}
