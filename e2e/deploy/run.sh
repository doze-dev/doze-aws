#!/usr/bin/env bash
# Deploy the same kind of application with each tool people actually use, against
# a doze-aws booted for the purpose:  ./run.sh [terraform] [sam] [cdk] [serverless]
#
# Each tool gets a fresh instance, so one tool's leftovers cannot make another
# pass or fail. What a tool needs on this machine — terraform, sam, node, the AWS
# CLI, and python3/node for the functions to run on — is the caller's to provide;
# CI installs them, and a missing one is named rather than skipped.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
tools=("$@")
[ ${#tools[@]} -gt 0 ] || tools=(terraform sam cdk serverless)

bin="${DOZE_AWS_BIN:-}"
if [ -z "$bin" ]; then
  bin="$(mktemp -d)/doze-aws"
  (cd "$root" && go build -o "$bin" ./cmd/doze-aws)
fi

port=14690
for tool in "${tools[@]}"; do
  port=$((port + 1))
  data="$(mktemp -d)"
  # Not from the repo: doze-aws applies ./template.yaml at boot when it finds one,
  # and the SAM example has one.
  (cd "$data" && "$bin" --data-dir "$data/state" --listen "127.0.0.1:$port" >"$data/doze.log" 2>&1) &
  pid=$!
  trap 'kill $pid 2>/dev/null || true' EXIT
  for _ in $(seq 1 50); do
    curl -s -o /dev/null "http://127.0.0.1:$port/" && break
    sleep 0.2
  done
  echo "=== $tool (127.0.0.1:$port)"
  if ! ENDPOINT="http://127.0.0.1:$port" "$here/$tool/smoke.sh"; then
    echo "--- doze-aws log (last 40 lines)" >&2
    tail -40 "$data/doze.log" >&2
    exit 1
  fi
  kill $pid 2>/dev/null || true
  wait $pid 2>/dev/null || true
  trap - EXIT
done
echo "all deploy tools: ok"
