.PHONY: run test mutation build migrate openapi

-include .env
export

DATABASE_HOST ?= localhost
DATABASE_PORT ?= 5432
DATABASE_USER ?= discordia
DATABASE_PASSWORD ?= discordia
DATABASE_NAME ?= discordia_servicio
DATABASE_SSLMODE ?= disable

run:
	go run ./cmd/api

test:
	go test ./... -p 1 -coverpkg=./internal/... -cover

# Mutation testing de internal/service con gremlins (ver docs/mutation-testing).
# Tarda unos 10 minutos (los tests usan bcrypt); no corre en el CI.
mutation:
	go clean -testcache
	gremlins unleash ./internal/service --timeout-coefficient 5 --workers 6 \
		--invert-assignments --invert-logical --invert-loopctrl --remove-self-assignments \
		--invert-bitwise --invert-bwassign --output docs/mutation-testing/after.json

build:
	go build -o bin/api ./cmd/api

# Lee la conexion de .env, asi el mismo target sirve tanto para el Postgres
# local (defaults de arriba) como para Supabase (host/sslmode=require/
# DATABASE_SCHEMA seteados en .env).
migrate:
	docker run --rm -v $(shell pwd)/migrations:/migrations --net=host migrate/migrate -path=/migrations -database "postgres://$(DATABASE_USER):$(DATABASE_PASSWORD)@$(DATABASE_HOST):$(DATABASE_PORT)/$(DATABASE_NAME)?sslmode=$(DATABASE_SSLMODE)$(if $(DATABASE_SCHEMA),&search_path=$(DATABASE_SCHEMA),)" up

openapi:
	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g cmd/api/main.go -o ./.openapi-tmp --parseInternal -q
	npx --yes swagger2openapi@7 ./.openapi-tmp/swagger.yaml -o openapi.yaml --patch
	rm -rf ./.openapi-tmp
