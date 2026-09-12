package cloudwatch

// The four dashboard operations, and the three tag operations.
//
// Both were refused by name until the thing they act on existed: dashboards
// wanted a store (D2), tags wanted resources worth tagging (alarms, D3).
// Those reasons stopped being true, which is the only kind of refusal worth
// removing.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

// tagsFromParams reads CloudWatch's tag shape — a list of {Key, Value}, not
// the map CloudWatch Logs uses — into the map the store keeps.
func tagsFromParams(p params, name string) map[string]string {
	entries := p.List(name)
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if k := e.Str("Key"); k != "" {
			out[k] = e.Str("Value")
		}
	}
	return out
}

// msToISO renders an epoch-millisecond stamp the way the API reports times.
func msToISO(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ---- dashboards ----

type dashboardEntryView struct {
	DashboardName string `json:"DashboardName" xml:"DashboardName"`
	DashboardArn  string `json:"DashboardArn" xml:"DashboardArn"`
	LastModified  string `json:"LastModified" xml:"LastModified"`
	Size          int64  `json:"Size" xml:"Size"`
}

type listDashboardsResult struct {
	DashboardEntries []dashboardEntryView `json:"DashboardEntries" xml:"DashboardEntries>member"`
}

type getDashboardResult struct {
	DashboardName string `json:"DashboardName" xml:"DashboardName"`
	DashboardArn  string `json:"DashboardArn" xml:"DashboardArn"`
	DashboardBody string `json:"DashboardBody" xml:"DashboardBody"`
}

// putDashboardResult carries DashboardValidationMessages, which AWS uses to
// report a body it accepted but did not fully understand. Always empty here:
// the body is stored verbatim and never interpreted, so there is nothing this
// build could truthfully complain about.
type putDashboardResult struct {
	DashboardValidationMessages []string `json:"DashboardValidationMessages" xml:"DashboardValidationMessages>member"`
}

func (s *Server) putDashboardAction(req *request) (any, *awshttp.APIError) {
	name := req.params.Str("DashboardName")
	body := req.params.Str("DashboardBody")
	if name == "" || body == "" {
		return nil, errMissingParameter("DashboardName and DashboardBody are required")
	}
	// AWS refuses a body that is not JSON, and so does this — storing it
	// verbatim is a promise about formatting, not a licence to store garbage.
	if !json.Valid([]byte(body)) {
		return nil, errf("InvalidParameterInput", "The dashboard body is not valid JSON.")
	}
	d := dashboard{Name: name, Body: body, UpdatedMs: s.now().UnixMilli(),
		Tags: tagsFromParams(req.params, "Tags")}
	if err := s.putDashboard(d); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return putDashboardResult{DashboardValidationMessages: []string{}}, nil
}

func (s *Server) getDashboard(req *request) (any, *awshttp.APIError) {
	name := req.params.Str("DashboardName")
	if name == "" {
		return nil, errMissingParameter("DashboardName is required")
	}
	d, err := s.readDashboard(name)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return getDashboardResult{DashboardName: d.Name, DashboardArn: s.dashboardARN(d.Name),
		DashboardBody: d.Body}, nil
}

func (s *Server) listDashboardsAction(req *request) (any, *awshttp.APIError) {
	found, err := s.listDashboards(req.params.Str("DashboardNamePrefix"))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	entries := make([]dashboardEntryView, 0, len(found))
	for _, d := range found {
		entries = append(entries, dashboardEntryView{
			DashboardName: d.Name, DashboardArn: s.dashboardARN(d.Name),
			LastModified: msToISO(d.UpdatedMs), Size: int64(len(d.Body)),
		})
	}
	return listDashboardsResult{DashboardEntries: entries}, nil
}

func (s *Server) deleteDashboardsAction(req *request) (any, *awshttp.APIError) {
	names := req.params.Strs("DashboardNames")
	if len(names) == 0 {
		return nil, errMissingParameter("DashboardNames is required")
	}
	if err := s.deleteDashboards(names); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return struct{}{}, nil
}

// ---- tags ----

type tagView struct {
	Key   string `json:"Key" xml:"Key"`
	Value string `json:"Value" xml:"Value"`
}

type listTagsForResourceResult struct {
	Tags []tagView `json:"Tags" xml:"Tags>member"`
}

// taggable resolves a CloudWatch ARN to the thing it names. Only alarms and
// dashboards exist here, and an ARN for anything else is refused by name
// rather than silently accepted into a tag store nothing reads.
func (s *Server) taggable(arn string) (kind, name string, aerr *awshttp.APIError) {
	switch {
	case strings.Contains(arn, ":alarm:"):
		name = arn[strings.Index(arn, ":alarm:")+len(":alarm:"):]
		a, err := s.getAlarm(name)
		if err != nil {
			return "", "", awshttp.AsAPIError(err)
		}
		if a == nil {
			return "", "", errf("ResourceNotFound", "Alarm %s does not exist.", name)
		}
		return "alarm", name, nil
	case strings.Contains(arn, ":dashboard/"):
		name = arn[strings.Index(arn, ":dashboard/")+len(":dashboard/"):]
		if _, err := s.readDashboard(name); err != nil {
			return "", "", errNoDashboard(name)
		}
		return "dashboard", name, nil
	}
	return "", "", errInvalidParameter(
		"ResourceARN %q must name a CloudWatch alarm or dashboard", arn)
}

// withTags reads the resource's tags, hands them to edit, and stores what
// comes back — so tagging an alarm and tagging a dashboard differ only in
// where the map lives.
func (s *Server) withTags(arn string, edit func(map[string]string)) *awshttp.APIError {
	kind, name, aerr := s.taggable(arn)
	if aerr != nil {
		return aerr
	}
	switch kind {
	case "alarm":
		a, err := s.getAlarm(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if a.Tags == nil {
			a.Tags = map[string]string{}
		}
		edit(a.Tags)
		// No history entry: a tag is not a configuration change to the alarm
		// AWS records, and inventing one would put noise in the pane a person
		// reads to find out why an alarm fired.
		if err := s.putAlarm(a, historyEntry{}); err != nil {
			return awshttp.AsAPIError(err)
		}
	case "dashboard":
		d, err := s.readDashboard(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if d.Tags == nil {
			d.Tags = map[string]string{}
		}
		edit(d.Tags)
		if err := s.putDashboard(d); err != nil {
			return awshttp.AsAPIError(err)
		}
	}
	return nil
}

func (s *Server) tagResource(req *request) (any, *awshttp.APIError) {
	tags := tagsFromParams(req.params, "Tags")
	if len(tags) == 0 {
		return nil, errMissingParameter("Tags is required")
	}
	if aerr := s.withTags(req.params.Str("ResourceARN"), func(have map[string]string) {
		for k, v := range tags {
			have[k] = v
		}
	}); aerr != nil {
		return nil, aerr
	}
	return struct{}{}, nil
}

func (s *Server) untagResource(req *request) (any, *awshttp.APIError) {
	keys := req.params.Strs("TagKeys")
	if len(keys) == 0 {
		return nil, errMissingParameter("TagKeys is required")
	}
	if aerr := s.withTags(req.params.Str("ResourceARN"), func(have map[string]string) {
		for _, k := range keys {
			delete(have, k)
		}
	}); aerr != nil {
		return nil, aerr
	}
	return struct{}{}, nil
}

func (s *Server) listTagsForResource(req *request) (any, *awshttp.APIError) {
	arn := req.params.Str("ResourceARN")
	kind, name, aerr := s.taggable(arn)
	if aerr != nil {
		return nil, aerr
	}
	var have map[string]string
	switch kind {
	case "alarm":
		a, err := s.getAlarm(name)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		have = a.Tags
	case "dashboard":
		d, err := s.readDashboard(name)
		if err != nil {
			return nil, awshttp.AsAPIError(err)
		}
		have = d.Tags
	}
	keys := make([]string, 0, len(have))
	for k := range have {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := listTagsForResourceResult{Tags: []tagView{}}
	for _, k := range keys {
		out.Tags = append(out.Tags, tagView{Key: k, Value: have[k]})
	}
	return out, nil
}
