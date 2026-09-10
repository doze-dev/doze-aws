package peercall

// Publishing metrics to the CloudWatch service from another service.
//
// The wire is AWS JSON 1.0 under the GraniteServiceVersion20100801 target,
// which is one of the three CloudWatch speaks. JSON rather than CBOR because
// this is doze-aws talking to itself: the CBOR path exists for SDKs that
// insist on it, and a peer call gains nothing from the extra encoder.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/doze-dev/doze-aws/peers"
)

// ErrNoCloudWatch reports that the metrics service is not enabled, so a
// producer can say so once and stop rather than logging every publish.
var ErrNoCloudWatch = errors.New("peercall: the cloudwatch service is not enabled")

// MetricDatum is one observation to publish.
type MetricDatum struct {
	MetricName string
	Value      float64
	Unit       string
	Dimensions map[string]string
	// TimestampMs is when it happened; zero means now.
	TimestampMs int64
	// StorageResolution is 1 for high-resolution metrics, 60 otherwise.
	StorageResolution int
}

// PutMetrics publishes a batch into one namespace.
func PutMetrics(ctx context.Context, dir peers.Directory, namespace string, data []MetricDatum) error {
	if len(data) == 0 {
		return nil
	}
	if _, ok := dir.Endpoint("cloudwatch"); !ok {
		return ErrNoCloudWatch
	}
	items := make([]any, 0, len(data))
	for _, d := range data {
		item := map[string]any{"MetricName": d.MetricName, "Value": d.Value}
		if d.Unit != "" {
			item["Unit"] = d.Unit
		}
		if d.TimestampMs > 0 {
			// AWS JSON carries a timestamp as epoch SECONDS, fractional part
			// included — not milliseconds, which is the easy mistake and puts
			// every observation fifty thousand years out.
			item["Timestamp"] = float64(d.TimestampMs) / 1000
		}
		if d.StorageResolution > 0 {
			item["StorageResolution"] = d.StorageResolution
		}
		if len(d.Dimensions) > 0 {
			dims := make([]any, 0, len(d.Dimensions))
			for _, k := range sortedKeys(d.Dimensions) {
				dims = append(dims, map[string]any{"Name": k, "Value": d.Dimensions[k]})
			}
			item["Dimensions"] = dims
		}
		items = append(items, item)
	}
	raw, err := json.Marshal(map[string]any{"Namespace": namespace, "MetricData": items})
	if err != nil {
		return err
	}
	if _, err := JSONCall(ctx, dir, "cloudwatch",
		"GraniteServiceVersion20100801.PutMetricData", "application/x-amz-json-1.0",
		namespace, raw); err != nil {
		return fmt.Errorf("peercall: PutMetricData: %w", err)
	}
	return nil
}

// sortedKeys keeps dimension order stable, so two identical publishes produce
// identical requests — which is what makes a recorded wire comparable.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
