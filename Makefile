.PHONY: run test build migrate

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
	go test ./... -cover

build:
	go build -o bin/api ./cmd/api

# Lee la conexion de .env, asi el mismo target sirve tanto para el Postgres
# local (defaults de arriba) como para Supabase (host/sslmode=require/
# DATABASE_SCHEMA seteados en .env).
migrate:
	docker run --rm -v $(shell pwd)/migrations:/migrations --net=host migrate/migrate -path=/migrations -database "postgres://$(DATABASE_USER):$(DATABASE_PASSWORD)@$(DATABASE_HOST):$(DATABASE_PORT)/$(DATABASE_NAME)?sslmode=$(DATABASE_SSLMODE)$(if $(DATABASE_SCHEMA),&search_path=$(DATABASE_SCHEMA),)" up
