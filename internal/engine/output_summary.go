package engine

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/clawssh/clawssh/pkg/dsl"
)

type OutputSummarizer struct {
	client        LLMClient
	enabled       bool
	timeout       time.Duration
	minChars      int
	maxInputChars int
}

func NewOutputSummarizerFromEnv(ctx context.Context) (*OutputSummarizer, error) {
	enabled := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_ENABLED"))
	if enabled == "" || enabled == "0" || strings.EqualFold(enabled, "false") {
		return &OutputSummarizer{enabled: false}, nil
	}
	timeout := 8 * time.Second
	if s := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_TIMEOUT_SECONDS")); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 60 {
			timeout = time.Duration(v) * time.Second
		}
	}
	minChars := 800
	if s := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_MIN_CHARS")); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			minChars = v
		}
	}
	maxInput := 3000
	if s := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_MAX_INPUT_CHARS")); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 200 && v <= 20000 {
			maxInput = v
		}
	}

	c, err := newSummaryClientFromEnv(ctx)
	if err != nil {
		// If enabled but client missing, return disabled summarizer so gateway still runs.
		return &OutputSummarizer{enabled: false}, nil
	}
	return &OutputSummarizer{
		client:        c,
		enabled:       true,
		timeout:       timeout,
		minChars:      minChars,
		maxInputChars: maxInput,
	}, nil
}

// Prefer dedicated summary vars; fallback to main client env.
func newSummaryClientFromEnv(ctx context.Context) (LLMClient, error) {
	baseURL := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_MODEL"))
	key := strings.TrimSpace(os.Getenv("CLAWSSH_SUMMARY_API_KEY"))
	if baseURL != "" || model != "" || key != "" {
		if baseURL == "" || model == "" || key == "" {
			return nil, fmt.Errorf("set CLAWSSH_SUMMARY_BASE_URL, CLAWSSH_SUMMARY_MODEL, CLAWSSH_SUMMARY_API_KEY together")
		}
		return newOpenAICompatibleClient(ctx, openAICompatibleConfig{BaseURL: baseURL, Model: model, APIKey: key})
	}
	return NewLLMClientFromEnv(ctx)
}

func (s *OutputSummarizer) ShouldSummarize(stdout, stderr string) bool {
	if s == nil || !s.enabled || s.client == nil {
		return false
	}
	return len(stdout)+len(stderr) >= s.minChars
}

func (s *OutputSummarizer) Summarize(ctx context.Context, task *dsl.Task, stdout, stderr string) (string, error) {
	if !s.ShouldSummarize(stdout, stderr) {
		return "", nil
	}
	sumCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	payload := fmt.Sprintf(
		"action=%s target=%s source=%s is_group=%v\n\nstdout:\n%s\n\nstderr:\n%s\n",
		safe(task, func(t *dsl.Task) string { return t.Action }),
		safe(task, func(t *dsl.Task) string { return t.Target }),
		safe(task, func(t *dsl.Task) string { return t.Source }),
		safeBool(task, func(t *dsl.Task) bool { return t.IsGroup }),
		trunc(stdout, s.maxInputChars),
		trunc(stderr, s.maxInputChars),
	)
	system := "You are an SRE assistant.\n" +
		"Summarize the command output into ONE concise Chinese sentence (<= 30 Chinese characters preferred).\n" +
		"Hard rules:\n" +
		"- Output ONE line only.\n" +
		"- Do NOT output <think> or any reasoning.\n" +
		"- Do NOT output markdown/code fences/bullets.\n" +
		"- Do NOT include JSON.\n"
	out, err := s.client.Generate(sumCtx, system, payload)
	if err != nil {
		return "", err
	}
	clean := sanitizeSummary(out)
	if clean == "" {
		return "", fmt.Errorf("empty/invalid summary output")
	}
	return clean, nil
}

func trunc(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sanitizeSummary(out string) string {
	s := strings.TrimSpace(out)
	if s == "" {
		return ""
	}
	// Drop any <think> blocks or reasoning tags.
	low := strings.ToLower(s)
	if strings.Contains(low, "<think") {
		// If it's a block, try to keep content after closing tag.
		if idx := strings.Index(low, "</think>"); idx >= 0 {
			s = strings.TrimSpace(s[idx+len("</think>"):])
		} else {
			return ""
		}
	}
	s = strings.TrimSpace(singleLine(s))
	low = strings.ToLower(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(low, "```") || strings.Contains(low, "```") {
		return ""
	}
	if strings.ContainsAny(s, "{}[]") {
		// Avoid leaking JSON blobs as "summary".
		return ""
	}
	// Avoid obvious meta markers.
	if strings.Contains(low, "think") {
		return ""
	}
	return s
}

func safe(task *dsl.Task, f func(*dsl.Task) string) string {
	if task == nil {
		return ""
	}
	return f(task)
}

func safeBool(task *dsl.Task, f func(*dsl.Task) bool) bool {
	if task == nil {
		return false
	}
	return f(task)
}
