package engine

import (
	"context"
	"io"

	"github.com/clawssh/clawssh/pkg/dsl"
)

// IntentEngine translates operator input into a structured Task.
type IntentEngine interface {
	// Parse converts natural language (or legacy keywords) into a Task.
	// progress receives short UX hints; implementations may ignore it when nil.
	Parse(ctx context.Context, input string, ses *Context, progress io.Writer) (*dsl.Task, error)
}
