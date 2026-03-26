package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/clawssh/clawssh/configs"
)

type stubLLM struct {
	replies []string
	i       int
	err     error
}

func (s *stubLLM) Generate(ctx context.Context, system, user string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if s.i >= len(s.replies) {
		return "", errors.New("no stub reply")
	}
	out := s.replies[s.i]
	s.i++
	return out, nil
}

func TestLLMIntentEngine_Parse_RetryOnBadJSON(t *testing.T) {
	stub := &stubLLM{
		replies: []string{
			"not json",
			`{"action":"check_disk","target":"local"}`,
		},
	}
	eng, err := NewLLMIntentEngine(LLMIntentConfig{
		Client:       stub,
		SystemPrompt: "sys",
		SchemaJSON:   configs.TaskSchemaJSON,
		Log:          slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := eng.Parse(context.Background(), "disk space", &Context{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if task.Action != "check_disk" {
		t.Fatalf("got %+v", task)
	}
	if stub.i != 2 {
		t.Fatalf("expected two LLM calls, got %d", stub.i)
	}
}

func TestLLMIntentEngine_Parse_APIError(t *testing.T) {
	stub := &stubLLM{err: errors.New("quota")}
	eng, err := NewLLMIntentEngine(LLMIntentConfig{
		Client:       stub,
		SystemPrompt: "x",
		SchemaJSON:   configs.TaskSchemaJSON,
		Log:          slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Parse(context.Background(), "hi", &Context{}, io.Discard)
	if !errors.Is(err, ErrAIUnavailable) {
		t.Fatalf("want ErrAIUnavailable, got %v", err)
	}
}
