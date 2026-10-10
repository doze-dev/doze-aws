package sqs

// Handlers added in the doze-aws port: tags, dead-letter source discovery,
// message move tasks, and the permission no-ops.

import (
	"sort"

	"github.com/doze-dev/doze-aws/internal/awspage"
)

type listTagsResult struct {
	Tags kvAttrs `json:"Tags,omitempty" xml:"Tag"`
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
	return listTagsResult{Tags: kvAttrs(tags)}, nil
}

type dlqSourcesResult struct {
	QueueURLs []string `json:"queueUrls,omitempty" xml:"QueueUrl"`
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
	return dlqSourcesResult{QueueURLs: urls, NextToken: next}, nil
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
	source := arnQueueName(req.p.str("SourceArn"))
	if source == "" {
		return nil, errInvalid("SourceArn is required")
	}
	// Optional: without one each message returns to the queue it was
	// dead-lettered from (doc: the StartMessageMoveTask API reference).
	dest := arnQueueName(req.p.str("DestinationArn"))
	// doc: "The maximum value for messages per second is 500".
	perSecond, _, aerr := req.p.intParam("MaxNumberOfMessagesPerSecond", 1, 500)
	if aerr != nil {
		return nil, aerr
	}
	task, err := s.StartMessageMoveTask(source, dest, perSecond)
	if err != nil {
		return nil, asAPIError(err)
	}
	return startMoveResult{TaskHandle: task.Handle}, nil
}

func hListMessageMoveTasks(s *store, req *request) (any, *apiError) {
	source := arnQueueName(req.p.str("SourceArn"))
	max := req.p.intDefault("MaxResults", 1)
	tasks, err := s.ListMessageMoveTasks(source, max)
	if err != nil {
		return nil, asAPIError(err)
	}
	var res listMoveTasksResult
	for _, t := range tasks {
		res.Results = append(res.Results, moveTaskView{
			TaskHandle:                        t.Handle,
			Status:                            t.Status,
			SourceArn:                         s.queueARN(t.Source),
			DestinationArn:                    s.queueARN(t.Destination),
			ApproximateNumberOfMessagesMoved:  int64(t.Moved),
			ApproximateNumberOfMessagesToMove: int64(t.ToMove),
			MaxNumberOfMessagesPerSecond:      t.MaxPerSecond,
			StartedTimestamp:                  t.StartedAt * 1000, // epoch millis
			FailureReason:                     t.FailureReason,
		})
	}
	return res, nil
}

func hCancelMessageMoveTask(s *store, req *request) (any, *apiError) {
	if req.p.str("TaskHandle") == "" {
		return nil, errInvalid("TaskHandle is required")
	}
	return nil, asAPIError(s.CancelMessageMoveTask(req.p.str("TaskHandle")))
}
