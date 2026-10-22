package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// TestIntegration runs every resource but Retention against a real Rustrak
// instance and removes what it created. It is skipped unless
// RUSTRAK_INTEGRATION_URL, RUSTRAK_INTEGRATION_EMAIL and
// RUSTRAK_INTEGRATION_PASSWORD name an instance and one of its admins.
// Retention is only read, since writing it would change the instance.
func TestIntegration(t *testing.T) {
	baseURL, email, password := os.Getenv("RUSTRAK_INTEGRATION_URL"), os.Getenv("RUSTRAK_INTEGRATION_EMAIL"), os.Getenv("RUSTRAK_INTEGRATION_PASSWORD")
	if baseURL == "" || email == "" || password == "" {
		t.Skip("set RUSTRAK_INTEGRATION_URL, RUSTRAK_INTEGRATION_EMAIL and RUSTRAK_INTEGRATION_PASSWORD to run against a real instance")
	}
	suffix := fmt.Sprint(time.Now().Unix())
	bootstrapInputs := BootstrapArgs{BaseURL: baseURL, Email: email, Password: password, TokenDescription: "provider-integration-" + suffix, ReadyTimeoutSeconds: 30}
	bootstrap, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapInputs})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() {
		if _, err := (Bootstrap{}).Delete(context.Background(), infer.DeleteRequest[BootstrapState]{ID: bootstrap.ID, State: bootstrap.Output}); err != nil {
			t.Errorf("revoking the token: %v", err)
		}
	})
	c, err := rustrak.New(baseURL, bootstrap.Output.Token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), clientKey{}, c)

	project, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: ProjectArgs{Name: "Provider integration " + suffix, Platform: "php-laravel", RateLimitPerMinute: 30}})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	t.Cleanup(func() {
		if _, err := (Project{}).Delete(ctx, infer.DeleteRequest[ProjectState]{ID: project.ID, State: project.Output}); err != nil {
			t.Errorf("deleting the project: %v", err)
		}
	})
	if !strings.Contains(project.Output.Dsn, strings.ReplaceAll(project.Output.SentryKey, "-", "")) {
		t.Fatalf("the DSN must carry the key: %+v", project.Output)
	}
	roundTrip(t, "project", project.Output.ProjectArgs, func() (ProjectArgs, error) {
		read, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: project.ID, Inputs: project.Output.ProjectArgs, State: project.Output})
		return read.Inputs, err
	})

	channels := map[string]AlertChannelArgs{
		"webhook": {Name: "Integration webhook " + suffix, Disabled: true, Webhook: &WebhookChannel{Url: "https://example.com/rustrak", Secret: "secret", Headers: map[string]string{"X-Test": "1"}}},
		"email":   {Name: "Integration email " + suffix, Disabled: true, Email: &EmailChannel{}},
		"slack":   {Name: "Integration slack " + suffix, Disabled: true, Slack: &SlackChannel{Token: "xoxb-integration", Channel: "#alerts"}},
	}
	created := map[string]infer.CreateResponse[AlertChannelState]{}
	for kind, inputs := range channels {
		channel, err := (AlertChannel{}).Create(ctx, infer.CreateRequest[AlertChannelArgs]{Inputs: inputs})
		if err != nil {
			t.Fatalf("AlertChannel %s: %v", kind, err)
		}
		created[kind] = channel
		t.Cleanup(func() {
			if _, err := (AlertChannel{}).Delete(ctx, infer.DeleteRequest[AlertChannelState]{ID: channel.ID, State: channel.Output}); err != nil {
				t.Errorf("deleting the %s channel: %v", kind, err)
			}
		})
		roundTrip(t, kind+" channel", inputs, func() (AlertChannelArgs, error) {
			read, err := (AlertChannel{}).Read(ctx, infer.ReadRequest[AlertChannelArgs, AlertChannelState]{ID: channel.ID, Inputs: inputs, State: channel.Output})
			return read.Inputs, err
		})
	}

	ruleInputs := AlertRuleArgs{
		ProjectID: project.Output.ProjectID, Name: "Integration rule", AlertType: "new_issue", Disabled: true,
		CooldownMinutes: 5, MinLevel: "error", Environments: []string{"production"}, MinEvents: 2,
		Channels: []AlertRuleChannel{
			{ChannelID: created["email"].Output.ChannelID, Recipients: []string{"ops@example.com"}},
			{ChannelID: created["webhook"].Output.ChannelID, Url: "https://example.com/other", ExtraHeaders: map[string]string{"X-Rule": "1"}},
			{ChannelID: created["slack"].Output.ChannelID, Channel: "#errors", Username: "Rustrak", IconEmoji: ":fire:"},
		},
	}
	rule, err := (AlertRule{}).Create(ctx, infer.CreateRequest[AlertRuleArgs]{Inputs: ruleInputs})
	if err != nil {
		t.Fatalf("AlertRule: %v", err)
	}
	t.Cleanup(func() {
		if _, err := (AlertRule{}).Delete(ctx, infer.DeleteRequest[AlertRuleState]{ID: rule.ID, State: rule.Output}); err != nil {
			t.Errorf("deleting the rule: %v", err)
		}
	})
	roundTrip(t, "rule", ruleInputs, func() (AlertRuleArgs, error) {
		read, err := (AlertRule{}).Read(ctx, infer.ReadRequest[AlertRuleArgs, AlertRuleState]{ID: rule.ID, Inputs: ruleInputs, State: rule.Output})
		return read.Inputs, err
	})

	monitorInputs := MonitorArgs{
		ProjectID: project.Output.ProjectID, Name: "Integration monitor", Interval: &MonitorInterval{Value: 6, Unit: "hour"},
		Timezone: "Europe/Berlin", CheckinMarginMinutes: 5, MaxRuntimeMinutes: 60, FailureIssueThreshold: 2, RecoveryThreshold: 1, Muted: true,
	}
	monitor, err := (Monitor{}).Create(ctx, infer.CreateRequest[MonitorArgs]{Inputs: monitorInputs})
	if err != nil {
		t.Fatalf("Monitor: %v", err)
	}
	t.Cleanup(func() {
		if _, err := (Monitor{}).Delete(ctx, infer.DeleteRequest[MonitorState]{ID: monitor.ID, State: monitor.Output}); err != nil {
			t.Errorf("deleting the monitor: %v", err)
		}
	})
	roundTrip(t, "monitor", monitorInputs, func() (MonitorArgs, error) {
		read, err := (Monitor{}).Read(ctx, infer.ReadRequest[MonitorArgs, MonitorState]{ID: monitor.ID, Inputs: monitorInputs, State: monitor.Output})
		return read.Inputs, err
	})

	if _, err := (Retention{}).Read(ctx, infer.ReadRequest[RetentionArgs, RetentionState]{ID: retentionID}); err != nil {
		t.Fatalf("reading the retention settings: %v", err)
	}
}

// roundTrip fails the test when reading a resource back does not reproduce
// its inputs, which would show up as a permanent diff.
func roundTrip[A any](t *testing.T, name string, inputs A, read func() (A, error)) {
	t.Helper()
	got, err := read()
	if err != nil {
		t.Fatalf("reading the %s: %v", name, err)
	}
	if diff := diffArgs(inputs, got); len(diff) != 0 {
		t.Errorf("the %s does not round-trip: %v\nwant %+v\ngot  %+v", name, diff, inputs, got)
	}
}
