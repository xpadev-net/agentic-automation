#!/usr/bin/env bash
# Keep local validation and CI aligned across both independent Go modules.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
check="${1:-}"
case "$check" in
  deps|fmt|test|vet|build) ;;
  *) echo "Usage: $0 {deps|fmt|test|vet|build}" >&2; exit 2 ;;
esac

if [[ "$check" == fmt ]]; then
  diff="$(gofmt -s -d "$root")"
  if [[ -n "$diff" ]]; then
    printf '%s\n' "$diff"
    exit 1
  fi
  exit 0
fi

for module in . agent-runner; do
  echo "==> $module: $check"
  (
    cd "$root/$module"
    case "$check" in
      deps) go mod download; go mod verify; go mod tidy -diff ;;
      test) CGO_ENABLED=1 go test ./... -v ;;
      vet) go vet ./... ;;
      build)
        mkdir -p "$root/bin"
        if [[ "$module" == . ]]; then
          go build -o "$root/bin/operator" ./cmd/operator
        else
          go build -o "$root/bin/agent-runner" .
        fi
        ;;
    esac
  )
done
