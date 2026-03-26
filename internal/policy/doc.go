// Package policy implements multi-level security policy checks for ClawSSH.
//
// PolicyEngine evaluates tasks before adapter execution:
// - Allowlist: only approved actions and targets
// - Blacklist: reject Args/Parameters containing shell injection chars (;, &&, |, etc.)
// - Risk levels (low/medium/high): high requires user confirmation; medium requires confirm in production
// - Environment-aware: env_overrides in policies.yaml adjust risk per environment (production/staging/development)
//
// Evaluate returns PolicyResult with Allowed, NeedConfirm, and Reason.
// When NeedConfirm is true, the SSH handler prompts the user; non-"yes" cancels execution.
package policy
