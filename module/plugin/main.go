// Command aws-plugin runs the whole local-AWS stack as ONE out-of-process
// doze module: every service behind one gateway, the web console at /_console,
// and the traffic recorder in the request path.
package main

import (
	"github.com/doze-dev/doze-aws/module/awslocal"
	aws "github.com/doze-dev/doze-aws/module"
)

func main() { awslocal.PluginMain(aws.New(), &aws.Config{}) }
