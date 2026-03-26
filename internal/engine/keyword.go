package engine

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// ErrUnknownIntent is returned when input cannot be mapped to a supported action.
var ErrUnknownIntent = errors.New("unknown intent: input does not match a supported command")

// KeywordRouter is a minimal MVP IntentEngine that maps fixed phrases to Tasks.
// It exists to validate the kernel/plugin boundaries before LLM integration lands.
type KeywordRouter struct{}

// NewKeywordRouter constructs a KeywordRouter instance.
func NewKeywordRouter() *KeywordRouter {
	return &KeywordRouter{}
}

// ShowProgress implements ProgressReporter (keyword routing is instantaneous).
func (k *KeywordRouter) ShowProgress() bool { return false }

// Parse normalizes whitespace and case, then applies a small phrase table.
func (k *KeywordRouter) Parse(ctx context.Context, input string, ses *Context, _ io.Writer) (*dsl.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = ses
	normalized := strings.TrimSpace(strings.ToLower(input))
	switch normalized {
	case "check cpu":
		return &dsl.Task{
			Action: dsl.ActionCheckCPU,
			Target: dsl.TargetLocal,
		}, nil
	case "check disk":
		return &dsl.Task{
			Action: dsl.ActionCheckDisk,
			Target: dsl.TargetLocal,
		}, nil
	default:
		return nil, ErrUnknownIntent
	}
}
