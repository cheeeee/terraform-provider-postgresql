package postgresql

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

var testAccProviders map[string]*schema.Provider
var testAccProvider *schema.Provider

func init() {
	testAccProvider = Provider()
	testAccProviders = map[string]*schema.Provider{
		"postgresql": testAccProvider,
	}
}

func TestProvider(t *testing.T) {
	if err := Provider().InternalValidate(); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestProvider_impl(t *testing.T) {
	var _ = Provider()
}

func testAccPreCheck(t *testing.T) {
	var host string
	if host = os.Getenv("PGHOST"); host == "" {
		t.Fatal("PGHOST must be set for acceptance tests")
	}
	if v := os.Getenv("PGUSER"); v == "" {
		t.Fatal("PGUSER must be set for acceptance tests")
	}

	err := testAccProvider.Configure(context.Background(), terraform.NewResourceConfigRaw(nil))
	if err != nil {
		t.Fatal(err)
	}
}

// The values of password_authentication are lowercase, as they are documented.
func TestProviderPasswordAuthenticationValidation(t *testing.T) {
	validate := Provider().Schema["password_authentication"].ValidateFunc
	for value, valid := range map[string]bool{
		"password": true, "scram-sha-256": true,
		"Password": false, "PASSWORD": false, "SCRAM-SHA-256": false, "scram": false, "": false,
	} {
		_, errs := validate(value, "password_authentication")
		if (len(errs) == 0) != valid {
			t.Errorf("%q: valid is %t, errors %v", value, valid, errs)
		}
	}
}
