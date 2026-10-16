package sqs

// Handlers added in the doze-aws port: tags, dead-letter source discovery,
// message move tasks, and the permission no-ops.

import (
	"sort"

	"github.com/doze-dev/doze-aws/internal/awspage"
)

type listTagsResult struct {
	Tags tagMap `json:"Tags,omitempty" xml:"Tag"`
}

func hTagQueue(s *store, req *request) (any, *apiError) {
	tags := req.p.tags()
	if len(tags) == 0 {
		return nil, errInvalid("at least one tag is required")
	}
	if err := s.TagQueue(targetQueue(req), tags); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hUntagQueue(s *store, req *request) (any, *apiError) {
	keys := req.p.tagKeys()
	if len(keys) == 0 {
		return nil, errInvalid("at least one tag key is required")
	}
	if err := s.UntagQueue(targetQueue(req), keys); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hListQueueTags(s *store, req *request) (any, *apiError) {
	tags, err := s.Tags(targetQueue(req))
	if err != nil {
		return nil, asAPIError(err)
	}
	return listTagsResult{Tags: tagMap(tags)}, nil
}

type dlqSourcesResult struct {
	QueueURLs []string `json:"queueUrls" xml:"QueueUrl"`
	NextToken string   `json:"NextToken,omitempty" xml:"NextToken,omitempty"`
}

func hListDeadLetterSourceQueues(s *store, req *request) (any, *apiError) {
	limit, _, aerr := req.p.intParam("MaxResults", 1, maxListQueues)
	if aerr != nil {
		return nil, aerr
	}
	names, err := s.DeadLetterSourceQueues(targetQueue(req))
	if err != nil {
		return nil, asAPIError(err)
	}
	sort.Strings(names)
	page, next, perr := awspage.Slice(names, func(n string) string { return n }, req.p.str("NextToken"),
		awspage.Limit(limit, maxListQueues, maxListQueues))
	if perr != nil {
		return nil, errInvalid("Value for parameter NextToken is invalid. Reason: the token is not one this listing issued.")
	}
	urls := make([]string, 0, len(page))
	for _, n := range page {
		urls = append(urls, s.queueURL(req.host, n))
	}
	if urls == nil {
		urls = []string{} // a required member: [] when empty
	}
	return dlqSourcesResult{QueueURLs: urls, NextToken: next}, nil
}

// moveTaskError answers a queue that is not there the way the move-task
// operations do: their modelled error is ResourceNotFoundException, not the
// QueueDoesNotExist of the queue operations.
func moveTaskError(err error) *apiError {
	aerr := asAPIError(err)
	if aerr != nil && aerr.Code == "AWS.SimpleQueueService.NonExistentQueue" {
		return &apiError{Code: "ResourceNotFoundException", Status: 404, SenderFault: true, Message: aerr.Message}
	}
	return aerr
}

type cancelMoveResult struct {
	Moved int64 `json:"ApproximateNumberOfMessagesMoved" xml:"ApproximateNumberOfMessagesMoved"`
}

type startMoveResult struct {
	TaskHandle string `json:"TaskHandle" xml:"TaskHandle"`
}

type moveTaskView struct {
	TaskHandle                        string `json:"TaskHandle,omitempty" xml:"TaskHandle,omitempty"`
	Status                            string `json:"Status" xml:"Status"`
	SourceArn                         string `json:"SourceArn" xml:"SourceArn"`
	DestinationArn                    string `json:"DestinationArn,omitempty" xml:"DestinationArn,omitempty"`
	ApproximateNumberOfMessagesToMove int64  `json:"ApproximateNumberOfMessagesToMove" xml:"ApproximateNumberOfMessagesToMove"`
	MaxNumberOfMessagesPerSecond      int    `json:"MaxNumberOfMessagesPerSecond,omitempty" xml:"MaxNumberOfMessagesPerSecond,omitempty"`
	ApproximateNumberOfMessagesMoved  int64  `json:"ApproximateNumberOfMessagesMoved" xml:"ApproximateNumberOfMessagesMoved"`
	StartedTimestamp                  int64  `json:"StartedTimestamp,omitempty" xml:"StartedTimestamp,omitempty"`
	FailureReason                     string `json:"FailureReason,omitempty" xml:"FailureReason,omitempty"`
}

type listMoveTasksResult struct {
	Results []moveTaskView `json:"Results,omitempty" xml:"ListMessageMoveTasksResultEntry"`
}

func hStartMessageMoveTask(s *store, req *request) (any, *apiError) {
	if req.p.str("SourceArn") == "" {
		return nil, errInvalid("SourceArn is required")
	}
	source, ok := s.ownQueue(req.p.str("SourceArn"))
	if !ok {
		return nil, moveTaskError(errQueueMissing(req.p.str("SourceArn")))
	}
	// Optional: without one each message returns to the queue it was
	// dead-lettered from (doc: the StartMessageMoveTask API reference).
	dest := ""
	if arn := req.p.str("DestinationArn"); arn != "" {
		if dest, ok = s.ownQueue(arn); !ok {
			return nil, moveTaskError(errQueueMissing(arn))
		}
	}
	// doc: "The maximum value for messages per second is 500".
	perSecond, _, aerr := req.p.intParam("MaxNumberOfMessagesPerSecond", 1, 500)
	if aerr != nil {
		return nil, aerr
	}
	task, err := s.StartMessageMoveTask(source, dest, perSecond)
	if err != nil {
		return nil, moveTaskError(err)
	}
	return startMoveResult{TaskHandle: task.Handle}, nil
}

func hListMessageMoveTasks(s *store, req *request) (any, *apiError) {
	source, ok := s.ownQueue(req.p.str("SourceArn"))
	if !ok {
		return nil, moveTaskError(errQueueMissing(req.p.str("SourceArn")))
	}
	// doc: "The default is 1 ... The upper limit is 10."
	max, present, aerr := req.p.intParam("MaxResults", 1, 10)
	if aerr != nil {
		return nil, aerr
	}
	if !present {
		max = 1
	}
	tasks, err := s.ListMessageMoveTasks(source, max)
	if err != nil {
		return nil, moveTaskError(err)
	}
	var res listMoveTasksResult
	for _, t := range tasks {
		view := moveTaskView{
			Status:                            t.Status,
			SourceArn:                         s.queueARN(t.Source),
			ApproximateNumberOfMessagesMoved:  int64(t.Moved),
			ApproximateNumberOfMessagesToMove: int64(t.ToMove),
			MaxNumberOfMessagesPerSecond:      t.MaxPerSecond,
			StartedTimestamp:                  t.StartedAt * 1000, // epoch millis
			FailureReason:                     t.FailureReason,
		}
		// doc: the handle "is only populated for tasks that are in RUNNING
		// status", and the destination is absent when none was named.
		if t.Status == "RUNNING" {
			view.TaskHandle = t.Handle
		}
		if t.Destination != "" {
			view.DestinationArn = s.queueARN(t.Destination)
		}
		res.Results = append(res.Results, view)
	}
	return res, nil
}

func hCancelMessageMoveTask(s *store, req *request) (any, *apiError) {
	if req.p.str("TaskHandle") == "" {
		return nil, errInvalid("TaskHandle is required")
	}
	moved, err := s.CancelMessageMoveTask(req.p.str("TaskHandle"))
	if err != nil {
		return nil, asAPIError(err)
	}
	return cancelMoveResult{Moved: int64(moved)}, nil
}
