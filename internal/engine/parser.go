package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// TaskParser validates raw JSON against the embedded ClawSSH task schema.
type TaskParser struct {
	schema *jsonschema.Schema
}

// NewTaskParser compiles the JSON Schema once per process.
func NewTaskParser(schemaJSON []byte) (*TaskParser, error) {
	if len(schemaJSON) == 0 {
		return nil, errors.New("task schema JSON is empty")
	}
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource("task.schema.json", bytes.NewReader(schemaJSON)); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	sch, err := c.Compile("task.schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return &TaskParser{schema: sch}, nil
}

// ExtractJSONFromModelOutput strips markdown fences and returns the innermost JSON object slice.
func ExtractJSONFromModelOutput(raw string) string {
	s := strings.TrimSpace(raw)
	s = stripMarkdownCodeFence(s)
	if obj := firstJSONObjectSlice(s); obj != "" {
		return obj
	}
	return s
}

func stripMarkdownCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "json")
	t = strings.TrimPrefix(t, "JSON")
	t = strings.TrimSpace(t)
	if idx := strings.LastIndex(t, "```"); idx >= 0 {
		t = t[:idx]
	}
	return strings.TrimSpace(t)
}

// ValidateAndUnmarshal checks schema conformance and decodes into dsl.Task.
func (p *TaskParser) ValidateAndUnmarshal(raw string) (*dsl.Task, error) {
	if p == nil || p.schema == nil {
		return nil, errors.New("parser not initialized")
	}
	trim := strings.TrimSpace(raw)
	if trim == "" {
		return nil, errors.New("empty JSON payload")
	}
	var doc any
	if err := json.Unmarshal([]byte(trim), &doc); err != nil {
		// Model outputs may contain extra non-JSON text before/after the object.
		// Try one more extraction pass from the raw payload.
		if obj := firstJSONObjectSlice(trim); obj != "" && obj != trim {
			trim = obj
			if err2 := json.Unmarshal([]byte(trim), &doc); err2 != nil {
				return nil, fmt.Errorf("json preprocess: %w", err2)
			}
		} else {
			return nil, fmt.Errorf("json preprocess: %w", err)
		}
	}
	if err := p.schema.Validate(doc); err != nil {
		return nil, fmt.Errorf("schema validation: %w", err)
	}
	var t dsl.Task
	if err := json.Unmarshal([]byte(trim), &t); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return &t, nil
}

// firstJSONObjectSlice returns the first balanced JSON object substring.
// It is quote-aware, so braces inside JSON strings are ignored.
func firstJSONObjectSlice(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	inStr := false
	escaped := false
	depth := 0
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[start : i+1])
			}
		}
	}
	return ""
}
