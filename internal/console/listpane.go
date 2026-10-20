package console

import "net/http"

// listPaneData is what a service's list pane reads, by the service's console
// key: its List (and, for the two that draw more beside it, their extras). It
// is the one place that says where each service's list comes from, so a page
// that draws the pane without being that service's own page (a create page, a
// page for a resource that is not there) cannot draw a different one.
//
// ok is false when the service could not be listed. A pane built from a failed
// listing would say "no queues", which is a claim; the caller leaves the pane
// out instead, and the failure it is drawing says what happened. A key with no
// list pane gets ok false too.
func (c *Console) listPaneData(r *http.Request, svc string) (data map[string]any, ok bool) {
	ctx := r.Context()
	var (
		list any
		err  error
	)
	switch svc {
	case "s3":
		list, err = c.be.ListBuckets(ctx)
	case "sqs":
		list, err = c.be.ListQueues(ctx)
	case "ddb":
		list, err = c.be.ListTables(ctx)
	case "sns":
		list, err = c.be.ListTopics(ctx)
	case "eb":
		list, err = c.be.ListBuses(ctx)
	case "kinesis":
		list, err = c.be.ListStreams(ctx)
	case "lambda":
		list, err = c.be.ListFunctions(ctx)
	case "apigw":
		list, err = c.be.ListAllAPIs(ctx)
	case "sfn":
		list, err = c.be.ListStateMachines(ctx)
	case "logs":
		list, err = c.be.ListLogGroups(ctx)
	case "cw":
		list, err = c.be.ListAlarms(ctx)
	case "kms":
		list, err = c.be.ListKeys(ctx)
	case "sm":
		list, err = c.be.ListSecrets(ctx)
	case "ssm":
		list, err = c.be.ListParameters(ctx)
	case "cfn":
		list, err = c.be.ListStacks(ctx)
	case "iam":
		// IAM's pane is four lists, each tolerated on its own as the service's
		// pages do; the principals are the one whose failure is the pane's.
		principals, perr := c.be.ListPrincipals(ctx)
		if perr != nil {
			return nil, false
		}
		_, policies, groups, profiles := c.iamNav(r)
		return map[string]any{"Principals": principals, "Policies": policies, "NavGroups": groups, "NavProfiles": profiles}, true
	default:
		return nil, false
	}
	if err != nil {
		return nil, false
	}
	data = map[string]any{"List": list}
	if svc == "sfn" {
		data["ActCount"] = c.sfnActivityCount(r)
	}
	return data, true
}
