#!/usr/bin/env bash
# check.sh - every check before a push: format, vet, tests, staticcheck, govulncheck.
# the pre-push hook and CI both run this file, so a push and CI never disagree.
set -euo pipefail
cd "$(dirname "$0")/.."

STATICCHECK=honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK=golang.org/x/vuln/cmd/govulncheck@v1.9.0

step() { printf '\033[1m▸ %s\033[0m\n' "$*"; }

step gofmt
bad="$(gofmt -l .)"
if [ -n "$bad" ]; then
  printf 'not formatted (run gofmt -w):\n%s\n' "$bad"
  exit 1
fi

step "go vet"
go vet ./...

step "go test"
go test -race ./...

step staticcheck
go run "$STATICCHECK" ./...

step govulncheck
go run "$GOVULNCHECK" ./...

printf '\033[32m✓ all checks passed\033[0m\n'
