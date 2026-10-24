#!/usr/bin/env sh
set -eu

workspace=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

cd "$workspace/services/inspection"
go run github.com/99designs/gqlgen@v0.17.95 generate --config gqlgen.yml
go run ./cmd/graphqlcontractmanifest
