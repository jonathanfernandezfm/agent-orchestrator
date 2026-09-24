package azuredevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// azureDevOpsResourceID is the Entra ID application id of Azure DevOps; `az`
// mints access tokens for it.
const azureDevOpsResourceID = "499b84ac-1321-427f-aa17-267ca6975798"

// ErrNoToken is returned when no credential source yields a token.
var ErrNoToken = errors.New("azure devops scm: no token configured (set AO_AZURE_DEVOPS_TOKEN or run `az login`)")

// ErrAuthFailed is returned when Azure DevOps rejects the credential.
var ErrAuthFailed = errors.New("azure devops scm: authentication failed")

// Credential is a token plus how Azure DevOps expects it to be presented:
// personal access tokens use Basic auth, Entra access tokens use Bearer.
type Credential struct {
	Token  string
	Bearer bool
}

func (c Credential) authorization() string {
	if c.Bearer {
		return "Bearer " + c.Token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+c.Token))
}

// TokenSource yields an Azure DevOps credential on demand.
type TokenSource interface {
	Credential(ctx context.Context) (Credential, error)
}

type tokenInvalidator interface{ InvalidateToken() }

// StaticTokenSource is a literal personal access token, used in tests.
type StaticTokenSource string

// Credential returns the literal PAT.
func (s StaticTokenSource) Credential(context.Context) (Credential, error) {
	if t := strings.TrimSpace(string(s)); t != "" {
		return Credential{Token: t}, nil
	}
	return Credential{}, ErrNoToken
}

// EnvTokenSource reads a personal access token from the first non-empty env var.
type EnvTokenSource struct{ EnvVars []string }

// Credential returns the first configured PAT.
func (s EnvTokenSource) Credential(context.Context) (Credential, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return Credential{Token: v}, nil
		}
	}
	return Credential{}, ErrNoToken
}

// AzCLITokenSource mints an Entra access token with the Azure CLI
// (`az account get-access-token`), reusing the user's `az login` session. The
// token is cached until shortly before it expires; a failed mint is not
// retried for a minute so a logged-out CLI is not spawned on every request.
type AzCLITokenSource struct {
	Run   func(ctx context.Context) ([]byte, error)
	Clock func() time.Time

	mu          sync.Mutex
	token       string
	expiresAt   time.Time
	failedUntil time.Time
}

// Credential returns the cached or freshly minted bearer token.
func (s *AzCLITokenSource) Credential(ctx context.Context) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock()
	}
	if s.token != "" && now.Before(s.expiresAt) {
		return Credential{Token: s.token, Bearer: true}, nil
	}
	if now.Before(s.failedUntil) {
		return Credential{}, ErrNoToken
	}
	run := s.Run
	if run == nil {
		run = azAccessToken
	}
	var resp struct {
		AccessToken string `json:"accessToken"`
		ExpiresOn   int64  `json:"expires_on"`
	}
	out, err := run(ctx)
	if err == nil {
		err = json.Unmarshal(out, &resp)
	}
	if err != nil || strings.TrimSpace(resp.AccessToken) == "" {
		s.failedUntil = now.Add(time.Minute)
		return Credential{}, ErrNoToken
	}
	expires := now.Add(30 * time.Minute)
	if resp.ExpiresOn > 0 {
		expires = time.Unix(resp.ExpiresOn, 0).Add(-5 * time.Minute)
	}
	s.token, s.expiresAt = strings.TrimSpace(resp.AccessToken), expires
	return Credential{Token: s.token, Bearer: true}, nil
}

// InvalidateToken drops the cached token so the next call re-mints it.
func (s *AzCLITokenSource) InvalidateToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token, s.expiresAt, s.failedUntil = "", time.Time{}, time.Time{}
}

func azAccessToken(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return aoprocess.CommandContext(ctx, "az", "account", "get-access-token", "--resource", azureDevOpsResourceID, "-o", "json").Output()
}

// FallbackTokenSource tries each source in order.
type FallbackTokenSource []TokenSource

// Credential returns the first credential any source yields.
func (s FallbackTokenSource) Credential(ctx context.Context) (Credential, error) {
	for _, src := range s {
		if src == nil {
			continue
		}
		if cred, err := src.Credential(ctx); err == nil {
			return cred, nil
		} else if !errors.Is(err, ErrNoToken) {
			return Credential{}, err
		}
	}
	return Credential{}, ErrNoToken
}

// InvalidateToken forwards to every source that caches.
func (s FallbackTokenSource) InvalidateToken() {
	for _, src := range s {
		if inv, ok := src.(tokenInvalidator); ok {
			inv.InvalidateToken()
		}
	}
}
