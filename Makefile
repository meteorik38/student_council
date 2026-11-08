.PHONY: build run test test-integration vet fmt up stop down rebuild list migrate create-admin

build:
	go build -trimpath -o bin/kan-db ./cmd/server
run:
	go run ./cmd/server
test:
	go test -race ./...
test-integration:
	@test -n "$$TEST_DATABASE_URL" || (echo "Set TEST_DATABASE_URL to a dedicated test PostgreSQL database"; exit 1)
	go test -race -count=1 ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal
up:
	docker compose up --build -d
stop:
	docker compose stop
down:
	docker compose down
rebuild:
	docker compose up --build --force-recreate -d
list:
	docker compose ps
migrate:
	go run ./cmd/server migrate
create-admin:
	docker compose run --rm backend create-admin
