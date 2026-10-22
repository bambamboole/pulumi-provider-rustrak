package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

const (
	defaultBootstrapDescription  = "pulumi"
	defaultBootstrapReadyTimeout = 180
)

// Variables so tests can shorten the waits.
var (
	// bootstrapRefreshTimeout bounds the readiness wait of a refresh. A refresh
	// asks the host recorded in state, which is gone after a hostname change;
	// it must not wait out the whole deployment timeout.
	bootstrapRefreshTimeout = 15 * time.Second
	bootstrapPollInterval   = 3 * time.Second
)

// Bootstrap issues an API token by logging in with the email and password of
// a Rustrak admin, typically the superuser the container created from
// CREATE_SUPERUSER. The token feeds a second provider instance that manages
// everything else.
type Bootstrap struct{}

type BootstrapArgs struct {
	BaseURL             string `pulumi:"baseUrl,optional"`
	Email               string `pulumi:"email" provider:"replaceOnChanges"`
	Password            string `pulumi:"password" provider:"secret"`
	TokenDescription    string `pulumi:"tokenDescription,optional"`
	ReadyTimeoutSeconds int    `pulumi:"readyTimeoutSeconds,optional"`
}

type BootstrapState struct {
	BootstrapArgs
	TokenID        string `pulumi:"tokenId"`
	Token          string `pulumi:"token" provider:"secret"`
	RepairRequired bool   `pulumi:"repairRequired,optional"`
}

func (r *Bootstrap) Annotate(a infer.Annotator) {
	a.SetToken("index", "Bootstrap")
	a.Describe(&r, "Issues an API token of a Rustrak admin without user interaction, for use as the apiToken of a second provider instance. The resource logs in with the user's email and password through the dashboard's login endpoint, waits for a freshly deployed instance to report ready, and adopts the one existing token with the same description before issuing a new one. A refresh that finds the token revoked, or the recorded host unreachable, marks the resource for repair, and the next update issues a replacement token against the declared host. Deleting the resource revokes the token and keeps the user. The resource ID is the email.")
}

func (args *BootstrapArgs) Annotate(a infer.Annotator) {
	a.Describe(&args.BaseURL, "Origin of the Rustrak instance. Defaults to the provider's base URL.")
	a.Describe(&args.Email, "Email of the admin whose token is issued. Changing it replaces the resource.")
	a.Describe(&args.Password, "Password of the admin. The resource never changes it; keep it in sync with the password of CREATE_SUPERUSER.")
	a.Describe(&args.TokenDescription, "Description of the API token, which identifies it for adoption. Changing it rotates the token. Defaults to \"pulumi\".")
	a.Describe(&args.ReadyTimeoutSeconds, "Seconds to wait for the instance to report ready before giving up on create and update. Defaults to 180.")
	a.SetDefault(&args.TokenDescription, defaultBootstrapDescription)
	a.SetDefault(&args.ReadyTimeoutSeconds, defaultBootstrapReadyTimeout)
}

func (state *BootstrapState) Annotate(a infer.Annotator) {
	a.Describe(&state.TokenID, "ID of the issued API token.")
	a.Describe(&state.Token, "The issued API token. Use it as the apiToken of a second provider instance.")
	a.Describe(&state.RepairRequired, "True after a refresh found the token revoked or the recorded host unreachable. The next update issues a replacement token.")
}

func (Bootstrap) Check(ctx context.Context, req infer.CheckRequest) (infer.CheckResponse[BootstrapArgs], error) {
	args, failures, err := infer.DefaultCheck[BootstrapArgs](ctx, req.NewInputs)
	if err != nil {
		return infer.CheckResponse[BootstrapArgs]{}, err
	}
	if args.TokenDescription == "" {
		args.TokenDescription = defaultBootstrapDescription
	}
	if args.ReadyTimeoutSeconds <= 0 {
		args.ReadyTimeoutSeconds = defaultBootstrapReadyTimeout
	}
	return infer.CheckResponse[BootstrapArgs]{Inputs: args, Failures: failures}, nil
}

func (Bootstrap) Create(ctx context.Context, req infer.CreateRequest[BootstrapArgs]) (infer.CreateResponse[BootstrapState], error) {
	state := BootstrapState{BootstrapArgs: req.Inputs}
	if req.DryRun {
		return infer.CreateResponse[BootstrapState]{Output: state}, nil
	}
	session, err := bootstrapLogin(ctx, req.Inputs, time.Duration(req.Inputs.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.CreateResponse[BootstrapState]{}, err
	}
	token, err := adoptToken(ctx, session, req.Inputs.TokenDescription)
	if err != nil {
		return infer.CreateResponse[BootstrapState]{}, err
	}
	if token == nil {
		issued, err := session.CreateToken(ctx, req.Inputs.TokenDescription)
		if err != nil {
			return infer.CreateResponse[BootstrapState]{}, fmt.Errorf("rustrak: issuing the API token: %w", err)
		}
		token = &issued
	}
	state.TokenID, state.Token = strconv.FormatInt(token.ID, 10), token.Token
	return infer.CreateResponse[BootstrapState]{ID: req.Inputs.Email, Output: state}, nil
}

// Read verifies the token still exists by logging in with the password. An
// unreachable host or a revoked token marks the resource for repair; a rejected
// login fails the refresh so a changed password never erases the state.
func (Bootstrap) Read(ctx context.Context, req infer.ReadRequest[BootstrapArgs, BootstrapState]) (infer.ReadResponse[BootstrapArgs, BootstrapState], error) {
	state := req.State
	if state.Token == "" {
		return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, nil
	}
	inputs := req.Inputs
	if inputs.Email == "" {
		inputs = state.BootstrapArgs
	}
	session, err := bootstrapLogin(ctx, state.BootstrapArgs, bootstrapRefreshTimeout)
	switch {
	case errors.Is(err, rustrak.ErrNotReady):
		state.RepairRequired = true
	case err != nil:
		return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, err
	default:
		valid, err := tokenValid(ctx, session, state.TokenID, state.Token)
		if err != nil {
			return infer.ReadResponse[BootstrapArgs, BootstrapState]{}, err
		}
		state.RepairRequired = !valid
	}
	state.BootstrapArgs = inputs
	return infer.ReadResponse[BootstrapArgs, BootstrapState]{ID: req.ID, Inputs: inputs, State: state}, nil
}

// Diff compares the inputs and also schedules an update when a refresh marked
// the token for repair.
func (Bootstrap) Diff(_ context.Context, req infer.DiffRequest[BootstrapArgs, BootstrapState]) (infer.DiffResponse, error) {
	diff := diffResponse(diffArgs(req.State.BootstrapArgs, req.Inputs, "email"))
	diff.HasChanges = diff.HasChanges || req.State.RepairRequired
	return diff, nil
}

func (Bootstrap) Update(ctx context.Context, req infer.UpdateRequest[BootstrapArgs, BootstrapState]) (infer.UpdateResponse[BootstrapState], error) {
	state := BootstrapState{BootstrapArgs: req.Inputs, TokenID: req.State.TokenID, Token: req.State.Token}
	if req.DryRun {
		return infer.UpdateResponse[BootstrapState]{Output: state}, nil
	}
	session, err := bootstrapLogin(ctx, req.Inputs, time.Duration(req.Inputs.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.UpdateResponse[BootstrapState]{}, err
	}
	sameDescription := req.State.TokenDescription == req.Inputs.TokenDescription
	if sameDescription && !req.State.RepairRequired {
		return infer.UpdateResponse[BootstrapState]{Output: state}, nil
	}
	// A repair keeps the token when the instance still knows it, e.g. after
	// a refresh that could not reach the old host.
	if sameDescription {
		valid, err := tokenValid(ctx, session, req.State.TokenID, req.State.Token)
		if err != nil {
			return infer.UpdateResponse[BootstrapState]{}, err
		}
		if valid {
			return infer.UpdateResponse[BootstrapState]{Output: state}, nil
		}
	}
	token, err := session.CreateToken(ctx, req.Inputs.TokenDescription)
	if err != nil {
		return infer.UpdateResponse[BootstrapState]{}, fmt.Errorf("rustrak: reissuing the API token: %w", err)
	}
	state.TokenID, state.Token = strconv.FormatInt(token.ID, 10), token.Token
	if err := revokeToken(ctx, session, req.State.TokenID); err != nil {
		return infer.UpdateResponse[BootstrapState]{}, err
	}
	return infer.UpdateResponse[BootstrapState]{Output: state}, nil
}

// Delete revokes the token. The user is kept; it is the instance admin.
func (Bootstrap) Delete(ctx context.Context, req infer.DeleteRequest[BootstrapState]) (infer.DeleteResponse, error) {
	if req.State.TokenID == "" {
		return infer.DeleteResponse{}, nil
	}
	session, err := bootstrapLogin(ctx, req.State.BootstrapArgs, time.Duration(req.State.ReadyTimeoutSeconds)*time.Second)
	if err != nil {
		return infer.DeleteResponse{}, err
	}
	return infer.DeleteResponse{}, revokeToken(ctx, session, req.State.TokenID)
}

// bootstrapLogin waits for the instance and opens a session for the user.
func bootstrapLogin(ctx context.Context, args BootstrapArgs, timeout time.Duration) (*rustrak.Client, error) {
	baseURL := args.BaseURL
	if baseURL == "" {
		baseURL = client(ctx).BaseURL()
	}
	session, err := rustrak.NewAnonymous(baseURL)
	if err != nil {
		return nil, err
	}
	if err := session.WaitReady(ctx, timeout, bootstrapPollInterval); err != nil {
		return nil, err
	}
	if err := session.Login(ctx, args.Email, args.Password); err != nil {
		return nil, fmt.Errorf("rustrak: login as %q failed: %w", args.Email, err)
	}
	return session, nil
}

// adoptToken reveals the one existing token with the description, so a
// recreated resource reuses the token an earlier run issued. It returns nil
// when no token or more than one matches.
func adoptToken(ctx context.Context, session *rustrak.Client, description string) (*rustrak.TokenSecret, error) {
	tokens, err := session.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	var matches []rustrak.Token
	for _, token := range tokens {
		if deref(token.Description) == description {
			matches = append(matches, token)
		}
	}
	if len(matches) != 1 {
		return nil, nil
	}
	token, err := session.GetToken(ctx, matches[0].ID)
	if err != nil {
		return nil, err
	}
	return &token, nil
}

// tokenValid reports whether the instance still knows the token with the ID
// and value.
func tokenValid(ctx context.Context, session *rustrak.Client, id, value string) (bool, error) {
	tokenID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return false, nil
	}
	token, err := session.GetToken(ctx, tokenID)
	if rustrak.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return token.Token == value, nil
}

// revokeToken deletes the token with the given ID; a token that is already
// gone is fine.
func revokeToken(ctx context.Context, session *rustrak.Client, id string) error {
	tokenID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	if err := session.DeleteToken(ctx, tokenID); err != nil && !rustrak.IsNotFound(err) {
		return fmt.Errorf("rustrak: revoking the API token: %w", err)
	}
	return nil
}
