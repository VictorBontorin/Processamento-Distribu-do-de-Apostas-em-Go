.PHONY: up down run migrate migrate-down fmt vet test test-race integration integration-short

up:
	docker compose up -d postgres localstack keycloak

down:
	docker compose down -v

# Sobe tudo (API inclusa).
run:
	docker compose up --build

# Aplica as migrations pendentes / reverte as N últimas (N=1 por padrão).
migrate:
	docker compose run --rm migrate

migrate-down:
	docker compose run --rm migrate sh /scripts/migrate.sh down $(or $(N),1)

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
