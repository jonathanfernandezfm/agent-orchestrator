package azuredevops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultBaseURL     = "https://dev.azure.com"
	apiVersion         = "7.1"
	defaultHTTPTimeout = 30 * time.Second
	errorBodyMaxBytes  = 4096
	logTailMaxBytes    = 65536
)

// ErrRateLimited is the category of a throttled Azure DevOps request.
var ErrRateLimited = errors.New("azure devops scm: rate limited")

// RateLimitError carries the Retry-After hint of a 429 response; the observer
// reads it through GetRetryAfter/GetResetAt to apply a provider cooldown.
type RateLimitError struct {
	RetryAfter time.Duration
	ResetAt    time.Time
}

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }

// Is matches ErrRateLimited.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// GetRetryAfter exposes the Retry-After hint to the observer.
func (e *RateLimitError) GetRetryAfter() time.Duration { return e.RetryAfter }

// GetResetAt exposes the reset hint to the observer.
func (e *RateLimitError) GetResetAt() time.Time { return e.ResetAt }

// HTTPError is a non-success Azure DevOps response.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("azure devops scm: HTTP %d: %s", e.StatusCode, e.Message)
}

// Client is a minimal Azure DevOps REST client. Paths start at the
// organization ("/{org}/{project}/_apis/...") and must be pre-escaped.
type Client struct {
	http    *http.Client
	tokens  TokenSource
	baseURL string
}

func newClient(hc *http.Client, tokens TokenSource, baseURL string) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{http: hc, tokens: tokens, baseURL: strings.TrimRight(baseURL, "/")}
}

// getJSON GETs path and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, q, nil, 0)
	if err != nil {
		return err
	}
	return decode(body, out)
}

// sendJSON issues a JSON mutation and decodes the response into out (nil to ignore).
func (c *Client) sendJSON(ctx context.Context, method, path string, q url.Values, in, out any) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	body, err := c.do(ctx, method, path, q, payload, 0)
	if err != nil || out == nil {
		return err
	}
	return decode(body, out)
}

// getTail GETs a text resource keeping at most the last maxBytes of it.
func (c *Client) getTail(ctx context.Context, path string, maxBytes int) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, nil, nil, maxBytes)
}

func decode(body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("azure devops scm: decode response: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, payload []byte, tailBytes int) ([]byte, error) {
	if c.tokens == nil {
		return nil, ErrNoToken
	}
	cred, err := c.tokens.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if q == nil {
		q = url.Values{}
	}
	if q.Get("api-version") == "" {
		q.Set("api-version", apiVersion)
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path+"?"+q.Encode(), reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", cred.authorization())
	req.Header.Set("Accept", "application/json")
	if tailBytes > 0 {
		req.Header.Set("Accept", "text/plain")
	}
	req.Header.Set("User-Agent", "ao-azure-devops-scm/1")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure devops scm: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Azure DevOps answers a rejected PAT with 203 + an HTML sign-in page.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNonAuthoritativeInfo {
		if inv, ok := c.tokens.(tokenInvalidator); ok {
			inv.InvalidateToken()
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrAuthFailed, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		rl := &RateLimitError{}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			rl.RetryAfter = time.Duration(secs) * time.Second
		}
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > 0 {
			rl.ResetAt = time.Unix(reset, 0)
		}
		return nil, rl
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMaxBytes))
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &msg)
		if msg.Message == "" {
			msg.Message = strings.TrimSpace(string(raw))
		}
		httpErr := &HTTPError{StatusCode: resp.StatusCode, Message: msg.Message}
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w", ports.ErrSCMNotFound, httpErr)
		}
		return nil, httpErr
	}
	if tailBytes <= 0 {
		return io.ReadAll(resp.Body)
	}
	return readTail(resp.Body, tailBytes)
}

// readTail streams r keeping only the last n bytes, so a huge build log never
// sits in memory in full.
func readTail(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, 0, n)
	chunk := make([]byte, 32*1024)
	for {
		k, err := r.Read(chunk)
		buf = append(buf, chunk[:k]...)
		if len(buf) > n {
			buf = append(buf[:0], buf[len(buf)-n:]...)
		}
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
