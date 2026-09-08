package console

import (
	"net/url"
	"strings"
)

// resourceRef is a resolved pointer to a console page.
//
// There were three private versions of this before it: resourceLink for
// CloudFormation's physical ids, nodeURL for the wiring graph, and the switch
// inside subViews for SNS subscription endpoints. Each knew a different subset
// of the services, each got a different set of them wrong, and only one of the
// three — subViews — carried the rule the console actually wants, written down
// at sns.html: the endpoint's NAME as a service-coloured link, never a
// truncated ARN. This is that rule, once, for everything.
type resourceRef struct {
	Svc  string // colour and icon key; "" when nothing resolves
	Name string // what to show a human — never an ARN
	Path string // prefix-relative, e.g. "/sqs/emails-dlq"; "" when unresolvable
	// Key is the identifier that is unique WITHIN the service, which is not
	// always the display name: an EventBridge rule is only unique together with
	// its bus. The wiring graph keys nodes on it, which is what stops two buses
	// with a rule of the same name collapsing into one node.
	Key string
}

// OK reports whether the ref points somewhere.
func (r resourceRef) OK() bool { return r.Path != "" }

// arnService maps the service field of an ARN onto the console's own key. They
// differ for four services and every previous resolver hardcoded the ones it
// happened to need.
var arnService = map[string]string{
	"s3": "s3", "sqs": "sqs", "sns": "sns", "lambda": "lambda",
	"dynamodb": "ddb", "kinesis": "kinesis", "events": "eb",
	"kms": "kms", "secretsmanager": "sm", "ssm": "ssm",
	"cloudformation": "cfn", "apigateway": "apigw", "execute-api": "apigw",
	"iam": "iam", "sts": "sts", "states": "sfn",
}

// resourceFromARN parses an ARN, a queue URL, or a bare name-with-kind and
// hands off to resourceURL.
//
// The parse is deliberately not "everything after the last colon", which is
// what the graph did: an ARN's resource part may itself contain colons and
// slashes, so arn:aws:kinesis:…:stream/events came back as "stream/events" and
// arn:aws:lambda:…:function:resize as "resize" only by luck. Three services
// were mis-parsed that way and one of them silently.
func resourceFromARN(s string) resourceRef {
	s = strings.TrimSpace(s)
	if s == "" {
		return resourceRef{}
	}
	// A queue URL is not an ARN but arrives in the same fields.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil && u.Path != "" {
			return resourceURL("sqs", lastSegment(u.Path))
		}
		return resourceRef{}
	}
	if !strings.HasPrefix(s, "arn:") {
		return resourceRef{}
	}
	// arn:partition:service:region:account:resource — the resource keeps its
	// own colons, so split at most six ways.
	parts := strings.SplitN(s, ":", 6)
	if len(parts) < 6 {
		return resourceRef{}
	}
	svc, ok := arnService[parts[2]]
	if !ok {
		return resourceRef{}
	}
	return resourceURL(svc, parts[5])
}

// resourceURL is the ONE place a (service, identifier) becomes a console path.
// It is pure: no context, no I/O, so it can be a template func and callers do
// not have to pre-compute. An identifier it cannot place returns a ref with a
// Name and no Path, so a caller can still render the name as plain text rather
// than choosing between a wrong link and nothing.
func resourceURL(svc, id string) resourceRef {
	id = strings.TrimSpace(id)
	if svc == "" || id == "" {
		return resourceRef{}
	}
	ref := resourceRef{Svc: svc, Name: id}
	switch svc {
	case "s3":
		// classify hands the wire "bucket/key", and dropping the key would land
		// you in the bucket root when the call was three folders down.
		bucket, key, _ := strings.Cut(id, "/")
		ref.Name, ref.Path = bucket, "/s3/"+bucket
		if dir := keyDir(key); dir != "" {
			ref.Path += "?prefix=" + url.QueryEscape(dir)
		}
		if key != "" {
			ref.Name = id
		}
	case "sqs":
		n := lastSegment(id)
		ref.Name, ref.Path = n, "/sqs/"+n
	case "sns":
		// A subscription ARN is the topic ARN plus a uuid; the topic is what has
		// a page. Cut the uuid BEFORE lastSegment — that helper splits on ":"
		// as well as "/", so calling it first eats the topic and keeps the uuid.
		n, _, _ := strings.Cut(id, ":")
		n = lastSegment(n)
		ref.Name, ref.Path = n, "/sns/"+n
	case "lambda":
		// function:name, function:name:version, or a bare name.
		n := strings.TrimPrefix(id, "function:")
		if i := strings.Index(n, ":"); i > 0 {
			n = n[:i]
		}
		ref.Name, ref.Path = n, "/lambda/"+n
	case "ddb":
		// table/orders, or table/orders/stream/2026… — the table owns the page.
		n := strings.TrimPrefix(id, "table/")
		n, _, _ = strings.Cut(n, "/")
		ref.Name, ref.Path = n, "/ddb/"+n
	case "kinesis":
		n := strings.TrimPrefix(id, "stream/")
		n, _, _ = strings.Cut(n, "/")
		ref.Name, ref.Path = n, "/kinesis/"+n
	case "sfn":
		// stateMachine:name, or execution:machine:name — colon-separated, the
		// machine owns the first page and the execution the second.
		if rest, ok := strings.CutPrefix(id, "stateMachine:"); ok {
			ref.Name, ref.Path = rest, "/sfn/"+rest
		} else if rest, ok := strings.CutPrefix(id, "execution:"); ok {
			machine, exec, _ := strings.Cut(rest, ":")
			ref.Name, ref.Path = exec, "/sfn/"+machine+"/execution/"+exec
			ref.Key = machine + "/" + exec
		} else if strings.HasPrefix(id, "activity:") {
			ref.Name = strings.TrimPrefix(id, "activity:")
			return ref // activities have no page
		} else {
			ref.Name, ref.Path = id, "/sfn/"+id
		}
	case "eb":
		// The bus is part of a rule's identity: two buses may each hold a rule
		// called "orders", and pointing both at /eb/default/rule/orders — which
		// is what the graph did — sends you to the wrong one, or to nothing.
		if rest, ok := strings.CutPrefix(id, "api-destination/"); ok {
			// api-destination/<name>/<id>: the destinations page, keyed by name.
			n, _, _ := strings.Cut(rest, "/")
			ref.Name, ref.Path, ref.Key = n, "/eb/destinations", "destination/"+n
			return ref
		}
		if rest, ok := strings.CutPrefix(id, "connection/"); ok {
			n, _, _ := strings.Cut(rest, "/")
			ref.Name, ref.Path, ref.Key = n, "/eb/destinations", "connection/"+n
			return ref
		}
		id = strings.TrimPrefix(id, "event-bus/")
		id = strings.TrimPrefix(id, "rule/")
		if bus, rule, ok := strings.Cut(id, "/"); ok {
			ref.Name, ref.Path = rule, "/eb/"+bus+"/rule/"+rule
			ref.Key = bus + "/" + rule
		} else {
			ref.Name, ref.Path, ref.Key = id, "/eb/"+id, "bus/"+id
		}
	case "kms":
		// An alias needs a lookup to become a key id, which this cannot do
		// without I/O. Name it, do not link it.
		if strings.HasPrefix(id, "alias/") {
			ref.Name = id
			return ref
		}
		n := strings.TrimPrefix(id, "key/")
		ref.Name, ref.Path = n, "/kms/"+n
	case "sm":
		// secret:prod/db-AbCdEf — Secrets Manager appends six random characters.
		n := strings.TrimPrefix(id, "secret:")
		if i := strings.LastIndex(n, "-"); i > 0 && len(n)-i == 7 {
			n = n[:i]
		}
		ref.Name, ref.Path = n, "/sm/secret?name="+url.QueryEscape(n)
	case "ssm":
		n := strings.TrimPrefix(id, "parameter")
		if !strings.HasPrefix(n, "/") {
			n = "/" + n
		}
		ref.Name, ref.Path = n, "/ssm/param?name="+url.QueryEscape(n)
	case "cfn":
		// stack/my-stack/uuid
		n := strings.TrimPrefix(id, "stack/")
		n, _, _ = strings.Cut(n, "/")
		ref.Name, ref.Path = n, "/cfn/"+n
	case "apigw":
		// An execute-api ARN leads with the api id; the rest is stage and path.
		n, _, _ := strings.Cut(strings.TrimPrefix(id, "/restapis/"), "/")
		ref.Name, ref.Path = n, "/apigw/"+n
	case "iam":
		kind, name, ok := strings.Cut(id, "/")
		if !ok || name == "" {
			return resourceRef{Svc: svc, Name: id}
		}
		// Roles and users have pages under their own kind; a policy is
		// addressed by ARN and does not.
		switch kind {
		case "user", "role", "group":
			ref.Name, ref.Path = lastSegment(name), "/iam/"+kind+"/"+lastSegment(name)
		default:
			ref.Name = lastSegment(name)
		}
	default:
		return resourceRef{Svc: svc, Name: id, Key: id}
	}
	if ref.Key == "" {
		ref.Key = ref.Name
	}
	return ref
}

// keyDir is the folder an object key sits in, for landing the S3 browser where
// the object actually is rather than at the bucket root.
func keyDir(key string) string {
	i := strings.LastIndex(key, "/")
	if i <= 0 {
		return ""
	}
	return key[:i+1]
}
