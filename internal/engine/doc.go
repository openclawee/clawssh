// Package engine contains intent resolution: translating human input into dsl.Task plans.
//
// KeywordRouter preserves deterministic keyword shortcuts for automation and fallback.
// LLMIntentEngine calls Gemini or Anthropic-compatible HTTP APIs, validates JSON against
// the embedded schema in /configs/schemas/task.schema.json, and performs a single
// self-correction retry when the model returns invalid JSON.
package engine
