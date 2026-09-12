package sns

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/doze-dev/doze-aws/internal/logship"
)

// Delivery status logging, the way SNS does it.
//
// A topic's <Protocol>SuccessFeedbackRoleArn and <Protocol>FailureFeedbackRoleArn
// attributes (Protocol one of SQS, Lambda, HTTP, Firehose, Application) turn
// on one JSON record per delivery attempt, written to the log group
// sns/<region>/<account>/<topic> for a success and .../Failure for a failure.
// <Protocol>SuccessFeedbackSampleRate (0–100, default 100) samples the
// successes; failures are always written. The role itself gates nothing
// here: setting it is the switch, as on AWS.

// deliveryOutcome is what one attempt produced.
type deliveryOutcome struct {
	protocol    string // sqs | lambda | http | https
	destination string
	status      int // the endpoint's HTTP status when there was one
	err         error
	dwell       time.Duration
}

// deliveryLogs writes delivery status records for every topic.
type deliveryLogs struct{ ship *logship.Shipper }

func newDeliveryLogs(srv *Server) *deliveryLogs {
	return &deliveryLogs{ship: logship.New("sns", srv.peers, srv.logf)}
}

// feedbackProtocol is the attribute-name spelling of a subscription protocol.
func feedbackProtocol(protocol string) string {
	switch protocol {
	case "sqs":
		return "SQS"
	case "lambda":
		return "Lambda"
	case "http", "https":
		return "HTTP"
	case "firehose":
		return "Firehose"
	case "application":
		return "Application"
	}
	return ""
}

// record writes the attempt when the topic's attributes ask for it.
func (l *deliveryLogs) record(srv *Server, topicARN, msgID, message string, o deliveryOutcome) {
	t, err := srv.store.GetTopic(topicARN)
	if err != nil || t == nil {
		return
	}
	p := feedbackProtocol(o.protocol)
	if p == "" {
		return
	}
	failed := o.err != nil
	group := "sns/" + srv.id.RegionName() + "/" + srv.id.Account() + "/" + t.Name
	if failed {
		if t.Attrs[p+"FailureFeedbackRoleArn"] == "" {
			return
		}
		group += "/Failure"
	} else {
		if t.Attrs[p+"SuccessFeedbackRoleArn"] == "" {
			return
		}
		rate := 100
		if v, err := strconv.Atoi(t.Attrs[p+"SuccessFeedbackSampleRate"]); err == nil && v >= 0 && v <= 100 {
			rate = v
		}
		if rate < 100 && rand.IntN(100) >= rate {
			return
		}
	}
	now := srv.now().UTC()
	sum := md5.Sum([]byte(message))
	status, response := "SUCCESS", "Message delivered"
	code := o.status
	if failed {
		status, response = "FAILURE", o.err.Error()
		if code == 0 {
			code = 400
		}
	} else if code == 0 {
		code = 200
	}
	rec := map[string]any{
		"notification": map[string]any{
			"messageMD5Sum": hex.EncodeToString(sum[:]),
			"messageId":     msgID,
			"topicArn":      topicARN,
			"timestamp":     now.Format("2006-01-02 15:04:05.000"),
		},
		"delivery": map[string]any{
			"deliveryId":       fmt.Sprintf("%x", rand.Uint64()),
			"destination":      o.destination,
			"providerResponse": response,
			"dwellTimeMs":      o.dwell.Milliseconds(),
			"attempts":         1,
			"statusCode":       code,
		},
		"status": status,
	}
	raw, _ := json.Marshal(rec)
	l.ship.Put(group, t.Name, logship.Event{Timestamp: now.UnixMilli(), Message: string(raw), RequestID: msgID})
}

func (l *deliveryLogs) close() { l.ship.Close() }
