package postgresql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

// The token given by the fake identity endpoint: it has the shape and the size of a real one
// (three parts in base64, more than a thousand characters), it is used as a password.
var azureTestToken = "eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9." + strings.Repeat("dGVycmFmb3JtLXByb3ZpZGVyLXBvc3RncmVzcWw", 30) + ".c2lnbmF0dXJl"

const azureTestIdentityHeader = "tf-tests-identity-header"

type azureIdentityRequest struct {
	resource   string
	apiVersion string
	header     string
}

var (
	azureIdentityOnce     sync.Once
	azureIdentityServer   *httptest.Server
	azureIdentityLock     sync.Mutex
	azureIdentityRequests []azureIdentityRequest
)

// useFakeAzureIdentity makes the Azure SDK ask its token to a local server which answers like the
// managed identity endpoint of Azure App Service (IDENTITY_ENDPOINT, the only source of tokens of
// the SDK which is not forced to HTTPS). There is one server for the whole run of the tests: the
// SDK keeps the token of a resource in memory, a second server would never be called.
func useFakeAzureIdentity(t *testing.T) {
	azureIdentityOnce.Do(func() {
		azureIdentityServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			azureIdentityLock.Lock()
			azureIdentityRequests = append(azureIdentityRequests, azureIdentityRequest{
				resource:   r.URL.Query().Get("resource"),
				apiVersion: r.URL.Query().Get("api-version"),
				header:     r.Header.Get("X-IDENTITY-HEADER"),
			})
			azureIdentityLock.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": azureTestToken,
				"expires_on":   strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
				"resource":     r.URL.Query().Get("resource"),
				"token_type":   "Bearer",
			})
		}))
	})
	t.Setenv("IDENTITY_ENDPOINT", azureIdentityServer.URL)
	t.Setenv("IDENTITY_HEADER", azureTestIdentityHeader)
	// The other sources of credentials of the SDK must not be found before this one
	for _, name := range []string{
		"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_CLIENT_CERTIFICATE_PATH", "AZURE_USERNAME", "AZURE_PASSWORD",
		"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TOKEN_CREDENTIALS", "MSI_ENDPOINT", "IMDS_ENDPOINT",
	} {
		// (t.Setenv first: the value is restored at the end of the test)
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

// azure_identity_auth: the token is asked for the resource of the databases of Azure, to the
// identity of the environment.
func TestAcquireAzureOauthToken(t *testing.T) {
	useFakeAzureIdentity(t)

	token, err := acquireAzureOauthToken("00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("could not get a token: %v", err)
	}
	if token != azureTestToken {
		t.Fatalf("the token is not the one of the identity endpoint: %q", token)
	}

	azureIdentityLock.Lock()
	defer azureIdentityLock.Unlock()
	if len(azureIdentityRequests) == 0 {
		t.Fatal("the identity endpoint was not called")
	}
	for _, request := range azureIdentityRequests {
		if request.resource != "https://ossrdbms-aad.database.windows.net" {
			t.Errorf("the token was asked for the resource %q", request.resource)
		}
		if request.header != azureTestIdentityHeader {
			t.Errorf("the identity header is %q", request.header)
		}
	}
}

// azure_identity_auth: the provider logs in with the token as the password of the role, not with
// the password of its configuration or of the environment. The server is a PostgreSQL server which
// asks for a password: the role of the test has the token as its password.
func TestAccProviderAzureIdentityAuth(t *testing.T) {
	skipIfNotAcc(t)
	testAccPreCheck(t)
	useFakeAzureIdentity(t)

	config := getTestConfig(t)
	dsn := config.connStr("postgres")
	const admin, probe = "tf_tests_azure_admin", "tf_tests_azure_probe"

	drop := func() {
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+probe)
		dbExecute(t, dsn, "DROP ROLE IF EXISTS "+admin)
	}
	drop()
	dbExecute(t, dsn, fmt.Sprintf("CREATE ROLE %s LOGIN CREATEROLE PASSWORD '%s'", admin, azureTestToken))
	t.Cleanup(func() {
		resetDBRegistry()
		drop()
	})

	// Without the token there is no way in: the server does ask for the password of this role
	if db, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%d user=%s password=wrong dbname=postgres sslmode=%s", config.Host, config.Port, admin, config.SSLMode)); err == nil {
		err = db.Ping()
		_ = db.Close()
		if err == nil {
			t.Skip("the server does not ask for a password: the token cannot be checked")
		}
	}

	providerConfig := fmt.Sprintf(`
provider "postgresql" {
  username  = %q
  password  = "not-the-password"
  superuser = false

  azure_identity_auth = true
  azure_tenant_id     = "00000000-0000-0000-0000-000000000000"
}

resource "postgresql_role" "probe" {
  name = %q
}`, admin, probe)

	probeExists := func(*terraform.State) error {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		var count int
		if err := db.QueryRow("SELECT count(*) FROM pg_roles WHERE rolname = $1", probe).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("the role %s was not created", probe)
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		// A provider of its own: the provider of the other tests must not keep this user
		ProviderFactories: map[string]func() (*schema.Provider, error){
			"postgresql": func() (*schema.Provider, error) { return Provider(), nil },
		},
		Steps: []resource.TestStep{
			{
				PreConfig: resetDBRegistry,
				Config:    providerConfig,
				Check:     probeExists,
			},
			{
				// The password of the role is not the token anymore: the provider is refused
				PreConfig: func() {
					resetDBRegistry()
					dbExecute(t, dsn, fmt.Sprintf("ALTER ROLE %s PASSWORD 'another-password'", admin))
				},
				Config:      providerConfig,
				ExpectError: regexp.MustCompile(`password authentication failed|authentication failed`),
			},
			{
				// The token again, for the destroy
				PreConfig: func() {
					resetDBRegistry()
					dbExecute(t, dsn, fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", admin, azureTestToken))
				},
				Config: providerConfig,
				Check:  probeExists,
			},
		},
	})
}
