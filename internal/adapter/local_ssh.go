package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// LocalSSHAdapter is the first ToolProvider: it runs allowlisted diagnostics on the local host.
// Despite the name, MVP uses os/exec rather than dialing loopback SSH to reduce moving parts.
// A future revision can swap the implementation to golang.org/x/crypto/ssh against localhost.
type LocalSSHAdapter struct {
	// execTimeout bounds how long a single command may run.
	execTimeout time.Duration
}

// NewLocalSSHAdapter constructs a LocalSSHAdapter with conservative defaults.
func NewLocalSSHAdapter() *LocalSSHAdapter {
	return &LocalSSHAdapter{
		execTimeout: 60 * time.Second,
	}
}

// Execute maps known actions to fixed, non-interactive commands suitable for automation.
func (l *LocalSSHAdapter) Execute(task *dsl.Task) (*dsl.Result, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	if task.Target != "" && task.Target != dsl.TargetLocal {
		return &dsl.Result{
			OK:       false,
			ExitCode: -1,
			Message:  "unsupported execution target for LocalSSHAdapter",
		}, fmt.Errorf("unsupported target %q", task.Target)
	}

	ctx, cancel := context.WithTimeout(context.Background(), l.execTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch task.Action {
	case dsl.ActionCheckCPU:
		cmd = exec.CommandContext(ctx, "top", "-bn1")
	case dsl.ActionCheckDisk:
		cmd = exec.CommandContext(ctx, "df", "-h")
	case dsl.ActionCheckMemory:
		cmd = exec.CommandContext(ctx, "free", "-m")
	case dsl.ActionCheckNetwork:
		cmd = exec.CommandContext(ctx, "sh", "-c", "command -v ip >/dev/null && ip -br addr || ifconfig 2>/dev/null || true")
	case dsl.ActionListProcesses:
		cmd = exec.CommandContext(ctx, "sh", "-c", "ps aux --sort=-%cpu 2>/dev/null | head -n 30")
	case dsl.ActionUptimeInfo:
		cmd = exec.CommandContext(ctx, "uptime")
	case dsl.ActionWhoamiContext:
		cmd = exec.CommandContext(ctx, "sh", "-c", "id && whoami")
	case dsl.ActionEnvSnapshot:
		cmd = exec.CommandContext(ctx, "sh", "-c", "printenv | head -n 60")
	case dsl.ActionDiskInodeCheck:
		cmd = exec.CommandContext(ctx, "df", "-i")
	case dsl.ActionPackageList:
		cmd = exec.CommandContext(ctx, "sh", "-c", "command -v dpkg >/dev/null && dpkg -l | tail -n +6 | head -n 40 || (command -v rpm >/dev/null && rpm -qa | head -n 40) || echo 'no dpkg/rpm'")
	case dsl.ActionReadFile:
		p := paramString(task.Parameters, "path")
		if p == "" || strings.Contains(p, "\x00") {
			return notImplemented("read_file requires parameters.path")
		}
		cmd = exec.CommandContext(ctx, "head", "-n", "200", p)
	case dsl.ActionListDirectory:
		p := paramString(task.Parameters, "path")
		if p == "" {
			p = "."
		}
		cmd = exec.CommandContext(ctx, "ls", "-la", p)
	case dsl.ActionPingHost:
		host := paramString(task.Parameters, "host")
		if host == "" {
			return notImplemented("ping_host requires parameters.host")
		}
		cmd = exec.CommandContext(ctx, "ping", "-c", "3", "-W", "3", host)
	case dsl.ActionPortCheck:
		port := paramString(task.Parameters, "port")
		if port == "" || !isAllDigits(port) {
			return notImplemented("port_check requires numeric parameters.port")
		}
		cmd = exec.CommandContext(ctx, "sh", "-c", "ss -tuln 2>/dev/null | grep -F ':"+port+"' | head -n 20 || ss -tuln 2>/dev/null | head -n 40")
	case dsl.ActionDNSLookup:
		name := paramString(task.Parameters, "name")
		if name == "" {
			return notImplemented("dns_lookup requires parameters.name")
		}
		cmd = exec.CommandContext(ctx, "getent", "hosts", name)
	case dsl.ActionTailLog:
		p := paramString(task.Parameters, "path")
		if p == "" {
			return notImplemented("tail_log requires parameters.path")
		}
		n := 50
		if s := paramString(task.Parameters, "lines"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 500 {
				n = v
			}
		}
		cmd = exec.CommandContext(ctx, "tail", "-n", strconv.Itoa(n), p)
	case dsl.ActionServiceStatus:
		svc := paramString(task.Parameters, "service")
		if svc == "" {
			return notImplemented("service_status requires parameters.service")
		}
		cmd = exec.CommandContext(ctx, "systemctl", "status", svc, "--no-pager")
	case dsl.ActionServiceControl:
		return notImplemented("service_control is not executed by LocalSSHAdapter in this build")
	case dsl.ActionFirewallStatus:
		cmd = exec.CommandContext(ctx, "sh", "-c", "command -v nft >/dev/null && nft list ruleset 2>/dev/null | head -n 40 || (command -v iptables >/dev/null && iptables -L -n 2>/dev/null | head -n 40) || echo 'no nft/iptables'")
	case dsl.ActionExplainIntent:
		msg := paramString(task.Parameters, "summary")
		if msg == "" {
			msg = fmt.Sprintf("%v", task.Parameters)
		}
		return &dsl.Result{OK: true, ExitCode: 0, Stdout: msg + "\n"}, nil
	default:
		return &dsl.Result{
			OK:       false,
			ExitCode: -1,
			Message:  fmt.Sprintf("action %q is not implemented on LocalSSHAdapter", task.Action),
		}, fmt.Errorf("unsupported action %q", task.Action)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := 0
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		// success
	case errors.As(runErr, &exitErr):
		exitCode = exitErr.ExitCode()
	default:
		// Non-exit errors (binary missing, timeout, etc.) surface as Go errors.
		return &dsl.Result{
			OK:       false,
			ExitCode: -1,
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
			Message:  runErr.Error(),
		}, runErr
	}

	res := &dsl.Result{
		OK:       runErr == nil,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if runErr != nil {
		res.Message = runErr.Error()
	}
	return res, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
