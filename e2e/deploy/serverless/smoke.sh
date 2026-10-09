#!/usr/bin/env bash
# The Serverless Framework's own sequence, with the AWS CLI doing the calls the
# framework would make: package, create the stack that holds the deployment
# bucket, upload the artifact there, update the stack to the full template.
#
# (`serverless deploy` itself cannot be pointed at a local endpoint — it ships
# its own SDK, which ignores AWS_ENDPOINT_URL — so this is how it is used here.)
#
#   ENDPOINT=http://127.0.0.1:4566 ./smoke.sh
set -euo pipefail
cd "$(dirname "$0")"
: "${ENDPOINT:=http://127.0.0.1:4566}"
export AWS_ENDPOINT_URL="$ENDPOINT" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1 AWS_REGION=us-east-1 SLS_TELEMETRY_DISABLED=1 SLS_NOTIFICATIONS_MODE=off

npm install --no-audit --no-fund --silent
npx serverless package

caps="CAPABILITY_IAM CAPABILITY_NAMED_IAM"
aws cloudformation deploy --stack-name sls-dev --capabilities $caps \
  --template-file .serverless/cloudformation-template-create-stack.json

bucket="$(aws cloudformation describe-stacks --stack-name sls-dev \
  --query "Stacks[0].Outputs[?OutputKey=='ServerlessDeploymentBucketName'].OutputValue" --output text)"
key="$(node -p "require('./.serverless/cloudformation-template-update-stack.json').Resources.HelloLambdaFunction.Properties.Code.S3Key")"
aws s3 cp .serverless/shop.zip "s3://$bucket/$key"

aws cloudformation deploy --stack-name sls-dev --capabilities $caps \
  --template-file .serverless/cloudformation-template-update-stack.json

status="$(aws cloudformation describe-stacks --stack-name sls-dev --query 'Stacks[0].StackStatus' --output text)"
[ "$status" = "UPDATE_COMPLETE" ] || [ "$status" = "CREATE_COMPLETE" ] || { echo "stack is $status" >&2; exit 1; }

payload="$(mktemp)"; out="$(mktemp)"; printf '{}' > "$payload"
aws lambda invoke --function-name shop-dev-hello --payload "fileb://$payload" "$out" >/dev/null
grep -q '"ok": *true' "$out" || { echo "function answered: $(cat "$out")" >&2; exit 1; }

aws s3 rm "s3://$bucket" --recursive >/dev/null
aws cloudformation delete-stack --stack-name sls-dev
aws cloudformation wait stack-delete-complete --stack-name sls-dev
echo "serverless smoke: ok"
