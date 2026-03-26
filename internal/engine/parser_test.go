package engine

import (
	"testing"

	"github.com/clawssh/clawssh/configs"
)

func TestExtractJSONFromModelOutput_MarkdownFence(t *testing.T) {
	raw := "```json\n{\"action\":\"check_cpu\",\"target\":\"local\"}\n```"
	got := ExtractJSONFromModelOutput(raw)
	p, err := NewTaskParser(configs.TaskSchemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	task, err := p.ValidateAndUnmarshal(got)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if task.Action != "check_cpu" {
		t.Fatalf("action=%q", task.Action)
	}
}

func TestTaskParser_RejectUnknownAction(t *testing.T) {
	p, err := NewTaskParser(configs.TaskSchemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ValidateAndUnmarshal(`{"action":"rm_rf","target":"local"}`)
	if err == nil {
		t.Fatal("expected schema error")
	}
}

func TestExtractJSONFromModelOutput_ExtraTrailingText(t *testing.T) {
	raw := `分析如下：
{"action":"check_cpu","target":"local","parameters":{}}
5) 结束`
	got := ExtractJSONFromModelOutput(raw)
	if got != `{"action":"check_cpu","target":"local","parameters":{}}` {
		t.Fatalf("unexpected extracted json: %q", got)
	}
}

func TestTaskParser_ValidateAndUnmarshal_RecoversFromNoisyPayload(t *testing.T) {
	p, err := NewTaskParser(configs.TaskSchemaJSON)
	if err != nil {
		t.Fatal(err)
	}
	raw := `结果(建议):
{"action":"check_disk","target":"local","parameters":{}}
附注: done`
	task, err := p.ValidateAndUnmarshal(raw)
	if err != nil {
		t.Fatalf("expected recovery parse success, got: %v", err)
	}
	if task.Action != "check_disk" {
		t.Fatalf("unexpected action: %q", task.Action)
	}
}
