package console

// Routes: SQS, SNS, EventBridge and Kinesis.

import "github.com/go-chi/chi/v5"

// SQS.
func (c *Console) sqsRoutes(r chi.Router) {
	r.Get("/create", c.createPage("sqs", "sqs_create"))
	r.Get("/", c.sqsQueues)
	r.Post("/create", c.sqsCreateQueue)
	r.Get("/{queue}", c.sqsQueue)
	r.Get("/{queue}/messages", c.sqsMessages) // HTMX partial (polled)
	r.Post("/{queue}/send", c.sqsSend)
	r.Post("/{queue}/purge", c.sqsPurge)
	r.Post("/{queue}/attributes", c.sqsSetAttributes)
	r.Post("/{queue}/delete-message", c.sqsDeleteMessage)
	r.Post("/{queue}/redrive", c.sqsRedrive)
	r.Post("/{queue}/receive", c.sqsReceive)                    // a REAL receive, not a peek
	r.Post("/{queue}/visibility", c.sqsChangeVisibility)        // re-hide or release one
	r.Post("/{queue}/delete-batch", c.sqsDeleteBatch)           // DeleteMessageBatch
	r.Post("/{queue}/visibility-batch", c.sqsVisibilityBatch)   // ChangeMessageVisibilityBatch
	r.Post("/{queue}/permission", c.sqsAddPermission)           // AddPermission (C-tier)
	r.Post("/{queue}/permission/delete", c.sqsRemovePermission) // RemovePermission (C-tier)
	r.Post("/{queue}/delete-queue", c.sqsDeleteQueue)
}

// SNS.
func (c *Console) snsRoutes(r chi.Router) {
	r.Get("/create", c.createPage("sns", "sns_create"))
	r.Get("/", c.snsTopics)
	r.Post("/create", c.snsCreateTopic)
	r.Get("/{topic}", c.snsTopic)
	r.Post("/{topic}/publish", c.snsPublish)
	r.Post("/{topic}/subscribe", c.snsSubscribe)
	r.Post("/{topic}/confirm", c.snsConfirm)
	r.Post("/{topic}/attribute", c.snsSetAttribute)             // SetTopicAttributes (C)
	r.Post("/{topic}/permission", c.snsAddPermission)           // AddPermission (C)
	r.Post("/{topic}/permission/delete", c.snsRemovePermission) // RemovePermission (C)
	r.Post("/{topic}/data-protection", c.snsDataProtection)     // PutDataProtectionPolicy (C) // ConfirmSubscription
	r.Post("/{topic}/unsubscribe", c.snsUnsubscribe)
	r.Post("/{topic}/sub-filter", c.snsSubFilter)
	r.Post("/{topic}/sub-raw", c.snsSubRaw)
	r.Post("/{topic}/delete-topic", c.snsDeleteTopic)
}

// EventBridge.
func (c *Console) ebRoutes(r chi.Router) {
	r.Get("/create-bus", c.createPage("eb", "eb_bus_create"))
	r.Get("/{bus}/create-rule", c.ebRuleCreatePage)
	r.Post("/test-pattern", c.ebTestPattern) // HTMX partial (TestEventPattern)
	r.Get("/", c.ebBuses)
	r.Get("/destinations", c.ebDestinations)                                         // ListConnections, ListApiDestinations
	r.Post("/destinations/create-connection", c.ebCreateConnection)                  // CreateConnection
	r.Post("/destinations/delete-connection", c.ebDeleteConnection)                  // DeleteConnection
	r.Get("/destinations/connection/{conn}", c.ebConnection)                         // DescribeConnection
	r.Post("/destinations/connection/{conn}/update", c.ebUpdateConnection)           // UpdateConnection
	r.Post("/destinations/connection/{conn}/deauthorize", c.ebDeauthorizeConnection) // DeauthorizeConnection
	r.Post("/destinations/create-destination", c.ebCreateDestination)                // CreateApiDestination
	r.Post("/destinations/delete-destination", c.ebDeleteDestination)                // DeleteApiDestination
	r.Get("/destinations/destination/{dest}", c.ebDestination)                       // DescribeApiDestination
	r.Post("/destinations/destination/{dest}/update", c.ebUpdateDestination)         // UpdateApiDestination
	r.Post("/create-bus", c.ebCreateBus)
	r.Post("/{bus}/delete-bus", c.ebDeleteBus)
	r.Get("/{bus}", c.ebBus)
	r.Post("/{bus}/create-rule", c.ebCreateRule)
	r.Post("/{bus}/test-event", c.ebTestEvent)
	r.Post("/{bus}/match", c.ebMatch) // HTMX partial (live rule matcher)
	r.Post("/{bus}/create-archive", c.ebCreateArchive)
	r.Post("/{bus}/delete-archive", c.ebDeleteArchive)
	r.Post("/{bus}/replay", c.ebReplay)
	r.Get("/{bus}/archive/{archive}", c.ebArchive)               // DescribeArchive
	r.Post("/{bus}/archive/{archive}/update", c.ebUpdateArchive) // UpdateArchive
	r.Get("/{bus}/replay/{replay}", c.ebReplayDetail)            // DescribeReplay
	r.Get("/{bus}/detail", c.ebBusDetail)                        // DescribeEventBus
	r.Post("/{bus}/rules-by-target", c.ebRulesByTarget)          // ListRuleNamesByTarget
	r.Get("/{bus}/rule/{rule}", c.ebRule)
	r.Post("/{bus}/rule/{rule}/add-target", c.ebAddTarget)
	r.Post("/{bus}/rule/{rule}/remove-target", c.ebRemoveTarget)
	r.Post("/{bus}/rule/{rule}/delete-rule", c.ebDeleteRule)
	r.Post("/{bus}/rule/{rule}/toggle", c.ebToggleRule)
}

// Kinesis.
func (c *Console) kinesisRoutes(r chi.Router) {
	r.Get("/create", c.createPage("kinesis", "kinesis_create"))
	r.Get("/", c.kinesisStreams)
	r.Post("/create", c.kinesisCreate)
	r.Get("/{stream}", c.kinesisStream)
	r.Get("/{stream}/records", c.kinesisRecords)
	r.Get("/{stream}/details", c.kinesisDetails)
	r.Get("/{stream}/tags", c.kinesisTags)
	r.Post("/{stream}/merge", c.kinesisMerge)
	r.Post("/{stream}/scale", c.kinesisScale)
	r.Post("/{stream}/mode", c.kinesisMode)
	r.Post("/{stream}/encryption", c.kinesisEncryption)
	r.Post("/{stream}/metrics", c.kinesisMetrics)
	r.Post("/{stream}/consumers/add", c.kinesisConsumerAdd)
	r.Post("/{stream}/consumers/del", c.kinesisConsumerDel)
	r.Post("/{stream}/policy", c.kinesisPolicy)
	r.Post("/{stream}/records/query", c.kinesisRecordsQuery)
	r.Get("/{stream}/record", c.kinesisRecord)
	r.Get("/{stream}/shards/{shard}/depth", c.kinesisShardDepth)
	r.Post("/{stream}/delete", c.kinesisDelete)
	r.Post("/{stream}/put", c.kinesisPut)
	r.Post("/{stream}/split", c.kinesisSplit)
	r.Post("/{stream}/retention", c.kinesisRetention)
}
