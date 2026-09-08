.PHONY: run test build migrate

run:
	go run ./cmd/api

test:
	go test ./... -cover

build:
	go build -o bin/api ./cmd/api

migrate:
	docker run --rm -v $(shell pwd)/migrations:/migrations --net=host migrate/migrate -path=/migrations -database "postgres://discordia:discordia@localhost:5432/discordia_servicio?sslmode=disable" up
