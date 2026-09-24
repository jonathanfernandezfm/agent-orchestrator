// Package azuredevops observes and mutates Azure DevOps Services pull requests
// for AO's provider-neutral SCM integration, over the Azure DevOps REST API.
//
// # Authentication
//
// A personal access token from AO_AZURE_DEVOPS_TOKEN (or AZURE_DEVOPS_EXT_PAT,
// the variable the `az devops` extension reads) is sent as Basic auth. Without
// one, the provider mints an Entra access token with `az account
// get-access-token`, so a plain `az login` is enough.
//
// # Repository identity
//
// Every remote form normalizes to host dev.azure.com, Owner "org/project",
// Name "repo" and Repo "org/project/repo". Callers elsewhere split Repo at the
// first slash, so the adapter always re-derives org/project/repo from Repo.
//
// # Polling
//
// Azure DevOps offers neither ETags nor an updated-since filter for pull
// requests, so both guards return an empty ETag. The observer then fetches
// every tracked PR each tick, which also covers terminal transitions of PRs
// that drop out of the active listing. UpdatedAtProvider is the newest thread
// activity, which drives review-thread refreshes when comments change.
//
// # State mapping
//
//   - PR: active → open/draft, completed → merged, abandoned → closed.
//   - CI: build-validation policy evaluations plus PR statuses. Failures of
//     optional (non-blocking) builds are informational only.
//   - Review: any negative vote → changes_requested; an unmet blocking reviewer
//     policy → review_required; any positive vote → approved.
//   - Mergeability: mergeStatus plus unmet blocking policies.
package azuredevops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	providerKey                 = domain.AzureDevOpsProvider
	listPageSize                = 100
	listMaxPages                = 10
	fetchConcurrency            = 5
	ciFailureLogTailLines       = 20
	reviewCommentLimitPerThread = 5
	previewAPIVersion           = "7.1-preview"
)

// Well-known policy type ids.
const (
	policyBuild          = "0609b952-1397-4640-95ec-e00a01b2c241"
	policyMinReviewers   = "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd"
	policyRequiredReview = "fd2167ab-b0be-447a-8ec8-39368250530e"
)

// ProviderOptions configures the Azure DevOps SCM provider.
type ProviderOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	// BaseURL overrides https://dev.azure.com (tests).
	BaseURL string
	Logger  *slog.Logger
}

// Provider implements the SCM observer contract plus merge, review-request and
// thread-resolve mutations for Azure DevOps.
type Provider struct {
	client *Client
	logger *slog.Logger

	mu       sync.Mutex
	orgs     map[string]bool
	identity *ports.SCMIdentity
}

// NewProvider builds an Azure DevOps provider.
func NewProvider(opts ProviderOptions) (*Provider, error) {
	if opts.Token == nil {
		return nil, ErrNoToken
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{client: newClient(opts.HTTPClient, opts.Token, opts.BaseURL), logger: logger, orgs: map[string]bool{}}, nil
}

// ---------------------------------------------------------------------------
// Repository identity
// ---------------------------------------------------------------------------

// ParseRepository accepts any Azure DevOps Git remote form.
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	r, ok := domain.ParseAzureDevOpsRemote(remote)
	if !ok {
		return ports.SCMRepo{}, false
	}
	p.mu.Lock()
	p.orgs[r.Org] = true
	p.mu.Unlock()
	return scmRepo(r), true
}

func scmRepo(r domain.AzureDevOpsRepo) ports.SCMRepo {
	return ports.SCMRepo{Provider: providerKey, Host: domain.AzureDevOpsHost, Owner: r.Namespace(), Name: r.Name, Repo: r.FullName()}
}

func coords(repo ports.SCMRepo) (domain.AzureDevOpsRepo, error) {
	if r, ok := domain.AzureDevOpsRepoFromFullName(repo.Repo); ok {
		return r, nil
	}
	if r, ok := domain.AzureDevOpsRepoFromFullName(repo.Owner + "/" + repo.Name); ok {
		return r, nil
	}
	return domain.AzureDevOpsRepo{}, fmt.Errorf("azure devops scm: invalid repository %q", repo.Repo)
}

func projectPath(r domain.AzureDevOpsRepo) string {
	return "/" + url.PathEscape(r.Org) + "/" + url.PathEscape(r.Project)
}

func repoPath(r domain.AzureDevOpsRepo) string {
	return projectPath(r) + "/_apis/git/repositories/" + url.PathEscape(r.Name)
}

func prPath(r domain.AzureDevOpsRepo, n int) string {
	return repoPath(r) + "/pullRequests/" + strconv.Itoa(n)
}

// ---------------------------------------------------------------------------
// Identity and credentials
// ---------------------------------------------------------------------------

// AuthenticatedIdentityForHost resolves the signed-in account through the
// connectionData endpoint of an organization seen by ParseRepository. Login
// is the account's unique name, the same value PR authors carry.
func (p *Provider) AuthenticatedIdentityForHost(ctx context.Context, _ string) (ports.SCMIdentity, error) {
	p.mu.Lock()
	if p.identity != nil {
		id := *p.identity
		p.mu.Unlock()
		return id, nil
	}
	orgs := make([]string, 0, len(p.orgs))
	for org := range p.orgs {
		orgs = append(orgs, org)
	}
	p.mu.Unlock()
	sort.Strings(orgs)
	if len(orgs) == 0 {
		return ports.SCMIdentity{}, fmt.Errorf("azure devops scm: no organization known yet")
	}
	var lastErr error
	for _, org := range orgs {
		var resp struct {
			AuthenticatedUser struct {
				ProviderDisplayName string `json:"providerDisplayName"`
				Properties          struct {
					Account struct {
						Value string `json:"$value"`
					} `json:"Account"`
				} `json:"properties"`
			} `json:"authenticatedUser"`
		}
		q := url.Values{"api-version": {previewAPIVersion}}
		if err := p.client.getJSON(ctx, "/"+url.PathEscape(org)+"/_apis/connectionData", q, &resp); err != nil {
			lastErr = err
			continue
		}
		login := strings.TrimSpace(resp.AuthenticatedUser.Properties.Account.Value)
		if login == "" {
			lastErr = fmt.Errorf("azure devops scm: connectionData returned no account")
			continue
		}
		id := ports.SCMIdentity{Login: login, Human: !isBotIdentity(restIdentity{UniqueName: login, DisplayName: resp.AuthenticatedUser.ProviderDisplayName})}
		p.mu.Lock()
		p.identity = &id
		p.mu.Unlock()
		return id, nil
	}
	return ports.SCMIdentity{}, lastErr
}

// SCMCredentialsAvailable reports whether a credential can be obtained.
func (p *Provider) SCMCredentialsAvailable(ctx context.Context) (bool, error) {
	_, err := p.client.tokens.Credential(ctx)
	if errors.Is(err, ErrNoToken) {
		return false, nil
	}
	return err == nil, err
}

// ---------------------------------------------------------------------------
// Guards and listing
// ---------------------------------------------------------------------------

// RepoPRListGuard never reports NotModified: Azure DevOps has no ETags here.
func (p *Provider) RepoPRListGuard(context.Context, ports.SCMRepo, string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{}, nil
}

// CommitChecksGuard never reports NotModified; CI is re-read with each fetch.
func (p *Provider) CommitChecksGuard(context.Context, ports.SCMRepo, string, string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{}, nil
}

// ListPRsByRepo lists active pull requests. Azure DevOps cannot filter by
// update time, so updatedAfter is ignored; terminal PRs are observed through
// the per-PR detail fetch.
func (p *Provider) ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, _ time.Time) ([]ports.SCMPRObservation, error) {
	r, err := coords(repo)
	if err != nil {
		return nil, err
	}
	var out []ports.SCMPRObservation
	for page := 0; page < listMaxPages; page++ {
		q := url.Values{
			"searchCriteria.status": {"active"},
			"$top":                  {strconv.Itoa(listPageSize)},
			"$skip":                 {strconv.Itoa(page * listPageSize)},
		}
		var resp struct {
			Value []restPR `json:"value"`
		}
		if err := p.client.getJSON(ctx, repoPath(r)+"/pullrequests", q, &resp); err != nil {
			return nil, err
		}
		for i := range resp.Value {
			out = append(out, prObservation(r, &resp.Value[i]))
		}
		if len(resp.Value) < listPageSize {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Pull request detail
// ---------------------------------------------------------------------------

// FetchPullRequests fetches detailed observations aligned with refs. Failed
// refs get a Fetched=false placeholder carrying the error.
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	results := make([]ports.SCMObservation, len(refs))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, fetchConcurrency)
	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, ref ports.SCMPRRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			obs, err := p.fetchPR(ctx, ref)
			if err != nil {
				p.logger.Warn("azure devops scm: fetch PR failed", "repo", ref.Repo.Repo, "pr", ref.Number, "err", err)
				obs = ports.SCMObservation{Provider: providerKey, Host: ref.Repo.Host, Repo: ref.Repo.Repo, PR: ports.SCMPRObservation{Number: ref.Number, URL: ref.URL}, Error: err}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
			results[idx] = obs
		}(i, ref)
	}
	wg.Wait()
	return results, firstErr
}

func (p *Provider) fetchPR(ctx context.Context, ref ports.SCMPRRef) (ports.SCMObservation, error) {
	r, err := coords(ref.Repo)
	if err != nil {
		return ports.SCMObservation{}, err
	}
	pr, err := p.getPR(ctx, r, ref.Number)
	if err != nil {
		return ports.SCMObservation{}, err
	}
	threads, err := p.getThreads(ctx, r, ref.Number)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("fetch threads: %w", err)
	}
	evals, err := p.getEvaluations(ctx, r, pr)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("fetch policy evaluations: %w", err)
	}
	var statuses struct {
		Value []restStatus `json:"value"`
	}
	if err := p.client.getJSON(ctx, prPath(r, ref.Number)+"/statuses", nil, &statuses); err != nil {
		return ports.SCMObservation{}, fmt.Errorf("fetch statuses: %w", err)
	}

	prObs := prObservation(r, pr)
	if u := strings.TrimSpace(ref.URL); u != "" && u != prObs.URL {
		prObs.URLAlias = u
	}
	for _, t := range threads {
		if updated := time.Time(t.LastUpdatedDate); updated.After(prObs.UpdatedAtProvider) {
			prObs.UpdatedAtProvider = updated
		}
	}
	ci := ciObservation(r, prObs.HeadSHA, evals, statuses.Value)
	decision := reviewDecision(pr.Reviewers, evals)
	return ports.SCMObservation{
		Fetched:      true,
		ObservedAt:   time.Now(),
		Provider:     providerKey,
		Host:         domain.AzureDevOpsHost,
		Repo:         r.FullName(),
		PR:           prObs,
		CI:           ci,
		Review:       ports.SCMReviewObservation{Decision: string(decision)},
		Mergeability: mergeability(pr, evals, ci.Summary, decision),
	}, nil
}

func (p *Provider) getPR(ctx context.Context, r domain.AzureDevOpsRepo, n int) (*restPR, error) {
	var pr restPR
	if err := p.client.getJSON(ctx, prPath(r, n), nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (p *Provider) getThreads(ctx context.Context, r domain.AzureDevOpsRepo, n int) ([]restThread, error) {
	var resp struct {
		Value []restThread `json:"value"`
	}
	if err := p.client.getJSON(ctx, prPath(r, n)+"/threads", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Value, nil
}

func (p *Provider) getEvaluations(ctx context.Context, r domain.AzureDevOpsRepo, pr *restPR) ([]restEvaluation, error) {
	if pr.Repository.Project.ID == "" {
		return nil, nil
	}
	q := url.Values{
		"artifactId":  {fmt.Sprintf("vstfs:///CodeReview/CodeReviewId/%s/%d", pr.Repository.Project.ID, pr.PullRequestID)},
		"api-version": {previewAPIVersion},
	}
	var resp struct {
		Value []restEvaluation `json:"value"`
	}
	if err := p.client.getJSON(ctx, projectPath(r)+"/_apis/policy/evaluations", q, &resp); err != nil {
		return nil, err
	}
	out := resp.Value[:0]
	for _, e := range resp.Value {
		if e.Configuration.IsEnabled && !e.Configuration.IsDeleted {
			out = append(out, e)
		}
	}
	return out, nil
}

func prObservation(r domain.AzureDevOpsRepo, pr *restPR) ports.SCMPRObservation {
	webURL := r.PRURL(pr.PullRequestID)
	obs := ports.SCMPRObservation{
		ProviderID:               strings.ToLower(r.Org) + "/" + strconv.Itoa(pr.PullRequestID),
		URL:                      webURL,
		HTMLURL:                  webURL,
		Number:                   pr.PullRequestID,
		Draft:                    pr.IsDraft,
		SourceBranch:             strings.TrimPrefix(pr.SourceRefName, "refs/heads/"),
		HeadRepo:                 r.FullName(),
		TargetBranch:             strings.TrimPrefix(pr.TargetRefName, "refs/heads/"),
		HeadSHA:                  pr.LastMergeSourceCommit.CommitID,
		BaseSHA:                  pr.LastMergeTargetCommit.CommitID,
		Title:                    pr.Title,
		Author:                   pr.CreatedBy.login(),
		ProviderState:            pr.Status,
		ProviderMergeable:        pr.MergeStatus,
		ProviderMergeStateStatus: pr.MergeStatus,
		CreatedAtProvider:        time.Time(pr.CreationDate),
		UpdatedAtProvider:        time.Time(pr.CreationDate),
		ClosedAtProvider:         time.Time(pr.ClosedDate),
	}
	if pr.ForkSource != nil {
		fork := pr.ForkSource.Repository
		obs.HeadRepo = r.Org + "/" + fork.Project.Name + "/" + fork.Name
	}
	switch pr.Status {
	case "completed":
		obs.State, obs.Merged = string(domain.PRStateMerged), true
		obs.MergedAtProvider = time.Time(pr.ClosedDate)
		obs.MergeCommitSHA = pr.LastMergeCommit.CommitID
	case "abandoned":
		obs.State, obs.Closed = string(domain.PRStateClosed), true
	default:
		obs.State = string(domain.PRStateOpen)
		if pr.IsDraft {
			obs.State = string(domain.PRStateDraft)
		}
	}
	if updated := time.Time(pr.ClosedDate); updated.After(obs.UpdatedAtProvider) {
		obs.UpdatedAtProvider = updated
	}
	return obs
}

// ---------------------------------------------------------------------------
// CI
// ---------------------------------------------------------------------------

func ciObservation(r domain.AzureDevOpsRepo, headSHA string, evals []restEvaluation, statuses []restStatus) ports.SCMCIObservation {
	var checks, failed []ports.SCMCheckObservation
	for _, e := range evals {
		if e.Configuration.Type.ID != policyBuild {
			continue
		}
		check := ports.SCMCheckObservation{
			Name:       firstNonEmpty(e.Configuration.Settings.DisplayName, e.Context.BuildDefinitionName, "Build"),
			Status:     string(buildCheckStatus(e)),
			Conclusion: e.Status,
		}
		if e.Context.BuildID > 0 {
			check.URL = fmt.Sprintf("https://%s%s/_build/results?buildId=%d", domain.AzureDevOpsHost, projectPath(r), e.Context.BuildID)
			check.ProviderID = "build:" + strconv.Itoa(e.Context.BuildID)
		}
		checks = append(checks, check)
		if isFailing(check.Status) && e.Configuration.IsBlocking {
			failed = append(failed, check)
		}
	}
	// Statuses are append-only; the highest id per context is the current one.
	latest := map[string]restStatus{}
	for _, s := range statuses {
		key := s.name()
		if prev, ok := latest[key]; !ok || s.ID > prev.ID {
			latest[key] = s
		}
	}
	for name, s := range latest {
		check := ports.SCMCheckObservation{Name: name, Status: string(statusCheckStatus(s.State)), Conclusion: s.State, URL: s.TargetURL}
		checks = append(checks, check)
		if isFailing(check.Status) {
			failed = append(failed, check)
		}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })

	summary := domain.CIUnknown
	for _, c := range checks {
		switch domain.PRCheckStatus(c.Status) {
		case domain.PRCheckQueued, domain.PRCheckInProgress:
			summary = domain.CIPending
		case domain.PRCheckPassed:
			if summary == domain.CIUnknown {
				summary = domain.CIPassing
			}
		}
	}
	if len(failed) > 0 {
		summary = domain.CIFailing
	}
	return ports.SCMCIObservation{
		Summary:           string(summary),
		HeadSHA:           headSHA,
		FailedFingerprint: failedFingerprint(headSHA, failed),
		Checks:            checks,
		FailedChecks:      failed,
	}
}

func buildCheckStatus(e restEvaluation) domain.PRCheckStatus {
	if e.Context.IsExpired || e.Context.BuildIsNotCurrent {
		return domain.PRCheckQueued
	}
	switch e.Status {
	case "approved":
		return domain.PRCheckPassed
	case "rejected", "broken":
		return domain.PRCheckFailed
	case "running":
		return domain.PRCheckInProgress
	case "queued":
		return domain.PRCheckQueued
	case "notApplicable":
		return domain.PRCheckSkipped
	default:
		return domain.PRCheckUnknown
	}
}

func statusCheckStatus(state string) domain.PRCheckStatus {
	switch state {
	case "succeeded":
		return domain.PRCheckPassed
	case "failed", "error":
		return domain.PRCheckFailed
	case "pending":
		return domain.PRCheckInProgress
	case "notApplicable":
		return domain.PRCheckSkipped
	default:
		return domain.PRCheckUnknown
	}
}

func isFailing(status string) bool {
	return status == string(domain.PRCheckFailed) || status == string(domain.PRCheckCancelled)
}

func failedFingerprint(headSHA string, checks []ports.SCMCheckObservation) string {
	if len(checks) == 0 {
		return ""
	}
	parts := make([]string, len(checks))
	for i, c := range checks {
		parts[i] = headSHA + "\x00" + c.Name + "\x00" + c.Status + "\x00" + c.URL + "\x00" + c.ProviderID
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(h[:])
}

// FetchFailedCheckLogTail returns the tail of the last failed task log of a
// build-validation run. PR statuses carry no logs.
func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	buildID, ok := strings.CutPrefix(check.ProviderID, "build:")
	if !ok || buildID == "" {
		return "", fmt.Errorf("azure devops scm: check %q has no build log", check.Name)
	}
	r, err := coords(repo)
	if err != nil {
		return "", err
	}
	buildPath := projectPath(r) + "/_apis/build/builds/" + url.PathEscape(buildID)
	var timeline struct {
		Records []struct {
			Type   string `json:"type"`
			Result string `json:"result"`
			Order  int    `json:"order"`
			Log    *struct {
				ID int `json:"id"`
			} `json:"log"`
		} `json:"records"`
	}
	if err := p.client.getJSON(ctx, buildPath+"/timeline", nil, &timeline); err != nil {
		return "", err
	}
	logID := 0
	for _, rec := range timeline.Records {
		if rec.Type == "Task" && rec.Result == "failed" && rec.Log != nil {
			logID = rec.Log.ID
		}
	}
	if logID == 0 {
		return "", fmt.Errorf("azure devops scm: build %s has no failed task log", buildID)
	}
	body, err := p.client.getTail(ctx, buildPath+"/logs/"+strconv.Itoa(logID), logTailMaxBytes)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) > ciFailureLogTailLines {
		lines = lines[len(lines)-ciFailureLogTailLines:]
	}
	return strings.Join(lines, "\n"), nil
}

// ---------------------------------------------------------------------------
// Review
// ---------------------------------------------------------------------------

// FetchReviewThreads returns comment threads, vote summaries and the review
// decision of a pull request.
func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	r, err := coords(ref.Repo)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	pr, err := p.getPR(ctx, r, ref.Number)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	raw, err := p.getThreads(ctx, r, ref.Number)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	evals, err := p.getEvaluations(ctx, r, pr)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	prURL := r.PRURL(ref.Number)
	var threads []ports.SCMReviewThreadObservation
	for _, t := range raw {
		if obs, ok := threadObservation(prURL, t); ok {
			threads = append(threads, obs)
		}
	}
	var reviews []ports.SCMReviewSummaryObservation
	for _, rv := range pr.Reviewers {
		if rv.Vote == 0 || rv.IsContainer {
			continue
		}
		state := domain.ReviewApproved
		if rv.Vote < 0 {
			state = domain.ReviewChangesRequest
		}
		reviews = append(reviews, ports.SCMReviewSummaryObservation{
			ID:     "vote:" + rv.ID,
			Author: rv.login(),
			State:  string(state),
			URL:    prURL,
			IsBot:  isBotIdentity(rv.restIdentity),
		})
	}
	return ports.SCMReviewObservation{
		Decision: string(reviewDecision(pr.Reviewers, evals)),
		Reviews:  reviews,
		Threads:  threads,
	}, nil
}

func threadObservation(prURL string, t restThread) (ports.SCMReviewThreadObservation, bool) {
	if t.IsDeleted || t.isSystem() {
		return ports.SCMReviewThreadObservation{}, false
	}
	threadID := strconv.Itoa(t.ID)
	obs := ports.SCMReviewThreadObservation{ID: threadID, Resolved: threadResolved(t.Status), IsBot: true}
	if tc := t.ThreadContext; tc != nil {
		obs.Path = strings.TrimPrefix(tc.FilePath, "/")
		switch {
		case tc.RightFileStart != nil:
			obs.Line = tc.RightFileStart.Line
		case tc.LeftFileStart != nil:
			obs.Line = tc.LeftFileStart.Line
		}
	}
	for _, c := range t.Comments {
		if c.IsDeleted || c.CommentType == "system" {
			continue
		}
		bot := isBotIdentity(c.Author)
		if !bot {
			obs.IsBot = false
		}
		if len(obs.Comments) < reviewCommentLimitPerThread {
			obs.Comments = append(obs.Comments, ports.SCMReviewCommentObservation{
				ID:     fmt.Sprintf("%s-%d", threadID, c.ID),
				Author: c.Author.login(),
				Body:   c.Content,
				URL:    fmt.Sprintf("%s?discussionId=%s&commentId=%d", prURL, threadID, c.ID),
				IsBot:  bot,
			})
		}
	}
	return obs, len(obs.Comments) > 0
}

// threadResolved maps thread status. Threads without a status are plain
// comments that cannot be resolved, so they never block.
func threadResolved(status string) bool {
	return status != "active" && status != "pending"
}

func reviewDecision(reviewers []restReviewer, evals []restEvaluation) domain.ReviewDecision {
	approved := false
	for _, r := range reviewers {
		if r.Vote < 0 {
			return domain.ReviewChangesRequest
		}
		if r.Vote > 0 {
			approved = true
		}
	}
	for _, e := range evals {
		if e.Configuration.IsBlocking && isReviewerPolicy(e) && !policySatisfied(e.Status) {
			return domain.ReviewRequired
		}
	}
	if approved {
		return domain.ReviewApproved
	}
	return domain.ReviewNone
}

func isReviewerPolicy(e restEvaluation) bool {
	id := e.Configuration.Type.ID
	return id == policyMinReviewers || id == policyRequiredReview
}

func policySatisfied(status string) bool {
	return status == "approved" || status == "notApplicable"
}

func mergeability(pr *restPR, evals []restEvaluation, ci string, review domain.ReviewDecision) ports.SCMMergeabilityObservation {
	if pr.Status != "active" {
		return ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	}
	var blockers []string
	switch pr.MergeStatus {
	case "conflicts":
		return ports.SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true, Blockers: []string{"conflicts"}}
	case "succeeded":
	case "rejectedByPolicy", "failure":
		blockers = append(blockers, "blocked_by_provider")
	default: // queued, notSet: the merge preview is still being computed
		return ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	}
	blocked := len(blockers) > 0
	add := func(b string) {
		blocked = true
		for _, have := range blockers {
			if have == b {
				return
			}
		}
		blockers = append(blockers, b)
	}
	if pr.IsDraft {
		add("draft")
	}
	if ci == string(domain.CIFailing) {
		add("ci_failing")
	}
	switch review {
	case domain.ReviewChangesRequest:
		add("changes_requested")
	case domain.ReviewRequired:
		add("review_required")
	}
	for _, e := range evals {
		if !e.Configuration.IsBlocking || policySatisfied(e.Status) {
			continue
		}
		switch {
		case e.Configuration.Type.ID == policyBuild:
			blocked = true // pending or failing CI; failing already reported above
		case isReviewerPolicy(e):
			add("review_required")
		default:
			add("blocked_by_provider")
		}
	}
	if !blocked {
		return ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true}
	}
	if len(blockers) == 0 {
		blockers = []string{"blocked_by_provider"}
	}
	return ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: blockers}
}

// ---------------------------------------------------------------------------
// REST shapes and helpers
// ---------------------------------------------------------------------------

// adoTime tolerates Azure DevOps' zero dates ("0001-01-01T00:00:00") and
// timestamps without a zone.
type adoTime time.Time

func (t *adoTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" || strings.HasPrefix(s, "0001-01-01") {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if v, err := time.Parse(layout, s); err == nil {
			*t = adoTime(v)
			return nil
		}
	}
	return nil
}

type restIdentity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
	Descriptor  string `json:"descriptor"`
	IsContainer bool   `json:"isContainer"`
}

// login is the unique name (UPN) when present; service identities have none.
func (i restIdentity) login() string {
	return firstNonEmpty(strings.TrimSpace(i.UniqueName), strings.TrimSpace(i.DisplayName))
}

// isBotIdentity recognizes build/service identities: service-principal
// descriptors, the "<Project> Build Service" account and service mailboxes.
func isBotIdentity(i restIdentity) bool {
	d := strings.ToLower(i.Descriptor)
	if strings.HasPrefix(d, "svc.") || strings.HasPrefix(d, "s2s.") || strings.HasPrefix(d, "aadsp.") {
		return true
	}
	name := strings.ToLower(i.login())
	return strings.Contains(name, "build service") || strings.HasPrefix(name, "svc-") ||
		strings.HasSuffix(name, "[bot]") || strings.HasSuffix(name, "-bot")
}

type restReviewer struct {
	restIdentity
	Vote int `json:"vote"`
}

type restCommit struct {
	CommitID string `json:"commitId"`
}

type restPR struct {
	PullRequestID         int            `json:"pullRequestId"`
	Status                string         `json:"status"`
	Title                 string         `json:"title"`
	IsDraft               bool           `json:"isDraft"`
	CreatedBy             restIdentity   `json:"createdBy"`
	CreationDate          adoTime        `json:"creationDate"`
	ClosedDate            adoTime        `json:"closedDate"`
	SourceRefName         string         `json:"sourceRefName"`
	TargetRefName         string         `json:"targetRefName"`
	MergeStatus           string         `json:"mergeStatus"`
	MergeFailureMessage   string         `json:"mergeFailureMessage"`
	LastMergeSourceCommit restCommit     `json:"lastMergeSourceCommit"`
	LastMergeTargetCommit restCommit     `json:"lastMergeTargetCommit"`
	LastMergeCommit       restCommit     `json:"lastMergeCommit"`
	Reviewers             []restReviewer `json:"reviewers"`
	Repository            struct {
		ID      string `json:"id"`
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	} `json:"repository"`
	ForkSource *struct {
		Repository struct {
			Name    string `json:"name"`
			Project struct {
				Name string `json:"name"`
			} `json:"project"`
		} `json:"repository"`
	} `json:"forkSource"`
}

type restEvaluation struct {
	Status        string `json:"status"`
	Configuration struct {
		IsEnabled  bool `json:"isEnabled"`
		IsBlocking bool `json:"isBlocking"`
		IsDeleted  bool `json:"isDeleted"`
		Type       struct {
			ID string `json:"id"`
		} `json:"type"`
		Settings struct {
			DisplayName string `json:"displayName"`
		} `json:"settings"`
	} `json:"configuration"`
	Context struct {
		BuildID             int    `json:"buildId"`
		BuildDefinitionName string `json:"buildDefinitionName"`
		IsExpired           bool   `json:"isExpired"`
		BuildIsNotCurrent   bool   `json:"buildIsNotCurrent"`
	} `json:"context"`
}

type restStatus struct {
	ID        int    `json:"id"`
	State     string `json:"state"`
	TargetURL string `json:"targetUrl"`
	Context   struct {
		Name  string `json:"name"`
		Genre string `json:"genre"`
	} `json:"context"`
}

func (s restStatus) name() string {
	if s.Context.Genre == "" {
		return s.Context.Name
	}
	return s.Context.Genre + "/" + s.Context.Name
}

type restThread struct {
	ID              int                        `json:"id"`
	Status          string                     `json:"status"`
	IsDeleted       bool                       `json:"isDeleted"`
	LastUpdatedDate adoTime                    `json:"lastUpdatedDate"`
	Properties      map[string]json.RawMessage `json:"properties"`
	ThreadContext   *struct {
		FilePath       string   `json:"filePath"`
		RightFileStart *linePos `json:"rightFileStart"`
		LeftFileStart  *linePos `json:"leftFileStart"`
	} `json:"threadContext"`
	Comments []restComment `json:"comments"`
}

type linePos struct {
	Line int `json:"line"`
}

// isSystem reports vote, push and other auto-generated threads.
func (t restThread) isSystem() bool {
	if _, ok := t.Properties["CodeReviewThreadType"]; ok {
		return true
	}
	for _, c := range t.Comments {
		if c.CommentType != "system" {
			return false
		}
	}
	return true
}

type restComment struct {
	ID          int          `json:"id"`
	Author      restIdentity `json:"author"`
	Content     string       `json:"content"`
	CommentType string       `json:"commentType"`
	IsDeleted   bool         `json:"isDeleted"`
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
