#!/usr/bin/env bash
# Apply a realistic stack with Terraform's AWS provider against a running
# doze-aws, then plan again and require nothing to change, then destroy.
#
#   ENDPOINT=http://127.0.0.1:4566 ./smoke.sh
set -euo pipefail
cd "$(dirname "$0")"
: "${ENDPOINT:=http://127.0.0.1:4566}"
tf="${TERRAFORM:-terraform}"
export TF_IN_AUTOMATION=1 TF_INPUT=0
export TF_VAR_endpoint="$ENDPOINT"

"$tf" init -input=false -no-color >/dev/null
trap '"$tf" destroy -auto-approve -no-color >/dev/null 2>&1 || true' EXIT
"$tf" apply -auto-approve -no-color

# The second plan is the check: the provider read every resource back after
# creating it, and anything doze-aws returned differently from what was written
# is a change Terraform would want to make.
if ! "$tf" plan -detailed-exitcode -no-color; then
  echo "terraform sees drift right after its own apply (exit $?)" >&2
  exit 1
fi
"$tf" destroy -auto-approve -no-color
trap - EXIT
echo "terraform smoke: ok"
