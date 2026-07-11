package console

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---- DynamoDB (JSON 1.0) ----

type Table struct {
	Name      string
	Status    string
	ItemCount int64
	SizeBytes int64
	ARN       string
	HashKey   string
	HashType  string
	RangeKey  string
	RangeType string
	GSIs      []GSI
}

type GSI struct {
	Name      string
	HashKey   string
	HashType  string
	RangeKey  string
	RangeType string
}

// Item is one scanned row prepared for display.
type Item struct {
	PK      string
	SK      string
	Preview string // truncated single-line JSON of the non-key attributes
	JSON    string // full pretty plain-JSON
	KeyJSON string // the primary-key AV map as JSON (for DeleteItem)
}

func (b *backend) ListTables(ctx context.Context) ([]Table, error) {
	body, err := b.ddbCall(ctx, "ListTables", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		TableNames []string `json:"TableNames"`
	}
	json.Unmarshal(body, &out)
	sort.Strings(out.TableNames)
	tables := make([]Table, 0, len(out.TableNames))
	for _, name := range out.TableNames {
		t, err := b.DescribeTable(ctx, name)
		if err != nil {
			t = &Table{Name: name, Status: "?"}
		}
		tables = append(tables, *t)
	}
	return tables, nil
}

func (b *backend) DescribeTable(ctx context.Context, name string) (*Table, error) {
	body, err := b.ddbCall(ctx, "DescribeTable", map[string]any{"TableName": name})
	if err != nil {
		return nil, err
	}
	var out struct {
		Table struct {
			TableName      string `json:"TableName"`
			TableStatus    string `json:"TableStatus"`
			ItemCount      int64  `json:"ItemCount"`
			TableSizeBytes int64  `json:"TableSizeBytes"`
			TableArn       string `json:"TableArn"`
			KeySchema      []struct {
				AttributeName string `json:"AttributeName"`
				KeyType       string `json:"KeyType"`
			} `json:"KeySchema"`
			AttributeDefinitions []struct {
				AttributeName string `json:"AttributeName"`
				AttributeType string `json:"AttributeType"`
			} `json:"AttributeDefinitions"`
			GlobalSecondaryIndexes []struct {
				IndexName string `json:"IndexName"`
				KeySchema []struct {
					AttributeName string `json:"AttributeName"`
					KeyType       string `json:"KeyType"`
				} `json:"KeySchema"`
			} `json:"GlobalSecondaryIndexes"`
		} `json:"Table"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	t := &Table{
		Name: out.Table.TableName, Status: out.Table.TableStatus,
		ItemCount: out.Table.ItemCount, SizeBytes: out.Table.TableSizeBytes, ARN: out.Table.TableArn,
	}
	types := map[string]string{}
	for _, ad := range out.Table.AttributeDefinitions {
		types[ad.AttributeName] = ad.AttributeType
	}
	for _, ks := range out.Table.KeySchema {
		if ks.KeyType == "HASH" {
			t.HashKey, t.HashType = ks.AttributeName, types[ks.AttributeName]
		} else {
			t.RangeKey, t.RangeType = ks.AttributeName, types[ks.AttributeName]
		}
	}
	for _, g := range out.Table.GlobalSecondaryIndexes {
		gi := GSI{Name: g.IndexName}
		for _, ks := range g.KeySchema {
			if ks.KeyType == "HASH" {
				gi.HashKey, gi.HashType = ks.AttributeName, types[ks.AttributeName]
			} else {
				gi.RangeKey, gi.RangeType = ks.AttributeName, types[ks.AttributeName]
			}
		}
		t.GSIs = append(t.GSIs, gi)
	}
	return t, nil
}

// GSICreate is one global secondary index requested at table-creation time.
type GSICreate struct {
	Name                string
	HashKey, HashType   string
	RangeKey, RangeType string
}

// TableCreateOpts is the full create-table request the console form builds.
type TableCreateOpts struct {
	Name                string
	HashKey, HashType   string
	RangeKey, RangeType string
	GSIs                []GSICreate
	TTLAttr             string // enable TTL on this attribute after creation
}

func (b *backend) CreateTable(ctx context.Context, o TableCreateOpts) error {
	// AttributeDefinitions must list every attribute used in any key schema,
	// exactly once — collect base + GSI key attributes, deduped by name.
	defs := map[string]string{o.HashKey: o.HashType}
	if o.RangeKey != "" {
		defs[o.RangeKey] = o.RangeType
	}
	schema := []map[string]string{{"AttributeName": o.HashKey, "KeyType": "HASH"}}
	if o.RangeKey != "" {
		schema = append(schema, map[string]string{"AttributeName": o.RangeKey, "KeyType": "RANGE"})
	}

	var gsis []map[string]any
	for _, g := range o.GSIs {
		if g.Name == "" || g.HashKey == "" {
			continue
		}
		defs[g.HashKey] = g.HashType
		ks := []map[string]string{{"AttributeName": g.HashKey, "KeyType": "HASH"}}
		if g.RangeKey != "" {
			defs[g.RangeKey] = g.RangeType
			ks = append(ks, map[string]string{"AttributeName": g.RangeKey, "KeyType": "RANGE"})
		}
		gsis = append(gsis, map[string]any{
			"IndexName": g.Name, "KeySchema": ks,
			"Projection": map[string]string{"ProjectionType": "ALL"},
		})
	}

	attrs := make([]map[string]string, 0, len(defs))
	for name, typ := range defs {
		attrs = append(attrs, map[string]string{"AttributeName": name, "AttributeType": typ})
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i]["AttributeName"] < attrs[j]["AttributeName"] })

	in := map[string]any{
		"TableName": o.Name, "AttributeDefinitions": attrs, "KeySchema": schema,
		"BillingMode": "PAY_PER_REQUEST",
	}
	if len(gsis) > 0 {
		in["GlobalSecondaryIndexes"] = gsis
	}
	if _, err := b.ddbCall(ctx, "CreateTable", in); err != nil {
		return err
	}

	// TTL is a follow-up call — CreateTable doesn't carry it.
	if o.TTLAttr != "" {
		_, err := b.ddbCall(ctx, "UpdateTimeToLive", map[string]any{
			"TableName": o.Name,
			"TimeToLiveSpecification": map[string]any{
				"Enabled": true, "AttributeName": o.TTLAttr,
			},
		})
		return err
	}
	return nil
}

func (b *backend) DeleteTable(ctx context.Context, name string) error {
	_, err := b.ddbCall(ctx, "DeleteTable", map[string]any{"TableName": name})
	return err
}

// ScanItems returns up to limit items prepared for display, plus whether the
// scan was truncated.
// itemsFromAV turns a list of AttributeValue maps into display Items, mapping
// each to the table's primary key so the row's delete/edit still address the
// base table (even when the results came from a GSI query).
func (b *backend) itemsFromAV(t *Table, avs []map[string]json.RawMessage) []Item {
	items := make([]Item, 0, len(avs))
	for _, av := range avs {
		plain := avMapToPlain(av)
		it := Item{}
		if v, ok := plain[t.HashKey]; ok {
			it.PK = plainScalar(v)
		}
		if t.RangeKey != "" {
			if v, ok := plain[t.RangeKey]; ok {
				it.SK = plainScalar(v)
			}
		}
		rest := map[string]any{}
		for k, v := range plain {
			if k != t.HashKey && k != t.RangeKey {
				rest[k] = v
			}
		}
		if pv, err := json.Marshal(rest); err == nil {
			s := string(pv)
			if len(s) > 100 {
				s = s[:100] + "…"
			}
			if s != "{}" {
				it.Preview = s
			}
		}
		if full, err := json.MarshalIndent(plain, "", "  "); err == nil {
			it.JSON = string(full)
		}
		// Key AV map for DeleteItem — always the base-table primary key.
		if _, ok := av[t.HashKey]; ok {
			key := map[string]json.RawMessage{t.HashKey: av[t.HashKey]}
			if t.RangeKey != "" {
				if rv, ok := av[t.RangeKey]; ok {
					key[t.RangeKey] = rv
				}
			}
			if kj, err := json.Marshal(key); err == nil {
				it.KeyJSON = string(kj)
			}
		}
		items = append(items, it)
	}
	return items
}

// avTyped builds a single-attribute AttributeValue for a key value, honoring
// the attribute's declared type (S/N/B).
func avTyped(typ, val string) map[string]string {
	switch typ {
	case "N":
		return map[string]string{"N": val}
	case "B":
		return map[string]string{"B": val}
	default:
		return map[string]string{"S": val}
	}
}

func (b *backend) ScanItems(ctx context.Context, t *Table, filter string, limit int) ([]Item, bool, error) {
	in := map[string]any{"TableName": t.Name, "Limit": limit}
	if strings.TrimSpace(filter) != "" {
		in["FilterExpression"] = filter
	}
	body, err := b.ddbCall(ctx, "Scan", in)
	if err != nil {
		return nil, false, err
	}
	var out struct {
		Items            []map[string]json.RawMessage `json:"Items"`
		LastEvaluatedKey map[string]json.RawMessage   `json:"LastEvaluatedKey"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, err
	}
	return b.itemsFromAV(t, out.Items), len(out.LastEvaluatedKey) > 0, nil
}

// QueryOpts describes a key-based query against the base table or a GSI.
type QueryOpts struct {
	Index    string // GSI name, or "" for the base table
	PKValue  string
	SKOp     string // "", "=", "<", "<=", ">", ">=", "begins_with", "between"
	SKValue  string
	SKValue2 string // for "between"
	Filter   string // optional FilterExpression on non-key attributes
	Limit    int
}

// QueryItems runs a Query, building the KeyConditionExpression from the chosen
// index's key schema. Names are aliased (#pk/#sk) to dodge reserved words.
func (b *backend) QueryItems(ctx context.Context, t *Table, o QueryOpts) ([]Item, bool, error) {
	pkName, pkType := t.HashKey, t.HashType
	skName, skType := t.RangeKey, t.RangeType
	if o.Index != "" {
		for _, g := range t.GSIs {
			if g.Name == o.Index {
				pkName, pkType = g.HashKey, g.HashType
				skName, skType = g.RangeKey, g.RangeType
			}
		}
	}
	names := map[string]string{"#pk": pkName}
	vals := map[string]any{":pk": avTyped(pkType, o.PKValue)}
	cond := "#pk = :pk"
	if o.SKOp != "" && skName != "" && o.SKValue != "" {
		names["#sk"] = skName
		vals[":sk"] = avTyped(skType, o.SKValue)
		switch o.SKOp {
		case "begins_with":
			cond += " AND begins_with(#sk, :sk)"
		case "between":
			vals[":sk2"] = avTyped(skType, o.SKValue2)
			cond += " AND #sk BETWEEN :sk AND :sk2"
		default:
			cond += " AND #sk " + o.SKOp + " :sk"
		}
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 50
	}
	in := map[string]any{
		"TableName": t.Name, "KeyConditionExpression": cond,
		"ExpressionAttributeNames": names, "ExpressionAttributeValues": vals, "Limit": limit,
	}
	if o.Index != "" {
		in["IndexName"] = o.Index
	}
	if strings.TrimSpace(o.Filter) != "" {
		in["FilterExpression"] = o.Filter
	}
	body, err := b.ddbCall(ctx, "Query", in)
	if err != nil {
		return nil, false, err
	}
	var out struct {
		Items            []map[string]json.RawMessage `json:"Items"`
		LastEvaluatedKey map[string]json.RawMessage   `json:"LastEvaluatedKey"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, err
	}
	return b.itemsFromAV(t, out.Items), len(out.LastEvaluatedKey) > 0, nil
}

// PartiQL runs an ExecuteStatement and maps results back to the base table.
func (b *backend) PartiQL(ctx context.Context, t *Table, statement string) ([]Item, error) {
	body, err := b.ddbCall(ctx, "ExecuteStatement", map[string]any{"Statement": statement})
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []map[string]json.RawMessage `json:"Items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return b.itemsFromAV(t, out.Items), nil
}

// PutItemJSON writes an item given as PLAIN JSON (the console's editor speaks
// plain JSON like the AWS console's form view; conversion to AttributeValue
// happens here).
func (b *backend) PutItemJSON(ctx context.Context, table, plainJSON string) error {
	av, err := plainToAV(plainJSON)
	if err != nil {
		return fmt.Errorf("invalid item JSON: %w", err)
	}
	_, err = b.ddbCall(ctx, "PutItem", map[string]any{"TableName": table, "Item": av})
	return err
}

func (b *backend) DeleteItem(ctx context.Context, table, keyJSON string) error {
	var key map[string]json.RawMessage
	if err := json.Unmarshal([]byte(keyJSON), &key); err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}
	_, err := b.ddbCall(ctx, "DeleteItem", map[string]any{"TableName": table, "Key": key})
	return err
}

// ---- plain JSON <-> AttributeValue ----

// plainToAV converts a plain JSON object into a DynamoDB AttributeValue map.
func plainToAV(src string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(obj))
	for k, v := range obj {
		out[k] = toAV(v)
	}
	return out, nil
}

func toAV(v any) map[string]any {
	switch t := v.(type) {
	case nil:
		return map[string]any{"NULL": true}
	case bool:
		return map[string]any{"BOOL": t}
	case string:
		return map[string]any{"S": t}
	case json.Number:
		return map[string]any{"N": t.String()}
	case []any:
		l := make([]map[string]any, 0, len(t))
		for _, e := range t {
			l = append(l, toAV(e))
		}
		return map[string]any{"L": l}
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[k] = toAV(e)
		}
		return map[string]any{"M": m}
	}
	return map[string]any{"S": fmt.Sprint(v)}
}

// avMapToPlain converts an AttributeValue map back to plain values for display.
func avMapToPlain(av map[string]json.RawMessage) map[string]any {
	out := make(map[string]any, len(av))
	for k, raw := range av {
		out[k] = avToPlain(raw)
	}
	return out
}

func avToPlain(raw json.RawMessage) any {
	var av map[string]json.RawMessage
	if json.Unmarshal(raw, &av) != nil {
		return nil
	}
	for typ, val := range av {
		switch typ {
		case "S", "B":
			var s string
			json.Unmarshal(val, &s)
			return s
		case "N":
			var s string
			json.Unmarshal(val, &s)
			return json.Number(s)
		case "BOOL":
			var b bool
			json.Unmarshal(val, &b)
			return b
		case "NULL":
			return nil
		case "SS", "NS", "BS":
			var l []string
			json.Unmarshal(val, &l)
			return l
		case "L":
			var l []json.RawMessage
			json.Unmarshal(val, &l)
			out := make([]any, 0, len(l))
			for _, e := range l {
				out = append(out, avToPlain(e))
			}
			return out
		case "M":
			var m map[string]json.RawMessage
			json.Unmarshal(val, &m)
			return avMapToPlain(m)
		}
	}
	return nil
}

// plainScalar renders a key value compactly.
func plainScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		b, _ := json.Marshal(v)
		return string(bytes.TrimSpace(b))
	}
}
