# pulumi-provider-rustrak

A native Pulumi provider for [Rustrak](https://github.com/rustrak/rustrak), the self-hosted, Sentry-compatible error tracker, written in Go with `pulumi-go-provider/infer`. It manages projects with their DSNs, alert channels and alert rules, cron monitors and the retention settings, and issues the first API token of a freshly deployed instance without user interaction. The API shapes follow the OpenAPI document in the Rustrak repository (`apps/server/openapi.json`) and were checked against Rustrak 0.19.1.

## Resources

- `rustrak:index:Bootstrap` (API token of an admin, obtained with email and password; see below)
- `rustrak:index:Project` (ID `<projectId>`; exposes the DSN)
- `rustrak:index:AlertChannel` (webhook, custom webhook, email or Slack; ID `<channelId>`)
- `rustrak:index:AlertRule` (ID `<projectId>/<ruleId>`; see below)
- `rustrak:index:Monitor` (cron monitor; ID `<projectId>/<slug>`)
- `rustrak:index:Retention` (the instance's retention settings; ID `retention`)

## Provider configuration

```bash
pulumi config set rustrak:baseUrl https://errors.example.com
pulumi config set --secret rustrak:apiToken <api-token>
```

Alternatively, use `RUSTRAK_BASE_URL` and `RUSTRAK_API_TOKEN`. The token may be omitted for a provider instance that only creates `Bootstrap` resources; every other request then fails with `rustrak: missing API token`. Alert channels and retention are instance-wide and need an admin's token; the token of a Bootstrap resource is one when the user is the superuser.

## Bootstrap: obtaining an API token without user interaction

A Rustrak container creates its first user, an admin, from `CREATE_SUPERUSER` (`email:password`) while the database has no users. `rustrak:index:Bootstrap` logs in with those credentials through the dashboard's `/auth/login` and issues an API token:

1. It waits until `/health/ready` answers, up to `readyTimeoutSeconds` (default 180), because a deployment triggered through Coolify or Compose starts asynchronously and may still be migrating.
2. It adopts the one existing token with the same `tokenDescription` (default `pulumi`), so a recreated resource does not pile up tokens. Otherwise, and when several tokens share the description, it issues a new one.
3. The token is exposed as `token` (secret) and `tokenId`.

Changing `tokenDescription` rotates the token, changing `email` replaces the resource, and the password is never changed by the resource; keep it equal to the deployed `CREATE_SUPERUSER`. A refresh logs in again and checks that the token still exists. A revoked token, or a host that no longer answers within 15 seconds (for example after a hostname change), marks the resource `repairRequired`; the next update issues a replacement token against the declared host, keeping the current one when the instance still knows it. A rejected login fails the refresh instead of dropping the state. Deleting the resource revokes the token and keeps the user.

Password login keeps working when single sign-on is configured, so the superuser can bootstrap an instance whose people sign in through OIDC.

Use two provider instances: one without a token for the bootstrap, one fed by its output for everything else.

```typescript
import * as rustrak from "@bambamboole/pulumi-rustrak";

const bootstrapProvider = new rustrak.Provider("rustrak-bootstrap", { baseUrl });
const bootstrap = new rustrak.Bootstrap("infrastructure", {
    email: "admin@example.com",
    password: adminPassword,
    tokenDescription: "infrastructure",
}, { provider: bootstrapProvider, protect: true, dependsOn: deployment });

const provider = new rustrak.Provider("rustrak", { baseUrl, apiToken: bootstrap.token });
const api = new rustrak.Project("api", { name: "API", platform: "php-laravel" }, { provider });
export const dsn = api.dsn;
```

## Alerts

Alerts have two parts. An `AlertChannel` is instance-wide and says how to deliver: set exactly one of `webhook` (Rustrak's JSON payload, optionally signed with `secret`), `customWebhook` (a body rendered from `template`), `email` (SMTP settings; leave them unset to use the instance's `SMTP_*` settings) or `slack` (`webhookUrl`, which Mattermost incoming webhooks accept as well, or a bot `token` with a default `channel`). Switching the type replaces the channel; `disabled` pauses it.

An `AlertRule` belongs to a project and says when to fire: `alertType` is `new_issue`, `regression` (a resolved issue comes back) or `unmute`. `minLevel`, `environments` and, for new issues, `minEvents` narrow it; `cooldownMinutes` keeps it quiet for the same issue after firing. Each entry of `channels` names a channel and the routing for this rule: `recipients` for email channels (required there), `channel`, `username` and `iconEmoji` for Slack bot tokens, `url` and `extraHeaders` for webhooks. The channel list is replaced as a whole on update.

```typescript
const mail = new rustrak.AlertChannel("mail", { name: "Mail", email: {} }, { provider });

new rustrak.AlertRule("api-new-errors", {
    projectId: api.projectId,
    name: "New errors",
    alertType: "new_issue",
    minLevel: "error",
    environments: ["production"],
    channels: [{ channelId: mail.channelId, recipients: ["ops@example.com"] }],
}, { provider });
```

## Cron monitors

`rustrak:index:Monitor` declares a cron monitor that jobs check in to, through a Sentry SDK or `/api/<projectId>/cron/<slug>/`. Set exactly one of `crontab` (five fields or an alias such as `@daily`, evaluated in `timezone`, default UTC) or `interval` (`value` and `unit`). `checkinMarginMinutes` (default 1) and `maxRuntimeMinutes` (default 30) bound a run; `failureIssueThreshold` and `recoveryThreshold` (default 1) say how many consecutive results open and resolve the issue, which alert rules then pick up. `muted` keeps recording without opening issues. The slug is derived from the name unless set; changing it replaces the monitor.

## Retention

`rustrak:index:Retention` holds the instance's retention settings: `eventsDays`, `transactionsDays` and `logsDays` (0 keeps that kind forever), `projectOverrides` that replace those windows for single projects, and when the cleanup runs (`frequency` daily or weekly, `hour` in UTC, `weekday` for weekly runs). There is one per instance: creating the resource overwrites the current settings, and deleting it restores Rustrak's default of keeping everything forever.

## Behaviour worth knowing

- **Projects** are identified by their numeric ID, which alert rules, monitors and retention overrides reference as `projectId`. Renaming and a new `slug` update in place; a slug taken by another project fails with Rustrak's message. An undeclared `platform` is Rustrak's to assign from the first event and never diffs. Unset rate limits follow the instance's `MAX_EVENTS_PER_PROJECT_PER_*`. Deleting a project deletes its events.
- **DSNs**: `Project.dsn` is the DSN for Sentry SDKs; `sentryKey` is its key as a UUID, which the DSN carries without dashes.
- **Slack bot tokens** are never returned by Rustrak, so a refresh keeps the token from state and cannot detect a change made outside Pulumi.
- **Imports** work with the IDs above, e.g. `pulumi import rustrak:index:Project api 1` or `pulumi import rustrak:index:AlertRule api-new-errors 1/4`. `Bootstrap` cannot be imported; create it and let it adopt the existing token by description.
- **Errors** carry the HTTP status and Rustrak's error message, so validation messages are visible. The login request never echoes anything but the status.
- **Not managed**: users, invitations and project memberships (people usually arrive through single sign-on), the GitHub integration and the MCP settings.

## Development

```bash
make build        # build the provider binary into bin/
make test         # unit tests against an in-memory Rustrak
make lint         # golangci-lint v2 (see .golangci.yml)
make fmt          # gofmt all Go sources
make schema       # build the provider and dump schema.json
make gen-sdk      # regenerate the TypeScript SDK sources in sdk/nodejs
make build-sdk    # compile the SDK into sdk/nodejs/bin (what gets published)
make install-local  # install the plugin into the local Pulumi plugin cache
```

`go.sum` and the generated SDK sources under `sdk/nodejs` are checked in. CI fails if `go mod tidy` or `make gen-sdk` would produce a different result, so run them after changing resources or dependencies.

`TestIntegration` runs every resource but `Retention` against a real instance and deletes what it created. It is skipped unless `RUSTRAK_INTEGRATION_URL`, `RUSTRAK_INTEGRATION_EMAIL` and `RUSTRAK_INTEGRATION_PASSWORD` name an instance and one of its admins:

```bash
RUSTRAK_INTEGRATION_URL=https://errors.example.com RUSTRAK_INTEGRATION_EMAIL=admin@example.com \
  RUSTRAK_INTEGRATION_PASSWORD=... go test ./provider -run TestIntegration -v
```

### Using the provider locally

1. Install the plugin into the local plugin cache:

   ```bash
   make install-local
   ```

2. Build the SDK and reference it from your Pulumi program:

   ```bash
   make build-sdk
   ```

   ```json
   {
     "dependencies": {
       "@bambamboole/pulumi-rustrak": "file:/absolute/path/to/pulumi-provider-rustrak/sdk/nodejs/bin"
     }
   }
   ```

   The plugin version reported by the binary, `pulumi-plugin.json` and the SDK's `package.json` must match; release-please keeps them in sync on releases.

## Releasing

Merging a release-please PR tags `vX.Y.Z`; the release workflow builds the plugin for Linux, macOS and Windows on amd64/arm64 with goreleaser, attaches `schema.json`, and publishes `@bambamboole/pulumi-rustrak` to npm from `sdk/nodejs/bin`. Pulumi downloads the plugin from the GitHub release on demand.

The repository expects `RELEASE_PLEASE_TOKEN` for release-please. `NPM_TOKEN` is optional; without it the release workflow performs an npm dry-run instead.

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
