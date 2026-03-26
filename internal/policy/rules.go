package policy

import "errors"

// ErrDenied indicates the task failed policy checks and must not execute.
// Kept for backward compatibility; new code should use PolicyResult.Allowed.
var ErrDenied = errors.New("policy denied task execution")

// RiskLevel indicates the severity of an action for policy decisions.
// Higher risk may require confirmation or be denied in strict environments.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// PolicyResult captures the outcome of a policy evaluation.
// Allowed=false means the task must not execute.
// NeedConfirm=true means the task may execute only after user confirmation.
type PolicyResult struct {
	Allowed    bool   // If false, execution is denied
	NeedConfirm bool   // If true, user must type "yes" to proceed
	Reason     string // Human-readable explanation for audit/UX
}

// Deny returns a result that blocks execution.
func Deny(reason string) *PolicyResult {
	return &PolicyResult{Allowed: false, NeedConfirm: false, Reason: reason}
}

// Allow returns a result that permits execution without confirmation.
func Allow() *PolicyResult {
	return &PolicyResult{Allowed: true, NeedConfirm: false, Reason: ""}
}

// AllowWithConfirm returns a result that requires user confirmation.
func AllowWithConfirm(reason string) *PolicyResult {
	return &PolicyResult{Allowed: true, NeedConfirm: true, Reason: reason}
}

// DangerousChars are shell metacharacters that may indicate command injection.
// DSL parameters are passed to adapters; these chars could break out of safe execution.
var DangerousChars = []string{
	";", "&&", "||", "|", "`", "$(", "${", "\n", "\r",
	">>", ">", "<", "\\", "!", "(", ")", "$",
}
