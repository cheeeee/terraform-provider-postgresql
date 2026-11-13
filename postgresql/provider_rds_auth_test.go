package postgresql

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

// testRDSAuthEnvironment isolates the AWS SDK from the machine which runs the test:
// no region and no profile unless the test gives them, static credentials, no network.
func testRDSAuthEnvironment(t *testing.T, sharedConfig string) {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config")
	if err := os.WriteFile(configFile, []byte(sharedConfig), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_SESSION_TOKEN",
		"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	// the credentials of a profile are used instead of the ones of the environment
	credentialsFile := filepath.Join(dir, "credentials")
	profileCredentials := "aws_access_key_id = AKIAPROFILE\naws_secret_access_key = profile-secret\n"
	if err := os.WriteFile(
		credentialsFile, []byte("[with-region]\n"+profileCredentials+"\n[without-region]\n"+profileCredentials), 0600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsFile)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAENVIRONMENT")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "environment-secret")
}

// testRDSTokenScope returns the access key and the region a token was signed with.
func testRDSTokenScope(t *testing.T, token string) (accessKey string, region string) {
	t.Helper()
	u, err := url.Parse("https://" + token)
	if err != nil {
		t.Fatalf("could not parse the token %q: %v", token, err)
	}
	// <access key>/<date>/<region>/rds-db/aws4_request
	scope := strings.Split(u.Query().Get("X-Amz-Credential"), "/")
	if len(scope) != 5 || scope[3] != "rds-db" {
		t.Fatalf("unexpected credential scope in the token: %q", u.Query().Get("X-Amz-Credential"))
	}
	return scope[0], scope[2]
}

func TestBuildRDSAuthToken(t *testing.T) {
	const profileConfig = "[profile with-region]\nregion = eu-west-3\n\n[profile without-region]\noutput = json\n"

	var assumedWith aws.Config
	assumeRole := func(_ context.Context, awscfg aws.Config, _ string) (aws.CredentialsProvider, error) {
		assumedWith = awscfg
		return credentials.NewStaticCredentialsProvider("AKIAASSUMEDROLE", "assumed-secret", "assumed-token"), nil
	}

	tests := []struct {
		name      string
		region    string
		profile   string
		role      string
		envRegion string

		expectedRegion string
		expectedKey    string
		expectedError  string
	}{
		{
			name:   "region argument",
			region: "ca-central-1", expectedRegion: "ca-central-1", expectedKey: "AKIAENVIRONMENT",
		},
		{
			name:      "region of the environment",
			envRegion: "us-east-2", expectedRegion: "us-east-2", expectedKey: "AKIAENVIRONMENT",
		},
		{
			name:    "region of the profile",
			profile: "with-region", expectedRegion: "eu-west-3", expectedKey: "AKIAPROFILE",
		},
		{
			name:   "the region argument takes precedence over the profile",
			region: "ca-central-1", profile: "with-region", expectedRegion: "ca-central-1", expectedKey: "AKIAPROFILE",
		},
		{
			name:   "the region argument completes a profile without region",
			region: "ca-central-1", profile: "without-region", expectedRegion: "ca-central-1", expectedKey: "AKIAPROFILE",
		},
		{
			// the region was lost when a role was assumed: the token was signed without region
			// and refused by the database with "PAM authentication failed"
			name:   "assumed role with the region argument",
			region: "ca-central-1", role: "arn:aws:iam::123456789012:role/connect",
			expectedRegion: "ca-central-1", expectedKey: "AKIAASSUMEDROLE",
		},
		{
			name:    "assumed role with the region of the profile",
			profile: "with-region", role: "arn:aws:iam::123456789012:role/connect",
			expectedRegion: "eu-west-3", expectedKey: "AKIAASSUMEDROLE",
		},
		{
			name:      "assumed role with the region of the environment",
			envRegion: "us-east-2", role: "arn:aws:iam::123456789012:role/connect",
			expectedRegion: "us-east-2", expectedKey: "AKIAASSUMEDROLE",
		},
		{
			name:          "no region",
			expectedError: "could not find the AWS region",
		},
		{
			name:          "no region with a profile",
			profile:       "without-region",
			expectedError: "could not find the AWS region",
		},
		{
			name:          "no region with an assumed role",
			role:          "arn:aws:iam::123456789012:role/connect",
			expectedError: "could not find the AWS region",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testRDSAuthEnvironment(t, profileConfig)
			if test.envRegion != "" {
				t.Setenv("AWS_REGION", test.envRegion)
			}
			assumedWith = aws.Config{}

			token, err := buildRDSAuthToken(
				context.Background(), test.region, test.profile, test.role, "iam_user", "db.example.com", 5432, assumeRole,
			)
			if test.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), test.expectedError) {
					t.Fatalf("expected the error %q, got %v (token %q)", test.expectedError, err, token)
				}
				if assumedWith.Region != "" || assumedWith.Credentials != nil {
					t.Fatal("the role must not be assumed when there is no region")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !strings.HasPrefix(token, "db.example.com:5432?") || !strings.Contains(token, "DBUser=iam_user") {
				t.Fatalf("unexpected token: %q", token)
			}
			key, region := testRDSTokenScope(t, token)
			if region != test.expectedRegion {
				t.Errorf("the token is signed for the region %q, expected %q", region, test.expectedRegion)
			}
			if key != test.expectedKey {
				t.Errorf("the token is signed with the access key %q, expected %q", key, test.expectedKey)
			}
			if test.role != "" {
				// the role is assumed in the same region, with the credentials of the environment or of the profile
				if assumedWith.Region != test.expectedRegion {
					t.Errorf("the role is assumed in the region %q, expected %q", assumedWith.Region, test.expectedRegion)
				}
				baseKey := "AKIAENVIRONMENT"
				if test.profile != "" {
					baseKey = "AKIAPROFILE"
				}
				creds, err := assumedWith.Credentials.Retrieve(context.Background())
				if err != nil || creds.AccessKeyID != baseKey {
					t.Errorf("the role is assumed with the access key %q (%v), expected %q", creds.AccessKeyID, err, baseKey)
				}
				if !strings.Contains(token, "X-Amz-Security-Token=assumed-token") {
					t.Errorf("the token is not signed with the session token of the role: %q", token)
				}
			}
		})
	}
}

func TestBuildRDSAuthToken_AssumeRoleError(t *testing.T) {
	testRDSAuthEnvironment(t, "")
	assumeRole := func(context.Context, aws.Config, string) (aws.CredentialsProvider, error) {
		return nil, errors.New("could not assume AWS role: access denied")
	}

	token, err := buildRDSAuthToken(
		context.Background(), "ca-central-1", "", "arn:aws:iam::123456789012:role/connect", "iam_user", "db.example.com", 5432, assumeRole,
	)
	if err == nil || !strings.Contains(err.Error(), "could not assume AWS role") {
		t.Fatalf("expected the error of the role, got %v (token %q)", err, token)
	}
}

// assumeRDSRole is run against a local server which answers like the AWS STS API: nothing is sent to AWS.
func TestAssumeRDSRole(t *testing.T) {
	var requested url.Values
	respond := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			requested = r.PostForm
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	config := func(server *httptest.Server) aws.Config {
		return aws.Config{
			Region:           "ca-central-1",
			Credentials:      credentials.NewStaticCredentialsProvider("AKIABASE", "base-secret", ""),
			BaseEndpoint:     aws.String(server.URL),
			RetryMaxAttempts: 1,
		}
	}
	const role = "arn:aws:iam::123456789012:role/connect"

	t.Run("credentials of the role", func(t *testing.T) {
		server := respond(http.StatusOK, `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult>
<Credentials><AccessKeyId>AKIAASSUMED</AccessKeyId><SecretAccessKey>assumed-secret</SecretAccessKey>
<SessionToken>assumed-token</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials>
</AssumeRoleResult><ResponseMetadata><RequestId>1</RequestId></ResponseMetadata></AssumeRoleResponse>`)
		defer server.Close()

		provider, err := assumeRDSRole(context.Background(), config(server), role)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		creds, err := provider.Retrieve(context.Background())
		if err != nil || creds.AccessKeyID != "AKIAASSUMED" || creds.SecretAccessKey != "assumed-secret" || creds.SessionToken != "assumed-token" {
			t.Errorf("unexpected credentials %+v (%v)", creds, err)
		}
		if requested.Get("Action") != "AssumeRole" || requested.Get("RoleArn") != role || requested.Get("RoleSessionName") != "TerraformPostgresqlProvider" {
			t.Errorf("unexpected request: %v", requested)
		}
	})

	t.Run("refused", func(t *testing.T) {
		server := respond(http.StatusForbidden, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type>
<Code>AccessDenied</Code><Message>not allowed</Message></Error><RequestId>1</RequestId></ErrorResponse>`)
		defer server.Close()

		provider, err := assumeRDSRole(context.Background(), config(server), role)
		if err == nil || !strings.Contains(err.Error(), "could not assume AWS role") || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("expected the error of the role, got %v (%v)", err, provider)
		}
		if provider != nil {
			t.Errorf("no credentials expected, got %v", provider)
		}
	})
}
