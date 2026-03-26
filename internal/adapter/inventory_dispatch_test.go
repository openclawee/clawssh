package adapter

import (
	"strings"
	"testing"

	"github.com/clawssh/clawssh/internal/inventory"
	"github.com/clawssh/clawssh/pkg/dsl"
)

type fakeExec struct{}

func (f fakeExec) Execute(task *dsl.Task) (*dsl.Result, error) {
	host := paramString(task.Parameters, "host")
	return &dsl.Result{OK: true, ExitCode: 0, Stdout: "ok@" + host}, nil
}

func TestInventoryDispatch_GroupParallel(t *testing.T) {
	inv, err := inventory.ParseINI(`
[web]
web1 ansible_host=10.0.0.12 ansible_user=ubuntu
web2 ansible_host=10.0.0.13 ansible_user=ubuntu
[all:vars]
ansible_port=22
`)
	if err != nil {
		t.Fatal(err)
	}
	d := NewInventoryDispatch(fakeExec{}, inv)
	res, err := d.Execute(&dsl.Task{
		Action:  dsl.ActionCheckCPU,
		Target:  "web",
		IsGroup: true,
		Source:  "ssh",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected group success, got %+v", res)
	}
	if res.Stdout == "" || res.Message == "" {
		t.Fatalf("unexpected empty output: %+v", res)
	}
}

func TestInventoryDispatch_AutoGroupWhenTargetIsGroupName(t *testing.T) {
	inv, err := inventory.ParseINI(`
[web]
web1 ansible_host=10.0.0.12 ansible_user=ubuntu
web2 ansible_host=10.0.0.13 ansible_user=ubuntu
`)
	if err != nil {
		t.Fatal(err)
	}
	d := NewInventoryDispatch(fakeExec{}, inv)
	res, err := d.Execute(&dsl.Task{
		Action: dsl.ActionCheckCPU,
		Target: "web",
		// is_group=false intentionally omitted to test auto-group
		Source: "ssh",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK || res.Message == "" {
		t.Fatalf("expected grouped success, got %+v", res)
	}
}

func TestOneLineSummary_PrefersMetricLineOverHeader(t *testing.T) {
	res := &dsl.Result{
		OK: true,
		Stdout: "              total        used        free\n" +
			"Mem:           7803        1311        6269\n",
	}
	got := oneLineSummary(dsl.ActionCheckMemory, res)
	if got == "" || got == "total        used        free" {
		t.Fatalf("expected metric summary line, got %q", got)
	}
	if !strings.Contains(got, "Memory total") {
		t.Fatalf("expected memory human summary, got %q", got)
	}
}

func TestOneLineSummary_CPUHumanReadable(t *testing.T) {
	res := &dsl.Result{
		OK: true,
		Stdout: `%Cpu(s):  6.4 us,  0.8 sy,  0.0 ni, 92.0 id,  0.8 wa,  0.0 hi,  0.0 si,  0.0 st
top - 20:16:42 up  1:48,  3 users`,
	}
	got := oneLineSummary(dsl.ActionCheckCPU, res)
	if !strings.Contains(got, "CPU busy") || !strings.Contains(got, "idle 92.0%") {
		t.Fatalf("expected human cpu summary, got %q", got)
	}
}

func TestOneLineSummary_DiskHumanReadable(t *testing.T) {
	res := &dsl.Result{
		OK: true,
		Stdout: `Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1        100G   22G   74G  23% /`,
	}
	got := oneLineSummary(dsl.ActionCheckDisk, res)
	if !strings.Contains(got, "Disk /dev/sda1 used 23%") {
		t.Fatalf("expected disk human summary, got %q", got)
	}
}

