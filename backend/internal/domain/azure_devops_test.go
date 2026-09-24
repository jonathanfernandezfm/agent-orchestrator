package domain

import "testing"

func TestParseAzureDevOpsRemote(t *testing.T) {
	want := AzureDevOpsRepo{Org: "matrix42", Project: "Pro - Service Management", Name: "esm-ui"}
	for _, tc := range []struct {
		remote string
		want   AzureDevOpsRepo
		ok     bool
	}{
		{"git@ssh.dev.azure.com:v3/matrix42/Pro%20-%20Service%20Management/esm-ui", want, true},
		{"ssh://git@ssh.dev.azure.com/v3/matrix42/Pro%20-%20Service%20Management/esm-ui", want, true},
		{"https://matrix42@dev.azure.com/matrix42/Pro%20-%20Service%20Management/_git/esm-ui", want, true},
		{"https://dev.azure.com/matrix42/Pro%20-%20Service%20Management/_git/esm-ui.git", want, true},
		{"https://matrix42.visualstudio.com/Pro%20-%20Service%20Management/_git/esm-ui", want, true},
		{"https://matrix42.visualstudio.com/DefaultCollection/Pro%20-%20Service%20Management/_git/esm-ui", want, true},
		{"matrix42@vs-ssh.visualstudio.com:v3/matrix42/Pro%20-%20Service%20Management/esm-ui", want, true},
		{"https://dev.azure.com/org/_git/tool", AzureDevOpsRepo{Org: "org", Project: "tool", Name: "tool"}, true},
		{"https://dev.azure.com/org/project/_git/repo/pullrequest/7", AzureDevOpsRepo{}, false},
		{"https://dev.azure.com/org/project/repo", AzureDevOpsRepo{}, false},
		{"https://dev.azure.com/org/project/_git/a%2Fb", AzureDevOpsRepo{}, false},
		{"http://dev.azure.com/org/project/_git/repo", AzureDevOpsRepo{}, false},
		{"git@github.com:org/repo.git", AzureDevOpsRepo{}, false},
		{"https://gitlab.com/org/project/_git/repo", AzureDevOpsRepo{}, false},
	} {
		got, ok := ParseAzureDevOpsRemote(tc.remote)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseAzureDevOpsRemote(%q) = %+v, %v; want %+v, %v", tc.remote, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseAzureDevOpsPRURL(t *testing.T) {
	want := AzureDevOpsRepo{Org: "matrix42", Project: "Pro - Service Management", Name: "esm-ui"}
	canonical := "https://dev.azure.com/matrix42/Pro%20-%20Service%20Management/_git/esm-ui/pullrequest/52658"
	for _, raw := range []string{
		canonical,
		canonical + "/",
		"https://matrix42.visualstudio.com/Pro%20-%20Service%20Management/_git/esm-ui/pullrequest/52658",
	} {
		repo, n, ok := ParseAzureDevOpsPRURL(raw)
		if !ok || repo != want || n != 52658 {
			t.Fatalf("ParseAzureDevOpsPRURL(%q) = %+v, %d, %v", raw, repo, n, ok)
		}
		if got := repo.PRURL(n); got != canonical {
			t.Fatalf("PRURL = %q, want %q", got, canonical)
		}
	}
	for _, raw := range []string{
		"https://dev.azure.com/org/project/_git/repo/pullrequest/0",
		"https://dev.azure.com/org/project/_git/repo/pullrequest/x",
		"https://dev.azure.com/org/project/_git/repo",
		"ssh://git@ssh.dev.azure.com/org/project/_git/repo/pullrequest/7",
		"https://github.com/org/repo/pull/7",
	} {
		if _, _, ok := ParseAzureDevOpsPRURL(raw); ok {
			t.Errorf("ParseAzureDevOpsPRURL(%q) accepted", raw)
		}
	}
}

func TestParseRepositoryIdentityAzureDevOps(t *testing.T) {
	got, err := ParseRepositoryIdentity("git@ssh.dev.azure.com:v3/matrix42/Pro%20-%20Service%20Management/esm-ui")
	if err != nil {
		t.Fatal(err)
	}
	want := RepositoryIdentity{Provider: AzureDevOpsProvider, Host: AzureDevOpsHost, Namespace: "matrix42/Pro - Service Management", Name: "esm-ui"}
	if got != want {
		t.Fatalf("identity = %+v, want %+v", got, want)
	}
	if _, err := ParseRepositoryIdentity("https://dev.azure.com/org/project/repo"); err == nil {
		t.Fatal("malformed Azure DevOps URL must not fall through to the GitLab shape")
	}
	if p := RepositoryProvider("matrix42.visualstudio.com"); p != AzureDevOpsProvider {
		t.Fatalf("RepositoryProvider = %q", p)
	}
	cfg := ProjectConfig{CanonicalRepoURL: "https://dev.azure.com/matrix42/Other/_git/esm-ui"}
	if err := cfg.ValidateCanonicalRepository("git@ssh.dev.azure.com:v3/matrix42/Pro%20-%20Service%20Management/esm-ui"); err != nil {
		t.Fatalf("same-host canonical repo rejected: %v", err)
	}
}
