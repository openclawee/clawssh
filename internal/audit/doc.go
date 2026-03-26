// Package audit provides action audit logging for ClawSSH.
// All executed actions (low/medium/high risk) are logged with User, Input, DSL, Timestamp
// for compliance and forensics. Phase 1 uses slog; future phases may write to file or external store.

package audit
