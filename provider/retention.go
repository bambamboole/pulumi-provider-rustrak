package provider

import (
	"context"
	"fmt"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// retentionID is the resource ID of the instance's only retention settings.
const retentionID = "retention"

// Rustrak's retention defaults: everything kept forever, cleaned up daily at
// 03:00 UTC once a window is set.
const (
	defaultCleanupFrequency = "daily"
	defaultCleanupHour      = 3
)

// RetentionOverride gives one project its own windows. An override replaces
// the defaults whole: a window left at 0 keeps that kind forever.
type RetentionOverride struct {
	ProjectID        int `pulumi:"projectId"`
	EventsDays       int `pulumi:"eventsDays,optional"`
	TransactionsDays int `pulumi:"transactionsDays,optional"`
	LogsDays         int `pulumi:"logsDays,optional"`
}

func (o *RetentionOverride) Annotate(a infer.Annotator) {
	a.Describe(&o.ProjectID, "projectId of the Project the override applies to.")
	a.Describe(&o.EventsDays, "Days error events are kept. 0 keeps them forever.")
	a.Describe(&o.TransactionsDays, "Days transactions and their spans are kept. 0 keeps them forever.")
	a.Describe(&o.LogsDays, "Days logs are kept. 0 keeps them forever.")
}

// Retention manages the instance-wide retention settings.
type Retention struct{}

type RetentionArgs struct {
	EventsDays       int                 `pulumi:"eventsDays,optional"`
	TransactionsDays int                 `pulumi:"transactionsDays,optional"`
	LogsDays         int                 `pulumi:"logsDays,optional"`
	ProjectOverrides []RetentionOverride `pulumi:"projectOverrides,optional"`
	Frequency        string              `pulumi:"frequency,optional"`
	Hour             int                 `pulumi:"hour,optional"`
	Weekday          int                 `pulumi:"weekday,optional"`
}

type RetentionState struct {
	RetentionArgs
}

func (r *Retention) Annotate(a infer.Annotator) {
	a.SetToken("index", "Retention")
	a.Describe(&r, "The retention settings of the Rustrak instance: how long events, transactions and logs are kept, by default and per project, and when the cleanup runs. There is one per instance; creating the resource adopts and overwrites the current settings, and deleting it restores Rustrak's default of keeping everything forever. Managing retention requires an admin token. The resource ID is \"retention\".")
}

func (args *RetentionArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.EventsDays, "Days error events, and the issues they leave empty, are kept. 0 keeps them forever.")
	a.Describe(&args.TransactionsDays, "Days transactions and their spans are kept. 0 keeps them forever.")
	a.Describe(&args.LogsDays, "Days logs are kept. 0 keeps them forever.")
	a.Describe(&args.ProjectOverrides, "Projects with their own windows; at most one per project.")
	a.Describe(&args.Frequency, "How often the cleanup runs: daily or weekly. Defaults to daily.")
	a.Describe(&args.Hour, "Hour of the day the cleanup runs, in UTC (0-23). Defaults to 3.")
	a.Describe(&args.Weekday, "Day a weekly cleanup runs, 0 = Sunday (0-6). Defaults to 0.")
	a.SetDefault(&args.Frequency, defaultCleanupFrequency)
	a.SetDefault(&args.Hour, defaultCleanupHour)
}

func (Retention) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[RetentionArgs], error) {
	args, failures, err := infer.DefaultCheck[RetentionArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[RetentionArgs]{}, err
	}
	if args.Frequency == "" {
		args.Frequency = defaultCleanupFrequency
	}
	if args.Frequency != "daily" && args.Frequency != "weekly" {
		failures = append(failures, p.CheckFailure{Property: "frequency", Reason: "must be daily or weekly"})
	}
	if args.Hour < 0 || args.Hour > 23 {
		failures = append(failures, p.CheckFailure{Property: "hour", Reason: "must be between 0 and 23"})
	}
	if args.Weekday < 0 || args.Weekday > 6 {
		failures = append(failures, p.CheckFailure{Property: "weekday", Reason: "must be between 0 and 6"})
	}
	for name, days := range map[string]int{"eventsDays": args.EventsDays, "transactionsDays": args.TransactionsDays, "logsDays": args.LogsDays} {
		if days < 0 {
			failures = append(failures, p.CheckFailure{Property: name, Reason: "must not be negative"})
		}
	}
	seen := map[int]bool{}
	for i, override := range args.ProjectOverrides {
		if seen[override.ProjectID] {
			failures = append(failures, p.CheckFailure{Property: fmt.Sprintf("projectOverrides[%d]", i), Reason: "a project can have only one override"})
		}
		seen[override.ProjectID] = true
	}
	return infer.CheckResponse[RetentionArgs]{Inputs: args, Failures: failures}, nil
}

func (Retention) Create(ctx context.Context, req infer.CreateRequest[RetentionArgs]) (infer.CreateResponse[RetentionState], error) {
	state := RetentionState{RetentionArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[RetentionState]{Output: state}, nil
	}
	if _, err := client(ctx).UpdateRetention(ctx, retentionSettings(req.Inputs)); err != nil {
		return infer.CreateResponse[RetentionState]{}, err
	}
	return infer.CreateResponse[RetentionState]{ID: retentionID, Output: state}, nil
}

func (Retention) Read(ctx context.Context, req infer.ReadRequest[RetentionArgs, RetentionState]) (infer.ReadResponse[RetentionArgs, RetentionState], error) {
	if req.ID != retentionID {
		return infer.ReadResponse[RetentionArgs, RetentionState]{}, fmt.Errorf("rustrak: resource ID %q must be %q", req.ID, retentionID)
	}
	settings, err := client(ctx).GetRetention(ctx)
	if err != nil {
		return infer.ReadResponse[RetentionArgs, RetentionState]{}, err
	}
	inputs := retentionArgs(settings)
	return infer.ReadResponse[RetentionArgs, RetentionState]{ID: req.ID, Inputs: inputs, State: RetentionState{RetentionArgs: inputs}}, nil
}

func (Retention) Diff(_ context.Context, req infer.DiffRequest[RetentionArgs, RetentionState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.RetentionArgs, req.Inputs)), nil
}

func (Retention) Update(ctx context.Context, req infer.UpdateRequest[RetentionArgs, RetentionState]) (infer.UpdateResponse[RetentionState], error) {
	state := RetentionState{RetentionArgs: req.Inputs}
	if req.DryRun {
		return infer.UpdateResponse[RetentionState]{Output: state}, nil
	}
	if _, err := client(ctx).UpdateRetention(ctx, retentionSettings(req.Inputs)); err != nil {
		return infer.UpdateResponse[RetentionState]{}, err
	}
	return infer.UpdateResponse[RetentionState]{Output: state}, nil
}

// Delete restores Rustrak's defaults, which keep everything forever.
func (Retention) Delete(ctx context.Context, _ infer.DeleteRequest[RetentionState]) (infer.DeleteResponse, error) {
	_, err := client(ctx).UpdateRetention(ctx, retentionSettings(RetentionArgs{Frequency: defaultCleanupFrequency, Hour: defaultCleanupHour}))
	return infer.DeleteResponse{}, err
}

func retentionSettings(args RetentionArgs) rustrak.RetentionSettings {
	overrides := make([]rustrak.ProjectRetention, 0, len(args.ProjectOverrides))
	for _, override := range args.ProjectOverrides {
		overrides = append(overrides, rustrak.ProjectRetention{
			ProjectID:        int64(override.ProjectID),
			RetentionWindows: retentionWindows(override.EventsDays, override.TransactionsDays, override.LogsDays),
		})
	}
	return rustrak.RetentionSettings{
		Defaults:  retentionWindows(args.EventsDays, args.TransactionsDays, args.LogsDays),
		Overrides: overrides,
		Frequency: args.Frequency,
		Hour:      int64(args.Hour),
		Weekday:   int64(args.Weekday),
	}
}

func retentionWindows(events, transactions, logs int) rustrak.RetentionWindows {
	return rustrak.RetentionWindows{
		EventsDays:       optionalInt(events),
		TransactionsDays: optionalInt(transactions),
		LogsDays:         optionalInt(logs),
	}
}

func retentionArgs(settings rustrak.RetentionSettings) RetentionArgs {
	args := RetentionArgs{
		EventsDays:       intValue(settings.Defaults.EventsDays),
		TransactionsDays: intValue(settings.Defaults.TransactionsDays),
		LogsDays:         intValue(settings.Defaults.LogsDays),
		Frequency:        settings.Frequency,
		Hour:             int(settings.Hour),
		Weekday:          int(settings.Weekday),
	}
	for _, override := range settings.Overrides {
		args.ProjectOverrides = append(args.ProjectOverrides, RetentionOverride{
			ProjectID:        int(override.ProjectID),
			EventsDays:       intValue(override.EventsDays),
			TransactionsDays: intValue(override.TransactionsDays),
			LogsDays:         intValue(override.LogsDays),
		})
	}
	return args
}
