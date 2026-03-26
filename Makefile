.PHONY: run setup-env test

# Run from repo root so .env is found.
run:
	go run ./cmd/clawssh

test:
	go test ./...

# One-time: copy template; then edit .env (password, API keys, etc.).
setup-env:
	@if [ -f .env ]; then echo ".env already exists — edit it or delete first"; exit 1; fi
	cp .env.example .env
	@echo "Created .env — edit it, then: make run"
