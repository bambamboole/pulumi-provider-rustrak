package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// fastBootstrap shortens the readiness waits for the duration of a test.
func fastBootstrap(t *testing.T) {
	t.Helper()
	refresh, poll := bootstrapRefreshTimeout, bootstrapPollInterval
	bootstrapRefreshTimeout, bootstrapPollInterval = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { bootstrapRefreshTimeout, bootstrapPollInterval = refresh, poll })
}

func bootstrapArgs(baseURL string) BootstrapArgs {
	return BootstrapArgs{
		BaseURL:             baseURL,
		Email:               "admin@example.com",
		Password:            "admin-secret",
		TokenDescription:    "infrastructure",
		ReadyTimeoutSeconds: 1,
	}
}

func TestBootstrapCheckAppliesDefaults(t *testing.T) {
	response, err := (Bootstrap{}).Check(context.Background(), infer.CheckRequest{NewInputs: property.NewMap(map[string]property.Value{
		"email":    property.New("admin@example.com"),
		"password": property.New("admin-secret"),
	})})
	if err != nil {
		t.Fatal(err)
	}
	if response.Inputs.TokenDescription != "pulumi" || response.Inputs.ReadyTimeoutSeconds != 180 {
		t.Fatalf("defaults not applied: %+v", response.Inputs)
	}
}

func TestBootstrapCreateIssuesTokenAfterReadiness(t *testing.T) {
	fake := newFakeRustrak(t)
	fake.ready = false
	go func() {
		time.Sleep(200 * time.Millisecond)
		fake.mu.Lock()
		fake.ready = true
		fake.mu.Unlock()
	}()

	created, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != "admin@example.com" || created.Output.Token == "" || created.Output.TokenID == "" {
		t.Fatalf("unexpected output %+v", created)
	}

	// The issued token authenticates API requests.
	c, err := rustrak.New(fake.server.URL, created.Output.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTokens(context.Background()); err != nil {
		t.Fatalf("issued token must authenticate: %v", err)
	}
}

func TestBootstrapAdoptsTheOneTokenWithTheDescription(t *testing.T) {
	fake := newFakeRustrak(t)
	existing := fake.addToken("infrastructure")

	created, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Output.Token != existing.token || len(fake.tokens) != 1 {
		t.Fatalf("the existing token must be adopted: %+v, %d tokens", created.Output, len(fake.tokens))
	}

	// Two tokens with the description are ambiguous: issue a new one.
	fake.addToken("infrastructure")
	again, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: bootstrapArgs(fake.server.URL)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if again.Output.Token == existing.token || len(fake.tokens) != 3 {
		t.Fatalf("an ambiguous description must issue a token: %+v, %d tokens", again.Output, len(fake.tokens))
	}
}

func TestBootstrapRejectsWrongPassword(t *testing.T) {
	fake := newFakeRustrak(t)
	args := bootstrapArgs(fake.server.URL)
	args.Password = "wrong"
	_, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err == nil || !strings.Contains(err.Error(), "login rejected") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("expected a login error without the password, got %v", err)
	}
}

func TestBootstrapRefreshRepairsARevokedToken(t *testing.T) {
	fake := newFakeRustrak(t)
	args := bootstrapArgs(fake.server.URL)
	created, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err != nil {
		t.Fatal(err)
	}

	read, err := (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil || read.State.RepairRequired {
		t.Fatalf("a valid token must not need repair: %+v %v", read.State, err)
	}

	fake.mu.Lock()
	fake.tokens = nil
	fake.mu.Unlock()
	read, err = (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil || !read.State.RepairRequired {
		t.Fatalf("a revoked token must need repair: %+v %v", read.State, err)
	}
	diff, err := (Bootstrap{}).Diff(context.Background(), infer.DiffRequest[BootstrapArgs, BootstrapState]{Inputs: args, State: read.State})
	if err != nil || !diff.HasChanges {
		t.Fatalf("repair must schedule an update: %+v %v", diff, err)
	}
	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: read.State})
	if err != nil || updated.Output.Token == created.Output.Token || updated.Output.RepairRequired {
		t.Fatalf("repair must issue a new token: %+v %v", updated.Output, err)
	}
}

func TestBootstrapRefreshOfAnUnreachableHostNeedsRepair(t *testing.T) {
	fake := newFakeRustrak(t)
	args := bootstrapArgs(fake.server.URL)
	created, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.ready = false
	fake.mu.Unlock()
	read, err := (Bootstrap{}).Read(context.Background(), infer.ReadRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil || !read.State.RepairRequired {
		t.Fatalf("an unreachable host must need repair: %+v %v", read.State, err)
	}

	// Back online, the update keeps the token the instance still knows.
	fake.mu.Lock()
	fake.ready = true
	fake.mu.Unlock()
	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: read.State})
	if err != nil || updated.Output.Token != created.Output.Token {
		t.Fatalf("a known token must be kept: %+v %v", updated.Output, err)
	}
}

func TestBootstrapRotatesOnNewDescriptionAndRevokesOnDelete(t *testing.T) {
	fake := newFakeRustrak(t)
	args := bootstrapArgs(fake.server.URL)
	created, err := (Bootstrap{}).Create(context.Background(), infer.CreateRequest[BootstrapArgs]{Inputs: args})
	if err != nil {
		t.Fatal(err)
	}
	args.TokenDescription = "rotated"
	updated, err := (Bootstrap{}).Update(context.Background(), infer.UpdateRequest[BootstrapArgs, BootstrapState]{ID: created.ID, Inputs: args, State: created.Output})
	if err != nil || updated.Output.Token == created.Output.Token {
		t.Fatalf("a new description must rotate the token: %+v %v", updated.Output, err)
	}
	if len(fake.tokens) != 1 || fake.tokens[0].description != "rotated" {
		t.Fatalf("the old token must be revoked: %+v", fake.tokens)
	}
	if _, err := (Bootstrap{}).Delete(context.Background(), infer.DeleteRequest[BootstrapState]{ID: created.ID, State: updated.Output}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fake.tokens) != 0 {
		t.Fatalf("delete must revoke the token: %+v", fake.tokens)
	}
}
