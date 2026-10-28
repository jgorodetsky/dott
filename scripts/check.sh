#!/usr/bin/env bash
# check.sh - every check before a push: format, vet, notices, tests, staticcheck, govulncheck.
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

step "third-party notices"
scripts/notices.sh --check

# the ranking tests run against the built-in fzf and the fzf binary of the same pinned version
step "fzf binary for the ranking tests"
fzfdir="$(mktemp -d)"
trap 'rm -r "$fzfdir"' EXIT
go build -o "$fzfdir/fzf" github.com/junegunn/fzf

step "go test"
DOTT_TEST_FZF="$fzfdir/fzf" go test -race ./...

step staticcheck
go run "$STATICCHECK" ./...

step govulncheck
go run "$GOVULNCHECK" ./...

printf '\033[32m✓ all checks passed\033[0m\n'
