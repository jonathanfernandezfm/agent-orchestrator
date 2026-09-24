package domain

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// AzureDevOpsProvider is the SCM provider key for Azure DevOps Services.
const AzureDevOpsProvider = "azure-devops"

// AzureDevOpsHost is the canonical host every Azure DevOps repository and pull
// request is normalized to, whichever remote form (ssh.dev.azure.com,
// {org}.visualstudio.com, vs-ssh.visualstudio.com) the checkout uses.
const AzureDevOpsHost = "dev.azure.com"

// AzureDevOpsRepo identifies an Azure DevOps Git repository. Segments are
// stored decoded (project names commonly contain spaces).
type AzureDevOpsRepo struct {
	Org     string
	Project string
	Name    string
}

// Namespace is the "org/project" owner path AO uses for Azure DevOps repos.
func (r AzureDevOpsRepo) Namespace() string { return r.Org + "/" + r.Project }

// FullName is the "org/project/repo" display and persistence name.
func (r AzureDevOpsRepo) FullName() string { return r.Namespace() + "/" + r.Name }

// PRURL is the canonical browser URL of pull request number n.
func (r AzureDevOpsRepo) PRURL(n int) string {
	return fmt.Sprintf("https://%s/%s/%s/_git/%s/pullrequest/%d", AzureDevOpsHost,
		url.PathEscape(r.Org), url.PathEscape(r.Project), url.PathEscape(r.Name), n)
}

// IsAzureDevOpsHost reports whether host (optionally with a port) serves Azure
// DevOps Git remotes or web pages.
func IsAzureDevOpsHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(strings.TrimSpace(host))
	return host == AzureDevOpsHost || host == "ssh.dev.azure.com" || strings.HasSuffix(host, ".visualstudio.com")
}

// AzureDevOpsRepoFromFullName splits an "org/project/repo" name.
func AzureDevOpsRepoFromFullName(full string) (AzureDevOpsRepo, bool) {
	parts := strings.Split(strings.Trim(full, "/"), "/")
	if len(parts) != 3 {
		return AzureDevOpsRepo{}, false
	}
	return validAzureDevOpsRepo(parts[0], parts[1], parts[2])
}

// ParseAzureDevOpsRemote parses the Git remote forms Azure DevOps hands out:
//
//	https://[user@]dev.azure.com/{org}/{project}/_git/{repo}
//	https://{org}.visualstudio.com/[DefaultCollection/]{project}/_git/{repo}
//	git@ssh.dev.azure.com:v3/{org}/{project}/{repo}
//	ssh://git@ssh.dev.azure.com/v3/{org}/{project}/{repo}
//	{org}@vs-ssh.visualstudio.com:v3/{org}/{project}/{repo}
//
// The project segment may be omitted in HTTPS remotes when it equals the repo.
func ParseAzureDevOpsRemote(raw string) (AzureDevOpsRepo, bool) {
	host, segments, ok := splitAzureDevOpsURL(raw)
	if !ok {
		return AzureDevOpsRepo{}, false
	}
	if n := len(segments); n > 0 {
		segments[n-1] = strings.TrimSuffix(segments[n-1], ".git")
	}
	if len(segments) == 4 && strings.EqualFold(segments[0], "v3") {
		return validAzureDevOpsRepo(segments[1], segments[2], segments[3])
	}
	org, rest, ok := azureDevOpsOrg(host, segments)
	if !ok {
		return AzureDevOpsRepo{}, false
	}
	switch {
	case len(rest) == 3 && rest[1] == "_git":
		return validAzureDevOpsRepo(org, rest[0], rest[2])
	case len(rest) == 2 && rest[0] == "_git":
		return validAzureDevOpsRepo(org, rest[1], rest[1])
	}
	return AzureDevOpsRepo{}, false
}

// ParseAzureDevOpsPRURL parses .../{project}/_git/{repo}/pullrequest/{n} on
// dev.azure.com or a legacy {org}.visualstudio.com host.
func ParseAzureDevOpsPRURL(raw string) (AzureDevOpsRepo, int, bool) {
	host, segments, ok := splitAzureDevOpsURL(raw)
	if !ok || !strings.HasPrefix(strings.TrimSpace(raw), "https://") {
		return AzureDevOpsRepo{}, 0, false
	}
	org, rest, ok := azureDevOpsOrg(host, segments)
	if !ok || len(rest) != 5 || rest[1] != "_git" || !strings.EqualFold(rest[3], "pullrequest") {
		return AzureDevOpsRepo{}, 0, false
	}
	n, err := strconv.Atoi(rest[4])
	if err != nil || n <= 0 {
		return AzureDevOpsRepo{}, 0, false
	}
	repo, ok := validAzureDevOpsRepo(org, rest[0], rest[2])
	return repo, n, ok
}

// splitAzureDevOpsURL returns the lowercase hostname and decoded path segments
// of an Azure DevOps URL or scp-style SSH remote.
func splitAzureDevOpsURL(raw string) (string, []string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, false
	}
	if !strings.Contains(raw, "://") {
		// scp-like: user@host:path
		userHost, path, ok := strings.Cut(raw, ":")
		if !ok || !strings.Contains(userHost, "@") {
			return "", nil, false
		}
		raw = "ssh://" + userHost + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || !IsAzureDevOpsHost(u.Host) {
		return "", nil, false
	}
	if u.Scheme != "https" && u.Scheme != "ssh" {
		return "", nil, false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", nil, false
	}
	return strings.ToLower(u.Hostname()), strings.Split(path, "/"), true
}

// azureDevOpsOrg peels the organization off a web-style path: the first
// segment on dev.azure.com, the subdomain on {org}.visualstudio.com.
func azureDevOpsOrg(host string, segments []string) (string, []string, bool) {
	if host == AzureDevOpsHost {
		if len(segments) < 2 {
			return "", nil, false
		}
		return segments[0], segments[1:], true
	}
	org, ok := strings.CutSuffix(host, ".visualstudio.com")
	if !ok || org == "" || org == "vs-ssh" || strings.Contains(org, ".") {
		return "", nil, false
	}
	if len(segments) > 0 && strings.EqualFold(segments[0], "DefaultCollection") {
		segments = segments[1:]
	}
	return org, segments, true
}

func validAzureDevOpsRepo(org, project, name string) (AzureDevOpsRepo, bool) {
	for _, s := range []string{org, project, name} {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\\t\r\n?#") {
			return AzureDevOpsRepo{}, false
		}
	}
	return AzureDevOpsRepo{Org: org, Project: project, Name: name}, true
}
