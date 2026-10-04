package console

// Routes: S3 and DynamoDB.

import "github.com/go-chi/chi/v5"

// S3.
func (c *Console) s3Routes(r chi.Router) {
	// Create forms render inside the shell (list pane + detail).
	r.Get("/create", c.createPage("s3", "s3_create"))
	r.Get("/", c.s3Buckets)
	r.Post("/create", c.s3CreateBucket)
	r.Post("/{bucket}/delete-bucket", c.s3DeleteBucket)
	r.Get("/{bucket}", c.s3Objects)
	r.Get("/{bucket}/object", c.s3GetObject)
	r.Get("/{bucket}/meta", c.s3Meta)
	r.Post("/{bucket}/upload", c.s3Upload)
	r.Post("/{bucket}/folder", c.s3NewFolder)
	r.Post("/{bucket}/delete", c.s3DeleteObject)
	r.Post("/{bucket}/delete-batch", c.s3BulkDelete)  // HTMX partial (DeleteObjects)
	r.Post("/{bucket}/combine", c.s3Combine)          // HTMX partial (UploadPartCopy)
	r.Post("/{bucket}/uploads", c.s3MPUploads)        // HTMX partial (ListMultipartUploads)
	r.Post("/{bucket}/abort-upload", c.s3AbortUpload) // HTMX partial (AbortMultipartUpload)
	r.Post("/{bucket}/website", c.s3Website)          // HTMX partial (PutBucketWebsite/DeleteBucketWebsite)
	r.Post("/{bucket}/lock-config", c.s3LockConfig)   // HTMX partial (PutObjectLockConfiguration)
	r.Post("/{bucket}/object-tags", c.s3ObjTagsSave)  // HTMX partial (PutObjectTagging)
	r.Post("/{bucket}/retention", c.s3Retention)      // HTMX partial (PutObjectRetention)
	r.Post("/{bucket}/legal-hold", c.s3LegalHold)     // HTMX partial (PutObjectLegalHold)
	r.Post("/check-name", c.s3CheckName)              // HTMX partial (HeadBucket)
	r.Post("/{bucket}/versioning", c.s3Versioning)
	r.Post("/{bucket}/add-tag", c.s3AddTag)
	r.Post("/{bucket}/remove-tag", c.s3RemoveTag)
	r.Post("/{bucket}/presign", c.s3Presign)
	r.Post("/{bucket}/copy", c.s3Copy)
	r.Get("/{bucket}/versions", c.s3Versions)
	r.Post("/{bucket}/restore-version", c.s3RestoreVersion)
	r.Post("/{bucket}/delete-version", c.s3DeleteVersion)
	r.Post("/{bucket}/notify-add", c.s3NotifyAdd)
	r.Post("/{bucket}/notify-remove", c.s3NotifyRemove)
	r.Post("/{bucket}/cors", c.s3SaveCORS)
	r.Post("/{bucket}/policy", c.s3SavePolicy)          // HTMX partial (PutBucketPolicy)
	r.Post("/{bucket}/public-access", c.s3PublicAccess) // HTMX partial (PutPublicAccessBlock / DeletePublicAccessBlock; GetPublicAccessBlock and GetBucketPolicyStatus render the row)
	r.Post("/{bucket}/lifecycle", c.s3SaveLifecycle)
}

// DynamoDB.
func (c *Console) ddbRoutes(r chi.Router) {
	r.Get("/create", c.createPage("ddb", "ddb_create"))
	r.Get("/", c.ddbTables)
	r.Post("/create", c.ddbCreateTable)
	r.Get("/{table}", c.ddbTable)
	r.Post("/{table}/explore", c.ddbExplore) // HTMX partial (scan/query/partiql)
	r.Post("/{table}/put", c.ddbPutItem)
	r.Post("/{table}/delete-item", c.ddbDeleteItem)
	r.Post("/{table}/batch-get", c.ddbBatchGet)       // HTMX partial (BatchGetItem / TransactGetItems)
	r.Post("/{table}/batch-delete", c.ddbBatchDelete) // HTMX partial (BatchWriteItem / TransactWriteItems)
	r.Post("/{table}/update-item", c.ddbUpdateItem)   // HTMX partial (UpdateItem)
	r.Post("/{table}/delete-table", c.ddbDeleteTable)
	r.Post("/{table}/ttl", c.ddbSetTTL)
	r.Post("/{table}/add-gsi", c.ddbAddGSI)
	r.Post("/{table}/delete-gsi", c.ddbDeleteGSI)
}
