package provider

import (
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// New builds the Rustrak provider, wiring configuration and all supported
// resources into the schema.
func New() (p.Provider, error) {
	return infer.NewProviderBuilder().
		WithConfig(infer.Config(&Config{})).
		// Nested types pick up the Go package name as their module; publish
		// them alongside the resources instead.
		WithModuleMap(map[tokens.ModuleName]tokens.ModuleName{"provider": "index"}).
		WithResources(
			infer.Resource(Bootstrap{}),
			infer.Resource(Project{}),
			infer.Resource(AlertChannel{}),
			infer.Resource(AlertRule{}),
			infer.Resource(Monitor{}),
			infer.Resource(Retention{}),
		).
		WithDisplayName("Rustrak").
		WithDescription("Manage resources on a Rustrak instance: projects with their DSNs, alert channels and alert rules, cron monitors, the retention settings, and the first API token of a freshly deployed instance.").
		WithPublisher("bambamboole").
		WithRepository("https://github.com/bambamboole/pulumi-provider-rustrak").
		WithHomepage("https://github.com/rustrak/rustrak").
		WithLicense("Apache-2.0").
		// Let Pulumi download the plugin binary from GitHub Releases on demand.
		// release-please tags releases vX.Y.Z and the archive is named
		// pulumi-resource-rustrak-vX.Y.Z-<os>-<arch>.tar.gz, matching Pulumi's
		// plugin naming. The $%7BVERSION%7D placeholder is interpolated by the
		// Pulumi CLI on download.
		WithPluginDownloadURL("https://github.com/bambamboole/pulumi-provider-rustrak/releases/download/v$%7BVERSION%7D").
		Build()
}
