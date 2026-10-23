.PHONY: generate fmt test vet build verify run migrate integration
-include .env.local
export APP_ENV APP_BASE_URL DATABASE_URL DEV_DATABASE_URL TEST_DATABASE_URL AUTH_ALLOWED_DOMAINS GOOGLE_CLIENT_ID GOOGLE_CLIENT_SECRET CSRF_SECRET SLACK_BOT_TOKEN SLACK_RECIPIENT_ALLOWLIST POSTHOG_PROJECT_TOKEN POSTHOG_HOST UPLOAD_TEMP_DIR UPLOAD_DIR GCS_BUCKET GOOGLE_APPLICATION_CREDENTIALS PORT

generate:
	go generate ./ent
	go tool templ generate
fmt: generate
	gofmt -w cmd internal ent migrations examples deploy
test:
	go test ./...
vet:
	go vet ./...
build:
	mkdir -p .cache
	go build -o .cache/server ./cmd/server
	go build -o .cache/migrate ./cmd/migrate
verify: generate
	test -z "$$(gofmt -l cmd internal ent migrations examples deploy)"
	go test -race ./...
	go vet ./...
	$(MAKE) build
run:
	go run ./cmd/server
migrate:
	@set -a; if [ -f .env ]; then . ./.env || exit $$?; fi; exec go run ./cmd/migrate
integration:
	@test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL obbligatoria" >&2; exit 1)
	go test -race ./... -count=1
