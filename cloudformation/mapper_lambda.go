package cloudformation

// Lambda versions, aliases, function URLs and layers: the resources around a
// function that CloudFormation, SAM and the CDK emit on essentially every
// deploy (`fn.currentVersion`, `AutoPublishAlias`, `fn.addFunctionUrl()`,
// `LayerVersion`), mapped onto the function they belong to.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/provision"
)

// layer maps AWS::Lambda::LayerVersion. Content is {S3Bucket, S3Key} — the
// bucket travels as an s3:// reference for the provisioner to fetch, or
// _local_ names a path on disk, the same rule as a function's Code.
func (m *mapper) layer(name string, props map[string]any) error {
	l := provision.Layer{Description: propStr(props, "Description")}
	if content := propMap(props, "Content"); content != nil {
		bucket, key := propStr(content, "S3Bucket"), propStr(content, "S3Key")
		switch {
		case bucket != "" && bucket != "_local_" && key != "":
			l.Code = "s3://" + bucket + "/" + key
		default:
			l.Code = key
		}
	}
	if uri := props["ContentUri"]; uri != nil { // SAM
		l.Code = fmt.Sprint(uri)
	}
	if l.Code == "" {
		return fmt.Errorf("a layer needs Content (S3Bucket and S3Key) or ContentUri")
	}
	for _, rt := range propList(props, "CompatibleRuntimes") {
		l.Runtimes = append(l.Runtimes, fmt.Sprint(rt))
	}
	m.stack.Layers[name] = l
	return nil
}

// functionLayers resolves a function's Layers list once every resource is
// known: a layer version ARN whose layer is in this stack is named, so the
// apply uses the version it publishes; any other ARN is kept as is.
func (m *mapper) functionLayers(fn string, refs []any) {
	m.deferred = append(m.deferred, func() error {
		f, ok := m.stack.Functions[fn]
		if !ok {
			return nil
		}
		for _, ref := range refs {
			arn := fmt.Sprint(ref)
			if i := strings.Index(arn, ":layer:"); i >= 0 {
				if layerName, _, ok := strings.Cut(arn[i+len(":layer:"):], ":"); ok {
					if _, inStack := m.stack.Layers[layerName]; inStack {
						f.Layers = append(f.Layers, layerName)
						continue
					}
				}
			}
			f.Layers = append(f.Layers, arn)
		}
		m.stack.Functions[fn] = f
		return nil
	})
}

// functionVersion maps AWS::Lambda::Version: the function publishes on apply.
func (m *mapper) functionVersion(props map[string]any) error {
	fn := nameFromARN(propStr(props, "FunctionName"))
	if fn == "" {
		return fmt.Errorf("FunctionName is required")
	}
	m.deferred = append(m.deferred, func() error {
		f, ok := m.stack.Functions[fn]
		if !ok {
			return fmt.Errorf("version references unknown function %q", fn)
		}
		f.Publish = true
		m.stack.Functions[fn] = f
		return nil
	})
	return nil
}

// functionAlias maps AWS::Lambda::Alias. FunctionVersion is the placeholder
// of a Version in this template (the version this deploy publishes) or a
// literal number. A weighted RoutingConfig collapses to that version.
func (m *mapper) functionAlias(name string, props map[string]any) error {
	fn := nameFromARN(propStr(props, "FunctionName"))
	if fn == "" {
		return fmt.Errorf("FunctionName is required")
	}
	alias := provision.FunctionAlias{Description: propStr(props, "Description")}
	if v := propStr(props, "FunctionVersion"); v != "" && v != PublishedVersion && v != "$LATEST" {
		alias.Version = v
	}
	m.deferred = append(m.deferred, func() error {
		f, ok := m.stack.Functions[fn]
		if !ok {
			return fmt.Errorf("alias %q references unknown function %q", name, fn)
		}
		if f.Aliases == nil {
			f.Aliases = map[string]provision.FunctionAlias{}
		}
		f.Aliases[name] = alias
		m.stack.Functions[fn] = f
		return nil
	})
	return nil
}

// functionURL maps AWS::Lambda::Url. A qualifier on the target ARN (a URL on
// an alias) is dropped: locally one URL addresses the function.
func (m *mapper) functionURL(props map[string]any) error {
	fn := nameFromARN(propStr(props, "TargetFunctionArn"))
	if fn == "" {
		return fmt.Errorf("TargetFunctionArn is required")
	}
	u := &provision.FunctionURL{AuthType: propStr(props, "AuthType")}
	if cors := propMap(props, "Cors"); cors != nil {
		raw, _ := json.Marshal(cors)
		u.CORS = provision.Doc{JSON: string(raw)}
	}
	m.deferred = append(m.deferred, func() error {
		f, ok := m.stack.Functions[fn]
		if !ok {
			return fmt.Errorf("function URL references unknown function %q", fn)
		}
		f.URL = u
		m.stack.Functions[fn] = f
		return nil
	})
	return nil
}

// ---- API Gateway stage logging ----

// stage maps AWS::ApiGateway::Stage: the API it names gets the stage name
// and the stage's logging settings.
func (m *mapper) stage(props map[string]any) error {
	apiName := nameFromARN(propStr(props, "RestApiId"))
	if apiName == "" {
		return fmt.Errorf("RestApiId is required")
	}
	stageName := propStr(props, "StageName")
	m.deferred = append(m.deferred, func() error {
		api, ok := m.stack.APIs[apiName]
		if !ok {
			return fmt.Errorf("stage references unknown API %q", apiName)
		}
		if stageName != "" {
			api.Stage = stageName
		}
		stageLogging(&api, props)
		m.stack.APIs[apiName] = api
		return nil
	})
	return nil
}

// stageLogging reads AccessLogSetting and MethodSettings — the same shape on
// an AWS::ApiGateway::Stage and a Serverless::Api — onto the API.
func stageLogging(api *provision.API, props map[string]any) {
	if al := propMap(props, "AccessLogSetting"); al != nil {
		api.AccessLog = &provision.APIAccessLog{DestinationARN: propStr(al, "DestinationArn"), Format: propStr(al, "Format")}
	}
	for _, item := range propList(props, "MethodSettings") {
		ms, ok := item.(map[string]any)
		if !ok {
			continue
		}
		api.MethodSettings = append(api.MethodSettings, provision.APIMethodSetting{
			Path: propStr(ms, "ResourcePath"), Method: propStr(ms, "HttpMethod"),
			LoggingLevel: propStr(ms, "LoggingLevel"),
			DataTrace:    propBool(ms, "DataTraceEnabled"), Metrics: propBool(ms, "MetricsEnabled"),
		})
	}
}
