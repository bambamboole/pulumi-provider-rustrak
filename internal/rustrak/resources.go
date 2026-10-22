package rustrak

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Token is an API token as the list endpoint returns it, without its value.
type Token struct {
	ID          int64   `json:"id"`
	TokenPrefix string  `json:"token_prefix"`
	Description *string `json:"description"`
}

// TokenSecret is an API token with its full value.
type TokenSecret struct {
	ID          int64   `json:"id"`
	Token       string  `json:"token"`
	Description *string `json:"description"`
}

// ListTokens lists the tokens the acting user may see; admins see all.
func (c *Client) ListTokens(ctx context.Context) ([]Token, error) {
	var tokens []Token
	return tokens, c.do(ctx, http.MethodGet, "/api/tokens", nil, &tokens)
}

// GetToken reveals the full value of a token.
func (c *Client) GetToken(ctx context.Context, id int64) (TokenSecret, error) {
	var token TokenSecret
	return token, c.do(ctx, http.MethodGet, fmt.Sprintf("/api/tokens/%d", id), nil, &token)
}

// CreateToken issues a token owned by the acting user.
func (c *Client) CreateToken(ctx context.Context, description string) (TokenSecret, error) {
	var token TokenSecret
	return token, c.do(ctx, http.MethodPost, "/api/tokens", map[string]string{"description": description}, &token)
}

// DeleteToken revokes a token.
func (c *Client) DeleteToken(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/tokens/%d", id), nil, nil)
}

// Project is a Rustrak project with the DSN of its key.
type Project struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Slug               string  `json:"slug"`
	SentryKey          string  `json:"sentry_key"`
	DSN                string  `json:"dsn"`
	Platform           *string `json:"platform"`
	RateLimitPerMinute *int64  `json:"rate_limit_per_minute"`
	RateLimitPerHour   *int64  `json:"rate_limit_per_hour"`
}

// ProjectCreate is the body of a project creation.
type ProjectCreate struct {
	Name     string  `json:"name"`
	Slug     *string `json:"slug,omitempty"`
	Platform *string `json:"platform,omitempty"`
}

// ProjectUpdate is the body of a project update. Rustrak leaves a field out
// of the request unchanged and removes a rate limit sent as null, so both
// limits are always sent.
type ProjectUpdate struct {
	Name               string  `json:"name"`
	Slug               *string `json:"slug,omitempty"`
	Platform           *string `json:"platform,omitempty"`
	RateLimitPerMinute *int64  `json:"rate_limit_per_minute"`
	RateLimitPerHour   *int64  `json:"rate_limit_per_hour"`
}

func (c *Client) CreateProject(ctx context.Context, input ProjectCreate) (Project, error) {
	var project Project
	return project, c.do(ctx, http.MethodPost, "/api/projects", input, &project)
}

func (c *Client) GetProject(ctx context.Context, id int64) (Project, error) {
	var project Project
	return project, c.do(ctx, http.MethodGet, fmt.Sprintf("/api/projects/%d", id), nil, &project)
}

func (c *Client) UpdateProject(ctx context.Context, id int64, input ProjectUpdate) (Project, error) {
	var project Project
	return project, c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/projects/%d", id), input, &project)
}

func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/projects/%d", id), nil, nil)
}

// Integration is an instance-wide alert channel. Rustrak calls them
// integrations in the API and channels in the dashboard.
type Integration struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	ProviderType string          `json:"provider_type"`
	Credentials  json.RawMessage `json:"credentials"`
	IsEnabled    bool            `json:"is_enabled"`
}

// IntegrationCreate is the body of an alert channel creation.
type IntegrationCreate struct {
	Name         string `json:"name"`
	ProviderType string `json:"provider_type"`
	Credentials  any    `json:"credentials"`
	IsEnabled    bool   `json:"is_enabled"`
}

// IntegrationUpdate is the body of an alert channel update; the provider
// type cannot change.
type IntegrationUpdate struct {
	Name        string `json:"name"`
	Credentials any    `json:"credentials"`
	IsEnabled   bool   `json:"is_enabled"`
}

func (c *Client) CreateIntegration(ctx context.Context, input IntegrationCreate) (Integration, error) {
	var integration Integration
	return integration, c.do(ctx, http.MethodPost, "/api/integrations", input, &integration)
}

func (c *Client) GetIntegration(ctx context.Context, id int64) (Integration, error) {
	var integration Integration
	return integration, c.do(ctx, http.MethodGet, fmt.Sprintf("/api/integrations/%d", id), nil, &integration)
}

func (c *Client) UpdateIntegration(ctx context.Context, id int64, input IntegrationUpdate) (Integration, error) {
	var integration Integration
	return integration, c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/integrations/%d", id), input, &integration)
}

func (c *Client) DeleteIntegration(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/integrations/%d", id), nil, nil)
}

// RuleConditions are the optional filters of an alert rule.
type RuleConditions struct {
	MinLevel     *string  `json:"min_level,omitempty"`
	Environments []string `json:"environments"`
	MinEvents    *int64   `json:"min_events,omitempty"`
}

// RuleChannel links an alert channel to a rule, with per-rule routing such
// as email recipients or a Slack channel.
type RuleChannel struct {
	IntegrationID   int64          `json:"integration_id"`
	RoutingOverride map[string]any `json:"routing_override"`
}

// AlertRule is an alert rule of a project.
type AlertRule struct {
	ID              int64          `json:"id"`
	ProjectID       int64          `json:"project_id"`
	Name            string         `json:"name"`
	AlertType       string         `json:"alert_type"`
	IsEnabled       bool           `json:"is_enabled"`
	Conditions      RuleConditions `json:"conditions"`
	CooldownMinutes int64          `json:"cooldown_minutes"`
	Channels        []RuleChannel  `json:"channels"`
}

// AlertRuleCreate is the body of a rule creation; rules start enabled.
type AlertRuleCreate struct {
	Name            string         `json:"name"`
	AlertType       string         `json:"alert_type"`
	Conditions      RuleConditions `json:"conditions"`
	CooldownMinutes int64          `json:"cooldown_minutes"`
	Channels        []RuleChannel  `json:"channels"`
}

// AlertRuleUpdate is the body of a rule update; the alert type cannot change.
type AlertRuleUpdate struct {
	Name            string         `json:"name"`
	IsEnabled       bool           `json:"is_enabled"`
	Conditions      RuleConditions `json:"conditions"`
	CooldownMinutes int64          `json:"cooldown_minutes"`
	Channels        []RuleChannel  `json:"channels"`
}

func alertRulePath(projectID int64) string {
	return fmt.Sprintf("/api/projects/%d/alert-rules", projectID)
}

func (c *Client) CreateAlertRule(ctx context.Context, projectID int64, input AlertRuleCreate) (AlertRule, error) {
	var rule AlertRule
	return rule, c.do(ctx, http.MethodPost, alertRulePath(projectID), input, &rule)
}

func (c *Client) GetAlertRule(ctx context.Context, projectID, id int64) (AlertRule, error) {
	var rule AlertRule
	return rule, c.do(ctx, http.MethodGet, fmt.Sprintf("%s/%d", alertRulePath(projectID), id), nil, &rule)
}

func (c *Client) UpdateAlertRule(ctx context.Context, projectID, id int64, input AlertRuleUpdate) (AlertRule, error) {
	var rule AlertRule
	return rule, c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", alertRulePath(projectID), id), input, &rule)
}

func (c *Client) DeleteAlertRule(ctx context.Context, projectID, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", alertRulePath(projectID), id), nil, nil)
}

// Schedule is a crontab (value is the expression) or an interval (value is
// the count of units).
type Schedule struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
	Unit  string          `json:"unit,omitempty"`
}

// CrontabSchedule builds a crontab schedule.
func CrontabSchedule(expression string) Schedule {
	value, _ := json.Marshal(expression)
	return Schedule{Type: "crontab", Value: value}
}

// IntervalSchedule builds an interval schedule.
func IntervalSchedule(value int64, unit string) Schedule {
	encoded, _ := json.Marshal(value)
	return Schedule{Type: "interval", Value: encoded, Unit: unit}
}

// Crontab returns the expression of a crontab schedule.
func (s Schedule) Crontab() string {
	var expression string
	if s.Type == "crontab" && json.Unmarshal(s.Value, &expression) == nil {
		return expression
	}
	return ""
}

// Interval returns the count of an interval schedule.
func (s Schedule) Interval() int64 {
	var value int64
	if s.Type == "interval" && json.Unmarshal(s.Value, &value) == nil {
		return value
	}
	return 0
}

// MonitorConfig is the full configuration of a cron monitor.
type MonitorConfig struct {
	Schedule              Schedule `json:"schedule"`
	CheckinMargin         int64    `json:"checkin_margin"`
	MaxRuntime            int64    `json:"max_runtime"`
	Timezone              string   `json:"timezone"`
	FailureIssueThreshold int64    `json:"failure_issue_threshold"`
	RecoveryThreshold     int64    `json:"recovery_threshold"`
}

// Monitor is a cron monitor of a project.
type Monitor struct {
	ID      string        `json:"id"`
	Slug    string        `json:"slug"`
	Name    string        `json:"name"`
	Config  MonitorConfig `json:"config"`
	IsMuted bool          `json:"is_muted"`
}

// MonitorCreate is the body of a monitor creation; monitors start unmuted.
type MonitorCreate struct {
	Name   string        `json:"name"`
	Slug   *string       `json:"slug,omitempty"`
	Config MonitorConfig `json:"config"`
}

// MonitorUpdate is the body of a monitor update; the slug cannot change.
type MonitorUpdate struct {
	Name    string        `json:"name"`
	Config  MonitorConfig `json:"config"`
	IsMuted bool          `json:"is_muted"`
}

func monitorPath(projectID int64) string {
	return fmt.Sprintf("/api/projects/%d/monitors", projectID)
}

func (c *Client) CreateMonitor(ctx context.Context, projectID int64, input MonitorCreate) (Monitor, error) {
	var monitor Monitor
	return monitor, c.do(ctx, http.MethodPost, monitorPath(projectID), input, &monitor)
}

func (c *Client) GetMonitor(ctx context.Context, projectID int64, slug string) (Monitor, error) {
	var monitor Monitor
	return monitor, c.do(ctx, http.MethodGet, monitorPath(projectID)+"/"+url.PathEscape(slug), nil, &monitor)
}

func (c *Client) UpdateMonitor(ctx context.Context, projectID int64, slug string, input MonitorUpdate) (Monitor, error) {
	var monitor Monitor
	return monitor, c.do(ctx, http.MethodPut, monitorPath(projectID)+"/"+url.PathEscape(slug), input, &monitor)
}

func (c *Client) DeleteMonitor(ctx context.Context, projectID int64, slug string) error {
	return c.do(ctx, http.MethodDelete, monitorPath(projectID)+"/"+url.PathEscape(slug), nil, nil)
}

// RetentionWindows holds how many days each kind of data is kept; nil keeps
// it forever.
type RetentionWindows struct {
	EventsDays       *int64 `json:"events_days"`
	TransactionsDays *int64 `json:"transactions_days"`
	LogsDays         *int64 `json:"logs_days"`
}

// ProjectRetention overrides the windows for one project.
type ProjectRetention struct {
	ProjectID int64 `json:"project_id"`
	RetentionWindows
}

// RetentionSettings are the instance-wide retention settings.
type RetentionSettings struct {
	Defaults  RetentionWindows   `json:"defaults"`
	Overrides []ProjectRetention `json:"overrides"`
	Frequency string             `json:"frequency"`
	Hour      int64              `json:"hour"`
	Weekday   int64              `json:"weekday"`
}

func (c *Client) GetRetention(ctx context.Context) (RetentionSettings, error) {
	var response struct {
		Settings RetentionSettings `json:"settings"`
	}
	return response.Settings, c.do(ctx, http.MethodGet, "/api/storage/retention", nil, &response)
}

func (c *Client) UpdateRetention(ctx context.Context, settings RetentionSettings) (RetentionSettings, error) {
	var response struct {
		Settings RetentionSettings `json:"settings"`
	}
	return response.Settings, c.do(ctx, http.MethodPut, "/api/storage/retention", settings, &response)
}
