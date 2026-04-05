.PHONY: run run-wsproxy setup-env test build

# Run from repo root so .env is found.
run:
	go run ./cmd/clawssh

run-wsproxy:
	go run ./cmd/clawssh-wsproxy

test:
	go test ./...

build:
	go build ./cmd/clawssh ./cmd/clawssh-wsproxy

# One-time: copy template; then edit .env (password, API keys, etc.).
setup-env:
	@if [ -f .env ]; then echo ".env already exists — edit it or delete first"; exit 1; fi
	cp .env.example .env
	@echo "Created .env — edit it, then: make run"
