// Package configs embeds static prompt and schema assets shipped with ClawSSH.
package configs

import _ "embed"

//go:embed prompts/intent_system_prompt.txt
var IntentSystemPrompt string

//go:embed schemas/task.schema.json
var TaskSchemaJSON []byte
