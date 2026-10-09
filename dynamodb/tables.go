package dynamodb

// Table lifecycle handlers and the wire↔store schema mapping.

import (
	"encoding/json"
	"sort"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/store"
)

// wire shapes for table definitions.
type attrDef struct {
	AttributeName string `json:"AttributeName"`
	AttributeType string `json:"AttributeType"`
}

type keySchemaEl struct {
	AttributeName string `json:"AttributeName"`
	KeyType       string `json:"KeyType"` // HASH | RANGE
}

type projectionWire struct {
	ProjectionType   string   `json:"ProjectionType,omitempty"`
	NonKeyAttributes []string `json:"NonKeyAttributes,omitempty"`
}

type gsiWire struct {
	IndexName             string          `json:"IndexName"`
	KeySchema             []keySchemaEl   `json:"KeySchema"`
	Projection            *projectionWire `json:"Projection,omitempty"`
	ProvisionedThroughput *throughputWire `json:"ProvisionedThroughput,omitempty"`
}

// throughputWire is a ProvisionedThroughput as sent.
type throughputWire struct {
	ReadCapacityUnits  *int64 `json:"ReadCapacityUnits"`
	WriteCapacityUnits *int64 `json:"WriteCapacityUnits"`
}

type createTableReq struct {
	TableName              string          `json:"TableName"`
	AttributeDefinitions   []attrDef       `json:"AttributeDefinitions"`
	KeySchema              []keySchemaEl   `json:"KeySchema"`
	GlobalSecondaryIndexes []gsiWire       `json:"GlobalSecondaryIndexes"`
	LocalSecondaryIndexes  []gsiWire       `json:"LocalSecondaryIndexes"`
	BillingMode            string          `json:"BillingMode"`
	ProvisionedThroughput  *throughputWire `json:"ProvisionedThroughput"`
	DeletionProtection     bool            `json:"DeletionProtectionEnabled"`
	StreamSpecification    json.RawMessage `json:"StreamSpecification"`
	SSESpecification       *struct {
		Enabled        bool   `json:"Enabled"`
		SSEType        string `json:"SSEType"`
		KMSMasterKeyID string `json:"KMSMasterKeyId"`
	} `json:"SSESpecification"`
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// keyParts resolves a key schema against the attribute definitions.
func keyParts(schema []keySchemaEl, defs []attrDef, what string) (hash store.KeyPart, rng *store.KeyPart, aerr *awshttp.APIError) {
	typeOf := func(name string) (string, bool) {
		for _, d := range defs {
			if d.AttributeName == name {
				return d.AttributeType, true
			}
		}
		return "", false
	}
	for _, el := range schema {
		t, ok := typeOf(el.AttributeName)
		if !ok {
			return store.KeyPart{}, nil, awshttp.Errf(400, "ValidationException",
				"%s key attribute %s has no AttributeDefinition", what, el.AttributeName)
		}
		switch el.KeyType {
		case "HASH":
			hash = store.KeyPart{Name: el.AttributeName, Type: t}
		case "RANGE":
			rng = &store.KeyPart{Name: el.AttributeName, Type: t}
		default:
			return store.KeyPart{}, nil, awshttp.Errf(400, "ValidationException", "KeyType must be HASH or RANGE")
		}
	}
	if hash.Name == "" {
		return store.KeyPart{}, nil, awshttp.Errf(400, "ValidationException", "%s needs a HASH key", what)
	}
	return hash, rng, nil
}

func indexFromWire(w gsiWire, defs []attrDef, local bool) (store.Index, *awshttp.APIError) {
	hash, rng, aerr := keyParts(w.KeySchema, defs, "index "+w.IndexName)
	if aerr != nil {
		return store.Index{}, aerr
	}
	idx := store.Index{
		Name: w.IndexName, Hash: hash, Range: rng,
		Projection: "ALL", Local: local,
	}
	if w.Projection != nil && w.Projection.ProjectionType != "" {
		idx.Projection = w.Projection.ProjectionType
		idx.NonKeyAttrs = w.Projection.NonKeyAttributes
	}
	return idx, nil
}

var handlers = map[string]handler{
	"CreateTable":           (*Server).createTable,
	"DescribeTable":         (*Server).describeTable,
	"DeleteTable":           (*Server).deleteTable,
	"ListTables":            (*Server).listTables,
	"UpdateTable":           (*Server).updateTable,
	"UpdateTimeToLive":      (*Server).updateTTL,
	"DescribeTimeToLive":    (*Server).describeTTL,
	"PutItem":               (*Server).putItem,
	"GetItem":               (*Server).getItem,
	"UpdateItem":            (*Server).updateItem,
	"DeleteItem":            (*Server).deleteItem,
	"Query":                 (*Server).query,
	"Scan":                  (*Server).scan,
	"ExecuteStatement":      (*Server).executeStatement,
	"BatchExecuteStatement": (*Server).batchExecuteStatement,
	"ExecuteTransaction":    (*Server).executeTransaction,
	"BatchGetItem":          (*Server).batchGet,
	"BatchWriteItem":        (*Server).batchWrite,
	"TransactWriteItems":    (*Server).transactWrite,
	"TransactGetItems":      (*Server).transactGet,
	"TagResource":           (*Server).tagResource,
	"UntagResource":         (*Server).untagResource,
	"ListTagsOfResource":    (*Server).listTags,
	"DescribeLimits":        (*Server).describeLimits,
	"DescribeEndpoints":     (*Server).describeEndpoints,
	// Tier C round-trips.
	"DescribeContinuousBackups":   (*Server).describeContinuousBackups,
	"UpdateContinuousBackups":     (*Server).describeContinuousBackups,
	"DescribeContributorInsights": (*Server).describeContributorInsights,
	"UpdateContributorInsights":   (*Server).describeContributorInsights,
}

func (s *Server) createTable(body []byte) (any, *awshttp.APIError) {
	var req createTableReq
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	hash, rng, aerr := keyParts(req.KeySchema, req.AttributeDefinitions, "table")
	if aerr != nil {
		return nil, aerr
	}
	mode, rcu, wcu, aerr := billingFor(req.BillingMode, req.ProvisionedThroughput)
	if aerr != nil {
		return nil, aerr
	}
	for _, w := range req.GlobalSecondaryIndexes {
		if mode == "PROVISIONED" && w.ProvisionedThroughput == nil {
			return nil, awshttp.Errf(400, "ValidationException",
				"One or more parameter values were invalid: ProvisionedThroughput must be specified for index: %s", w.IndexName)
		}
		if mode == "PAY_PER_REQUEST" && w.ProvisionedThroughput != nil {
			return nil, awshttp.Errf(400, "ValidationException",
				"One or more parameter values were invalid: ProvisionedThroughput should not be specified for index: %s when BillingMode is PAY_PER_REQUEST", w.IndexName)
		}
	}
	t := store.Table{
		Name: req.TableName, Hash: hash, Range: rng,
		BillingMode: mode, ReadCap: rcu, WriteCap: wcu,
		DeletionProtection: req.DeletionProtection,
	}
	if len(req.StreamSpecification) > 0 {
		t.StreamSpec = string(req.StreamSpecification)
	}
	if sse := req.SSESpecification; sse != nil && sse.Enabled {
		// DynamoDB reports KMS whatever was asked for: the AWS-owned default
		// is the only other option and it is not described this way.
		t.SSEEnabled, t.SSEType, t.SSEKeyID = true, orDefault(sse.SSEType, "KMS"), sse.KMSMasterKeyID
	}
	for _, tag := range req.Tags {
		if t.Tags == nil {
			t.Tags = map[string]string{}
		}
		t.Tags[tag.Key] = tag.Value
	}
	for _, w := range req.GlobalSecondaryIndexes {
		idx, aerr := indexFromWire(w, req.AttributeDefinitions, false)
		if aerr != nil {
			return nil, aerr
		}
		if w.ProvisionedThroughput != nil {
			idx.ReadCap, idx.WriteCap = capacity(w.ProvisionedThroughput)
		}
		t.Indexes = append(t.Indexes, idx)
	}
	for _, w := range req.LocalSecondaryIndexes {
		idx, aerr := indexFromWire(w, req.AttributeDefinitions, true)
		if aerr != nil {
			return nil, aerr
		}
		if idx.Hash.Name != hash.Name {
			return nil, awshttp.Errf(400, "ValidationException", "LSI %s must share the table's partition key", idx.Name)
		}
		t.Indexes = append(t.Indexes, idx)
	}
	created, err := s.store.CreateTable(t)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"TableDescription": s.describe(created)}, nil
}

// describe renders a TableDescription. Tables are ACTIVE immediately: no fake
// CREATING delay, so SDK waiters pass on their first probe.
func (s *Server) describe(t *store.Table) map[string]any {
	attrTypes := map[string]string{t.Hash.Name: t.Hash.Type}
	keySchema := []map[string]string{{"AttributeName": t.Hash.Name, "KeyType": "HASH"}}
	if t.Range != nil {
		attrTypes[t.Range.Name] = t.Range.Type
		keySchema = append(keySchema, map[string]string{"AttributeName": t.Range.Name, "KeyType": "RANGE"})
	}
	var gsis, lsis []map[string]any
	for _, idx := range t.Indexes {
		attrTypes[idx.Hash.Name] = idx.Hash.Type
		ks := []map[string]string{{"AttributeName": idx.Hash.Name, "KeyType": "HASH"}}
		if idx.Range != nil {
			attrTypes[idx.Range.Name] = idx.Range.Type
			ks = append(ks, map[string]string{"AttributeName": idx.Range.Name, "KeyType": "RANGE"})
		}
		proj := map[string]any{"ProjectionType": orDefault(idx.Projection, "ALL")}
		if len(idx.NonKeyAttrs) > 0 {
			proj["NonKeyAttributes"] = idx.NonKeyAttrs
		}
		desc := map[string]any{
			"IndexName":  idx.Name,
			"KeySchema":  ks,
			"Projection": proj,
			"IndexArn":   t.ARN() + "/index/" + idx.Name,
		}
		if !idx.Local {
			desc["ProvisionedThroughput"] = provisioned(idx.ReadCap, idx.WriteCap)
			desc["WarmThroughput"] = warm(orDefault(t.BillingMode, "PAY_PER_REQUEST"))
		}
		if idx.Local {
			lsis = append(lsis, desc)
		} else {
			desc["IndexStatus"] = "ACTIVE"
			gsis = append(gsis, desc)
		}
	}
	var defs []map[string]string
	names := make([]string, 0, len(attrTypes))
	for n := range attrTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		defs = append(defs, map[string]string{"AttributeName": n, "AttributeType": attrTypes[n]})
	}
	out := map[string]any{
		"TableName":                 t.Name,
		"TableArn":                  t.ARN(),
		"TableId":                   t.Name,
		"TableStatus":               "ACTIVE",
		"CreationDateTime":          float64(t.Created),
		"AttributeDefinitions":      defs,
		"KeySchema":                 keySchema,
		"ItemCount":                 s.store.CountItems(t.Name),
		"TableSizeBytes":            0,
		"BillingModeSummary":        map[string]any{"BillingMode": orDefault(t.BillingMode, "PAY_PER_REQUEST")},
		"DeletionProtectionEnabled": t.DeletionProtection,
		"ProvisionedThroughput":     provisioned(t.ReadCap, t.WriteCap),
		"WarmThroughput":            warm(orDefault(t.BillingMode, "PAY_PER_REQUEST")),
	}
	if len(gsis) > 0 {
		out["GlobalSecondaryIndexes"] = gsis
	}
	if len(lsis) > 0 {
		out["LocalSecondaryIndexes"] = lsis
	}
	if t.SSEEnabled {
		sse := map[string]any{"Status": "ENABLED", "SSEType": orDefault(t.SSEType, "KMS")}
		if t.SSEKeyID != "" {
			sse["KMSMasterKeyArn"] = t.SSEKeyID
		}
		out["SSEDescription"] = sse
	}
	if viewType, ok := t.StreamViewType(); ok {
		out["LatestStreamArn"] = t.StreamARN()
		out["LatestStreamLabel"] = t.StreamLabel()
		out["StreamSpecification"] = map[string]any{
			"StreamEnabled": true, "StreamViewType": viewType,
		}
	}
	return out
}

func (s *Server) describeTable(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)
	t, err := s.store.GetTable(req.TableName)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"Table": s.describe(t)}, nil
}

func (s *Server) deleteTable(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)
	t, err := s.store.DeleteTable(req.TableName)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"TableDescription": s.describe(t)}, nil
}

func (s *Server) listTables(body []byte) (any, *awshttp.APIError) {
	names, err := s.store.ListTables()
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	if names == nil {
		names = []string{}
	}
	return map[string]any{"TableNames": names}, nil
}

func (s *Server) updateTable(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName             string          `json:"TableName"`
		AttributeDefinitions  []attrDef       `json:"AttributeDefinitions"`
		BillingMode           string          `json:"BillingMode"`
		ProvisionedThroughput *throughputWire `json:"ProvisionedThroughput"`
		DeletionProtection    *bool           `json:"DeletionProtectionEnabled"`
		SSESpecification      *struct {
			Enabled        bool   `json:"Enabled"`
			SSEType        string `json:"SSEType"`
			KMSMasterKeyID string `json:"KMSMasterKeyId"`
		} `json:"SSESpecification"`
		GSIUpdates []struct {
			Create *gsiWire `json:"Create"`
			Delete *struct {
				IndexName string `json:"IndexName"`
			} `json:"Delete"`
		} `json:"GlobalSecondaryIndexUpdates"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	t, err := s.store.UpdateTable(req.TableName, func(t *store.Table) error {
		if req.BillingMode != "" || req.ProvisionedThroughput != nil {
			mode := orDefault(req.BillingMode, orDefault(t.BillingMode, "PAY_PER_REQUEST"))
			rcu, wcu := t.ReadCap, t.WriteCap
			if req.ProvisionedThroughput != nil {
				rcu, wcu = capacity(req.ProvisionedThroughput)
			}
			m, r, w, aerr := billingFor(mode, &throughputWire{ReadCapacityUnits: &rcu, WriteCapacityUnits: &wcu})
			if mode == "PAY_PER_REQUEST" {
				m, r, w, aerr = mode, 0, 0, nil
			}
			if aerr != nil {
				return aerr
			}
			t.BillingMode, t.ReadCap, t.WriteCap = m, r, w
		}
		if req.DeletionProtection != nil {
			t.DeletionProtection = *req.DeletionProtection
		}
		if sse := req.SSESpecification; sse != nil {
			// Disabling clears the description entirely, the way DynamoDB
			// stops reporting SSEDescription once encryption is turned off.
			t.SSEEnabled = sse.Enabled
			if sse.Enabled {
				t.SSEType, t.SSEKeyID = orDefault(sse.SSEType, "KMS"), sse.KMSMasterKeyID
			} else {
				t.SSEType, t.SSEKeyID = "", ""
			}
		}
		for _, u := range req.GSIUpdates {
			if u.Create != nil {
				idx, aerr := indexFromWire(*u.Create, req.AttributeDefinitions, false)
				if aerr != nil {
					return aerr
				}
				t.Indexes = append(t.Indexes, idx) // UpdateTable backfills new indexes
			}
			if u.Delete != nil {
				for i := range t.Indexes {
					if t.Indexes[i].Name == u.Delete.IndexName {
						t.Indexes = append(t.Indexes[:i], t.Indexes[i+1:]...)
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"TableDescription": s.describe(t)}, nil
}

func (s *Server) updateTTL(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName               string `json:"TableName"`
		TimeToLiveSpecification struct {
			AttributeName string `json:"AttributeName"`
			Enabled       bool   `json:"Enabled"`
		} `json:"TimeToLiveSpecification"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	_, err := s.store.UpdateTable(req.TableName, func(t *store.Table) error {
		// TTL is switched, not set: asking for the state it is already in
		// is refused. It used to be accepted, so a deploy that enables TTL
		// on every run passed here and failed on its second run against AWS.
		if t.TTLEnabled == req.TimeToLiveSpecification.Enabled {
			state := "disabled"
			if t.TTLEnabled {
				state = "enabled"
			}
			return awshttp.Errf(400, "ValidationException", "TimeToLive is already %s", state)
		}
		t.TTLAttribute = req.TimeToLiveSpecification.AttributeName
		t.TTLEnabled = req.TimeToLiveSpecification.Enabled
		return nil
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"TimeToLiveSpecification": req.TimeToLiveSpecification}, nil
}

func (s *Server) describeTTL(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)
	t, err := s.store.GetTable(req.TableName)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	status := "DISABLED"
	desc := map[string]any{"TimeToLiveStatus": status}
	if t.TTLEnabled {
		desc["TimeToLiveStatus"] = "ENABLED"
		desc["AttributeName"] = t.TTLAttribute
	}
	return map[string]any{"TimeToLiveDescription": desc}, nil
}

// ---- tags & canned describes ----

func (s *Server) tagResource(body []byte) (any, *awshttp.APIError) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
		Tags        []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	json.Unmarshal(body, &req)
	_, err := s.store.UpdateTable(tableFromARN(req.ResourceArn), func(t *store.Table) error {
		for _, tag := range req.Tags {
			if t.Tags == nil {
				t.Tags = map[string]string{}
			}
			t.Tags[tag.Key] = tag.Value
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) untagResource(body []byte) (any, *awshttp.APIError) {
	var req struct {
		ResourceArn string   `json:"ResourceArn"`
		TagKeys     []string `json:"TagKeys"`
	}
	json.Unmarshal(body, &req)
	_, err := s.store.UpdateTable(tableFromARN(req.ResourceArn), func(t *store.Table) error {
		for _, k := range req.TagKeys {
			delete(t.Tags, k)
		}
		return nil
	})
	return nil, awshttp.AsAPIErrorOrNil(err)
}

func (s *Server) listTags(body []byte) (any, *awshttp.APIError) {
	var req struct {
		ResourceArn string `json:"ResourceArn"`
	}
	json.Unmarshal(body, &req)
	t, err := s.store.GetTable(tableFromARN(req.ResourceArn))
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	type tag struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	}
	keys := make([]string, 0, len(t.Tags))
	for k := range t.Tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tags := []tag{}
	for _, k := range keys {
		tags = append(tags, tag{Key: k, Value: t.Tags[k]})
	}
	return map[string]any{"Tags": tags}, nil
}

// tableFromARN extracts the table name from arn:aws:dynamodb:...:table/<name>.
func tableFromARN(arn string) string {
	const marker = ":table/"
	for i := 0; i+len(marker) <= len(arn); i++ {
		if arn[i:i+len(marker)] == marker {
			return arn[i+len(marker):]
		}
	}
	return arn
}

func (s *Server) describeLimits([]byte) (any, *awshttp.APIError) {
	return map[string]any{
		"AccountMaxReadCapacityUnits":  80000,
		"AccountMaxWriteCapacityUnits": 80000,
		"TableMaxReadCapacityUnits":    40000,
		"TableMaxWriteCapacityUnits":   40000,
	}, nil
}

func (s *Server) describeEndpoints([]byte) (any, *awshttp.APIError) {
	return map[string]any{
		"Endpoints": []map[string]any{{"Address": "dynamodb.us-east-1.amazonaws.com", "CachePeriodInMinutes": 1440}},
	}, nil
}

func (s *Server) describeContinuousBackups(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)
	if _, err := s.store.GetTable(req.TableName); err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{"ContinuousBackupsDescription": map[string]any{
		"ContinuousBackupsStatus": "ENABLED",
		"PointInTimeRecoveryDescription": map[string]any{
			"PointInTimeRecoveryStatus": "DISABLED",
		},
	}}, nil
}

func (s *Server) describeContributorInsights(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string `json:"TableName"`
	}
	json.Unmarshal(body, &req)
	return map[string]any{
		"TableName":                 req.TableName,
		"ContributorInsightsStatus": "DISABLED",
	}, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// CreateTable's input validation lives in validate.go, with the model-derived
// constraint table it runs.

// billingFor settles a table's capacity mode from what CreateTable was given.
// With no BillingMode the table is PROVISIONED and must say how much; asking for
// on-demand while naming capacity is a contradiction DynamoDB refuses.
func billingFor(mode string, tp *throughputWire) (string, int64, int64, *awshttp.APIError) {
	bad := func(format string, a ...any) *awshttp.APIError {
		return awshttp.Errf(400, "ValidationException", format, a...)
	}
	switch mode {
	case "":
		if tp == nil {
			return "", 0, 0, bad("No provisioned throughput specified for the table")
		}
		mode = "PROVISIONED"
	case "PAY_PER_REQUEST":
		if tp != nil {
			return "", 0, 0, bad("One or more parameter values were invalid: Neither ReadCapacityUnits nor WriteCapacityUnits can be specified when BillingMode is PAY_PER_REQUEST")
		}
		return mode, 0, 0, nil
	}
	if tp == nil || tp.ReadCapacityUnits == nil || tp.WriteCapacityUnits == nil {
		return "", 0, 0, bad("One or more parameter values were invalid: ReadCapacityUnits and WriteCapacityUnits must both be specified when BillingMode is PROVISIONED")
	}
	if *tp.ReadCapacityUnits < 1 || *tp.WriteCapacityUnits < 1 {
		return "", 0, 0, bad("One or more parameter values were invalid: ReadCapacityUnits and WriteCapacityUnits must both be at least 1")
	}
	r, w := capacity(tp)
	return mode, r, w, nil
}

func capacity(tp *throughputWire) (r, w int64) {
	if tp.ReadCapacityUnits != nil {
		r = *tp.ReadCapacityUnits
	}
	if tp.WriteCapacityUnits != nil {
		w = *tp.WriteCapacityUnits
	}
	return r, w
}

// provisioned is the ProvisionedThroughput block every description carries —
// zeros for an on-demand table, which is how DynamoDB reports one.
func provisioned(r, w int64) map[string]any {
	return map[string]any{"ReadCapacityUnits": r, "WriteCapacityUnits": w, "NumberOfDecreasesToday": 0}
}

// warm is the warm throughput a new table starts with. Terraform's AWS provider
// waits for it to read ACTIVE after create, and a description without it makes
// that wait fail with "couldn't find resource".
func warm(mode string) map[string]any {
	r, w := int64(12000), int64(4000)
	if mode == "PROVISIONED" {
		r, w = 3000, 1000
	}
	return map[string]any{"ReadUnitsPerSecond": r, "WriteUnitsPerSecond": w, "Status": "ACTIVE"}
}
