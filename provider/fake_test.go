package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bambamboole/pulumi-provider-rustrak/internal/rustrak"
)

// fakeRustrak is an in-memory Rustrak covering the endpoints the resources
// use: readiness, login, API tokens, projects, integrations, alert rules,
// monitors and the retention settings. It mirrors the behaviour the
// resources depend on: PATCH semantics of absent and null fields, a redacted
// Slack bot token, and the defaults Rustrak fills into a monitor config.
type fakeRustrak struct {
	t            *testing.T
	mu           sync.Mutex
	server       *httptest.Server
	users        map[string]string // email -> password
	sessions     map[string]string // session cookie -> email
	tokens       []*fakeToken
	projects     map[int64]*rustrak.Project
	integrations map[int64]*rustrak.Integration
	rules        map[int64]*rustrak.AlertRule
	monitors     map[int64][]*rustrak.Monitor // project ID -> monitors
	retention    rustrak.RetentionSettings
	nextID       int64
	ready        bool
	requests     []string
}

type fakeToken struct {
	id          int64
	token       string
	description string
}

func newFakeRustrak(t *testing.T) *fakeRustrak {
	fastBootstrap(t)
	f := &fakeRustrak{
		t:            t,
		users:        map[string]string{"admin@example.com": "admin-secret"},
		sessions:     map[string]string{},
		projects:     map[int64]*rustrak.Project{},
		integrations: map[int64]*rustrak.Integration{},
		rules:        map[int64]*rustrak.AlertRule{},
		monitors:     map[int64][]*rustrak.Monitor{},
		retention:    rustrak.RetentionSettings{Overrides: []rustrak.ProjectRetention{}, Frequency: "daily", Hour: 3},
		ready:        true,
	}
	f.server = httptest.NewServer(f)
	t.Cleanup(f.server.Close)
	return f
}

// ctx returns a context whose client carries an admin token of the fake.
func (f *fakeRustrak) ctx() context.Context {
	token := f.addToken("tests")
	c, err := rustrak.New(f.server.URL, token.token)
	if err != nil {
		f.t.Fatal(err)
	}
	return context.WithValue(context.Background(), clientKey{}, c)
}

// addToken issues a token directly, as the dashboard would.
func (f *fakeRustrak) addToken(description string) *fakeToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issueToken(description)
}

func (f *fakeRustrak) issueToken(description string) *fakeToken {
	f.nextID++
	token := &fakeToken{id: f.nextID, token: fmt.Sprintf("%040d", f.nextID), description: description}
	f.tokens = append(f.tokens, token)
	return token
}

func (f *fakeRustrak) addProject(name string) *rustrak.Project {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createProject(rustrak.ProjectCreate{Name: name})
}

func (f *fakeRustrak) createProject(input rustrak.ProjectCreate) *rustrak.Project {
	f.nextID++
	slug := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input.Name), " ", "-"))
	if input.Slug != nil {
		slug = *input.Slug
	}
	// Rustrak returns the key as a UUID and puts it into the DSN without dashes.
	key := fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID)
	project := &rustrak.Project{
		ID: f.nextID, Name: input.Name, Slug: slug, Platform: input.Platform, SentryKey: key,
		DSN: fmt.Sprintf("http://%s@%s/%d", strings.ReplaceAll(key, "-", ""), strings.TrimPrefix(f.server.URL, "http://"), f.nextID),
	}
	f.projects[project.ID] = project
	return project
}

func (f *fakeRustrak) requestLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

var (
	tokenPath       = regexp.MustCompile(`^/api/tokens/(\d+)$`)
	projectPath     = regexp.MustCompile(`^/api/projects/(\d+)$`)
	integrationPath = regexp.MustCompile(`^/api/integrations/(\d+)$`)
	rulesPath       = regexp.MustCompile(`^/api/projects/(\d+)/alert-rules(?:/(\d+))?$`)
	monitorsPath    = regexp.MustCompile(`^/api/projects/(\d+)/monitors(?:/([^/]+))?$`)
)

func (f *fakeRustrak) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	body, _ := io.ReadAll(r.Body)

	switch {
	case r.URL.Path == "/health/ready":
		if !f.ready {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	case r.URL.Path == "/auth/login" && r.Method == http.MethodPost:
		var login struct{ Email, Password string }
		_ = json.Unmarshal(body, &login)
		if password, ok := f.users[login.Email]; !ok || password != login.Password {
			writeError(w, http.StatusUnauthorized, "Invalid credentials")
			return
		}
		f.nextID++
		session := fmt.Sprintf("session-%d", f.nextID)
		f.sessions[session] = login.Email
		http.SetCookie(w, &http.Cookie{Name: "rustrak_session", Value: session, HttpOnly: true})
		writeJSON(w, http.StatusOK, map[string]any{"user": map[string]any{"email": login.Email}})
		return
	}

	if !f.authorized(r) {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	if match := tokenPath.FindStringSubmatch(r.URL.Path); match != nil {
		f.serveToken(w, r, parseInt(match[1]))
		return
	}
	if match := projectPath.FindStringSubmatch(r.URL.Path); match != nil {
		f.serveProject(w, r, parseInt(match[1]), body)
		return
	}
	if match := integrationPath.FindStringSubmatch(r.URL.Path); match != nil {
		f.serveIntegration(w, r, parseInt(match[1]), body)
		return
	}
	if match := rulesPath.FindStringSubmatch(r.URL.Path); match != nil {
		f.serveRules(w, r, parseInt(match[1]), match[2], body)
		return
	}
	if match := monitorsPath.FindStringSubmatch(r.URL.Path); match != nil {
		f.serveMonitors(w, r, parseInt(match[1]), match[2], body)
		return
	}

	switch r.Method + " " + r.URL.Path {
	case "GET /api/tokens":
		list := []map[string]any{}
		for _, token := range f.tokens {
			list = append(list, map[string]any{"id": token.id, "token_prefix": token.token[:8] + "...", "description": token.description})
		}
		writeJSON(w, http.StatusOK, list)
	case "POST /api/tokens":
		var input struct{ Description string }
		_ = json.Unmarshal(body, &input)
		token := f.issueToken(input.Description)
		writeJSON(w, http.StatusCreated, map[string]any{"id": token.id, "token": token.token, "description": token.description})
	case "POST /api/projects":
		var input rustrak.ProjectCreate
		_ = json.Unmarshal(body, &input)
		if input.Slug != nil && f.slugTaken(*input.Slug, 0) {
			writeError(w, http.StatusConflict, "slug taken")
			return
		}
		writeJSON(w, http.StatusCreated, f.createProject(input))
	case "POST /api/integrations":
		var input struct {
			Name         string          `json:"name"`
			ProviderType string          `json:"provider_type"`
			Credentials  json.RawMessage `json:"credentials"`
			IsEnabled    bool            `json:"is_enabled"`
		}
		_ = json.Unmarshal(body, &input)
		f.nextID++
		integration := &rustrak.Integration{ID: f.nextID, Name: input.Name, ProviderType: input.ProviderType, Credentials: input.Credentials, IsEnabled: input.IsEnabled}
		f.integrations[integration.ID] = integration
		writeJSON(w, http.StatusCreated, redact(*integration))
	case "GET /api/storage/retention":
		writeJSON(w, http.StatusOK, map[string]any{"settings": f.retention, "next_run_at": nil, "last_run": nil})
	case "PUT /api/storage/retention":
		var settings rustrak.RetentionSettings
		_ = json.Unmarshal(body, &settings)
		if settings.Overrides == nil {
			writeError(w, http.StatusBadRequest, "overrides is required")
			return
		}
		f.retention = settings
		writeJSON(w, http.StatusOK, map[string]any{"settings": f.retention, "next_run_at": nil, "last_run": nil})
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

func (f *fakeRustrak) authorized(r *http.Request) bool {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		for _, token := range f.tokens {
			if token.token == bearer {
				return true
			}
		}
		return false
	}
	cookie, err := r.Cookie("rustrak_session")
	return err == nil && f.sessions[cookie.Value] != ""
}

func (f *fakeRustrak) serveToken(w http.ResponseWriter, r *http.Request, id int64) {
	for i, token := range f.tokens {
		if token.id != id {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"id": token.id, "token": token.token, "description": token.description})
		case http.MethodDelete:
			f.tokens = append(f.tokens[:i], f.tokens[i+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	writeError(w, http.StatusNotFound, "Token not found")
}

func (f *fakeRustrak) serveProject(w http.ResponseWriter, r *http.Request, id int64, body []byte) {
	project := f.projects[id]
	if project == nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, project)
	case http.MethodPatch:
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		if raw, ok := fields["slug"]; ok && string(raw) != "null" {
			var slug string
			_ = json.Unmarshal(raw, &slug)
			if f.slugTaken(slug, id) {
				writeError(w, http.StatusConflict, "slug taken")
				return
			}
			project.Slug = slug
		}
		if raw, ok := fields["name"]; ok && string(raw) != "null" {
			_ = json.Unmarshal(raw, &project.Name)
		}
		// A null platform leaves it untouched; null rate limits remove them.
		if raw, ok := fields["platform"]; ok && string(raw) != "null" {
			_ = json.Unmarshal(raw, &project.Platform)
		}
		if raw, ok := fields["rate_limit_per_minute"]; ok {
			project.RateLimitPerMinute = nil
			_ = json.Unmarshal(raw, &project.RateLimitPerMinute)
		}
		if raw, ok := fields["rate_limit_per_hour"]; ok {
			project.RateLimitPerHour = nil
			_ = json.Unmarshal(raw, &project.RateLimitPerHour)
		}
		writeJSON(w, http.StatusOK, project)
	case http.MethodDelete:
		delete(f.projects, id)
		delete(f.monitors, id)
		for ruleID, rule := range f.rules {
			if rule.ProjectID == id {
				delete(f.rules, ruleID)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeRustrak) slugTaken(slug string, except int64) bool {
	for id, project := range f.projects {
		if id != except && project.Slug == slug {
			return true
		}
	}
	return false
}

func (f *fakeRustrak) serveIntegration(w http.ResponseWriter, r *http.Request, id int64, body []byte) {
	integration := f.integrations[id]
	if integration == nil {
		writeError(w, http.StatusNotFound, "Integration not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, redact(*integration))
	case http.MethodPatch:
		var input struct {
			Name        *string         `json:"name"`
			Credentials json.RawMessage `json:"credentials"`
			IsEnabled   *bool           `json:"is_enabled"`
		}
		_ = json.Unmarshal(body, &input)
		if input.Name != nil {
			integration.Name = *input.Name
		}
		if len(input.Credentials) > 0 && string(input.Credentials) != "null" {
			integration.Credentials = input.Credentials
		}
		if input.IsEnabled != nil {
			integration.IsEnabled = *input.IsEnabled
		}
		writeJSON(w, http.StatusOK, redact(*integration))
	case http.MethodDelete:
		delete(f.integrations, id)
		w.WriteHeader(http.StatusNoContent)
	}
}

// redact replaces a Slack bot token the way Rustrak's API does.
func redact(integration rustrak.Integration) rustrak.Integration {
	var creds map[string]any
	if integration.ProviderType == "slack" && json.Unmarshal(integration.Credentials, &creds) == nil && creds["method"] == "bot_token" {
		creds["token"] = redactedSlackToken
		integration.Credentials, _ = json.Marshal(creds)
	}
	return integration
}

func (f *fakeRustrak) serveRules(w http.ResponseWriter, r *http.Request, projectID int64, ruleID string, body []byte) {
	if f.projects[projectID] == nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}
	if ruleID == "" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "unsupported")
			return
		}
		var input rustrak.AlertRuleCreate
		_ = json.Unmarshal(body, &input)
		if err := f.validateChannels(input.Channels); err != "" {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		f.nextID++
		rule := &rustrak.AlertRule{ID: f.nextID, ProjectID: projectID, Name: input.Name, AlertType: input.AlertType, IsEnabled: true, Conditions: input.Conditions, CooldownMinutes: input.CooldownMinutes, Channels: input.Channels}
		f.rules[rule.ID] = rule
		writeJSON(w, http.StatusCreated, rule)
		return
	}
	rule := f.rules[parseInt(ruleID)]
	if rule == nil || rule.ProjectID != projectID {
		writeError(w, http.StatusNotFound, "Alert rule not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, rule)
	case http.MethodPatch:
		var input rustrak.AlertRuleUpdate
		_ = json.Unmarshal(body, &input)
		if err := f.validateChannels(input.Channels); err != "" {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		rule.Name, rule.IsEnabled, rule.Conditions, rule.CooldownMinutes, rule.Channels = input.Name, input.IsEnabled, input.Conditions, input.CooldownMinutes, input.Channels
		writeJSON(w, http.StatusOK, rule)
	case http.MethodDelete:
		delete(f.rules, rule.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// validateChannels rejects unknown channels and email channels without
// recipients, as Rustrak does.
func (f *fakeRustrak) validateChannels(channels []rustrak.RuleChannel) string {
	for _, channel := range channels {
		integration := f.integrations[channel.IntegrationID]
		if integration == nil {
			return fmt.Sprintf("integration %d not found", channel.IntegrationID)
		}
		if channel.RoutingOverride == nil {
			return "routing_override must be a JSON object"
		}
		if integration.ProviderType == "email" && len(stringList(channel.RoutingOverride["recipients"])) == 0 {
			return "Email routing_override must include at least one recipient"
		}
	}
	return ""
}

func (f *fakeRustrak) serveMonitors(w http.ResponseWriter, r *http.Request, projectID int64, slug string, body []byte) {
	if f.projects[projectID] == nil {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}
	if slug == "" {
		var input rustrak.MonitorCreate
		_ = json.Unmarshal(body, &input)
		f.nextID++
		monitor := &rustrak.Monitor{ID: fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID), Name: input.Name, Config: input.Config}
		monitor.Slug = strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
		if input.Slug != nil {
			monitor.Slug = *input.Slug
		}
		f.monitors[projectID] = append(f.monitors[projectID], monitor)
		writeJSON(w, http.StatusCreated, monitor)
		return
	}
	for i, monitor := range f.monitors[projectID] {
		if monitor.Slug != slug {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, monitor)
		case http.MethodPut:
			var input rustrak.MonitorUpdate
			_ = json.Unmarshal(body, &input)
			monitor.Name, monitor.Config, monitor.IsMuted = input.Name, input.Config, input.IsMuted
			writeJSON(w, http.StatusOK, monitor)
		case http.MethodDelete:
			f.monitors[projectID] = append(f.monitors[projectID][:i], f.monitors[projectID][i+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	writeError(w, http.StatusNotFound, "Monitor not found")
}

func parseInt(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": "error", "message": message}})
}
