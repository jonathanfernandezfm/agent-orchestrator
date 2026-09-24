package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.SCMMerger          = (*Provider)(nil)
	_ ports.SCMReviewRequester = (*Provider)(nil)
	_ ports.SCMReviewResolver  = (*Provider)(nil)
)

var headSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// completionPollInterval/Attempts bound how long MergePullRequest waits for
// Azure DevOps' asynchronous completion to land the merge commit.
var (
	completionPollInterval = time.Second
	completionPollAttempts = 10
)

// MergePullRequest completes the pull request with a squash merge. Azure
// DevOps rejects the completion when lastMergeSourceCommit no longer matches
// the source head, which gives the compare-and-swap guard.
func (p *Provider) MergePullRequest(ctx context.Context, request ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	if request.Method != ports.SCMMergeSquash {
		return ports.SCMMergeResult{}, fmt.Errorf("azure devops scm: unsupported merge method %q", request.Method)
	}
	expected := strings.TrimSpace(request.ExpectedHeadSHA)
	if !headSHAPattern.MatchString(expected) || request.PR.Number <= 0 {
		return ports.SCMMergeResult{}, fmt.Errorf("azure devops scm: invalid merge request")
	}
	r, err := coords(request.PR.Repo)
	if err != nil {
		return ports.SCMMergeResult{}, err
	}
	body := map[string]any{
		"status":                "completed",
		"lastMergeSourceCommit": map[string]string{"commitId": expected},
		"completionOptions":     map[string]any{"mergeStrategy": "squash"},
	}
	var pr restPR
	if err := p.client.sendJSON(ctx, http.MethodPatch, prPath(r, request.PR.Number), nil, body, &pr); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			switch httpErr.StatusCode {
			case http.StatusConflict:
				return ports.SCMMergeResult{}, fmt.Errorf("%w: %w", ports.ErrSCMHeadChanged, err)
			case http.StatusBadRequest:
				return ports.SCMMergeResult{}, fmt.Errorf("%w: %w", ports.ErrSCMNotMergeable, err)
			}
		}
		return ports.SCMMergeResult{}, err
	}
	// Completion is asynchronous; wait briefly for the merge commit. A PR
	// still merging when the wait ends is reported as accepted, and the
	// observer records the merge once it lands.
	for attempt := 0; pr.Status != "completed" && attempt < completionPollAttempts; attempt++ {
		switch pr.MergeStatus {
		case "conflicts", "rejectedByPolicy", "failure":
			return ports.SCMMergeResult{}, fmt.Errorf("%w: azure devops merge status %s: %s", ports.ErrSCMNotMergeable, pr.MergeStatus, pr.MergeFailureMessage)
		}
		select {
		case <-ctx.Done():
			return ports.SCMMergeResult{}, ctx.Err()
		case <-time.After(completionPollInterval):
		}
		next, err := p.getPR(ctx, r, request.PR.Number)
		if err != nil {
			return ports.SCMMergeResult{}, err
		}
		pr = *next
	}
	if pr.Status != "completed" {
		return ports.SCMMergeResult{}, nil
	}
	return ports.SCMMergeResult{MergeCommitSHA: pr.LastMergeCommit.CommitID}, nil
}

// RequestReview adds reviewer (the unique name AO recorded as a comment
// author or voter) to the pull request. The identity id is resolved from the
// PR's own reviewers and threads, which the reviewer must already appear in.
func (p *Provider) RequestReview(ctx context.Context, request ports.SCMReviewRequest) error {
	reviewer := strings.TrimSpace(strings.TrimPrefix(request.Reviewer, "@"))
	if reviewer == "" || request.PR.Number <= 0 {
		return fmt.Errorf("azure devops scm: invalid review request")
	}
	r, err := coords(request.PR.Repo)
	if err != nil {
		return err
	}
	id, err := p.findIdentityID(ctx, r, request.PR.Number, reviewer)
	if err != nil {
		return err
	}
	return p.client.sendJSON(ctx, http.MethodPut, prPath(r, request.PR.Number)+"/reviewers/"+url.PathEscape(id), nil, map[string]string{"id": id}, nil)
}

func (p *Provider) findIdentityID(ctx context.Context, r domain.AzureDevOpsRepo, n int, login string) (string, error) {
	pr, err := p.getPR(ctx, r, n)
	if err != nil {
		return "", err
	}
	for _, rv := range pr.Reviewers {
		if strings.EqualFold(rv.login(), login) {
			return rv.ID, nil
		}
	}
	threads, err := p.getThreads(ctx, r, n)
	if err != nil {
		return "", err
	}
	for _, t := range threads {
		for _, c := range t.Comments {
			if c.Author.ID != "" && strings.EqualFold(c.Author.login(), login) {
				return c.Author.ID, nil
			}
		}
	}
	return "", fmt.Errorf("%w: azure devops reviewer %q not found on pull request", ports.ErrSCMNotFound, login)
}

// ResolveReviewThread marks a comment thread as fixed.
func (p *Provider) ResolveReviewThread(ctx context.Context, request ports.SCMReviewResolveRequest) error {
	threadID, err := strconv.Atoi(strings.TrimSpace(request.ThreadID))
	if err != nil || threadID <= 0 || request.PR.Number <= 0 {
		return fmt.Errorf("azure devops scm: invalid review thread %q", request.ThreadID)
	}
	r, err := coords(request.PR.Repo)
	if err != nil {
		return err
	}
	path := prPath(r, request.PR.Number) + "/threads/" + strconv.Itoa(threadID)
	return p.client.sendJSON(ctx, http.MethodPatch, path, nil, map[string]string{"status": "fixed"}, nil)
}
