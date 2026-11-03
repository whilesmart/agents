#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
test -z "$(gofmt -l .)"
go mod verify
go test -race -count=1 -coverprofile=coverage.out ./...
go vet ./...
