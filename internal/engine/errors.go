package engine

import "errors"

// ErrAIUnavailable indicates the remote LLM provider failed (network, quota, auth, etc.).
// Callers may degrade to keyword routing or show an operator-facing message.
var ErrAIUnavailable = errors.New("ai engine unavailable")
