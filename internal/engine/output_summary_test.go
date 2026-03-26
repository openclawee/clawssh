package engine

import (
	"context"
	"testing"

	"github.com/clawssh/clawssh/pkg/dsl"
)

type stubSummaryLLM struct {
	lastSystem string
	lastUser   string
	reply      string
}

func (s *stubSummaryLLM) Generate(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	s.lastSystem = systemPrompt
	s.lastUser = userMessage
	return s.reply, nil
}

func TestOutputSummarizer_Summarize(t *testing.T) {
	llm := &stubSummaryLLM{reply: "CPU 空闲，负载较低。"}
	sum := &OutputSummarizer{
		client:        llm,
		enabled:       true,
		timeout:       2,
		minChars:      1,
		maxInputChars: 100,
	}
	out, err := sum.Summarize(context.Background(), &dsl.Task{Action: dsl.ActionCheckCPU, Target: "web1"}, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected summary")
	}
}

func TestOutputSummarizer_Summarize_DropsThink(t *testing.T) {
	llm := &stubSummaryLLM{reply: "<think>reason</think>\nCPU 空闲。"}
	sum := &OutputSummarizer{
		client:        llm,
		enabled:       true,
		timeout:       2,
		minChars:      1,
		maxInputChars: 100,
	}
	out, err := sum.Summarize(context.Background(), &dsl.Task{Action: dsl.ActionCheckCPU, Target: "web1"}, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "CPU 空闲。" {
		t.Fatalf("unexpected cleaned summary: %q", out)
	}
}
