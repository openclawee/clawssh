# Contributing to ClawSSH

Thanks for your interest in contributing! ClawSSH is an SSH operations gateway focused on **security**, **auditability**, and **extensible execution backends**.

## Development setup

### Prerequisites

- Go 1.22+
- (Optional) Docker / Docker Compose for container testing

### Run locally

From repo root:

```bash
cp .env.example .env
# edit .env: set CLAWSSH_PASSWORD (and LLM variables if you want llm mode)
go run ./cmd/clawssh
```

Connect:

```bash
ssh -p 2222 operator@127.0.0.1
```

### Run tests

```bash
go test ./...
```

## Project structure (high level)

- `cmd/clawssh/`: entrypoint
- `internal/server/`: SSH REPL + orchestration (intent → policy → execute → audit)
- `internal/engine/`: intent engines (keyword, LLM, hybrid), schema validation, output summarizer
- `internal/policy/`: pre-execution policy checks and risk decisions
- `internal/audit/`: async audit persistence (JSONL) + slog
- `internal/adapter/`: execution backends (local, remote ssh, inventory dispatcher, registry)
- `internal/inventory/`: Ansible-INI inventory parsing (alias/group resolution)
- `internal/knowledge/`: local SOP loading + retrieval injection for LLM
- `internal/monitor/`: gateway status report (`status` command)
- `configs/`: embedded schema/prompt + example configs

## Adding a new Adapter (example: Kubernetes / AWS / HTTP)

Adapters are implementations of `internal/adapter.ToolProvider` (and optionally `StreamToolProvider` / `PoolStatsProvider`).

### 1) Implement the provider

Create a new file under `internal/adapter/`, e.g. `k8s.go`:

- Implement:
  - `Execute(task *dsl.Task) (*dsl.Result, error)`
- Optional:
  - `ExecuteStream(task, stdout, stderr)` if your backend can stream logs/progress
  - `ConnectionPoolStats()` if your backend maintains a pool (for `status`)

### 2) Define routing via `task.Source`

ClawSSH routes tasks via `task.Source` through `internal/adapter.Registry`.

Pick a stable source name, e.g.:
- `k8s`
- `aws`
- `http`

### 3) Register the adapter

Registration happens in `cmd/clawssh/main.go` in `buildAdapterRegistry(...)`.

Add:

```go
_ = reg.Register("k8s", adapter.NewK8sProvider(...))
```

### 4) Return stable errors for recovery hints

When possible, return `*adapter.ExecError` with a meaningful `Code`:

- `connection_failed`
- `auth_failed`
- `timeout`
- `invalid_input`
- `unsupported_action`
- `execution_failed`

This enables:
- better UX hints in the SSH session
- better audit categorization
- future policy/engine decisions

### 5) Keep security boundaries intact

Adapters **must not** bypass the policy layer:
- Policy checks happen in `internal/server/handler.go` before `tools.Execute(...)`.
- Do not run shell pipelines or interpolate untrusted parameters.
- Prefer structured parameters and strict allowlists.

### 6) Audit and observability

Every task run is audited automatically, including:
- user/session
- raw input
- generated DSL
- policy outcome
- stdout/stderr (or streamed output metadata)
- optional one-line summary

If your adapter emits additional useful metadata, attach it via `dsl.Result.Message` or return a typed `ExecError`.

## Contribution guidelines

- **Security-first**: avoid executing arbitrary shell; validate inputs aggressively.
- **Small PRs**: keep changes focused and easy to review.
- **Tests**: add or update tests for new logic.
- **No secrets**: do not commit `.env`, SSH keys, or audit logs.

## Before opening a PR

- `go test ./...`
- Confirm `.gitignore` excludes:
  - `.env`
  - `host_key`
  - `logs/` and `data/`

