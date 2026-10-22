package provider

import (
	"context"
	"fmt"
	"slices"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

var (
	alertTypes  = []string{"new_issue", "regression", "unmute"}
	alertLevels = []string{"debug", "info", "warning", "error", "fatal"}
)

// AlertRuleChannel is one channel a rule delivers to, with the routing that
// applies to this rule only.
type AlertRuleChannel struct {
	ChannelID    int               `pulumi:"channelId"`
	Recipients   []string          `pulumi:"recipients,optional"`
	Channel      string            `pulumi:"channel,optional"`
	Username     string            `pulumi:"username,optional"`
	IconEmoji    string            `pulumi:"iconEmoji,optional"`
	Url          string            `pulumi:"url,optional"`
	ExtraHeaders map[string]string `pulumi:"extraHeaders,optional" provider:"secret"`
}

func (c *AlertRuleChannel) Annotate(a infer.Annotator) {
	a.Describe(&c.ChannelID, "channelId of the AlertChannel to deliver to.")
	a.Describe(&c.Recipients, "Email channels: the addresses to notify. Required for email channels.")
	a.Describe(&c.Channel, "Slack bot token channels: the Slack channel, overriding the channel's default.")
	a.Describe(&c.Username, "Slack bot token channels: the display name of the message.")
	a.Describe(&c.IconEmoji, "Slack bot token channels: the icon of the message.")
	a.Describe(&c.Url, "Webhook channels: a URL overriding the channel's URL.")
	a.Describe(&c.ExtraHeaders, "Webhook channels: headers added to the channel's headers.")
}

// AlertRule manages an alert rule of a project.
type AlertRule struct{}

type AlertRuleArgs struct {
	ProjectID       int                `pulumi:"projectId" provider:"replaceOnChanges"`
	Name            string             `pulumi:"name"`
	AlertType       string             `pulumi:"alertType" provider:"replaceOnChanges"`
	Disabled        bool               `pulumi:"disabled,optional"`
	CooldownMinutes int                `pulumi:"cooldownMinutes,optional"`
	MinLevel        string             `pulumi:"minLevel,optional"`
	Environments    []string           `pulumi:"environments,optional"`
	MinEvents       int                `pulumi:"minEvents,optional"`
	Channels        []AlertRuleChannel `pulumi:"channels"`
}

type AlertRuleState struct {
	AlertRuleArgs
	RuleID int `pulumi:"ruleId"`
}

func (r *AlertRule) Annotate(a infer.Annotator) {
	a.SetToken("index", "AlertRule")
	a.Describe(&r, "An alert rule of a Rustrak project: notify channels when an issue is new, regresses after being resolved, or is unmuted. Conditions narrow the rule to a minimum level, to environments, or to the nth event of a new issue. Changing the project or the alert type replaces the rule. The resource ID is <projectId>/<ruleId>.")
}

func (args *AlertRuleArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.ProjectID, "projectId of the Project the rule belongs to.")
	a.Describe(&args.Name, "Display name of the rule.")
	a.Describe(&args.AlertType, "When the rule fires: new_issue, regression or unmute.")
	a.Describe(&args.Disabled, "Keep the rule without firing it.")
	a.Describe(&args.CooldownMinutes, "Minutes after firing during which the rule stays quiet for the same issue. Defaults to 0.")
	a.Describe(&args.MinLevel, "Quietest level that still fires: debug, info, warning, error or fatal. Unset fires on every level.")
	a.Describe(&args.Environments, "Only events from these environments fire. Unset fires on every environment.")
	a.Describe(&args.MinEvents, "new_issue only: fire on the event that brings the issue to this count instead of on its first event.")
	a.Describe(&args.Channels, "Channels the rule delivers to. Replaced as a whole on update.")
}

func (state *AlertRuleState) Annotate(a infer.Annotator) {
	a.Describe(&state.RuleID, "Numeric ID of the rule.")
}

func (AlertRule) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[AlertRuleArgs], error) {
	args, failures, err := infer.DefaultCheck[AlertRuleArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[AlertRuleArgs]{}, err
	}
	if !slices.Contains(alertTypes, args.AlertType) {
		failures = append(failures, p.CheckFailure{Property: "alertType", Reason: "must be new_issue, regression or unmute"})
	}
	if args.MinLevel != "" && !slices.Contains(alertLevels, args.MinLevel) {
		failures = append(failures, p.CheckFailure{Property: "minLevel", Reason: "must be debug, info, warning, error or fatal"})
	}
	if args.MinEvents < 0 || (args.MinEvents > 0 && args.AlertType != "new_issue") {
		failures = append(failures, p.CheckFailure{Property: "minEvents", Reason: "must be positive and applies to new_issue rules only"})
	}
	if args.CooldownMinutes < 0 {
		failures = append(failures, p.CheckFailure{Property: "cooldownMinutes", Reason: "must not be negative"})
	}
	seen := map[int]bool{}
	for i, channel := range args.Channels {
		if seen[channel.ChannelID] {
			failures = append(failures, p.CheckFailure{Property: fmt.Sprintf("channels[%d]", i), Reason: "each channel once per rule"})
		}
		seen[channel.ChannelID] = true
	}
	return infer.CheckResponse[AlertRuleArgs]{Inputs: args, Failures: failures}, nil
}

func (AlertRule) Create(ctx context.Context, req infer.CreateRequest[AlertRuleArgs]) (infer.CreateResponse[AlertRuleState], error) {
	state := AlertRuleState{AlertRuleArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[AlertRuleState]{Output: state}, nil
	}
	c := client(ctx)
	projectID := int64(req.Inputs.ProjectID)
	rule, err := c.CreateAlertRule(ctx, projectID, rustrak.AlertRuleCreate{
		Name:            req.Inputs.Name,
		AlertType:       req.Inputs.AlertType,
		Conditions:      ruleConditions(req.Inputs),
		CooldownMinutes: int64(req.Inputs.CooldownMinutes),
		Channels:        ruleChannels(req.Inputs.Channels),
	})
	if err != nil {
		return infer.CreateResponse[AlertRuleState]{}, err
	}
	state.RuleID = int(rule.ID)
	id := fmt.Sprintf("%d/%d", projectID, rule.ID)
	// Rules start enabled.
	if req.Inputs.Disabled {
		if _, err := c.UpdateAlertRule(ctx, projectID, rule.ID, ruleUpdate(req.Inputs)); err != nil {
			return infer.CreateResponse[AlertRuleState]{ID: id, Output: state}, err
		}
	}
	return infer.CreateResponse[AlertRuleState]{ID: id, Output: state}, nil
}

func (AlertRule) Read(ctx context.Context, req infer.ReadRequest[AlertRuleArgs, AlertRuleState]) (infer.ReadResponse[AlertRuleArgs, AlertRuleState], error) {
	projectID, rest, err := splitProjectID(req.ID, "<projectId>/<ruleId>")
	if err != nil {
		return infer.ReadResponse[AlertRuleArgs, AlertRuleState]{}, err
	}
	ruleID, err := parseID(rest, "<projectId>/<ruleId>")
	if err != nil {
		return infer.ReadResponse[AlertRuleArgs, AlertRuleState]{}, err
	}
	rule, err := client(ctx).GetAlertRule(ctx, projectID, ruleID)
	if rustrak.IsNotFound(err) {
		return infer.ReadResponse[AlertRuleArgs, AlertRuleState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[AlertRuleArgs, AlertRuleState]{}, err
	}
	inputs := AlertRuleArgs{
		ProjectID:       int(rule.ProjectID),
		Name:            rule.Name,
		AlertType:       rule.AlertType,
		Disabled:        !rule.IsEnabled,
		CooldownMinutes: int(rule.CooldownMinutes),
		MinLevel:        deref(rule.Conditions.MinLevel),
		Environments:    rule.Conditions.Environments,
		MinEvents:       intValue(rule.Conditions.MinEvents),
		Channels:        alertRuleChannels(rule.Channels),
	}
	state := AlertRuleState{AlertRuleArgs: inputs, RuleID: int(rule.ID)}
	return infer.ReadResponse[AlertRuleArgs, AlertRuleState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

func (AlertRule) Diff(_ context.Context, req infer.DiffRequest[AlertRuleArgs, AlertRuleState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.AlertRuleArgs, req.Inputs, "projectId", "alertType")), nil
}

func (AlertRule) Update(ctx context.Context, req infer.UpdateRequest[AlertRuleArgs, AlertRuleState]) (infer.UpdateResponse[AlertRuleState], error) {
	state := AlertRuleState{AlertRuleArgs: req.Inputs, RuleID: req.State.RuleID}
	if req.DryRun {
		return infer.UpdateResponse[AlertRuleState]{Output: state}, nil
	}
	if _, err := client(ctx).UpdateAlertRule(ctx, int64(req.State.ProjectID), int64(req.State.RuleID), ruleUpdate(req.Inputs)); err != nil {
		return infer.UpdateResponse[AlertRuleState]{}, err
	}
	return infer.UpdateResponse[AlertRuleState]{Output: state}, nil
}

func (AlertRule) Delete(ctx context.Context, req infer.DeleteRequest[AlertRuleState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteAlertRule(ctx, int64(req.State.ProjectID), int64(req.State.RuleID)); err != nil && !rustrak.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func ruleUpdate(args AlertRuleArgs) rustrak.AlertRuleUpdate {
	return rustrak.AlertRuleUpdate{
		Name:            args.Name,
		IsEnabled:       !args.Disabled,
		Conditions:      ruleConditions(args),
		CooldownMinutes: int64(args.CooldownMinutes),
		Channels:        ruleChannels(args.Channels),
	}
}

func ruleConditions(args AlertRuleArgs) rustrak.RuleConditions {
	environments := args.Environments
	if environments == nil {
		environments = []string{}
	}
	return rustrak.RuleConditions{
		MinLevel:     optional(args.MinLevel),
		Environments: environments,
		MinEvents:    optionalInt(args.MinEvents),
	}
}

func ruleChannels(channels []AlertRuleChannel) []rustrak.RuleChannel {
	converted := make([]rustrak.RuleChannel, 0, len(channels))
	for _, channel := range channels {
		routing := map[string]any{}
		if len(channel.Recipients) > 0 {
			routing["recipients"] = channel.Recipients
		}
		for key, value := range map[string]string{"channel": channel.Channel, "username": channel.Username, "icon_emoji": channel.IconEmoji, "url": channel.Url} {
			if value != "" {
				routing[key] = value
			}
		}
		if len(channel.ExtraHeaders) > 0 {
			routing["extra_headers"] = channel.ExtraHeaders
		}
		converted = append(converted, rustrak.RuleChannel{IntegrationID: int64(channel.ChannelID), RoutingOverride: routing})
	}
	return converted
}

func alertRuleChannels(channels []rustrak.RuleChannel) []AlertRuleChannel {
	converted := make([]AlertRuleChannel, 0, len(channels))
	for _, channel := range channels {
		routing := channel.RoutingOverride
		converted = append(converted, AlertRuleChannel{
			ChannelID:    int(channel.IntegrationID),
			Recipients:   stringList(routing["recipients"]),
			Channel:      stringValue(routing["channel"]),
			Username:     stringValue(routing["username"]),
			IconEmoji:    stringValue(routing["icon_emoji"]),
			Url:          stringValue(routing["url"]),
			ExtraHeaders: stringMap(routing["extra_headers"]),
		})
	}
	return converted
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringList(value any) []string {
	items, _ := value.([]any)
	var list []string
	for _, item := range items {
		if text, ok := item.(string); ok {
			list = append(list, text)
		}
	}
	return list
}

func stringMap(value any) map[string]string {
	entries, _ := value.(map[string]any)
	if len(entries) == 0 {
		return nil
	}
	converted := make(map[string]string, len(entries))
	for key, entry := range entries {
		converted[key] = fmt.Sprint(entry)
	}
	return converted
}
