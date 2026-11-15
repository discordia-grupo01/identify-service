.PHONY: run test mutation build migrate openapi contract-deps-up contract-deps-down contract-test

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

# Tests de contrato: Schemathesis contra el servicio levantado con un Postgres y
# un RabbitMQ efimeros y locales (puertos 55504 y 55514) y un SMTP falso local.
# Ver README.
contract-deps-up:
	docker run -d --rm --name contract-identify-pg -p 55504:5432 -e POSTGRES_USER=discordia -e POSTGRES_PASSWORD=discordia -e POSTGRES_DB=discordia_identify_contract postgres:16-alpine
	docker run -d --rm --name contract-identify-rabbitmq -p 55514:5672 rabbitmq:3.13-alpine
	until docker exec contract-identify-pg pg_isready -U discordia -d discordia_identify_contract >/dev/null 2>&1; do sleep 1; done
	sleep 2
	MSYS_NO_PATHCONV=1 docker run --rm -v $(shell pwd)/migrations:/migrations --network container:contract-identify-pg migrate/migrate -path=/migrations -database "postgres://discordia:discordia@localhost:5432/discordia_identify_contract?sslmode=disable" up

contract-deps-down:
	-docker rm -f contract-identify-pg contract-identify-rabbitmq

contract-test:
	bash scripts/contract-test.sh
