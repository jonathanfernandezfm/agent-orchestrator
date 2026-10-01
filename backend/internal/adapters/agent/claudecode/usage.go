package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

const (
	claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	// claudeUsageTTL spaces upstream reads: the endpoint throttles polling.
	claudeUsageTTL = time.Minute
)

// UsageReader reads the signed-in Claude subscription's rate-limit windows
// from the endpoint Claude Code's /usage command uses. A failed read keeps the
// last good snapshot rather than blanking the meter.
type UsageReader struct {
	URL    string
	Client *http.Client
	Now    func() time.Time
	Token  func(context.Context) (string, bool)

	mu          sync.Mutex
	snapshot    domain.ClaudeSubscriptionUsage
	lastAttempt time.Time
}

// NewUsageReader returns a reader wired to the local Claude login.
func NewUsageReader() *UsageReader {
	return &UsageReader{
		URL:    claudeUsageURL,
		Client: &http.Client{Timeout: 10 * time.Second},
		Now:    time.Now,
		Token:  localSubscriptionToken,
	}
}

// SubscriptionUsage returns the cached snapshot, refreshing it at most once
// per claudeUsageTTL.
func (r *UsageReader) SubscriptionUsage(ctx context.Context) domain.ClaudeSubscriptionUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	if !r.lastAttempt.IsZero() && now.Sub(r.lastAttempt) < claudeUsageTTL {
		return r.snapshot
	}
	r.lastAttempt = now
	token, ok := r.Token(ctx)
	if !ok {
		r.snapshot = domain.ClaudeSubscriptionUsage{State: domain.ClaudeUsageSignedOut, CheckedAt: now}
		return r.snapshot
	}
	windows, err := r.fetch(ctx, token)
	if err != nil {
		if r.snapshot.State != domain.ClaudeUsageAvailable {
			r.snapshot = domain.ClaudeSubscriptionUsage{State: domain.ClaudeUsageUnavailable, CheckedAt: now}
		}
		return r.snapshot
	}
	r.snapshot = domain.ClaudeSubscriptionUsage{State: domain.ClaudeUsageAvailable, Windows: windows, CheckedAt: now}
	return r.snapshot
}

func (r *UsageReader) fetch(ctx context.Context, token string) ([]domain.ClaudeUsageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude usage: status %d", resp.StatusCode)
	}
	var body struct {
		Limits []struct {
			Kind     string     `json:"kind"`
			Group    string     `json:"group"`
			Percent  float64    `json:"percent"`
			Severity string     `json:"severity"`
			ResetsAt *time.Time `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					DisplayName string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	windows := make([]domain.ClaudeUsageWindow, 0, len(body.Limits))
	for _, limit := range body.Limits {
		window := domain.ClaudeUsageWindow{
			Kind: limit.Kind, Group: limit.Group, UsedPercent: limit.Percent,
			Severity: limit.Severity, ResetsAt: limit.ResetsAt,
		}
		if limit.Scope != nil && limit.Scope.Model != nil {
			window.Model = limit.Scope.Model.DisplayName
		}
		windows = append(windows, window)
	}
	return windows, nil
}

// localSubscriptionToken resolves the claude.ai login Claude Code uses. API
// keys and gateway/cloud providers have no subscription windows to report.
func localSubscriptionToken(ctx context.Context) (string, bool) {
	opts := (agentcreds.ResolveOptions{AllowKeychain: true}).WithClaudeSettings(ctx)
	provider, ok := agentcreds.ResolveProvider("", opts)
	if !ok || provider != agentcreds.ProviderFirstParty {
		return "", false
	}
	cred, ok := agentcreds.ResolveLocal(ctx, provider, opts)
	if !ok || cred.Kind != agentcreds.KindOAuthToken {
		return "", false
	}
	return cred.Secret, true
}
