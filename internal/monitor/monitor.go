package monitor

import (
	"encoding/json"
	"runtime"
	"sync"
	"time"

	"github.com/clawssh/clawssh/internal/adapter"
)

type llmStats struct {
	Count       int64   `json:"count"`
	ErrorCount  int64   `json:"error_count"`
	LastMs      float64 `json:"last_ms"`
	AvgMs       float64 `json:"avg_ms"`
	LastError   string  `json:"last_error,omitempty"`
	UpdatedUnix int64   `json:"updated_unix"`
}

type Reporter struct {
	start time.Time
	mu    sync.Mutex
	llm   llmStats
}

var defaultReporter = NewReporter()

func Default() *Reporter { return defaultReporter }

func NewReporter() *Reporter {
	return &Reporter{start: time.Now()}
}

func (r *Reporter) RecordLLM(duration time.Duration, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ms := float64(duration.Milliseconds())
	r.llm.Count++
	r.llm.LastMs = ms
	if r.llm.Count == 1 {
		r.llm.AvgMs = ms
	} else {
		r.llm.AvgMs = ((r.llm.AvgMs * float64(r.llm.Count-1)) + ms) / float64(r.llm.Count)
	}
	if err != nil {
		r.llm.ErrorCount++
		r.llm.LastError = err.Error()
	}
	r.llm.UpdatedUnix = time.Now().Unix()
}

func (r *Reporter) Status(tools adapter.ToolProvider) map[string]any {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	out := map[string]any{
		"uptime_sec":       int(time.Since(r.start).Seconds()),
		"go_routines":      runtime.NumGoroutine(),
		"memory_alloc_mb":  bytesToMB(mem.Alloc),
		"memory_heap_mb":   bytesToMB(mem.HeapAlloc),
		"memory_sys_mb":    bytesToMB(mem.Sys),
		"gc_total_count":   mem.NumGC,
		"last_gc_unix":     int64(mem.LastGC / 1e9),
		"timestamp_unix":   time.Now().Unix(),
		"connection_pools": map[string]any{},
	}
	r.mu.Lock()
	out["llm"] = r.llm
	r.mu.Unlock()
	if ps, ok := tools.(adapter.PoolStatsProvider); ok {
		out["connection_pools"] = ps.ConnectionPoolStats()
	}
	return out
}

func (r *Reporter) StatusText(tools adapter.ToolProvider) string {
	data := r.Status(tools)
	b, _ := json.MarshalIndent(data, "", "  ")
	return string(b)
}

func bytesToMB(v uint64) float64 {
	return float64(v) / 1024.0 / 1024.0
}
