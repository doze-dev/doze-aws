#!/usr/bin/env bash
# `sam deploy`, then `aws lambda invoke` on what it made, then delete the stack.
#
#   ENDPOINT=http://127.0.0.1:4566 ./smoke.sh
set -euo pipefail
cd "$(dirname "$0")"
: "${ENDPOINT:=http://127.0.0.1:4566}"
export AWS_ENDPOINT_URL="$ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1 AWS_REGION=us-east-1 SAM_CLI_TELEMETRY=0

aws s3 mb s3://sam-artifacts >/dev/null
sam deploy --stack-name shop --s3-bucket sam-artifacts --capabilities CAPABILITY_IAM \
  --no-confirm-changeset --no-fail-on-empty-changeset --region us-east-1
# The README's command, plus the flags a non-interactive run requires.

status="$(aws cloudformation describe-stacks --stack-name shop --query 'Stacks[0].StackStatus' --output text)"
[ "$status" = "CREATE_COMPLETE" ] || { echo "stack is $status" >&2; exit 1; }

fn="$(aws cloudformation describe-stacks --stack-name shop \
  --query "Stacks[0].Outputs[?OutputKey=='FunctionName'].OutputValue" --output text)"
out="$(mktemp)"; payload="$(mktemp)"; printf '{}' > "$payload"
# fileb:// is raw bytes in both major versions of the CLI; an inline payload
# needs --cli-binary-format in v2 and is refused by v1.
aws lambda invoke --function-name "$fn" --payload "fileb://$payload" "$out" >/dev/null
grep -q '"statusCode": 200' "$out" || { echo "function answered: $(cat "$out")" >&2; exit 1; }

# A second deploy of the same template finds nothing to change.
sam deploy --stack-name shop --s3-bucket sam-artifacts --capabilities CAPABILITY_IAM \
  --no-confirm-changeset --no-fail-on-empty-changeset --region us-east-1

aws cloudformation delete-stack --stack-name shop
aws cloudformation wait stack-delete-complete --stack-name shop
echo "sam smoke: ok"
