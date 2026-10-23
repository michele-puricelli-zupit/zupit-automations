.PHONY: generate fmt test vet build verify run migrate integration
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
	@set -a; if [ -f .env ]; then . ./.env || exit $$?; fi; exec go run ./cmd/server
migrate:
	@set -a; if [ -f .env ]; then . ./.env || exit $$?; fi; exec go run ./cmd/migrate
integration:
	@test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL obbligatoria" >&2; exit 1)
	go test -race ./... -count=1
