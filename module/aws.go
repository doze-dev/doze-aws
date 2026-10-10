// Package aws implements the doze engine.Driver for the WHOLE local-AWS
// surface as ONE instance: the embedded doze-aws stack — every service behind
// one gateway on one endpoint, with the web console at /_console and the
// traffic recorder in the request path. One block, one process, one line in
// the dash; the console is where you interact with what's inside.
//
//	aws "local" {
//	  bucket "uploads" { versioning = true }
//	  queue  "emails"  { dlq = "auto" }
//	  topic  "signups" { subscribe { queue = "emails" } }
//	  table  "sessions" { key = "session_id:S" }
//	  function "resize" { code = "./fn" }
//	  rule "orders" { pattern = "{\"source\":[\"shop\"]}"  targets = ["lambda:resize"] }
//	  state_machine "order_flow" { definition = file("./order_flow.asl.json") }
//	}
package aws

import (
	"github.com/doze-dev/doze-aws/module/awslocal"
)

// New returns the configured aws driver. One endpoint serves every service
// (the doze-aws gateway routes by protocol, exactly like the standalone
// binary's :4566), so the SDK var is the global AWS_ENDPOINT_URL.
func New() Driver {
	return Driver{awslocal.BaseDriver{
		Name:        "aws",
		EndpointEnv: "AWS_ENDPOINT_URL",
	}}
}

// Driver is the aws engine driver.
type Driver struct {
	awslocal.BaseDriver
}
