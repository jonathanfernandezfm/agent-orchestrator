package domain

import "time"

// Claude subscription usage states.
const (
	ClaudeUsageAvailable   = "available"
	ClaudeUsageSignedOut   = "signed_out"
	ClaudeUsageUnavailable = "unavailable"
)

// ClaudeUsageWindow is one rate-limit window of the signed-in Claude
// subscription, e.g. the current 5-hour session or a weekly cap.
type ClaudeUsageWindow struct {
	Kind        string
	Group       string
	Model       string
	UsedPercent float64
	Severity    string
	ResetsAt    *time.Time
}

// ClaudeSubscriptionUsage is the account-level Claude usage snapshot.
type ClaudeSubscriptionUsage struct {
	State     string
	Windows   []ClaudeUsageWindow
	CheckedAt time.Time
}
