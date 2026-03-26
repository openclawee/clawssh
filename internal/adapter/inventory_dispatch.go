package adapter

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/clawssh/clawssh/internal/inventory"
	"github.com/clawssh/clawssh/pkg/dsl"
)

// InventoryDispatch adds alias/group resolution and group parallel execution on top of providers.
type InventoryDispatch struct {
	base ToolProvider
	inv  *inventory.Inventory
}

func NewInventoryDispatch(base ToolProvider, inv *inventory.Inventory) *InventoryDispatch {
	return &InventoryDispatch{base: base, inv: inv}
}

func (d *InventoryDispatch) Execute(task *dsl.Task) (*dsl.Result, error) {
	if task == nil {
		return nil, newExecError(ErrCodeInvalidInput, "task is nil", nil)
	}
	if d.base == nil {
		return nil, newExecError(ErrCodeInvalidInput, "base provider is nil", nil)
	}
	if d.base == nil || d.inv == nil {
		return d.base.Execute(task)
	}
	if d.shouldTreatAsGroup(task) {
		return d.executeGroup(task, nil)
	}
	single := d.resolveSingle(task)
	return d.base.Execute(single)
}

func (d *InventoryDispatch) ExecuteStream(task *dsl.Task, stdout, stderr io.Writer) (*dsl.Result, error) {
	if task == nil {
		return nil, newExecError(ErrCodeInvalidInput, "task is nil", nil)
	}
	if d.base == nil {
		return nil, newExecError(ErrCodeInvalidInput, "base provider is nil", nil)
	}
	if d.base == nil || d.inv == nil {
		if sp, ok := d.base.(StreamToolProvider); ok {
			return sp.ExecuteStream(task, stdout, stderr)
		}
		return d.base.Execute(task)
	}
	if d.shouldTreatAsGroup(task) {
		return d.executeGroup(task, stdout)
	}
	single := d.resolveSingle(task)
	if sp, ok := d.base.(StreamToolProvider); ok {
		return sp.ExecuteStream(single, stdout, stderr)
	}
	return d.base.Execute(single)
}

func (d *InventoryDispatch) shouldTreatAsGroup(task *dsl.Task) bool {
	if task == nil || d.inv == nil {
		return false
	}
	if task.IsGroup {
		return true
	}
	t := strings.TrimSpace(task.Target)
	if t == "" || t == dsl.TargetLocal {
		return false
	}
	_, aliasExists := d.inv.ResolveAlias(t)
	groupHosts := d.inv.ResolveGroup(t)
	// Auto-group only when target is a known group and not a host alias.
	return !aliasExists && len(groupHosts) > 0
}

func (d *InventoryDispatch) executeGroup(task *dsl.Task, progress io.Writer) (*dsl.Result, error) {
	hosts := d.inv.ResolveGroup(task.Target)
	if len(hosts) == 0 {
		msg := fmt.Sprintf("inventory group %q not found or empty", task.Target)
		return &dsl.Result{OK: false, ExitCode: -1, Message: msg}, newExecError(ErrCodeInvalidInput, msg, nil)
	}
	if progress != nil {
		_, _ = fmt.Fprintf(progress, "[ClawSSH]: 正在对 [%s] 组内的 %d 台主机执行指令...\n", task.Target, len(hosts))
	}

	type one struct {
		alias string
		res   *dsl.Result
		err   error
	}
	results := make([]one, len(hosts))
	var wg sync.WaitGroup
	for i := range hosts {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := d.applyHost(task, hosts[i])
			r, err := d.base.Execute(t)
			results[i] = one{alias: hosts[i].Alias, res: r, err: err}
			if progress != nil {
				_, _ = fmt.Fprintf(progress, "[ClawSSH]: %s done\n", hosts[i].Alias)
			}
		}()
	}
	wg.Wait()

	var (
		okAll    = true
		combined strings.Builder
	)
	for _, r := range results {
		if r.err != nil || r.res == nil || !r.res.OK {
			okAll = false
		}
		line := formatHostLine(task.Action, r.alias, r.res, r.err)
		combined.WriteString(line)
		combined.WriteString("\n")
	}
	exit := 0
	if !okAll {
		exit = 1
	}
	return &dsl.Result{
		OK:       okAll,
		ExitCode: exit,
		Stdout:   strings.TrimRight(combined.String(), "\n"),
		Message:  fmt.Sprintf("group %s finished (%d hosts)", task.Target, len(hosts)),
	}, nil
}

func (d *InventoryDispatch) resolveSingle(task *dsl.Task) *dsl.Task {
	if d.inv == nil || task == nil {
		return task
	}
	if strings.TrimSpace(task.Target) == "" || task.Target == dsl.TargetLocal {
		return task
	}
	h, ok := d.inv.ResolveAlias(task.Target)
	if !ok {
		return task
	}
	return d.applyHost(task, h)
}

func (d *InventoryDispatch) applyHost(task *dsl.Task, h inventory.HostInfo) *dsl.Task {
	cp := *task
	cp.IsGroup = false
	cp.Source = "ssh_remote"
	cp.Target = h.Alias
	cp.Parameters = copyMap(task.Parameters)
	cp.Parameters["host"] = h.Host
	cp.Parameters["user"] = h.User
	cp.Parameters["port"] = fmt.Sprintf("%d", h.Port)
	if strings.TrimSpace(h.PrivateKeyFile) != "" {
		cp.Parameters["key_path"] = h.PrivateKeyFile
	}
	if strings.TrimSpace(h.Password) != "" {
		cp.Parameters["password"] = h.Password
	}
	return &cp
}

func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func formatHostLine(action, alias string, res *dsl.Result, err error) string {
	if err != nil {
		return fmt.Sprintf("%s | Error: %v", alias, err)
	}
	if res == nil {
		return fmt.Sprintf("%s | Error: empty result", alias)
	}
	if res.OK {
		return fmt.Sprintf("%s | Success: %s", alias, oneLineSummary(action, res))
	}
	return fmt.Sprintf("%s | Failed: %s", alias, oneLineSummary(action, res))
}

func oneLineSummary(action string, res *dsl.Result) string {
	if res == nil {
		return "empty"
	}
	if human := humanSummaryByAction(action, res.Stdout); human != "" {
		return trimSummary(human)
	}
	if t := bestSummaryLine(res.Stdout); t != "" {
		return trimSummary(t)
	}
	if t := bestSummaryLine(res.Stderr); t != "" {
		return trimSummary(t)
	}
	if t := strings.TrimSpace(res.Message); t != "" {
		return trimSummary(t)
	}
	return fmt.Sprintf("exit=%d ok=%v", res.ExitCode, res.OK)
}

func (d *InventoryDispatch) ConnectionPoolStats() map[string]any {
	out := map[string]any{
		"type": "inventory_dispatch",
	}
	if d == nil || d.base == nil {
		return out
	}
	if ps, ok := d.base.(PoolStatsProvider); ok {
		out["downstream"] = ps.ConnectionPoolStats()
	}
	return out
}

func humanSummaryByAction(action, stdout string) string {
	switch action {
	case dsl.ActionCheckCPU:
		return cpuHumanSummary(stdout)
	case dsl.ActionCheckMemory:
		return memoryHumanSummary(stdout)
	case dsl.ActionCheckDisk:
		return diskHumanSummary(stdout)
	default:
		return ""
	}
}

var cpuLineRE = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+(us|sy|ni|id|wa|hi|si|st)\b`)

func cpuHumanSummary(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var cpuLine string
	for _, ln := range lines {
		if strings.Contains(ln, "%Cpu") || strings.Contains(ln, "Cpu(s)") {
			cpuLine = ln
			break
		}
	}
	if cpuLine == "" {
		return ""
	}
	matches := cpuLineRE.FindAllStringSubmatch(cpuLine, -1)
	if len(matches) == 0 {
		return ""
	}
	val := map[string]float64{}
	for _, m := range matches {
		f, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		val[m[2]] = f
	}
	user := val["us"]
	sys := val["sy"]
	idle := val["id"]
	iowait := val["wa"]
	busy := 100 - idle
	if busy < 0 {
		busy = user + sys + iowait
	}
	return fmt.Sprintf("CPU busy %.1f%% (user %.1f%%, system %.1f%%), idle %.1f%%, iowait %.1f%%", busy, user, sys, idle, iowait)
}

func memoryHumanSummary(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" {
			continue
		}
		// free -m usually prints: Mem: total used free shared buff/cache available
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 7 {
				return fmt.Sprintf(
					"Memory total %sMiB, used %sMiB, free %sMiB, available %sMiB",
					fields[1], fields[2], fields[3], fields[6],
				)
			}
			if len(fields) >= 4 {
				return fmt.Sprintf(
					"Memory total %sMiB, used %sMiB, free %sMiB",
					fields[1], fields[2], fields[3],
				)
			}
		}
		// fallback for other free outputs
		if strings.HasPrefix(line, "MiB Mem") {
			return line
		}
	}
	return ""
}

func diskHumanSummary(stdout string) string {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" || strings.HasPrefix(line, "Filesystem") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			return fmt.Sprintf("Disk %s used %s (%s/%s), mount %s", fields[0], fields[4], fields[2], fields[1], fields[5])
		}
	}
	return ""
}

func bestSummaryLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" {
			continue
		}
		// Prefer key metric rows.
		if strings.HasPrefix(line, "Mem:") || strings.HasPrefix(line, "MiB Mem") || strings.Contains(line, "%Cpu") {
			return line
		}
	}
	for _, ln := range lines {
		line := strings.TrimSpace(ln)
		if line == "" {
			continue
		}
		// Skip common table headers like "total used free ...".
		lower := strings.ToLower(line)
		if strings.Contains(lower, "total") && strings.Contains(lower, "used") && strings.Contains(lower, "free") {
			continue
		}
		return line
	}
	return ""
}

func trimSummary(t string) string {
	t = strings.TrimSpace(t)
	if len(t) > 140 {
		return t[:140] + "..."
	}
	return t
}
