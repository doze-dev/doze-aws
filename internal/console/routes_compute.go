package console

// Routes: Lambda, API Gateway, Step Functions, CloudWatch Logs and CloudWatch.

import "github.com/go-chi/chi/v5"

// Lambda.
func (c *Console) lambdaRoutes(r chi.Router) {
	r.Get("/", c.lambdaFns)
	r.Get("/create", c.createPage("lambda", "lambda_create"))
	r.Post("/create", c.lambdaCreate)
	r.Get("/{fn}", c.lambdaFn)
	r.Get("/{fn}/runtime", c.lambdaRuntimeBadge) // HTMX partial (polled)
	r.Get("/{fn}/logs", c.lambdaLogs)            // HTMX partial (polled FilterLogEvents)
	r.Post("/{fn}/invoke", c.lambdaInvoke)
	r.Post("/{fn}/delete-fn", c.lambdaDelete)
	r.Post("/{fn}/delete-mapping", c.lambdaDeleteMapping)
	r.Post("/{fn}/add-mapping", c.lambdaAddMapping)
	r.Post("/{fn}/config", c.lambdaSaveConfig)
	r.Post("/{fn}/code", c.lambdaUpdateCode)          // UpdateFunctionCode
	r.Post("/{fn}/reset-async", c.lambdaResetAsync)   // DeleteFunctionEventInvokeConfig
	r.Post("/layers/publish", c.lambdaLayerPublish)   // PublishLayerVersion
	r.Post("/layers/versions", c.lambdaLayerVersions) // HTMX partial (ListLayerVersions)
	r.Post("/layers/version", c.lambdaLayerVersion)   // HTMX partial (GetLayerVersion)
	r.Post("/layers/find", c.lambdaLayerFind)         // HTMX partial (GetLayerVersionByArn)
	r.Post("/layers/delete", c.lambdaLayerDelete)     // DeleteLayerVersion
	r.Post("/layers/grant", c.lambdaLayerGrant)       // AddLayerVersionPermission
	r.Post("/layers/revoke", c.lambdaLayerRevoke)     // RemoveLayerVersionPermission
	r.Post("/{fn}/create-url", c.lambdaCreateURL)
	r.Post("/{fn}/delete-url", c.lambdaDeleteURL)
	r.Post("/{fn}/update-url", c.lambdaUpdateURL)
}

// API Gateway.
func (c *Console) apigwRoutes(r chi.Router) {
	r.Get("/", c.apigwList)
	r.Get("/create", c.createPage("apigw", "apigw_create"))
	r.Post("/create", c.apigwCreate)
	r.Get("/{api}", c.apigwAPI)
	r.Post("/{api}/invoke", c.apigwInvoke)
	r.Post("/{api}/update", c.apigwUpdate)
	r.Post("/{api}/delete", c.apigwDelete)
	r.Post("/{api}/add-resource", c.apigwAddResource)       // HTMX partial (CreateResource)
	r.Post("/{api}/delete-resource", c.apigwDeleteResource) // HTMX partial (DeleteResource)
	r.Post("/{api}/rename-resource", c.apigwRenameResource) // HTMX partial (UpdateResource)
	r.Post("/{api}/put-method", c.apigwPutMethod)           // HTMX partial (PutMethod)
	r.Post("/{api}/delete-method", c.apigwDeleteMethod)     // HTMX partial (DeleteMethod)
	r.Post("/{api}/method", c.apigwMethodPanel)             // HTMX partial (GetMethod)
	r.Post("/{api}/put-integration", c.apigwPutIntegration) // HTMX partial (PutIntegration)
	r.Post("/{api}/delete-integration", c.apigwDeleteIntegration)
	r.Post("/{api}/put-response", c.apigwPutResponse) // method + integration halves
	r.Post("/{api}/delete-response", c.apigwDeleteResponse)
	r.Post("/{api}/deploy", c.apigwDeploy) // CreateDeployment
	r.Post("/{api}/delete-deployment", c.apigwDeleteDeployment)
	r.Post("/{api}/create-stage", c.apigwCreateStage)
	r.Post("/{api}/update-stage", c.apigwUpdateStage)
	r.Post("/{api}/delete-stage", c.apigwDeleteStage)
	r.Post("/{api}/create-authorizer", c.apigwCreateAuthorizer) // HTMX partial (CreateAuthorizer, GetAuthorizers)
	r.Post("/{api}/update-authorizer", c.apigwUpdateAuthorizer) // HTMX partial (UpdateAuthorizer)
	r.Post("/{api}/delete-authorizer", c.apigwDeleteAuthorizer) // HTMX partial (DeleteAuthorizer)
	r.Get("/{api}/authorizer/{auth}", c.apigwAuthorizer)        // HTMX partial (GetAuthorizer)
}

// API Gateway HTTP APIs.
func (c *Console) apigwHttpRoutes(r chi.Router) {
	r.Get("/{api}", c.apigwHTTP)                          // GetApis, GetRoutes, GetIntegrations, GetStages, GetAuthorizers
	r.Post("/{api}/update", c.apigwHTTPUpdate)            // UpdateApi, DeleteCorsConfiguration
	r.Post("/{api}/delete", c.apigwHTTPDelete)            // DeleteApi
	r.Post("/{api}/add-route", c.apigwHTTPAddRoute)       // HTMX partial (CreateIntegration, CreateRoute)
	r.Post("/{api}/delete-route", c.apigwHTTPDeleteRoute) // HTMX partial (DeleteRoute, DeleteIntegration)
	r.Post("/{api}/invoke", c.apigwHTTPInvoke)
	r.Post("/{api}/create-stage", c.apigwHTTPCreateStage)           // CreateStage
	r.Post("/{api}/delete-stage", c.apigwHTTPDeleteStage)           // DeleteStage
	r.Post("/{api}/deploy", c.apigwHTTPDeploy)                      // CreateDeployment
	r.Post("/{api}/create-authorizer", c.apigwHTTPCreateAuthorizer) // CreateAuthorizer
	r.Post("/{api}/delete-authorizer", c.apigwHTTPDeleteAuthorizer) // DeleteAuthorizer
}

// API Gateway API keys and usage plans.
func (c *Console) apigwKeysRoutes(r chi.Router) {
	r.Get("/", c.apigwKeys)                                  // GetApiKeys, GetUsagePlans, GetUsagePlanKeys
	r.Post("/create", c.apigwCreateKey)                      // CreateApiKey
	r.Get("/key/{key}/reveal", c.apigwRevealKey)             // GetApiKey
	r.Post("/toggle", c.apigwToggleKey)                      // UpdateApiKey
	r.Post("/delete", c.apigwDeleteKey)                      // DeleteApiKey
	r.Post("/plans/create", c.apigwCreatePlan)               // CreateUsagePlan
	r.Get("/plans/{plan}", c.apigwPlanDetail)                // GetUsagePlan, GetUsagePlanKey
	r.Post("/plans/{plan}/add-stage", c.apigwPlanAddStage)   // UpdateUsagePlan
	r.Post("/plans/delete", c.apigwDeletePlan)               // DeleteUsagePlan
	r.Post("/plans/{plan}/attach-key", c.apigwPlanAttachKey) // CreateUsagePlanKey
	r.Post("/plans/{plan}/detach-key", c.apigwPlanDetachKey) // DeleteUsagePlanKey
}

// Step Functions.
func (c *Console) sfnRoutes(r chi.Router) {
	// Step Functions. validate and create sit before {machine} so a machine
	// called "create" cannot shadow them.
	r.Post("/validate", c.sfnValidate) // HTMX partial (ValidateStateMachineDefinition)
	r.Get("/", c.sfnMachines)
	r.Get("/create", c.createPage("sfn", "sfn_create"))
	r.Post("/create", c.sfnCreate)
	r.Get("/{machine}", c.sfnMachine)
	r.Post("/{machine}/start", c.sfnStart)
	r.Post("/{machine}/delete", c.sfnDelete)
	r.Post("/{machine}/definition", c.sfnUpdateDefinition)
	r.Get("/{machine}/executions", c.sfnExecutions) // HTMX partial (polled ListExecutions)
	r.Get("/{machine}/logs", c.sfnLogs)             // HTMX partial (polled FilterLogEvents)
	r.Get("/{machine}/execution/{exec}", c.sfnExecution)
	r.Get("/{machine}/execution/{exec}/history", c.sfnHistory) // HTMX partial (polled GetExecutionHistory)
	r.Get("/{machine}/execution/{exec}/graph", c.sfnGraph)     // HTMX partial (polled GetExecutionHistory over DescribeStateMachineForExecution)
	r.Post("/{machine}/execution/{exec}/stop", c.sfnStop)
	r.Post("/{machine}/execution/{exec}/task-result", c.sfnTaskResult) // HTMX partial (SendTaskSuccess / SendTaskFailure)
	r.Post("/{machine}/execution/{exec}/heartbeat", c.sfnHeartbeat)    // HTMX partial (SendTaskHeartbeat)
	r.Post("/{machine}/execution/{exec}/redrive", c.sfnRedrive)
	r.Post("/{machine}/execution/{exec}/maprun", c.sfnMapRunUpdate)           // HTMX partial (UpdateMapRun)
	r.Get("/{machine}/execution/{exec}/maprun-children", c.sfnMapRunChildren) // HTMX partial (ListExecutions by mapRunArn)
	r.Post("/{machine}/start-sync", c.sfnStartSync)                           // HTMX partial (StartSyncExecution)
	r.Post("/{machine}/test-state", c.sfnTestState)                           // HTMX partial (TestState)
	r.Post("/{machine}/publish", c.sfnPublish)
	r.Get("/{machine}/version/{n}", c.sfnVersion) // HTMX partial (DescribeStateMachine on a version ARN)
	r.Post("/{machine}/version/{n}/delete", c.sfnVersionDelete)
	r.Post("/{machine}/alias/create", c.sfnAliasCreate)
	r.Post("/{machine}/alias/{alias}/update", c.sfnAliasUpdate)
	r.Post("/{machine}/alias/{alias}/delete", c.sfnAliasDelete)
	// Activities are machine-independent, so they live beside the machines
	// rather than under one. The literal segment wins over {machine}.
	r.Get("/activities", c.sfnActivities)
	r.Post("/activities/create", c.sfnActivityCreate)
	r.Post("/activities/task-result", c.sfnActivityTaskResult) // HTMX partial (SendTaskSuccess / SendTaskFailure)
	r.Post("/activities/heartbeat", c.sfnHeartbeat)            // HTMX partial (SendTaskHeartbeat)
	r.Post("/activities/{activity}/take", c.sfnActivityTake)   // HTMX partial (GetActivityTask)
	r.Post("/activities/{activity}/delete", c.sfnActivityDelete)
}

// CloudWatch Logs.
func (c *Console) logsRoutes(r chi.Router) {
	// CloudWatch Logs: groups by name in the query, since names carry slashes.
	r.Get("/", c.logsHome)
	r.Get("/create", c.createPage("logs", "logs_create"))
	r.Post("/create", c.logsCreate)
	r.Post("/delete-stream", c.logsDeleteStream)
	r.Get("/group", c.logsGroup)
	r.Get("/tail", c.logsTail) // HTMX partial (polled FilterLogEvents)
	r.Post("/retention", c.logsRetention)
	r.Post("/delete", c.logsDelete)
	r.Post("/subscribe", c.logsSubscribe)                     // PutSubscriptionFilter
	r.Post("/unsubscribe", c.logsUnsubscribe)                 // DeleteSubscriptionFilter
	r.Post("/metric-filter", c.logsPutMetricFilter)           // PutMetricFilter
	r.Post("/delete-metric-filter", c.logsDeleteMetricFilter) // DeleteMetricFilter
	r.Post("/test-metric-filter", c.logsTestMetricFilter)     // TestMetricFilter
}

// CloudWatch.
func (c *Console) cwRoutes(r chi.Router) {
	// CloudWatch: alarms by name in the path, metrics by an encoded key in the
	// query — a metric identity is namespace + name + its dimension set, which
	r.Get("/create", c.createPage("cw", "cw_create"))
	r.Get("/", c.cwHome)                            // DescribeAlarms, ListMetrics
	r.Get("/metric", c.cwMetric)                    // GetMetricData, DescribeAlarmsForMetric
	r.Get("/alarm/{name}", c.cwAlarm)               // DescribeAlarmHistory
	r.Post("/create-alarm", c.cwCreateAlarm)        // PutMetricAlarm
	r.Post("/alarm/{name}/state", c.cwSetState)     // SetAlarmState
	r.Post("/alarm/{name}/actions", c.cwSetActions) // Enable/DisableAlarmActions
	r.Post("/alarm/{name}/delete", c.cwDeleteAlarm) // DeleteAlarms
}
