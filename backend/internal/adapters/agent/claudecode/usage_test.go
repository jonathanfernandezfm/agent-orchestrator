package claudecode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUsageReaderParsesCachesAndKeepsStaleSnapshot(t *testing.T) {
	calls, fail := 0, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if fail {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"limits":[
			{"kind":"session","group":"session","percent":32,"severity":"normal","resets_at":"2026-09-25T16:49:59Z","scope":null},
			{"kind":"weekly_scoped","group":"weekly","percent":0,"severity":"normal","resets_at":null,"scope":{"model":{"display_name":"Fable"}}}
		]}`))
	}))
	defer server.Close()

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	reader := &UsageReader{
		URL: server.URL, Client: server.Client(),
		Now:   func() time.Time { return now },
		Token: func(context.Context) (string, bool) { return "tok", true },
	}

	got := reader.SubscriptionUsage(context.Background())
	if got.State != domain.ClaudeUsageAvailable || len(got.Windows) != 2 {
		t.Fatalf("usage = %+v", got)
	}
	if w := got.Windows[0]; w.Kind != "session" || w.UsedPercent != 32 || w.ResetsAt == nil {
		t.Fatalf("session window = %+v", w)
	}
	if got.Windows[1].Model != "Fable" || got.Windows[1].ResetsAt != nil {
		t.Fatalf("scoped window = %+v", got.Windows[1])
	}

	reader.SubscriptionUsage(context.Background())
	if calls != 1 {
		t.Fatalf("calls within TTL = %d, want 1", calls)
	}

	fail = true
	now = now.Add(claudeUsageTTL)
	if stale := reader.SubscriptionUsage(context.Background()); stale.State != domain.ClaudeUsageAvailable || len(stale.Windows) != 2 {
		t.Fatalf("failed refresh should keep last snapshot, got %+v", stale)
	}
	if calls != 2 {
		t.Fatalf("calls after TTL = %d, want 2", calls)
	}

	reader.Token = func(context.Context) (string, bool) { return "", false }
	now = now.Add(claudeUsageTTL)
	if out := reader.SubscriptionUsage(context.Background()); out.State != domain.ClaudeUsageSignedOut {
		t.Fatalf("no token state = %q", out.State)
	}
}
