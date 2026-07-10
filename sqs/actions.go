package sqs

import (
	"strconv"

	"github.com/doze-dev/doze-aws/awsident"
)

// handler implements one SQS action against the store. Returns the result value
// (nil for empty-body actions) or an apiError.
type handler func(s *Store, req *request) (any, *apiError)

var handlers = map[string]handler{
	"CreateQueue":                hCreateQueue,
	"DeleteQueue":                hDeleteQueue,
	"ListQueues":                 hListQueues,
	"GetQueueUrl":                hGetQueueURL,
	"GetQueueAttributes":         hGetQueueAttributes,
	"SetQueueAttributes":         hSetQueueAttributes,
	"SendMessage":                hSendMessage,
	"SendMessageBatch":           hSendMessageBatch,
	"ReceiveMessage":             hReceiveMessage,
	"DeleteMessage":              hDeleteMessage,
	"DeleteMessageBatch":         hDeleteMessageBatch,
	"ChangeMessageVisibility":    hChangeMessageVisibility,
	"PurgeQueue":                 hPurgeQueue,
	"TagQueue":                   hTagQueue,
	"UntagQueue":                 hUntagQueue,
	"ListQueueTags":              hListQueueTags,
	"ListDeadLetterSourceQueues": hListDeadLetterSourceQueues,
	"AddPermission":              hAddPermission,
	"RemovePermission":           hRemovePermission,
	"StartMessageMoveTask":       hStartMessageMoveTask,
	"ListMessageMoveTasks":       hListMessageMoveTasks,
	"CancelMessageMoveTask":      hCancelMessageMoveTask,
	"DozePeek":                   hDozePeek, // non-AWS: read-only inspector peek
}

func queueURL(host, name string) string {
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + host + "/" + awsident.AccountID + "/" + name
}

// targetQueue resolves the queue name from a QueueUrl param (or QueueName).
func targetQueue(req *request) string {
	if q := req.p.str("QueueUrl"); q != "" {
		return queueNameFromURL(q)
	}
	return req.p.str("QueueName")
}

func asAPIError(err error) *apiError {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*apiError); ok {
		return ae
	}
	return &apiError{Code: "InternalError", Status: 500, Msg: err.Error()}
}

func hCreateQueue(s *Store, req *request) (any, *apiError) {
	name := req.p.str("QueueName")
	if _, err := s.CreateQueue(name, req.p.queueAttrs(), req.p.tags()); err != nil {
		return nil, asAPIError(err)
	}
	return queueURLResult{QueueURL: queueURL(req.host, name)}, nil
}

func hDeleteQueue(s *Store, req *request) (any, *apiError) {
	if err := s.DeleteQueue(targetQueue(req)); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hListQueues(s *Store, req *request) (any, *apiError) {
	names, err := s.ListQueues(req.p.str("QueueNamePrefix"))
	if err != nil {
		return nil, asAPIError(err)
	}
	urls := make([]string, 0, len(names))
	for _, n := range names {
		urls = append(urls, queueURL(req.host, n))
	}
	return listQueuesResult{QueueURLs: urls}, nil
}

func hGetQueueURL(s *Store, req *request) (any, *apiError) {
	name := req.p.str("QueueName")
	if _, err := s.Attributes(name); err != nil {
		return nil, asAPIError(err)
	}
	return queueURLResult{QueueURL: queueURL(req.host, name)}, nil
}

func hGetQueueAttributes(s *Store, req *request) (any, *apiError) {
	attrs, err := s.Attributes(targetQueue(req))
	if err != nil {
		return nil, asAPIError(err)
	}
	requested := req.p.attributeNames()
	if len(requested) > 0 {
		filtered := kvAttrs{}
		for k, v := range attrs {
			if wants(requested, k) {
				filtered[k] = v
			}
		}
		return getAttrsResult{Attributes: filtered}, nil
	}
	return getAttrsResult{Attributes: kvAttrs(attrs)}, nil
}

func hSetQueueAttributes(s *Store, req *request) (any, *apiError) {
	if err := s.SetAttributes(targetQueue(req), req.p.queueAttrs()); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hSendMessage(s *Store, req *request) (any, *apiError) {
	delay := -1
	if n, ok := req.p.intp("DelaySeconds"); ok {
		delay = n
	}
	m, err := s.Send(targetQueue(req), req.p.str("MessageBody"), req.p.messageAttrs(),
		delay, req.p.str("MessageGroupId"), req.p.str("MessageDeduplicationId"))
	if err != nil {
		return nil, asAPIError(err)
	}
	return sendResult{MessageID: m.ID, MD5OfBody: m.MD5Body, MD5OfAttrs: m.MD5Attrs}, nil
}

func hSendMessageBatch(s *Store, req *request) (any, *apiError) {
	queue := targetQueue(req)
	var res sendBatchResult
	for _, e := range req.p.sendBatchEntries() {
		delay := -1
		if e.Delay != nil {
			delay = *e.Delay
		}
		m, err := s.Send(queue, e.Body, e.Attrs, delay, e.GroupID, e.DedupID)
		if err != nil {
			ae := asAPIError(err)
			res.Failed = append(res.Failed, batchErr{ID: e.ID, Code: ae.Code, Message: ae.Msg, SenderFault: true})
			continue
		}
		res.Successful = append(res.Successful, sendBatchOK{ID: e.ID, MessageID: m.ID, MD5OfBody: m.MD5Body, MD5OfAttrs: m.MD5Attrs})
	}
	return res, nil
}

func hReceiveMessage(s *Store, req *request) (any, *apiError) {
	queue := targetQueue(req)
	max := req.p.intDefault("MaxNumberOfMessages", 1)
	wait := -1
	if n, ok := req.p.intp("WaitTimeSeconds"); ok {
		wait = n
	}
	vis := -1
	if n, ok := req.p.intp("VisibilityTimeout"); ok {
		vis = n
	}
	attrNames := req.p.attributeNames()
	maNames := req.p.messageAttributeNames()

	msgs, err := s.Receive(queue, max, wait, vis)
	if err != nil {
		return nil, asAPIError(err)
	}
	var res receiveResult
	for _, m := range msgs {
		mv := msgView{MessageID: m.ID, ReceiptHandle: m.Handle(), MD5OfBody: m.MD5Body, Body: m.Body}
		if sa := systemAttrs(m, attrNames); len(sa) > 0 {
			mv.Attributes = sa
		}
		if ma := filterAttrs(m.Attrs, maNames); len(ma) > 0 {
			mv.MessageAttributes = ma
			mv.MD5OfMessageAttributes = md5Attributes(map[string]Attr(ma))
		}
		res.Messages = append(res.Messages, mv)
	}
	return res, nil
}

// hDozePeek is doze's non-AWS read-only peek: it returns the queue's full visible
// contents (every message, not just FIFO group heads) without consuming, hiding,
// or bumping the receive count — so the dash inspector matches the queue depth and
// repeated refreshes don't look like consumption.
func hDozePeek(s *Store, req *request) (any, *apiError) {
	queue := targetQueue(req)
	max := req.p.intDefault("MaxNumberOfMessages", 10)
	attrNames := req.p.attributeNames()
	maNames := req.p.messageAttributeNames()

	msgs, err := s.Peek(queue, max)
	if err != nil {
		return nil, asAPIError(err)
	}
	var res receiveResult
	for _, m := range msgs {
		mv := msgView{MessageID: m.ID, ReceiptHandle: m.Handle(), MD5OfBody: m.MD5Body, Body: m.Body}
		if sa := systemAttrs(m, attrNames); len(sa) > 0 {
			mv.Attributes = sa
		}
		if ma := filterAttrs(m.Attrs, maNames); len(ma) > 0 {
			mv.MessageAttributes = ma
			mv.MD5OfMessageAttributes = md5Attributes(map[string]Attr(ma))
		}
		res.Messages = append(res.Messages, mv)
	}
	return res, nil
}

func hDeleteMessage(s *Store, req *request) (any, *apiError) {
	if err := s.Delete(targetQueue(req), req.p.str("ReceiptHandle")); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hDeleteMessageBatch(s *Store, req *request) (any, *apiError) {
	queue := targetQueue(req)
	var res deleteBatchResult
	for _, e := range req.p.deleteBatchEntries() {
		if err := s.Delete(queue, e.ReceiptHandle); err != nil {
			ae := asAPIError(err)
			res.Failed = append(res.Failed, batchErr{ID: e.ID, Code: ae.Code, Message: ae.Msg, SenderFault: true})
			continue
		}
		res.Successful = append(res.Successful, deleteBatchOK{ID: e.ID})
	}
	return res, nil
}

func hChangeMessageVisibility(s *Store, req *request) (any, *apiError) {
	timeout := req.p.intDefault("VisibilityTimeout", 0)
	if err := s.ChangeVisibility(targetQueue(req), req.p.str("ReceiptHandle"), timeout); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hPurgeQueue(s *Store, req *request) (any, *apiError) {
	if err := s.Purge(targetQueue(req)); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

// systemAttrs builds the requested message system attributes.
func systemAttrs(m Message, names []string) kvAttrs {
	if len(names) == 0 {
		return nil
	}
	out := kvAttrs{}
	add := func(k, v string) {
		if wants(names, k) {
			out[k] = v
		}
	}
	add("ApproximateReceiveCount", strconv.Itoa(m.ReceiveCount))
	add("SentTimestamp", strconv.FormatInt(m.Sent/1e6, 10))
	if m.FirstReceived > 0 {
		add("ApproximateFirstReceiveTimestamp", strconv.FormatInt(m.FirstReceived/1e6, 10))
	}
	if m.GroupID != "" {
		add("MessageGroupId", m.GroupID)
	}
	if m.DedupID != "" {
		add("MessageDeduplicationId", m.DedupID)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterAttrs(all map[string]Attr, names []string) msgAttrs {
	if len(all) == 0 || len(names) == 0 {
		return nil
	}
	out := msgAttrs{}
	for k, v := range all {
		if wants(names, k) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
