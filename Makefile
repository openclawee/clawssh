.PHONY: run run-p2p-coord run-p2p-client setup-env test build

# Run from repo root so .env is found.
run:
	go run ./cmd/clawssh

run-p2p-coord:
	go run ./cmd/clawssh-p2p-coord

run-p2p-client:
	go run ./cmd/clawssh-p2p-client

test:
	go test ./...

build:
	go build ./cmd/clawssh ./cmd/clawssh-p2p-coord ./cmd/clawssh-p2p-client

# One-time: copy template; then edit .env (password, API keys, etc.).
setup-env:
	@if [ -f .env ]; then echo ".env already exists — edit it or delete first"; exit 1; fi
	cp .env.example .env
	@echo "Created .env — edit it, then: make run"
