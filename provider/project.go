package provider

import (
	"context"
	"strconv"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// Project manages a Rustrak project and exposes the DSN of its key.
type Project struct{}

type ProjectArgs struct {
	Name               string `pulumi:"name"`
	Slug               string `pulumi:"slug,optional"`
	Platform           string `pulumi:"platform,optional"`
	RateLimitPerMinute int    `pulumi:"rateLimitPerMinute,optional"`
	RateLimitPerHour   int    `pulumi:"rateLimitPerHour,optional"`
}

type ProjectState struct {
	ProjectArgs
	ProjectID   int    `pulumi:"projectId"`
	ProjectSlug string `pulumi:"projectSlug"`
	Dsn         string `pulumi:"dsn" provider:"secret"`
	SentryKey   string `pulumi:"sentryKey" provider:"secret"`
}

func (r *Project) Annotate(a infer.Annotator) {
	a.SetToken("index", "Project")
	a.Describe(&r, "A Rustrak project. Rustrak creates one key with every project; its DSN is exposed as dsn. The resource ID is the numeric project ID. Renaming and changing the slug happen in place. Deleting the resource deletes the project with its events.")
}

func (args *ProjectArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.Name, "Display name of the project.")
	a.Describe(&args.Slug, "URL slug of the project. Derived from the name by Rustrak when unset; see projectSlug for the effective value. A slug taken by another project fails the update.")
	a.Describe(&args.Platform, "Platform identifier, e.g. php-laravel or javascript-nextjs. Unset lets Rustrak assign one from the first event and leaves it alone afterwards.")
	a.Describe(&args.RateLimitPerMinute, "The project's own events-per-minute limit. It can only tighten the instance's MAX_EVENTS_PER_PROJECT_PER_MINUTE. Unset follows the instance limit.")
	a.Describe(&args.RateLimitPerHour, "The project's own events-per-hour limit. It can only tighten the instance's MAX_EVENTS_PER_PROJECT_PER_HOUR. Unset follows the instance limit.")
}

func (state *ProjectState) Annotate(a infer.Annotator) {
	a.Describe(&state.ProjectID, "Numeric ID of the project, used by alert rules and monitors.")
	a.Describe(&state.ProjectSlug, "Effective URL slug of the project.")
	a.Describe(&state.Dsn, "DSN for Sentry SDKs, e.g. SENTRY_LARAVEL_DSN.")
	a.Describe(&state.SentryKey, "Public key of the DSN as a UUID; the DSN carries it without dashes.")
}

func (Project) Create(ctx context.Context, req infer.CreateRequest[ProjectArgs]) (infer.CreateResponse[ProjectState], error) {
	state := ProjectState{ProjectArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[ProjectState]{Output: state}, nil
	}
	c := client(ctx)
	project, err := c.CreateProject(ctx, rustrak.ProjectCreate{
		Name:     req.Inputs.Name,
		Slug:     optional(req.Inputs.Slug),
		Platform: optional(req.Inputs.Platform),
	})
	if err != nil {
		return infer.CreateResponse[ProjectState]{}, err
	}
	id := strconv.FormatInt(project.ID, 10)
	// Creation takes no rate limits; set them right away.
	if req.Inputs.RateLimitPerMinute != 0 || req.Inputs.RateLimitPerHour != 0 {
		project, err = c.UpdateProject(ctx, project.ID, projectUpdate(req.Inputs, project.Slug))
		if err != nil {
			return infer.CreateResponse[ProjectState]{ID: id, Output: projectState(req.Inputs, project)}, err
		}
	}
	return infer.CreateResponse[ProjectState]{ID: id, Output: projectState(req.Inputs, project)}, nil
}

func (Project) Read(ctx context.Context, req infer.ReadRequest[ProjectArgs, ProjectState]) (infer.ReadResponse[ProjectArgs, ProjectState], error) {
	id, err := parseID(req.ID, "<projectId>")
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	project, err := client(ctx).GetProject(ctx, id)
	if rustrak.IsNotFound(err) {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, nil
	}
	if err != nil {
		return infer.ReadResponse[ProjectArgs, ProjectState]{}, err
	}
	imported := req.Inputs.Name == ""
	inputs := ProjectArgs{
		Name:               project.Name,
		Slug:               req.Inputs.Slug,
		Platform:           req.Inputs.Platform,
		RateLimitPerMinute: intValue(project.RateLimitPerMinute),
		RateLimitPerHour:   intValue(project.RateLimitPerHour),
	}
	// Keep slug and platform managed when they were declared, and adopt them
	// on import. An undeclared platform is Rustrak's to assign.
	if inputs.Slug != "" || imported {
		inputs.Slug = project.Slug
	}
	if inputs.Platform != "" || imported {
		inputs.Platform = deref(project.Platform)
	}
	return infer.ReadResponse[ProjectArgs, ProjectState]{ID: req.ID, Inputs: inputs, State: projectState(inputs, project)}, nil
}

func (Project) Diff(_ context.Context, req infer.DiffRequest[ProjectArgs, ProjectState]) (infer.DiffResponse, error) {
	return diffResponse(diffArgs(req.State.ProjectArgs, req.Inputs)), nil
}

func (Project) Update(ctx context.Context, req infer.UpdateRequest[ProjectArgs, ProjectState]) (infer.UpdateResponse[ProjectState], error) {
	state := req.State
	state.ProjectArgs = req.Inputs
	if req.DryRun {
		return infer.UpdateResponse[ProjectState]{Output: state}, nil
	}
	slug := req.Inputs.Slug
	if slug == "" {
		slug = req.State.ProjectSlug
	}
	project, err := client(ctx).UpdateProject(ctx, int64(req.State.ProjectID), projectUpdate(req.Inputs, slug))
	if err != nil {
		return infer.UpdateResponse[ProjectState]{}, err
	}
	return infer.UpdateResponse[ProjectState]{Output: projectState(req.Inputs, project)}, nil
}

func (Project) Delete(ctx context.Context, req infer.DeleteRequest[ProjectState]) (infer.DeleteResponse, error) {
	if err := client(ctx).DeleteProject(ctx, int64(req.State.ProjectID)); err != nil && !rustrak.IsNotFound(err) {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, nil
}

func projectUpdate(args ProjectArgs, slug string) rustrak.ProjectUpdate {
	return rustrak.ProjectUpdate{
		Name:               args.Name,
		Slug:               optional(slug),
		Platform:           optional(args.Platform),
		RateLimitPerMinute: optionalInt(args.RateLimitPerMinute),
		RateLimitPerHour:   optionalInt(args.RateLimitPerHour),
	}
}

func projectState(args ProjectArgs, project rustrak.Project) ProjectState {
	return ProjectState{
		ProjectArgs: args,
		ProjectID:   int(project.ID),
		ProjectSlug: project.Slug,
		Dsn:         project.DSN,
		SentryKey:   project.SentryKey,
	}
}
