#!/usr/bin/env bash
# `cdk bootstrap && cdk deploy`, invoke what it made, `cdk destroy`.
#
#   ENDPOINT=http://127.0.0.1:4566 ./smoke.sh
set -euo pipefail
cd "$(dirname "$0")"
: "${ENDPOINT:=http://127.0.0.1:4566}"
export AWS_ENDPOINT_URL="$ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1 AWS_REGION=us-east-1 CDK_DISABLE_VERSION_CHECK=1 CI=true

# The registry now and then answers "no version matching" for a package that exists
# (a mirror not yet caught up); a retry is the whole fix.
for attempt in 1 2 3; do npm install --no-audit --no-fund --silent && break; [ "$attempt" = 3 ] && exit 1; sleep 10; done
npx cdk bootstrap aws://000000000000/us-east-1
npx cdk deploy ShopStack --require-approval never --outputs-file outputs.json

fn="$(node -p "require('./outputs.json').ShopStack.FunctionName")"
payload="$(mktemp)"; out="$(mktemp)"; printf '{}' > "$payload"
aws lambda invoke --function-name "$fn" --payload "fileb://$payload" "$out" >/dev/null
grep -q '"ok": *true' "$out" || { echo "function answered: $(cat "$out")" >&2; exit 1; }

npx cdk destroy ShopStack --force
echo "cdk smoke: ok"
