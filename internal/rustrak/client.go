// Package rustrak is a hand-written client for the parts of the Rustrak API
// the provider manages: API tokens, projects, alert channels, alert rules,
// cron monitors and the retention settings. Field shapes follow the OpenAPI
// document in the Rustrak repository (apps/server/openapi.json, checked
// against Rustrak 0.19.1).
//
// Two authentication modes exist. A Client built with New sends an API token
// as a bearer token. A Client built with NewAnonymous has no credentials until
// Login establishes a session cookie through the dashboard's login endpoint,
// which is how the Bootstrap resource issues the first API token.
package rustrak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	sessionCookie = "rustrak_session"
	maxErrorBody  = 300
)

// ErrMissingToken is returned by every request of a provider configured
// without an API token; only Bootstrap resources work with such a provider.
var ErrMissingToken = errors.New("rustrak: missing API token; set the provider's apiToken or the RUSTRAK_API_TOKEN environment variable")

// ErrNotReady reports that the instance did not answer within the deadline.
var ErrNotReady = errors.New("rustrak: the instance did not become ready before the deadline")

// Error is an unexpected HTTP status from the Rustrak API.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *Error) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("rustrak: %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("rustrak: %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is an HTTP 404.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports whether err is an HTTP 409.
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

// IsUnauthorized reports whether err is an HTTP 401 or 403.
func IsUnauthorized(err error) bool {
	return hasStatus(err, http.StatusUnauthorized) || hasStatus(err, http.StatusForbidden)
}

func hasStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// Client talks to one Rustrak instance.
type Client struct {
	baseURL string
	http    *http.Client
	token   string
	// session holds the session cookie after Login.
	session string
}

// New returns a client authenticated with an API token. An empty token yields
// a client whose requests fail with ErrMissingToken.
func New(baseURL, token string) (*Client, error) {
	c, err := NewAnonymous(baseURL)
	if err != nil {
		return nil, err
	}
	c.token = strings.TrimSpace(token)
	return c, nil
}

// NewAnonymous returns a client without credentials, to be used with Login.
func NewAnonymous(baseURL string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("rustrak: base URL %q must include scheme and host", baseURL)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return nil, fmt.Errorf("rustrak: base URL %q must be an origin without credentials, path, or query", baseURL)
	}
	return &Client{
		baseURL: parsed.Scheme + "://" + parsed.Host,
		http:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// BaseURL returns the normalized origin of the instance.
func (c *Client) BaseURL() string { return c.baseURL }

// HasCredentials reports whether requests carry a token or a session.
func (c *Client) HasCredentials() bool { return c.token != "" || c.session != "" }

// WaitReady polls the readiness endpoint until the instance and its database
// answer or the timeout passes. Coolify starts a deployment asynchronously,
// so the first request after a deploy may reach a container that is still
// migrating.
func (c *Client) WaitReady(ctx context.Context, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		response, err := c.send(ctx, http.MethodGet, "/health/ready", nil)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return ErrNotReady
		}
		wait := interval
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Login opens a session for the user. Failures never echo the request
// because it carries the password.
func (c *Client) Login(ctx context.Context, email, password string) error {
	response, err := c.send(ctx, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password})
	if err != nil {
		return fmt.Errorf("rustrak: login request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return &Error{StatusCode: response.StatusCode, Method: http.MethodPost, Path: "/auth/login", Body: "login rejected; check the user's email and password"}
	}
	if c.session == "" {
		return errors.New("rustrak: login did not issue a session cookie")
	}
	return nil
}

// send performs one request with the client's credentials and records the
// session cookie of the response.
func (c *Client) send(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.token != "":
		request.Header.Set("Authorization", "Bearer "+c.token)
	case c.session != "":
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: c.session})
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			c.session = cookie.Value
		}
	}
	return response, nil
}

// do performs an authenticated API request and decodes a JSON response into
// out when out is not nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	if !c.HasCredentials() {
		return ErrMissingToken
	}
	response, err := c.send(ctx, method, path, body)
	if err != nil {
		return fmt.Errorf("rustrak: %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("rustrak: reading %s %s: %w", method, path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{StatusCode: response.StatusCode, Method: method, Path: path, Body: errorMessage(payload)}
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("rustrak: decoding %s %s: %w", method, path, err)
	}
	return nil
}

// errorMessage extracts the message of Rustrak's {"error":{"message":...}}
// envelope and falls back to the truncated body.
func errorMessage(payload []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) == nil && envelope.Error.Message != "" {
		return truncate(envelope.Error.Message)
	}
	return truncate(strings.TrimSpace(string(payload)))
}

func truncate(value string) string {
	if len(value) <= maxErrorBody {
		return value
	}
	return value[:maxErrorBody] + "…"
}
