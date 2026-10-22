package provider

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

const (
	envBaseURL  = "RUSTRAK_BASE_URL"
	envAPIToken = "RUSTRAK_API_TOKEN"
)

// Config holds the provider-level configuration.
type Config struct {
	// BaseURL is the origin of the Rustrak instance.
	BaseURL string `pulumi:"baseUrl,optional"`
	// ApiToken is an API token of a Rustrak admin.
	ApiToken string `pulumi:"apiToken,optional" provider:"secret"`

	client *rustrak.Client
}

func (c *Config) Annotate(a infer.Annotator) {
	a.Describe(&c, "Manage resources on a Rustrak instance through its REST API.")
	a.Describe(&c.BaseURL, "Origin of the Rustrak instance, e.g. https://errors.example.com. Defaults to the RUSTRAK_BASE_URL environment variable.")
	a.Describe(&c.ApiToken, "API token of a Rustrak admin (Settings > API tokens), or the token issued by a Bootstrap resource. Defaults to the RUSTRAK_API_TOKEN environment variable. May be omitted for a provider that only creates Bootstrap resources.")
	a.SetDefault(&c.BaseURL, "", envBaseURL)
	a.SetDefault(&c.ApiToken, "", envAPIToken)
}

// Configure validates the configuration and builds the API client once per
// provider process.
func (c *Config) Configure(_ context.Context) error {
	baseURL := firstNonEmpty(c.BaseURL, os.Getenv(envBaseURL))
	token := firstNonEmpty(c.ApiToken, os.Getenv(envAPIToken))
	if baseURL == "" {
		return errors.New("rustrak: missing base URL; set the provider's baseUrl or the " + envBaseURL + " environment variable")
	}
	// Without a token the provider can still run Bootstrap resources, which
	// authenticate on their own; every other request fails with ErrMissingToken.
	client, err := rustrak.New(baseURL, token)
	if err != nil {
		return err
	}
	c.client = client
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
