.PHONY: up down fmt vet test test-race integration integration-short

up:
	docker compose up -d postgres localstack keycloak

down:
	docker compose down -v

fmt:
	gofmt -l -w .

vet:
	go vet ./...
	go vet -tags=integration ./...

test:
	go test ./...

test-race:
	go test -race ./...

# Exige `make up` (PostgreSQL, LocalStack e Keycloak em execução).
integration:
	go test -tags=integration -race -count=1 -timeout=20m ./test/integration/...

# Pula os testes que dependem de visibility timeout / lease (30s).
integration-short:
	go test -tags=integration -race -short -count=1 -timeout=10m ./test/integration/...
