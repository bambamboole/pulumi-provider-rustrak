package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// redactedSlackToken is what Rustrak returns instead of a Slack bot token.
const redactedSlackToken = "xoxb-****"

// WebhookChannel posts Rustrak's JSON payload to a URL.
type WebhookChannel struct {
	Url     string            `pulumi:"url"`
	Secret  string            `pulumi:"secret,optional" provider:"secret"`
	Headers map[string]string `pulumi:"headers,optional" provider:"secret"`
}

func (w *WebhookChannel) Annotate(a infer.Annotator) {
	a.Describe(&w.Url, "URL the alert is posted to. A rule can override it per channel.")
	a.Describe(&w.Secret, "Shared secret Rustrak signs the payload with.")
	a.Describe(&w.Headers, "Extra HTTP headers sent with every request.")
}

// CustomWebhookChannel posts a payload rendered from a template.
type CustomWebhookChannel struct {
	Url      string            `pulumi:"url"`
	Template string            `pulumi:"template"`
	Secret   string            `pulumi:"secret,optional" provider:"secret"`
	Headers  map[string]string `pulumi:"headers,optional" provider:"secret"`
}

func (w *CustomWebhookChannel) Annotate(a infer.Annotator) {
	a.Describe(&w.Url, "URL the alert is posted to. A rule can override it per channel.")
	a.Describe(&w.Template, "Template of the request body; the dashboard previews it.")
	a.Describe(&w.Secret, "Shared secret Rustrak signs the payload with.")
	a.Describe(&w.Headers, "Extra HTTP headers sent with every request.")
}

// EmailChannel sends alerts by mail. The recipients are set per rule.
type EmailChannel struct {
	SmtpHost     string `pulumi:"smtpHost,optional"`
	SmtpPort     int    `pulumi:"smtpPort,optional"`
	SmtpUsername string `pulumi:"smtpUsername,optional"`
	SmtpPassword string `pulumi:"smtpPassword,optional" provider:"secret"`
	FromAddress  string `pulumi:"fromAddress,optional"`
}

func (e *EmailChannel) Annotate(a infer.Annotator) {
	a.Describe(&e.SmtpHost, "SMTP host. Unset uses the instance's SMTP_* settings, which then must exist.")
	a.Describe(&e.SmtpPort, "SMTP port.")
	a.Describe(&e.SmtpUsername, "SMTP username.")
	a.Describe(&e.SmtpPassword, "SMTP password.")
	a.Describe(&e.FromAddress, "Sender address. Unset uses the instance's SMTP_FROM.")
}

// SlackChannel posts to Slack, either through an incoming webhook or with a
// bot token. Mattermost incoming webhooks accept the webhook method.
type SlackChannel struct {
	WebhookUrl string `pulumi:"webhookUrl,optional" provider:"secret"`
	Token      string `pulumi:"token,optional" provider:"secret"`
	Channel    string `pulumi:"channel,optional"`
	Username   string `pulumi:"username,optional"`
	IconEmoji  string `pulumi:"iconEmoji,optional"`
}

func (s *SlackChannel) Annotate(a infer.Annotator) {
	a.Describe(&s.WebhookUrl, "Incoming webhook URL. Set either webhookUrl or token.")
	a.Describe(&s.Token, "Bot token (xoxb-...). Set either webhookUrl or token. Rustrak never returns it, so a refresh cannot detect a token changed outside Pulumi.")
	a.Describe(&s.Channel, "Default channel of a bot token; a rule can override it. A bot token needs a channel here or in every rule.")
	a.Describe(&s.Username, "Display name of a bot token's messages.")
	a.Describe(&s.IconEmoji, "Icon of a bot token's messages, e.g. :rotating_light:.")
}

// AlertChannel manages an instance-wide alert channel. Rustrak calls them
// integrations in its API.
type AlertChannel struct{}

type AlertChannelArgs struct {
	Name          string                `pulumi:"name"`
	Disabled      bool                  `pulumi:"disabled,optional"`
	Webhook       *WebhookChannel       `pulumi:"webhook,optional"`
	CustomWebhook *CustomWebhookChannel `pulumi:"customWebhook,optional"`
	Email         *EmailChannel         `pulumi:"email,optional"`
	Slack         *SlackChannel         `pulumi:"slack,optional"`
}

type AlertChannelState struct {
	AlertChannelArgs
	ChannelID    int    `pulumi:"channelId"`
	ProviderType string `pulumi:"providerType"`
}

func (r *AlertChannel) Annotate(a infer.Annotator) {
	a.SetToken("index", "AlertChannel")
	a.Describe(&r, "An instance-wide alert channel that alert rules deliver to: a webhook, a templated custom webhook, email or Slack. Set exactly one of webhook, customWebhook, email or slack; switching between them replaces the channel. Managing channels requires an admin token. The resource ID is the numeric channel ID.")
}

func (args *AlertChannelArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.Name, "Display name of the channel.")
	a.Describe(&args.Disabled, "Stop delivering to the channel without removing it from rules.")
	a.Describe(&args.Webhook, "Post Rustrak's JSON payload to a URL.")
	a.Describe(&args.CustomWebhook, "Post a payload rendered from a template to a URL.")
	a.Describe(&args.Email, "Send alerts by mail; the recipients are set on each rule.")
	a.Describe(&args.Slack, "Post to Slack through an incoming webhook or with a bot token.")
}

func (state *AlertChannelState) Annotate(a infer.Annotator) {
	a.Describe(&state.ChannelID, "Numeric ID of the channel, used by alert rules.")
	a.Describe(&state.ProviderType, "Rustrak's provider type: webhook, custom_webhook, email or slack.")
}

func (AlertChannel) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[AlertChannelArgs], error) {
	args, failures, err := infer.DefaultCheck[AlertChannelArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[AlertChannelArgs]{}, err
	}
	set := 0
	for _, present := range []bool{args.Webhook != nil, args.CustomWebhook != nil, args.Email != nil, args.Slack != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		failures = append(failures, p.CheckFailure{Property: "webhook", Reason: "set exactly one of webhook, customWebhook, email or slack"})
	}
	if args.Slack != nil && (args.Slack.WebhookUrl == "") == (args.Slack.Token == "") {
		failures = append(failures, p.CheckFailure{Property: "slack", Reason: "set either webhookUrl or token"})
	}
	return infer.CheckResponse[AlertChannelArgs]{Inputs: args, Failures: failures}, nil
}

func (AlertChannel) Create(ctx context.Context, req infer.CreateRequest[AlertChannelArgs]) (infer.CreateResponse[AlertChannelState], error) {
	state := AlertChannelState{AlertChannelArgs: req.Inputs, ProviderType: providerType(req.Inputs)}
	if req.DryRun {
		return infer.CreateResponse[AlertChannelState]{Output: state}, nil
	}
	integration, err := client(ctx).CreateIntegration(ctx, rustrak.IntegrationCreate{
		Name:         req.Inputs.Name,
		ProviderType: state.ProviderType,
		Credentials:  credentials(req.Inputs),
		IsEnabled:    !req.Inputs.Disabled,
	})
	if err != nil {
		return infer.CreateResponse[AlertChannelState]{}, err
	}
	state.ChannelID = int(integration.ID)
	return infer.CreateResponse[AlertChannelState]{ID: strconv.FormatInt(integration.ID, 10), Output: state}, nil
}

func (AlertChannel) Read(ctx context.Context, req infer.ReadRequest[AlertChannelArgs, AlertChannelState]) (infer.ReadResponse[AlertChannelArgs, AlertChannelState], error) {
	id, err := parseID(req.ID, "<channelId>")
	if err != nil {
		return infer.ReadResponse[AlertChannelArgs, AlertChannelState]{}, err
	}
	integration, err := client(ctx).GetIntegration(ctx, id)
	if rustrak.IsNotFound(err) {
		return infer.ReadResponse[AlertChannelArgs, AlertChannelState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[AlertChannelArgs, AlertChannelState]{}, err
	}
	inputs, err := channelArgs(integration, req.State.AlertChannelArgs)
	if err != nil {
		return infer.ReadResponse[AlertChannelArgs, AlertChannelState]{}, err
	}
	state := AlertChannelState{AlertChannelArgs: inputs, ChannelID: int(integration.ID), ProviderType: integration.ProviderType}
	return infer.ReadResponse[AlertChannelArgs, AlertChannelState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

// Diff replaces the channel when its provider type changes, which Rustrak
// cannot update.
func (AlertChannel) Diff(_ context.Context, req infer.DiffRequest[AlertChannelArgs, AlertChannelState]) (infer.DiffResponse, error) {
	diff := diffArgs(req.State.AlertChannelArgs, req.Inputs)
	if providerType(req.Inputs) != req.State.ProviderType {
		for name, change := range diff {
			if name != "name" && name != "disabled" {
				change.Kind = p.UpdateReplace
				diff[name] = change
			}
		}
	}
	return diffResponse(diff), nil
}

func (AlertChannel) Update(ctx context.Context, req infer.UpdateRequest[AlertChannelArgs, AlertChannelState]) (infer.UpdateResponse[AlertChannelState], error) {
	state := AlertChannelState{AlertChannelArgs: req.Inputs, ChannelID: req.State.ChannelID, ProviderType: req.State.ProviderType}
	if req.DryRun {
		return infer.UpdateResponse[AlertChannelState]{Output: state}, nil
	}
	_, err := client(ctx).UpdateIntegration(ctx, int64(req.State.ChannelID), rustrak.IntegrationUpdate{
		Name:        req.Inputs.Name,
		Credentials: credentials(req.Inputs),
		IsEnabled:   !req.Inputs.Disabled,
	})
	if err != nil {
		return infer.UpdateResponse[AlertChannelState]{}, err
	}
	return infer.UpdateResponse[AlertChannelState]{Output: state}, nil
}

func (AlertChannel) Delete(ctx context.Context, req infer.DeleteRequest[AlertChannelState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteIntegration(ctx, int64(req.State.ChannelID)); err != nil && !rustrak.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func providerType(args AlertChannelArgs) string {
	switch {
	case args.Webhook != nil:
		return "webhook"
	case args.CustomWebhook != nil:
		return "custom_webhook"
	case args.Email != nil:
		return "email"
	case args.Slack != nil:
		return "slack"
	}
	return ""
}

// channelCredentials is the union of the credential fields of every provider
// type, as Rustrak stores them.
type channelCredentials struct {
	URL          string            `json:"url,omitempty"`
	Secret       string            `json:"secret,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Template     string            `json:"template,omitempty"`
	SMTPHost     string            `json:"smtp_host,omitempty"`
	SMTPPort     int               `json:"smtp_port,omitempty"`
	SMTPUsername string            `json:"smtp_username,omitempty"`
	SMTPPassword string            `json:"smtp_password,omitempty"`
	FromAddress  string            `json:"from_address,omitempty"`
	Method       string            `json:"method,omitempty"`
	WebhookURL   string            `json:"webhook_url,omitempty"`
	Token        string            `json:"token,omitempty"`
	Channel      string            `json:"channel,omitempty"`
	Username     string            `json:"username,omitempty"`
	IconEmoji    string            `json:"icon_emoji,omitempty"`
}

func credentials(args AlertChannelArgs) channelCredentials {
	switch {
	case args.Webhook != nil:
		return channelCredentials{URL: args.Webhook.Url, Secret: args.Webhook.Secret, Headers: args.Webhook.Headers}
	case args.CustomWebhook != nil:
		return channelCredentials{URL: args.CustomWebhook.Url, Template: args.CustomWebhook.Template, Secret: args.CustomWebhook.Secret, Headers: args.CustomWebhook.Headers}
	case args.Email != nil:
		return channelCredentials{SMTPHost: args.Email.SmtpHost, SMTPPort: args.Email.SmtpPort, SMTPUsername: args.Email.SmtpUsername, SMTPPassword: args.Email.SmtpPassword, FromAddress: args.Email.FromAddress}
	case args.Slack != nil && args.Slack.WebhookUrl != "":
		return channelCredentials{Method: "webhook", WebhookURL: args.Slack.WebhookUrl}
	case args.Slack != nil:
		return channelCredentials{Method: "bot_token", Token: args.Slack.Token, Channel: args.Slack.Channel, Username: args.Slack.Username, IconEmoji: args.Slack.IconEmoji}
	}
	return channelCredentials{}
}

// channelArgs maps a channel back to resource inputs. A redacted Slack bot
// token keeps the value from state.
func channelArgs(integration rustrak.Integration, previous AlertChannelArgs) (AlertChannelArgs, error) {
	var creds channelCredentials
	if len(integration.Credentials) > 0 {
		if err := json.Unmarshal(integration.Credentials, &creds); err != nil {
			return AlertChannelArgs{}, fmt.Errorf("rustrak: decoding the credentials of channel %d: %w", integration.ID, err)
		}
	}
	args := AlertChannelArgs{Name: integration.Name, Disabled: !integration.IsEnabled}
	switch integration.ProviderType {
	case "webhook":
		args.Webhook = &WebhookChannel{Url: creds.URL, Secret: creds.Secret, Headers: creds.Headers}
	case "custom_webhook":
		args.CustomWebhook = &CustomWebhookChannel{Url: creds.URL, Template: creds.Template, Secret: creds.Secret, Headers: creds.Headers}
	case "email":
		args.Email = &EmailChannel{SmtpHost: creds.SMTPHost, SmtpPort: creds.SMTPPort, SmtpUsername: creds.SMTPUsername, SmtpPassword: creds.SMTPPassword, FromAddress: creds.FromAddress}
	case "slack":
		token := creds.Token
		if token == redactedSlackToken && previous.Slack != nil {
			token = previous.Slack.Token
		}
		args.Slack = &SlackChannel{WebhookUrl: creds.WebhookURL, Token: token, Channel: creds.Channel, Username: creds.Username, IconEmoji: creds.IconEmoji}
	default:
		return AlertChannelArgs{}, fmt.Errorf("rustrak: channel %d has the unsupported provider type %q", integration.ID, integration.ProviderType)
	}
	return args, nil
}
