package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	head    = "a356e3277dd83797cc964ad0a4bce10381d0b1b3"
	base    = "691dee0839034baa9b58f67a2dc73b4493acd0f9"
	prRoute = "/org/Big%20Project/_apis/git/repositories/web/pullRequests/7"
	remote  = "git@ssh.dev.azure.com:v3/org/Big%20Project/web"
)

func fakeADO(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) (*Provider, ports.SCMRepo) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Basic "+base64.StdEncoding.EncodeToString([]byte(":pat")); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		h, ok := routes[r.Method+" "+r.URL.EscapedPath()]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	p, err := NewProvider(ProviderOptions{Token: StaticTokenSource("pat"), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	repo, ok := p.ParseRepository(remote)
	if !ok {
		t.Fatal("ParseRepository rejected the remote")
	}
	return p, repo
}

func reply(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

var activePR = `{
	"pullRequestId": 7, "status": "active", "title": "Add thing", "isDraft": false,
	"createdBy": {"id": "u1", "uniqueName": "me@example.com", "displayName": "Me"},
	"creationDate": "2026-09-24T13:08:47.81309Z", "closedDate": "0001-01-01T00:00:00",
	"sourceRefName": "refs/heads/ao/s-1/root", "targetRefName": "refs/heads/develop",
	"mergeStatus": "succeeded",
	"lastMergeSourceCommit": {"commitId": "` + head + `"},
	"lastMergeTargetCommit": {"commitId": "` + base + `"},
	"reviewers": [{"id": "u2", "uniqueName": "rev@example.com", "vote": -5}],
	"repository": {"id": "r1", "project": {"id": "p1"}}
}`

var threads = `{"value": [
	{"id": 10, "status": "active", "lastUpdatedDate": "2026-09-24T14:00:00Z",
	 "threadContext": {"filePath": "/src/app.ts", "rightFileStart": {"line": 12}},
	 "comments": [{"id": 1, "commentType": "text", "content": "Rename this", "author": {"id": "u2", "uniqueName": "rev@example.com"}}]},
	{"id": 11, "status": "wontFix", "lastUpdatedDate": "2026-09-24T13:09:16Z",
	 "comments": [{"id": 1, "commentType": "text", "content": "AI summary", "author": {"id": "b1", "uniqueName": "", "displayName": "Big Project Build Service (org)", "descriptor": "svc.abc"}}]},
	{"id": 12, "lastUpdatedDate": "2026-09-24T15:00:00Z", "properties": {"CodeReviewThreadType": {"$value": "VoteUpdate"}},
	 "comments": [{"id": 1, "commentType": "system", "content": "voted", "author": {"id": "u2"}}]}
]}`

var evaluations = `{"value": [
	{"status": "rejected", "configuration": {"isEnabled": true, "isBlocking": true, "type": {"id": "` + policyBuild + `"}, "settings": {}},
	 "context": {"buildId": 99, "buildDefinitionName": "CI"}},
	{"status": "rejected", "configuration": {"isEnabled": true, "isBlocking": false, "type": {"id": "` + policyBuild + `"}, "settings": {"displayName": "Optional"}},
	 "context": {"buildId": 100}},
	{"status": "queued", "configuration": {"isEnabled": true, "isBlocking": true, "type": {"id": "` + policyMinReviewers + `"}}}
]}`

var statuses = `{"value": [
	{"id": 1, "state": "pending", "context": {"genre": "SonarCloud", "name": "quality gate"}},
	{"id": 2, "state": "succeeded", "targetUrl": "https://sonar/x", "context": {"genre": "SonarCloud", "name": "quality gate"}}
]}`

func detailRoutes() map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		"GET " + prRoute:                                  reply(activePR),
		"GET " + prRoute + "/threads":                     reply(threads),
		"GET " + prRoute + "/statuses":                    reply(statuses),
		"GET /org/Big%20Project/_apis/policy/evaluations": reply(evaluations),
	}
}

func TestParseRepositoryNormalizesRemote(t *testing.T) {
	p, _ := NewProvider(ProviderOptions{Token: StaticTokenSource("pat")})
	repo, ok := p.ParseRepository("https://org.visualstudio.com/Big%20Project/_git/web")
	want := ports.SCMRepo{Provider: domain.AzureDevOpsProvider, Host: "dev.azure.com", Owner: "org/Big Project", Name: "web", Repo: "org/Big Project/web"}
	if !ok || repo != want {
		t.Fatalf("ParseRepository = %+v, %v", repo, ok)
	}
	if _, ok := p.ParseRepository("git@github.com:org/web.git"); ok {
		t.Fatal("GitHub remote accepted")
	}
}

func TestFetchPullRequestsMapsDetail(t *testing.T) {
	p, repo := fakeADO(t, detailRoutes())
	// repoForTrackedPR splits Repo at the first slash; the adapter must cope.
	repo.Owner, repo.Name = "org", "Big Project/web"
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{{Repo: repo, Number: 7}})
	if err != nil || len(obs) != 1 || !obs[0].Fetched {
		t.Fatalf("FetchPullRequests = %+v, %v", obs, err)
	}
	o := obs[0]
	if o.PR.URL != "https://dev.azure.com/org/Big%20Project/_git/web/pullrequest/7" || o.PR.State != "open" ||
		o.PR.SourceBranch != "ao/s-1/root" || o.PR.TargetBranch != "develop" || o.PR.HeadSHA != head ||
		o.PR.BaseSHA != base || o.PR.Author != "me@example.com" || o.PR.HeadRepo != "org/Big Project/web" {
		t.Fatalf("PR = %+v", o.PR)
	}
	if want := time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC); !o.PR.UpdatedAtProvider.Equal(want) {
		t.Fatalf("UpdatedAtProvider = %v, want newest thread activity %v", o.PR.UpdatedAtProvider, want)
	}
	if o.CI.Summary != string(domain.CIFailing) || o.CI.HeadSHA != head || len(o.CI.Checks) != 3 {
		t.Fatalf("CI = %+v", o.CI)
	}
	if len(o.CI.FailedChecks) != 1 || o.CI.FailedChecks[0].Name != "CI" || o.CI.FailedChecks[0].ProviderID != "build:99" {
		t.Fatalf("failed checks = %+v (optional build must not be actionable)", o.CI.FailedChecks)
	}
	for _, c := range o.CI.Checks {
		if c.Name == "SonarCloud/quality gate" && c.Status != string(domain.PRCheckPassed) {
			t.Fatalf("latest status must win: %+v", c)
		}
	}
	if o.Review.Decision != string(domain.ReviewChangesRequest) {
		t.Fatalf("review = %q", o.Review.Decision)
	}
	if o.Mergeability.State != string(domain.MergeBlocked) || strings.Join(o.Mergeability.Blockers, ",") != "ci_failing,changes_requested,review_required" {
		t.Fatalf("mergeability = %+v", o.Mergeability)
	}
}

func TestFetchPullRequestsPlaceholderOnFailure(t *testing.T) {
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET " + prRoute: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"message":"gone"}`, http.StatusNotFound) },
	})
	obs, err := p.FetchPullRequests(context.Background(), []ports.SCMPRRef{{Repo: repo, Number: 7}})
	if err == nil || len(obs) != 1 || obs[0].Fetched || !errors.Is(obs[0].Error, ports.ErrSCMNotFound) {
		t.Fatalf("obs = %+v, err = %v", obs, err)
	}
}

func TestFetchReviewThreads(t *testing.T) {
	p, repo := fakeADO(t, detailRoutes())
	rev, err := p.FetchReviewThreads(context.Background(), ports.SCMPRRef{Repo: repo, Number: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Threads) != 2 {
		t.Fatalf("threads = %+v (system vote thread must be dropped)", rev.Threads)
	}
	human, bot := rev.Threads[0], rev.Threads[1]
	if human.ID != "10" || human.Resolved || human.IsBot || human.Path != "src/app.ts" || human.Line != 12 {
		t.Fatalf("human thread = %+v", human)
	}
	if c := human.Comments[0]; c.ID != "10-1" || c.Author != "rev@example.com" || c.Body != "Rename this" ||
		c.URL != "https://dev.azure.com/org/Big%20Project/_git/web/pullrequest/7?discussionId=10&commentId=1" {
		t.Fatalf("comment = %+v", c)
	}
	if !bot.Resolved || !bot.IsBot || bot.Comments[0].Author != "Big Project Build Service (org)" {
		t.Fatalf("bot thread = %+v", bot)
	}
	if rev.Decision != string(domain.ReviewChangesRequest) || len(rev.Reviews) != 1 || rev.Reviews[0].Author != "rev@example.com" {
		t.Fatalf("review = %+v", rev)
	}
}

func TestReviewDecisionAndMergeability(t *testing.T) {
	reviewerPolicy := restEvaluation{Status: "approved"}
	reviewerPolicy.Configuration.IsBlocking = true
	reviewerPolicy.Configuration.Type.ID = policyMinReviewers
	approvedVote := []restReviewer{{Vote: 10}}
	if got := reviewDecision(approvedVote, []restEvaluation{reviewerPolicy}); got != domain.ReviewApproved {
		t.Fatalf("decision = %q", got)
	}
	if got := reviewDecision(nil, nil); got != domain.ReviewNone {
		t.Fatalf("decision = %q", got)
	}
	pr := &restPR{Status: "active", MergeStatus: "succeeded"}
	if m := mergeability(pr, []restEvaluation{reviewerPolicy}, string(domain.CIPassing), domain.ReviewApproved); m.State != string(domain.MergeMergeable) || !m.Mergeable {
		t.Fatalf("mergeability = %+v", m)
	}
	pr.MergeStatus = "conflicts"
	if m := mergeability(pr, nil, "", ""); m.State != string(domain.MergeConflicting) {
		t.Fatalf("mergeability = %+v", m)
	}
	pr.MergeStatus = "queued"
	if m := mergeability(pr, nil, "", ""); m.State != string(domain.MergeUnknown) {
		t.Fatalf("mergeability = %+v", m)
	}
	workItems := restEvaluation{Status: "rejected"}
	workItems.Configuration.IsBlocking = true
	pr.MergeStatus = "succeeded"
	if m := mergeability(pr, []restEvaluation{workItems}, string(domain.CIPassing), domain.ReviewApproved); m.State != string(domain.MergeBlocked) || m.Blockers[0] != "blocked_by_provider" {
		t.Fatalf("mergeability = %+v", m)
	}
}

func TestPRStates(t *testing.T) {
	r := domain.AzureDevOpsRepo{Org: "org", Project: "p", Name: "r"}
	merged := prObservation(r, &restPR{PullRequestID: 1, Status: "completed", LastMergeCommit: restCommit{CommitID: "abc"}})
	if !merged.Merged || merged.State != "merged" || merged.MergeCommitSHA != "abc" {
		t.Fatalf("completed = %+v", merged)
	}
	if closed := prObservation(r, &restPR{PullRequestID: 1, Status: "abandoned"}); !closed.Closed || closed.State != "closed" {
		t.Fatalf("abandoned = %+v", closed)
	}
	if draft := prObservation(r, &restPR{PullRequestID: 1, Status: "active", IsDraft: true}); draft.State != "draft" {
		t.Fatalf("draft = %+v", draft)
	}
}

func TestListPRsByRepoFiltersActive(t *testing.T) {
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /org/Big%20Project/_apis/git/repositories/web/pullrequests": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("searchCriteria.status") != "active" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"value": [`+activePR+`]}`)
		},
	})
	prs, err := p.ListPRsByRepo(context.Background(), repo, time.Time{})
	if err != nil || len(prs) != 1 || prs[0].Number != 7 || prs[0].Author != "me@example.com" {
		t.Fatalf("ListPRsByRepo = %+v, %v", prs, err)
	}
}

func TestAuthenticatedIdentityUsesKnownOrg(t *testing.T) {
	calls := 0
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /org/_apis/connectionData": func(w http.ResponseWriter, _ *http.Request) {
			calls++
			_, _ = io.WriteString(w, `{"authenticatedUser": {"providerDisplayName": "Me", "properties": {"Account": {"$value": "me@example.com"}}}}`)
		},
	})
	for range 2 {
		id, err := p.AuthenticatedIdentityForHost(context.Background(), repo.Host)
		if err != nil || id.Login != "me@example.com" || !id.Human {
			t.Fatalf("identity = %+v, %v", id, err)
		}
	}
	if calls != 1 {
		t.Fatalf("connectionData calls = %d, want cached after first", calls)
	}
}

func TestMergePullRequestSquashCompletesWithHeadGuard(t *testing.T) {
	completionPollInterval = time.Millisecond
	gets := 0
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"PATCH " + prRoute: func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Status                string `json:"status"`
				LastMergeSourceCommit struct {
					CommitID string `json:"commitId"`
				} `json:"lastMergeSourceCommit"`
				CompletionOptions struct {
					MergeStrategy string `json:"mergeStrategy"`
				} `json:"completionOptions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Status != "completed" || body.LastMergeSourceCommit.CommitID != head || body.CompletionOptions.MergeStrategy != "squash" {
				t.Errorf("merge body = %+v", body)
			}
			_, _ = io.WriteString(w, `{"pullRequestId": 7, "status": "active", "mergeStatus": "queued"}`)
		},
		"GET " + prRoute: func(w http.ResponseWriter, _ *http.Request) {
			gets++
			_, _ = io.WriteString(w, `{"pullRequestId": 7, "status": "completed", "lastMergeCommit": {"commitId": "merged1"}}`)
		},
	})
	res, err := p.MergePullRequest(context.Background(), ports.SCMMergeRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, ExpectedHeadSHA: head, Method: ports.SCMMergeSquash})
	if err != nil || res.MergeCommitSHA != "merged1" || gets != 1 {
		t.Fatalf("merge = %+v, %v (gets %d)", res, err, gets)
	}
}

func TestMergePullRequestMapsErrors(t *testing.T) {
	for status, want := range map[int]error{http.StatusConflict: ports.ErrSCMHeadChanged, http.StatusBadRequest: ports.ErrSCMNotMergeable, http.StatusNotFound: ports.ErrSCMNotFound} {
		p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
			"PATCH " + prRoute: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, `{"message":"no"}`, status) },
		})
		_, err := p.MergePullRequest(context.Background(), ports.SCMMergeRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, ExpectedHeadSHA: head, Method: ports.SCMMergeSquash})
		if !errors.Is(err, want) {
			t.Errorf("HTTP %d: err = %v, want %v", status, err, want)
		}
	}
}

func TestResolveReviewThreadMarksFixed(t *testing.T) {
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"PATCH " + prRoute + "/threads/10": func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if string(b) != `{"status":"fixed"}` {
				t.Errorf("body = %s", b)
			}
			_, _ = io.WriteString(w, `{}`)
		},
	})
	if err := p.ResolveReviewThread(context.Background(), ports.SCMReviewResolveRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, ThreadID: "10"}); err != nil {
		t.Fatal(err)
	}
	if err := p.ResolveReviewThread(context.Background(), ports.SCMReviewResolveRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, ThreadID: "x"}); err == nil {
		t.Fatal("non-numeric thread id accepted")
	}
}

func TestRequestReviewAddsKnownReviewer(t *testing.T) {
	routes := detailRoutes()
	put := false
	routes["PUT "+prRoute+"/reviewers/u2"] = func(w http.ResponseWriter, _ *http.Request) {
		put = true
		_, _ = io.WriteString(w, `{}`)
	}
	p, repo := fakeADO(t, routes)
	if err := p.RequestReview(context.Background(), ports.SCMReviewRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, Reviewer: "@REV@example.com"}); err != nil || !put {
		t.Fatalf("RequestReview err = %v, put = %v", err, put)
	}
	if err := p.RequestReview(context.Background(), ports.SCMReviewRequest{PR: ports.SCMPRRef{Repo: repo, Number: 7}, Reviewer: "stranger@example.com"}); !errors.Is(err, ports.ErrSCMNotFound) {
		t.Fatalf("unknown reviewer err = %v", err)
	}
}

func TestFetchFailedCheckLogTail(t *testing.T) {
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET /org/Big%20Project/_apis/build/builds/99/timeline": reply(`{"records": [
			{"type": "Task", "result": "succeeded", "log": {"id": 3}},
			{"type": "Task", "result": "failed", "log": {"id": 5}}]}`),
		"GET /org/Big%20Project/_apis/build/builds/99/logs/5": reply(strings.Repeat("noise\n", 50) + "error TS2304\n"),
	})
	tail, err := p.FetchFailedCheckLogTail(context.Background(), repo, ports.SCMCheckObservation{Name: "CI", ProviderID: "build:99"})
	if err != nil || len(strings.Split(tail, "\n")) != ciFailureLogTailLines || !strings.HasSuffix(tail, "error TS2304") {
		t.Fatalf("tail = %q, %v", tail, err)
	}
	if _, err := p.FetchFailedCheckLogTail(context.Background(), repo, ports.SCMCheckObservation{Name: "Sonar"}); err == nil {
		t.Fatal("status check without build accepted")
	}
}

func TestAuthFailureAndRateLimit(t *testing.T) {
	p, repo := fakeADO(t, map[string]func(http.ResponseWriter, *http.Request){
		"GET " + prRoute: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNonAuthoritativeInfo) },
		"GET /org/Big%20Project/_apis/git/repositories/web/pullrequests": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	})
	if _, err := p.getPR(context.Background(), domain.AzureDevOpsRepo{Org: "org", Project: "Big Project", Name: "web"}, 7); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("203 err = %v", err)
	}
	_, err := p.ListPRsByRepo(context.Background(), repo, time.Time{})
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.GetRetryAfter() != 42*time.Second {
		t.Fatalf("429 err = %v", err)
	}
}

func TestAzCLITokenSource(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	runs := 0
	src := &AzCLITokenSource{Clock: func() time.Time { return now }, Run: func(context.Context) ([]byte, error) {
		runs++
		if runs == 1 {
			return nil, errors.New("not logged in")
		}
		return []byte(`{"accessToken": "tok", "expires_on": ` + strings.TrimSpace(jsonInt(now.Add(time.Hour).Unix())) + `}`), nil
	}}
	if _, err := src.Credential(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("first err = %v", err)
	}
	if _, err := src.Credential(context.Background()); !errors.Is(err, ErrNoToken) || runs != 1 {
		t.Fatalf("failure must be cached: err = %v, runs = %d", err, runs)
	}
	now = now.Add(2 * time.Minute)
	for range 2 {
		cred, err := src.Credential(context.Background())
		if err != nil || cred.Token != "tok" || !cred.Bearer || cred.authorization() != "Bearer tok" {
			t.Fatalf("cred = %+v, %v", cred, err)
		}
	}
	if runs != 2 {
		t.Fatalf("runs = %d, want token cached", runs)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
