package policy

import (
	"testing"

	"github.com/clawssh/clawssh/pkg/dsl"
)

func TestPolicyEngine_AllowsLowRiskActions(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvDevelopment}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionCheckCPU, Target: dsl.TargetLocal})
	if !res.Allowed || res.NeedConfirm {
		t.Fatalf("expected allow without confirm, got allowed=%v needConfirm=%v reason=%q", res.Allowed, res.NeedConfirm, res.Reason)
	}
}

func TestPolicyEngine_DeniesUnknownAction(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvDevelopment}
	res := p.Evaluate(ctx, &dsl.Task{Action: "rm_rf_root", Target: dsl.TargetLocal})
	if res.Allowed {
		t.Fatal("expected denial")
	}
}

func TestPolicyEngine_DeniesUnknownTarget(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvDevelopment}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionCheckCPU, Target: "prod-cluster"})
	if res.Allowed {
		t.Fatal("expected denial")
	}
}

func TestPolicyEngine_AllowsInventoryTargetFromContext(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{
		Environment: EnvDevelopment,
		AllowedTargets: map[string]struct{}{
			"web1":       {},
			"webservers": {},
		},
	}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionCheckCPU, Target: "web1"})
	if !res.Allowed {
		t.Fatalf("expected allow for inventory alias target, got reason=%q", res.Reason)
	}
	res = p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionCheckCPU, Target: "webservers"})
	if !res.Allowed {
		t.Fatalf("expected allow for inventory group target, got reason=%q", res.Reason)
	}
}

func TestPolicyEngine_NeedConfirmForHighRisk(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvDevelopment}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionServiceControl, Target: dsl.TargetLocal})
	if !res.Allowed || !res.NeedConfirm {
		t.Fatalf("expected allow with confirm for service_control, got allowed=%v needConfirm=%v", res.Allowed, res.NeedConfirm)
	}
}

func TestPolicyEngine_BlacklistRejectsInjection(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvDevelopment}
	res := p.Evaluate(ctx, &dsl.Task{
		Action: dsl.ActionReadFile,
		Target: dsl.TargetLocal,
		Parameters: map[string]any{
			"path": "/etc/passwd; rm -rf /",
		},
	})
	if res.Allowed {
		t.Fatal("expected denial for injection chars in parameters")
	}
}

func TestPolicyEngine_EnvOverride_StagingServiceControlMedium(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvStaging}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionServiceControl, Target: dsl.TargetLocal})
	// In staging, service_control is medium -> allow without confirm (only prod needs confirm for medium)
	if !res.Allowed {
		t.Fatalf("expected allow in staging, got denied: %s", res.Reason)
	}
	if res.NeedConfirm {
		t.Fatalf("staging medium should not need confirm for service_control (override), got needConfirm=true")
	}
}

func TestPolicyEngine_EnvOverride_ProductionServiceControlHigh(t *testing.T) {
	p := NewPolicyEngine()
	ctx := &Context{Environment: EnvProduction}
	res := p.Evaluate(ctx, &dsl.Task{Action: dsl.ActionServiceControl, Target: dsl.TargetLocal})
	if !res.Allowed || !res.NeedConfirm {
		t.Fatalf("production service_control should need confirm, got allowed=%v needConfirm=%v", res.Allowed, res.NeedConfirm)
	}
}
