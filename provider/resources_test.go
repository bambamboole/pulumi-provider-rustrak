package provider

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

func TestMissingTokenFailsRequests(t *testing.T) {
	fake := newFakeRustrak(t)
	c, err := rustrak.New(fake.server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), clientKey{}, c)
	_, err = (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: ProjectArgs{Name: "Lock"}})
	if err == nil || !strings.Contains(err.Error(), "missing API token") {
		t.Fatalf("expected ErrMissingToken, got %v", err)
	}
}

func TestProjectLifecycle(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()

	created, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: ProjectArgs{Name: "Lock", Platform: "php-laravel", RateLimitPerMinute: 60}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Output.ProjectID == 0 || created.ID != strconv.Itoa(created.Output.ProjectID) || created.Output.ProjectSlug != "lock" || !strings.Contains(created.Output.Dsn, strings.ReplaceAll(created.Output.SentryKey, "-", "")) {
		t.Fatalf("unexpected output %+v", created)
	}
	if limit := fake.projects[int64(created.Output.ProjectID)].RateLimitPerMinute; limit == nil || *limit != 60 {
		t.Fatalf("the rate limit must be set after creation: %v", limit)
	}

	// An undeclared slug and an auto-assigned platform do not diff.
	declared := ProjectArgs{Name: "Lock", RateLimitPerMinute: 60}
	read, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: declared, State: created.Output})
	if err != nil || read.Inputs != declared {
		t.Fatalf("Read must keep undeclared fields unmanaged: %+v %v", read.Inputs, err)
	}

	imported, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID})
	if err != nil || imported.Inputs.Slug != "lock" || imported.Inputs.Platform != "php-laravel" || imported.State.Dsn == "" {
		t.Fatalf("import must adopt slug and platform: %+v %v", imported, err)
	}

	inputs := ProjectArgs{Name: "Lock IdP", Slug: "lock-idp"}
	diff, err := (Project{}).Diff(ctx, infer.DiffRequest[ProjectArgs, ProjectState]{Inputs: inputs, State: created.Output})
	if err != nil || diff.DetailedDiff["slug"].Kind != p.Update || diff.DetailedDiff["name"].Kind != p.Update {
		t.Fatalf("renaming and a new slug must update in place: %+v %v", diff, err)
	}
	updated, err := (Project{}).Update(ctx, infer.UpdateRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || updated.Output.ProjectSlug != "lock-idp" || updated.Output.Dsn != created.Output.Dsn {
		t.Fatalf("Update: %+v %v", updated, err)
	}
	if fake.projects[int64(created.Output.ProjectID)].RateLimitPerMinute != nil {
		t.Fatal("an unset rate limit must be removed")
	}

	fake.addProject("Taken")
	_, err = (Project{}).Update(ctx, infer.UpdateRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: ProjectArgs{Name: "Lock", Slug: "taken"}, State: updated.Output})
	if !rustrak.IsConflict(err) || !strings.Contains(err.Error(), "slug taken") {
		t.Fatalf("a taken slug must surface Rustrak's message: %v", err)
	}

	if _, err := (Project{}).Delete(ctx, infer.DeleteRequest[ProjectState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: created.ID, Inputs: inputs, State: updated.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("a deleted project must read as missing: %+v %v", gone, err)
	}
	if _, err := (Project{}).Delete(ctx, infer.DeleteRequest[ProjectState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("deleting a missing project must succeed: %v", err)
	}
	if _, err := (Project{}).Read(ctx, infer.ReadRequest[ProjectArgs, ProjectState]{ID: "lock"}); err == nil {
		t.Fatal("a malformed ID must be rejected")
	}
}

func TestProjectCreateWithoutLimitsSkipsTheUpdate(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()
	if _, err := (Project{}).Create(ctx, infer.CreateRequest[ProjectArgs]{Inputs: ProjectArgs{Name: "Escaperoom"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.requestLog(), ","); got != "POST /api/projects" {
		t.Fatalf("a project without rate limits needs one request, got %s", got)
	}
}

func checkFailures[A any](t *testing.T, check func(context.Context, infer.CheckRequest) (infer.CheckResponse[A], error), inputs map[string]property.Value) (A, []string) {
	t.Helper()
	response, err := check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(inputs)})
	if err != nil {
		t.Fatal(err)
	}
	var properties []string
	for _, failure := range response.Failures {
		properties = append(properties, failure.Property)
	}
	return response.Inputs, properties
}

func TestAlertChannelCheck(t *testing.T) {
	_, failures := checkFailures(t, (AlertChannel{}).Check, map[string]property.Value{"name": property.New("None")})
	if len(failures) != 1 || failures[0] != "webhook" {
		t.Fatalf("a channel without a type must fail: %v", failures)
	}
	_, failures = checkFailures(t, (AlertChannel{}).Check, map[string]property.Value{
		"name":  property.New("Both"),
		"slack": property.New(property.NewMap(map[string]property.Value{"webhookUrl": property.New("https://hooks"), "token": property.New("xoxb-1")})),
	})
	if len(failures) != 1 || failures[0] != "slack" {
		t.Fatalf("slack needs exactly one of webhookUrl and token: %v", failures)
	}
}

func TestAlertChannelLifecycle(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()

	inputs := AlertChannelArgs{Name: "Ops", Webhook: &WebhookChannel{Url: "https://hooks.example.com/rustrak", Secret: "s3cret", Headers: map[string]string{"X-Env": "production"}}}
	created, err := (AlertChannel{}).Create(ctx, infer.CreateRequest[AlertChannelArgs]{Inputs: inputs})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Output.ChannelID == 0 || created.Output.ProviderType != "webhook" || !fake.integrations[int64(created.Output.ChannelID)].IsEnabled {
		t.Fatalf("unexpected output %+v", created.Output)
	}
	read, err := (AlertChannel{}).Read(ctx, infer.ReadRequest[AlertChannelArgs, AlertChannelState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil {
		t.Fatal(err)
	}
	if diff := diffArgs(inputs, read.Inputs); len(diff) != 0 {
		t.Fatalf("a read must round-trip the inputs: %+v", diff)
	}

	disabled := inputs
	disabled.Disabled = true
	if _, err := (AlertChannel{}).Update(ctx, infer.UpdateRequest[AlertChannelArgs, AlertChannelState]{ID: created.ID, Inputs: disabled, State: created.Output}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if fake.integrations[int64(created.Output.ChannelID)].IsEnabled {
		t.Fatal("disabled must disable the channel")
	}

	switched := AlertChannelArgs{Name: "Ops", Email: &EmailChannel{}}
	diff, err := (AlertChannel{}).Diff(ctx, infer.DiffRequest[AlertChannelArgs, AlertChannelState]{Inputs: switched, State: created.Output})
	if err != nil || diff.DetailedDiff["webhook"].Kind != p.UpdateReplace || diff.DetailedDiff["email"].Kind != p.UpdateReplace {
		t.Fatalf("a new provider type must replace the channel: %+v %v", diff, err)
	}

	if _, err := (AlertChannel{}).Delete(ctx, infer.DeleteRequest[AlertChannelState]{ID: created.ID, State: created.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (AlertChannel{}).Read(ctx, infer.ReadRequest[AlertChannelArgs, AlertChannelState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("a deleted channel must read as missing: %+v %v", gone, err)
	}
}

func TestAlertChannelKeepsTheRedactedSlackToken(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()

	inputs := AlertChannelArgs{Name: "Slack", Slack: &SlackChannel{Token: "xoxb-real", Channel: "#alerts"}}
	created, err := (AlertChannel{}).Create(ctx, infer.CreateRequest[AlertChannelArgs]{Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	var stored channelCredentials
	_ = json.Unmarshal(fake.integrations[int64(created.Output.ChannelID)].Credentials, &stored)
	if stored.Method != "bot_token" || stored.Token != "xoxb-real" {
		t.Fatalf("the bot token must be sent: %+v", stored)
	}
	read, err := (AlertChannel{}).Read(ctx, infer.ReadRequest[AlertChannelArgs, AlertChannelState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || read.Inputs.Slack == nil || read.Inputs.Slack.Token != "xoxb-real" || read.Inputs.Slack.Channel != "#alerts" {
		t.Fatalf("a redacted token must keep the value from state: %+v %v", read.Inputs.Slack, err)
	}
}

func TestAlertRuleLifecycle(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()
	project := fake.addProject("Lock")
	email, err := (AlertChannel{}).Create(ctx, infer.CreateRequest[AlertChannelArgs]{Inputs: AlertChannelArgs{Name: "Mail", Email: &EmailChannel{}}})
	if err != nil {
		t.Fatal(err)
	}
	webhook, err := (AlertChannel{}).Create(ctx, infer.CreateRequest[AlertChannelArgs]{Inputs: AlertChannelArgs{Name: "Hook", Webhook: &WebhookChannel{Url: "https://hooks.example.com"}}})
	if err != nil {
		t.Fatal(err)
	}

	inputs := AlertRuleArgs{
		ProjectID: int(project.ID), Name: "New errors", AlertType: "new_issue", Disabled: true,
		CooldownMinutes: 10, MinLevel: "error", Environments: []string{"production"}, MinEvents: 3,
		Channels: []AlertRuleChannel{
			{ChannelID: email.Output.ChannelID, Recipients: []string{"ops@example.com"}},
			{ChannelID: webhook.Output.ChannelID, Url: "https://hooks.example.com/other", ExtraHeaders: map[string]string{"X-Rule": "new"}},
		},
	}
	created, err := (AlertRule{}).Create(ctx, infer.CreateRequest[AlertRuleArgs]{Inputs: inputs})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != strconv.Itoa(int(project.ID))+"/"+strconv.Itoa(created.Output.RuleID) {
		t.Fatalf("unexpected ID %q", created.ID)
	}
	if fake.rules[int64(created.Output.RuleID)].IsEnabled {
		t.Fatal("a disabled rule must be disabled after creation")
	}

	read, err := (AlertRule{}).Read(ctx, infer.ReadRequest[AlertRuleArgs, AlertRuleState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil {
		t.Fatal(err)
	}
	if diff := diffArgs(inputs, read.Inputs); len(diff) != 0 {
		t.Fatalf("a read must round-trip the inputs: %+v (%+v)", diff, read.Inputs)
	}

	changed := inputs
	changed.AlertType = "regression"
	changed.MinEvents = 0
	diff, err := (AlertRule{}).Diff(ctx, infer.DiffRequest[AlertRuleArgs, AlertRuleState]{Inputs: changed, State: created.Output})
	if err != nil || diff.DetailedDiff["alertType"].Kind != p.UpdateReplace {
		t.Fatalf("a new alert type must replace the rule: %+v %v", diff, err)
	}

	enabled := inputs
	enabled.Disabled = false
	enabled.Channels = inputs.Channels[:1]
	if _, err := (AlertRule{}).Update(ctx, infer.UpdateRequest[AlertRuleArgs, AlertRuleState]{ID: created.ID, Inputs: enabled, State: created.Output}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	rule := fake.rules[int64(created.Output.RuleID)]
	if !rule.IsEnabled || len(rule.Channels) != 1 {
		t.Fatalf("update must enable the rule and replace the channels: %+v", rule)
	}

	noRecipients := inputs
	noRecipients.Channels = []AlertRuleChannel{{ChannelID: email.Output.ChannelID}}
	if _, err := (AlertRule{}).Update(ctx, infer.UpdateRequest[AlertRuleArgs, AlertRuleState]{ID: created.ID, Inputs: noRecipients, State: created.Output}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("Rustrak's validation must surface: %v", err)
	}

	if _, err := (AlertRule{}).Delete(ctx, infer.DeleteRequest[AlertRuleState]{ID: created.ID, State: created.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (AlertRule{}).Read(ctx, infer.ReadRequest[AlertRuleArgs, AlertRuleState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("a deleted rule must read as missing: %+v %v", gone, err)
	}
}

func TestAlertRuleCheck(t *testing.T) {
	_, failures := checkFailures(t, (AlertRule{}).Check, map[string]property.Value{
		"projectId": property.New(1.0),
		"name":      property.New("Rule"),
		"alertType": property.New("regression"),
		"minLevel":  property.New("loud"),
		"minEvents": property.New(2.0),
		"channels": property.New([]property.Value{
			property.New(property.NewMap(map[string]property.Value{"channelId": property.New(7.0)})),
			property.New(property.NewMap(map[string]property.Value{"channelId": property.New(7.0)})),
		}),
	})
	if strings.Join(failures, ",") != "minLevel,minEvents,channels[1]" {
		t.Fatalf("unexpected failures %v", failures)
	}
}

func TestMonitorLifecycle(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()
	project := fake.addProject("Escaperoom")

	inputs, failures := checkFailures(t, (Monitor{}).Check, map[string]property.Value{
		"projectId": property.New(float64(project.ID)),
		"name":      property.New("Nightly backup"),
		"crontab":   property.New("0 3 * * *"),
		"muted":     property.New(true),
	})
	if len(failures) != 0 || inputs.Timezone != "UTC" || inputs.CheckinMarginMinutes != 1 || inputs.MaxRuntimeMinutes != 30 || inputs.FailureIssueThreshold != 1 || inputs.RecoveryThreshold != 1 {
		t.Fatalf("Check must fill Rustrak's defaults: %+v %v", inputs, failures)
	}
	created, err := (Monitor{}).Create(ctx, infer.CreateRequest[MonitorArgs]{Inputs: inputs})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != strconv.Itoa(int(project.ID))+"/nightly-backup" || created.Output.MonitorID == "" || !fake.monitors[project.ID][0].IsMuted {
		t.Fatalf("unexpected output %+v", created)
	}
	read, err := (Monitor{}).Read(ctx, infer.ReadRequest[MonitorArgs, MonitorState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil {
		t.Fatal(err)
	}
	if diff := diffArgs(inputs, read.Inputs); len(diff) != 0 {
		t.Fatalf("a read must round-trip the inputs: %+v", diff)
	}

	interval := inputs
	interval.Crontab = ""
	interval.Interval = &MonitorInterval{Value: 6, Unit: "hour"}
	if _, err := (Monitor{}).Update(ctx, infer.UpdateRequest[MonitorArgs, MonitorState]{ID: created.ID, Inputs: interval, State: created.Output}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	read, err = (Monitor{}).Read(ctx, infer.ReadRequest[MonitorArgs, MonitorState]{ID: created.ID, Inputs: interval, State: created.Output})
	if err != nil || read.Inputs.Interval == nil || *read.Inputs.Interval != (MonitorInterval{Value: 6, Unit: "hour"}) || read.Inputs.Crontab != "" {
		t.Fatalf("an interval must round-trip: %+v %v", read.Inputs, err)
	}

	if _, err := (Monitor{}).Delete(ctx, infer.DeleteRequest[MonitorState]{ID: created.ID, State: created.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	gone, err := (Monitor{}).Read(ctx, infer.ReadRequest[MonitorArgs, MonitorState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil || gone.ID != "" {
		t.Fatalf("a deleted monitor must read as missing: %+v %v", gone, err)
	}

	_, failures = checkFailures(t, (Monitor{}).Check, map[string]property.Value{
		"projectId": property.New(1.0),
		"name":      property.New("Both"),
		"crontab":   property.New("@daily"),
		"interval":  property.New(property.NewMap(map[string]property.Value{"value": property.New(1.0), "unit": property.New("fortnight")})),
	})
	if strings.Join(failures, ",") != "crontab,interval.unit" {
		t.Fatalf("unexpected failures %v", failures)
	}
}

func TestRetentionLifecycle(t *testing.T) {
	fake := newFakeRustrak(t)
	ctx := fake.ctx()
	project := fake.addProject("Lock")

	inputs, failures := checkFailures(t, (Retention{}).Check, map[string]property.Value{
		"eventsDays": property.New(90.0),
		"logsDays":   property.New(14.0),
		"projectOverrides": property.New([]property.Value{
			property.New(property.NewMap(map[string]property.Value{"projectId": property.New(float64(project.ID)), "eventsDays": property.New(30.0)})),
		}),
	})
	if len(failures) != 0 || inputs.Frequency != "daily" || inputs.Hour != 3 {
		t.Fatalf("Check must fill Rustrak's defaults: %+v %v", inputs, failures)
	}
	created, err := (Retention{}).Create(ctx, infer.CreateRequest[RetentionArgs]{Inputs: inputs})
	if err != nil || created.ID != "retention" {
		t.Fatalf("Create: %+v %v", created, err)
	}
	if days := fake.retention.Defaults.EventsDays; days == nil || *days != 90 || fake.retention.Defaults.TransactionsDays != nil {
		t.Fatalf("the windows must be stored, 0 as forever: %+v", fake.retention.Defaults)
	}
	read, err := (Retention{}).Read(ctx, infer.ReadRequest[RetentionArgs, RetentionState]{ID: created.ID, Inputs: inputs, State: created.Output})
	if err != nil {
		t.Fatal(err)
	}
	if diff := diffArgs(inputs, read.Inputs); len(diff) != 0 {
		t.Fatalf("a read must round-trip the inputs: %+v", diff)
	}

	if _, err := (Retention{}).Delete(ctx, infer.DeleteRequest[RetentionState]{ID: created.ID, State: created.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if fake.retention.Defaults.EventsDays != nil || len(fake.retention.Overrides) != 0 || fake.retention.Hour != 3 {
		t.Fatalf("delete must restore keeping everything forever: %+v", fake.retention)
	}

	_, failures = checkFailures(t, (Retention{}).Check, map[string]property.Value{
		"frequency": property.New("hourly"),
		"hour":      property.New(24.0),
		"projectOverrides": property.New([]property.Value{
			property.New(property.NewMap(map[string]property.Value{"projectId": property.New(1.0)})),
			property.New(property.NewMap(map[string]property.Value{"projectId": property.New(1.0)})),
		}),
	})
	if strings.Join(failures, ",") != "frequency,hour,projectOverrides[1]" {
		t.Fatalf("unexpected failures %v", failures)
	}
}
