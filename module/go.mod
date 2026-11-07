module github.com/doze-dev/doze-aws/module

go 1.27.0

// The module is built beside the stack it embeds, so a change to doze-aws and to its
// doze module is one commit, and the module can use doze-aws's internal packages
// (it lives under the same import path). A nested module keeps doze-aws's own
// go.mod free of the plugin SDK and gRPC.
require (
	github.com/doze-dev/doze-aws v0.3.0
	github.com/doze-dev/doze-sdk v0.12.0
	github.com/hashicorp/hcl/v2 v2.24.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/agext/levenshtein v1.2.1 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/blues/jsonata-go v1.5.4 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/go-chi/chi/v5 v5.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-plugin v1.8.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/zclconf/go-cty v1.18.1 // indirect
	go.etcd.io/bbolt v1.5.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260630182238-925bb5da69e7 // indirect
	google.golang.org/grpc v1.82.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/doze-dev/doze-aws => ../
