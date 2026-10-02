package provision

// Part of a stack: the resources somebody picks out of it.
//
// Destroy removes everything in a Stack. Two callers need it to remove less.
// A rollback must delete what a failed apply created and nothing else — a
// resource that already existed under a name the template uses was adopted,
// not made, and deleting it would destroy something the stack never owned.
// And an update must delete what the new template dropped while leaving what
// it kept. Both are "this stack, restricted to these resources", handed to the
// same Destroy.

import "reflect"

// kinds names each resource map of Stack the way Apply's report names it:
// the "queue" in "queue/orders". TestEveryStackFieldHasAKind holds this to the
// struct, so a resource kind added to Stack cannot be forgotten here — which
// would make it invisible to rollback.
var kinds = map[string]string{
	"Queues": "queue", "Topics": "topic", "Buckets": "bucket", "Tables": "table",
	"Functions": "function", "Rules": "rule", "Keys": "key", "Secrets": "secret",
	"Parameters": "parameter", "APIs": "api", "StateMachines": "statemachine",
	"Activities": "activity", "LogGroups": "loggroup", "Layers": "layer",
	"Connections": "connection", "APIDestinations": "apidestination",
	"APIKeys": "apikey", "UsagePlans": "usageplan", "Alarms": "alarm", "Dashboards": "dashboard",
}

// Subset returns a Stack holding only the resources keep says yes to. kind is
// the report's spelling ("queue"), name the resource's key in the stack. The
// original is not changed, and the resources themselves are shared, not copied.
func Subset(s *Stack, keep func(kind, name string) bool) *Stack {
	out := &Stack{}
	src, dst := reflect.ValueOf(s).Elem(), reflect.ValueOf(out).Elem()
	for i := 0; i < src.NumField(); i++ {
		kind, ok := kinds[src.Type().Field(i).Name]
		field := src.Field(i)
		if !ok || field.Kind() != reflect.Map || field.Len() == 0 {
			continue
		}
		picked := reflect.MakeMap(field.Type())
		for _, key := range field.MapKeys() {
			if keep(kind, key.String()) {
				picked.SetMapIndex(key, field.MapIndex(key))
			}
		}
		if picked.Len() > 0 {
			dst.Field(i).Set(picked)
		}
	}
	return out
}

// Names lists every resource in a stack as "kind/name", the form Apply and
// Destroy report in.
func Names(s *Stack) map[string]bool {
	out := map[string]bool{}
	Subset(s, func(kind, name string) bool {
		out[kind+"/"+name] = true
		return false
	})
	return out
}
