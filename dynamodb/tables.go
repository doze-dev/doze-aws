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
	IndexName  string          `json:"IndexName"`
	KeySchema  []keySchemaEl   `json:"KeySchema"`
	Projection *projectionWire `json:"Projection,omitempty"`
}

type createTableReq struct {
	TableName              string          `json:"TableName"`
	AttributeDefinitions   []attrDef       `json:"AttributeDefinitions"`
	KeySchema              []keySchemaEl   `json:"KeySchema"`
	GlobalSecondaryIndexes []gsiWire       `json:"GlobalSecondaryIndexes"`
	LocalSecondaryIndexes  []gsiWire       `json:"LocalSecondaryIndexes"`
	BillingMode            string          `json:"BillingMode"`
	DeletionProtection     bool            `json:"DeletionProtectionEnabled"`
	StreamSpecification    json.RawMessage `json:"StreamSpecification"`
	Tags                   []struct {
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
	t := store.Table{
		Name: req.TableName, Hash: hash, Range: rng,
		BillingMode:        orDefault(req.BillingMode, "PAY_PER_REQUEST"),
		DeletionProtection: req.DeletionProtection,
	}
	if len(req.StreamSpecification) > 0 {
		t.StreamSpec = string(req.StreamSpecification)
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
	}
	if len(gsis) > 0 {
		out["GlobalSecondaryIndexes"] = gsis
	}
	if len(lsis) > 0 {
		out["LocalSecondaryIndexes"] = lsis
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
		TableName            string    `json:"TableName"`
		AttributeDefinitions []attrDef `json:"AttributeDefinitions"`
		BillingMode          string    `json:"BillingMode"`
		DeletionProtection   *bool     `json:"DeletionProtectionEnabled"`
		GSIUpdates           []struct {
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
		if req.BillingMode != "" {
			t.BillingMode = req.BillingMode
		}
		if req.DeletionProtection != nil {
			t.DeletionProtection = *req.DeletionProtection
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
