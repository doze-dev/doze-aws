package sqs

// Handlers added in the doze-aws port: tags, dead-letter source discovery,
// message move tasks, and the permission no-ops.

type listTagsResult struct {
	Tags kvAttrs `json:"Tags,omitempty" xml:"Tag"`
}

func hTagQueue(s *Store, req *request) (any, *apiError) {
	tags := req.p.tags()
	if len(tags) == 0 {
		return nil, errInvalid("at least one tag is required")
	}
	if err := s.TagQueue(targetQueue(req), tags); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hUntagQueue(s *Store, req *request) (any, *apiError) {
	keys := req.p.tagKeys()
	if len(keys) == 0 {
		return nil, errInvalid("at least one tag key is required")
	}
	if err := s.UntagQueue(targetQueue(req), keys); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hListQueueTags(s *Store, req *request) (any, *apiError) {
	tags, err := s.Tags(targetQueue(req))
	if err != nil {
		return nil, asAPIError(err)
	}
	return listTagsResult{Tags: kvAttrs(tags)}, nil
}

type dlqSourcesResult struct {
	QueueURLs []string `json:"queueUrls,omitempty" xml:"QueueUrl"`
}

func hListDeadLetterSourceQueues(s *Store, req *request) (any, *apiError) {
	names, err := s.DeadLetterSourceQueues(targetQueue(req))
	if err != nil {
		return nil, asAPIError(err)
	}
	urls := make([]string, 0, len(names))
	for _, n := range names {
		urls = append(urls, queueURL(req.host, n))
	}
	return dlqSourcesResult{QueueURLs: urls}, nil
}

// hAddPermission / hRemovePermission are Tier C: doze-aws has no IAM, so queue
// policies can't grant anything — the calls succeed (application setup code
// keeps working) and change nothing.
func hAddPermission(s *Store, req *request) (any, *apiError) {
	if _, err := s.Attributes(targetQueue(req)); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

func hRemovePermission(s *Store, req *request) (any, *apiError) {
	if _, err := s.Attributes(targetQueue(req)); err != nil {
		return nil, asAPIError(err)
	}
	return nil, nil
}

type startMoveResult struct {
	TaskHandle string `json:"TaskHandle" xml:"TaskHandle"`
}

type moveTaskView struct {
	TaskHandle                       string `json:"TaskHandle,omitempty" xml:"TaskHandle,omitempty"`
	Status                           string `json:"Status" xml:"Status"`
	SourceArn                        string `json:"SourceArn" xml:"SourceArn"`
	DestinationArn                   string `json:"DestinationArn,omitempty" xml:"DestinationArn,omitempty"`
	ApproximateNumberOfMessagesMoved int64  `json:"ApproximateNumberOfMessagesMoved" xml:"ApproximateNumberOfMessagesMoved"`
	StartedTimestamp                 int64  `json:"StartedTimestamp,omitempty" xml:"StartedTimestamp,omitempty"`
	FailureReason                    string `json:"FailureReason,omitempty" xml:"FailureReason,omitempty"`
}

type listMoveTasksResult struct {
	Results []moveTaskView `json:"Results,omitempty" xml:"ListMessageMoveTasksResultEntry"`
}

func hStartMessageMoveTask(s *Store, req *request) (any, *apiError) {
	source := arnQueueName(req.p.str("SourceArn"))
	if source == "" {
		return nil, errInvalid("SourceArn is required")
	}
	dest := arnQueueName(req.p.str("DestinationArn"))
	if dest == "" {
		// AWS defaults to each message's original source queue; doze-aws does
		// not track message provenance, so the destination must be explicit.
		return nil, errInvalid("DestinationArn is required (doze-aws does not track per-message origin queues)")
	}
	task, err := s.StartMessageMoveTask(source, dest)
	if err != nil {
		return nil, asAPIError(err)
	}
	return startMoveResult{TaskHandle: task.Handle}, nil
}

func hListMessageMoveTasks(s *Store, req *request) (any, *apiError) {
	source := arnQueueName(req.p.str("SourceArn"))
	max := req.p.intDefault("MaxResults", 1)
	tasks, err := s.ListMessageMoveTasks(source, max)
	if err != nil {
		return nil, asAPIError(err)
	}
	var res listMoveTasksResult
	for _, t := range tasks {
		res.Results = append(res.Results, moveTaskView{
			TaskHandle:                       t.Handle,
			Status:                           t.Status,
			SourceArn:                        queueARN(t.Source),
			DestinationArn:                   queueARN(t.Destination),
			ApproximateNumberOfMessagesMoved: int64(t.Moved),
			StartedTimestamp:                 t.StartedAt * 1000, // epoch millis
			FailureReason:                    t.FailureWhy,
		})
	}
	return res, nil
}

func hCancelMessageMoveTask(s *Store, req *request) (any, *apiError) {
	if req.p.str("TaskHandle") == "" {
		return nil, errInvalid("TaskHandle is required")
	}
	return nil, asAPIError(s.CancelMessageMoveTask(req.p.str("TaskHandle")))
}
