package provider

import (
	"context"
	"fmt"
	"slices"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// Rustrak's defaults for a monitor config field left out.
const (
	defaultCheckinMargin     = 1
	defaultMaxRuntime        = 30
	defaultMonitorTimezone   = "UTC"
	defaultMonitorThresholds = 1
)

var intervalUnits = []string{"minute", "hour", "day", "week", "month", "year"}

// MonitorInterval is a schedule of every value units, counted from the
// previous run.
type MonitorInterval struct {
	Value int    `pulumi:"value"`
	Unit  string `pulumi:"unit"`
}

func (i *MonitorInterval) Annotate(a infer.Annotator) {
	a.Describe(&i.Value, "Number of units between runs.")
	a.Describe(&i.Unit, "minute, hour, day, week, month or year.")
}

// Monitor manages a cron monitor of a project.
type Monitor struct{}

type MonitorArgs struct {
	ProjectID             int              `pulumi:"projectId" provider:"replaceOnChanges"`
	Name                  string           `pulumi:"name"`
	Slug                  string           `pulumi:"slug,optional" provider:"replaceOnChanges"`
	Crontab               string           `pulumi:"crontab,optional"`
	Interval              *MonitorInterval `pulumi:"interval,optional"`
	Timezone              string           `pulumi:"timezone,optional"`
	CheckinMarginMinutes  int              `pulumi:"checkinMarginMinutes,optional"`
	MaxRuntimeMinutes     int              `pulumi:"maxRuntimeMinutes,optional"`
	FailureIssueThreshold int              `pulumi:"failureIssueThreshold,optional"`
	RecoveryThreshold     int              `pulumi:"recoveryThreshold,optional"`
	Muted                 bool             `pulumi:"muted,optional"`
}

type MonitorState struct {
	MonitorArgs
	MonitorID   string `pulumi:"monitorId"`
	MonitorSlug string `pulumi:"monitorSlug"`
}

func (r *Monitor) Annotate(a infer.Annotator) {
	a.SetToken("index", "Monitor")
	a.Describe(&r, "A cron monitor of a Rustrak project. Jobs check in with a Sentry SDK (e.g. Laravel's scheduler integration) or the /api/<projectId>/cron/<slug>/ endpoint; a missed or failed check-in opens an issue, which alert rules pick up. Set exactly one of crontab or interval. Changing the project or the slug replaces the monitor. The resource ID is <projectId>/<slug>.")
}

func (args *MonitorArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.ProjectID, "projectId of the Project the monitor belongs to.")
	a.Describe(&args.Name, "Display name of the monitor.")
	a.Describe(&args.Slug, "Slug the jobs check in with. Derived from the name by Rustrak when unset; see monitorSlug for the effective value.")
	a.Describe(&args.Crontab, "Five-field crontab or an alias such as @daily.")
	a.Describe(&args.Interval, "Run every value units instead of on a crontab.")
	a.Describe(&args.Timezone, "IANA time zone of the crontab. Defaults to UTC.")
	a.Describe(&args.CheckinMarginMinutes, "Minutes a check-in may be late before it counts as missed. Defaults to 1.")
	a.Describe(&args.MaxRuntimeMinutes, "Minutes a run may take before it counts as timed out. Defaults to 30.")
	a.Describe(&args.FailureIssueThreshold, "Consecutive failed check-ins that open an issue. Defaults to 1.")
	a.Describe(&args.RecoveryThreshold, "Consecutive successful check-ins that resolve it. Defaults to 1.")
	a.Describe(&args.Muted, "Keep recording check-ins without opening issues.")
	a.SetDefault(&args.Timezone, defaultMonitorTimezone)
	a.SetDefault(&args.CheckinMarginMinutes, defaultCheckinMargin)
	a.SetDefault(&args.MaxRuntimeMinutes, defaultMaxRuntime)
	a.SetDefault(&args.FailureIssueThreshold, defaultMonitorThresholds)
	a.SetDefault(&args.RecoveryThreshold, defaultMonitorThresholds)
}

func (state *MonitorState) Annotate(a infer.Annotator) {
	a.Describe(&state.MonitorID, "UUID of the monitor.")
	a.Describe(&state.MonitorSlug, "Effective slug of the monitor.")
}

func (Monitor) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[MonitorArgs], error) {
	args, failures, err := infer.DefaultCheck[MonitorArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[MonitorArgs]{}, err
	}
	if args.Timezone == "" {
		args.Timezone = defaultMonitorTimezone
	}
	if args.CheckinMarginMinutes <= 0 {
		args.CheckinMarginMinutes = defaultCheckinMargin
	}
	if args.MaxRuntimeMinutes <= 0 {
		args.MaxRuntimeMinutes = defaultMaxRuntime
	}
	if args.FailureIssueThreshold <= 0 {
		args.FailureIssueThreshold = defaultMonitorThresholds
	}
	if args.RecoveryThreshold <= 0 {
		args.RecoveryThreshold = defaultMonitorThresholds
	}
	if (args.Crontab == "") == (args.Interval == nil) {
		failures = append(failures, p.CheckFailure{Property: "crontab", Reason: "set exactly one of crontab or interval"})
	}
	if args.Interval != nil {
		if args.Interval.Value <= 0 {
			failures = append(failures, p.CheckFailure{Property: "interval.value", Reason: "must be positive"})
		}
		if !slices.Contains(intervalUnits, args.Interval.Unit) {
			failures = append(failures, p.CheckFailure{Property: "interval.unit", Reason: "must be minute, hour, day, week, month or year"})
		}
	}
	return infer.CheckResponse[MonitorArgs]{Inputs: args, Failures: failures}, nil
}

func (Monitor) Create(ctx context.Context, req infer.CreateRequest[MonitorArgs]) (infer.CreateResponse[MonitorState], error) {
	state := MonitorState{MonitorArgs: req.Inputs, MonitorSlug: req.Inputs.Slug}
	if req.DryRun {
		return infer.CreateResponse[MonitorState]{Output: state}, nil
	}
	c := client(ctx)
	projectID := int64(req.Inputs.ProjectID)
	monitor, err := c.CreateMonitor(ctx, projectID, rustrak.MonitorCreate{
		Name:   req.Inputs.Name,
		Slug:   optional(req.Inputs.Slug),
		Config: monitorConfig(req.Inputs),
	})
	if err != nil {
		return infer.CreateResponse[MonitorState]{}, err
	}
	state.MonitorID, state.MonitorSlug = monitor.ID, monitor.Slug
	id := fmt.Sprintf("%d/%s", projectID, monitor.Slug)
	// Monitors start unmuted.
	if req.Inputs.Muted {
		if _, err := c.UpdateMonitor(ctx, projectID, monitor.Slug, monitorUpdate(req.Inputs)); err != nil {
			return infer.CreateResponse[MonitorState]{ID: id, Output: state}, err
		}
	}
	return infer.CreateResponse[MonitorState]{ID: id, Output: state}, nil
}

func (Monitor) Read(ctx context.Context, req infer.ReadRequest[MonitorArgs, MonitorState]) (infer.ReadResponse[MonitorArgs, MonitorState], error) {
	projectID, slug, err := splitProjectID(req.ID, "<projectId>/<slug>")
	if err != nil {
		return infer.ReadResponse[MonitorArgs, MonitorState]{}, err
	}
	monitor, err := client(ctx).GetMonitor(ctx, projectID, slug)
	if rustrak.IsNotFound(err) {
		return infer.ReadResponse[MonitorArgs, MonitorState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[MonitorArgs, MonitorState]{}, err
	}
	config := monitor.Config
	inputs := MonitorArgs{
		ProjectID:             int(projectID),
		Name:                  monitor.Name,
		Slug:                  req.Inputs.Slug,
		Crontab:               config.Schedule.Crontab(),
		Timezone:              config.Timezone,
		CheckinMarginMinutes:  int(config.CheckinMargin),
		MaxRuntimeMinutes:     int(config.MaxRuntime),
		FailureIssueThreshold: int(config.FailureIssueThreshold),
		RecoveryThreshold:     int(config.RecoveryThreshold),
		Muted:                 monitor.IsMuted,
	}
	if config.Schedule.Type == "interval" {
		inputs.Interval = &MonitorInterval{Value: int(config.Schedule.Interval()), Unit: config.Schedule.Unit}
	}
	// Keep the slug managed when it was declared, and adopt it on import.
	if inputs.Slug != "" || req.Inputs.Name == "" {
		inputs.Slug = monitor.Slug
	}
	state := MonitorState{MonitorArgs: inputs, MonitorID: monitor.ID, MonitorSlug: monitor.Slug}
	return infer.ReadResponse[MonitorArgs, MonitorState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

func (Monitor) Diff(_ context.Context, req infer.DiffRequest[MonitorArgs, MonitorState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.MonitorArgs, req.Inputs, "projectId", "slug")), nil
}

func (Monitor) Update(ctx context.Context, req infer.UpdateRequest[MonitorArgs, MonitorState]) (infer.UpdateResponse[MonitorState], error) {
	state := MonitorState{MonitorArgs: req.Inputs, MonitorID: req.State.MonitorID, MonitorSlug: req.State.MonitorSlug}
	if req.DryRun {
		return infer.UpdateResponse[MonitorState]{Output: state}, nil
	}
	if _, err := client(ctx).UpdateMonitor(ctx, int64(req.State.ProjectID), req.State.MonitorSlug, monitorUpdate(req.Inputs)); err != nil {
		return infer.UpdateResponse[MonitorState]{}, err
	}
	return infer.UpdateResponse[MonitorState]{Output: state}, nil
}

func (Monitor) Delete(ctx context.Context, req infer.DeleteRequest[MonitorState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteMonitor(ctx, int64(req.State.ProjectID), req.State.MonitorSlug); err != nil && !rustrak.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func monitorUpdate(args MonitorArgs) rustrak.MonitorUpdate {
	return rustrak.MonitorUpdate{Name: args.Name, Config: monitorConfig(args), IsMuted: args.Muted}
}

func monitorConfig(args MonitorArgs) rustrak.MonitorConfig {
	schedule := rustrak.CrontabSchedule(args.Crontab)
	if args.Interval != nil {
		schedule = rustrak.IntervalSchedule(int64(args.Interval.Value), args.Interval.Unit)
	}
	return rustrak.MonitorConfig{
		Schedule:              schedule,
		CheckinMargin:         int64(args.CheckinMarginMinutes),
		MaxRuntime:            int64(args.MaxRuntimeMinutes),
		Timezone:              args.Timezone,
		FailureIssueThreshold: int64(args.FailureIssueThreshold),
		RecoveryThreshold:     int64(args.RecoveryThreshold),
	}
}
