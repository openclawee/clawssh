# ClawSSH

**ClawSSH turns operator input into an auditable, governed execution pipeline.**

It provides an interactive SSH gateway: natural language → structured DSL → policy checks → multi-adapter execution → durable audit logs and replay-ready records.

## Why ClawSSH

Classic “SSH into prod and run commands” does not scale: commands are hard to trace, hard to replay, hard to govern, and privileges tend to sprawl. ClawSSH standardizes each operation into a **Task**, enforces **multi-layer security policy** (with **interactive confirmation** for high-risk actions), and records an end-to-end **structured audit trail**.

## Architecture (Mermaid)

```mermaid
flowchart LR
  A[SSH Session] --> B[Input Line]
  B --> C{Hybrid Intent}
  C -->|Keyword fast-path| D[Task DSL]
  C -->|LLM fallback + Knowledge| D[Task DSL]

  D --> E[Policy Engine]
  E -->|Denied| X[Reject + Audit]
  E -->|NeedConfirm| F[SSH Confirm]
  F -->|No| X
  F -->|Yes| G[Adapter Registry]

  G --> H1[Local Adapter]
  G --> H2[Remote SSH Adapter]
  G --> H3[Group Dispatcher (Inventory)]

  H1 --> I[Result]
  H2 --> I
  H3 --> I

  I --> J[Optional: Output Summary LLM]
  I --> K[Audit JSONL + slog]
  J --> K
  I --> L[Terminal Output]
```

## Quickstart (Docker)

1) Prepare persistent data dir:

```bash
mkdir -p data/logs/audit data/docs/ops
cp configs/policies.yaml data/policies.yaml
cp configs/hosts data/hosts
```

2) Edit `docker-compose.yaml` and set:
- `CLAWSSH_PASSWORD`
- LLM credentials (`OPENAI_API_KEY` etc.)

3) Run:

```bash
docker compose up -d --build
```

4) Connect:

```bash
ssh -p 2222 operator@127.0.0.1
```

## Inventory (Ansible INI-style hosts)

ClawSSH uses an Ansible-like inventory to resolve **alias/group** to real connection details.

Example `configs/hosts`:

```ini
[webservers]
web1 ansible_host=10.0.0.12 ansible_user=ubuntu ansible_ssh_private_key_file=~/.ssh/id_rsa
web2 ansible_host=10.0.0.13 ansible_user=ubuntu ansible_password=changeme

[all:vars]
ansible_port=22
ansible_user=ops
```

Try in the SSH session:

```text
check cpu on webservers
check memory on webservers
check disk on web1
status
```

## Security Statement

ClawSSH is designed for production governance:

- **Policy Engine (pre-exec gates)**:
  - allowlist actions/targets
  - blacklist dangerous characters in parameters (`;`, `&&`, `|`, `$(`, …)
  - risk tiers (low/medium/high) and environment-aware overrides
  - interactive confirmation for high-risk actions
- **Audit Trail (async persistence)**:
  - logs to `logs/audit/YYYY-MM-DD.jsonl`
  - captures: user info, raw input, generated DSL, policy result, execution output, and optional summary

## Configuration Guide (minimal)

- **Required** (choose one):
  - `CLAWSSH_PASSWORD`
  - or `CLAWSSH_AUTHORIZED_KEYS`
- **LLM**:
  - `CLAWSSH_INTENT_ENGINE=llm`
  - `CLAWSSH_OPENAI_BASE_URL` + `CLAWSSH_OPENAI_MODEL` + `OPENAI_API_KEY` (or other provider preset)
- **Inventory**:
  - `CLAWSSH_INVENTORY=configs/hosts` (or `/data/hosts` in Docker)

See `.env.example` for full options.
