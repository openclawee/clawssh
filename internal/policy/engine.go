package policy

import (
	"fmt"
	"strings"

	"github.com/clawssh/clawssh/pkg/dsl"
	"gopkg.in/yaml.v3"
)

// Environment names for context-aware policy (e.g. Production vs Staging).
const (
	EnvProduction  = "production"
	EnvStaging     = "staging"
	EnvDevelopment = "development"
)

// Context carries environment and session info for policy evaluation.
type Context struct {
	Environment string // "production", "staging", "development"
	Username    string
	RemoteAddr  string
	// AllowedTargets contains dynamic inventory alias/group names allowed by policy.
	AllowedTargets map[string]struct{}
}

// RuleSet defines action risk levels, with optional per-environment overrides.
type RuleSet struct {
	// Default risk for each action when no env override exists.
	Actions map[string]string `yaml:"actions"`
	// EnvOverrides: env -> action -> risk (overrides Actions for that env).
	EnvOverrides map[string]map[string]string `yaml:"env_overrides,omitempty"`
}

// PolicyEngine performs multi-level validation: blacklist, risk assessment, and allowlist.
type PolicyEngine struct {
	allowedActions map[string]struct{}
	allowedTargets map[string]struct{}
	rules          *RuleSet
}

// NewPolicyEngine builds an engine with default allowlists and optional rules.
// If rules is nil, built-in defaults are used (high risk for service_control, etc.).
func NewPolicyEngine() *PolicyEngine {
	acts := make(map[string]struct{})
	for _, a := range dsl.CoreActionIDs() {
		acts[a] = struct{}{}
	}
	return &PolicyEngine{
		allowedActions: acts,
		allowedTargets: map[string]struct{}{
			dsl.TargetLocal: {},
			"":              {},
		},
		rules: defaultRuleSet(),
	}
}

// NewPolicyEngineWithRules builds an engine with custom rules from YAML bytes.
func NewPolicyEngineWithRules(yamlBytes []byte) (*PolicyEngine, error) {
	pe := NewPolicyEngine()
	var rs RuleSet
	if err := yaml.Unmarshal(yamlBytes, &rs); err != nil {
		return nil, fmt.Errorf("policy rules: %w", err)
	}
	if len(rs.Actions) > 0 {
		pe.rules = &rs
	}
	return pe, nil
}

// Evaluate returns a PolicyResult. Caller must check Allowed and NeedConfirm.
// If Allowed is false, the task must not execute.
// If NeedConfirm is true, execution must wait for user confirmation.
func (p *PolicyEngine) Evaluate(ctx *Context, task *dsl.Task) *PolicyResult {
	if task == nil {
		return Deny("nil task")
	}
	if _, ok := p.allowedActions[task.Action]; !ok {
		return Deny(fmt.Sprintf("action %q not allowlisted", task.Action))
	}
	if _, ok := p.allowedTargets[task.Target]; !ok {
		if ctx == nil || ctx.AllowedTargets == nil {
			return Deny(fmt.Sprintf("target %q not allowlisted", task.Target))
		}
		if _, dynOK := ctx.AllowedTargets[task.Target]; !dynOK {
			return Deny(fmt.Sprintf("target %q not allowlisted", task.Target))
		}
	}

	// Blacklist: check for dangerous characters in Args and Parameters
	if r := p.checkBlacklist(task); r != nil {
		return r
	}

	// Risk-based decision: high -> need confirm (or deny in prod if desired),
	// medium -> need confirm in production, low -> allow.
	risk := p.getRisk(ctx, task.Action)
	switch risk {
	case RiskHigh:
		return AllowWithConfirm(fmt.Sprintf("操作 %q 为高风险，请确认后执行", task.Action))
	case RiskMedium:
		if ctx != nil && ctx.Environment == EnvProduction {
			return AllowWithConfirm(fmt.Sprintf("操作 %q 在生产环境中需确认", task.Action))
		}
		return Allow()
	case RiskLow:
		return Allow()
	default:
		return Allow()
	}
}

func (p *PolicyEngine) checkBlacklist(task *dsl.Task) *PolicyResult {
	check := func(s string) bool {
		for _, c := range DangerousChars {
			if strings.Contains(s, c) {
				return true
			}
		}
		return false
	}
	for _, a := range task.Args {
		if check(a) {
			return Deny("参数包含潜在注入字符，已拒绝执行")
		}
	}
	if task.Parameters != nil {
		for _, v := range task.Parameters {
			if s, ok := v.(string); ok && check(s) {
				return Deny("参数包含潜在注入字符，已拒绝执行")
			}
		}
	}
	return nil
}

func (p *PolicyEngine) getRisk(ctx *Context, action string) RiskLevel {
	env := EnvDevelopment
	if ctx != nil && ctx.Environment != "" {
		env = strings.ToLower(ctx.Environment)
	}

	// Env override first
	if p.rules != nil && p.rules.EnvOverrides != nil {
		if overrides, ok := p.rules.EnvOverrides[env]; ok {
			if r, ok := overrides[action]; ok {
				return RiskLevel(strings.ToLower(r))
			}
		}
	}

	// Default action risk
	if p.rules != nil && p.rules.Actions != nil {
		if r, ok := p.rules.Actions[action]; ok {
			return RiskLevel(strings.ToLower(r))
		}
	}

	return RiskLow
}

func defaultRuleSet() *RuleSet {
	return &RuleSet{
		Actions: map[string]string{
			dsl.ActionServiceControl:  "high",
			dsl.ActionTailLog:         "medium",
			dsl.ActionReadFile:        "medium",
			dsl.ActionListDirectory:   "low",
			dsl.ActionListProcesses:   "low",
			dsl.ActionCheckCPU:        "low",
			dsl.ActionCheckDisk:       "low",
			dsl.ActionCheckMemory:     "low",
			dsl.ActionCheckNetwork:    "low",
			dsl.ActionServiceStatus:   "low",
			dsl.ActionPingHost:        "low",
			dsl.ActionPortCheck:       "low",
			dsl.ActionDNSLookup:       "low",
			dsl.ActionUptimeInfo:      "low",
			dsl.ActionWhoamiContext:   "low",
			dsl.ActionEnvSnapshot:     "low",
			dsl.ActionPackageList:     "low",
			dsl.ActionDiskInodeCheck:  "low",
			dsl.ActionFirewallStatus:  "medium",
			dsl.ActionExplainIntent:   "low",
		},
		EnvOverrides: map[string]map[string]string{
			EnvProduction: {
				dsl.ActionServiceControl: "high",
				dsl.ActionTailLog:        "medium",
				dsl.ActionReadFile:       "medium",
				dsl.ActionFirewallStatus: "high",
			},
			EnvStaging: {
				dsl.ActionServiceControl: "medium",
				dsl.ActionFirewallStatus: "medium",
			},
		},
	}
}
