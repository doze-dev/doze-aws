// Not Go. This file stops the go command from walking node_modules (aws-cdk ships
// Go project templates) when it expands ./...
module cdk-smoke

go 1.21
