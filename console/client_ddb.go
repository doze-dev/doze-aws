package console

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
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
	TTLAttr   string // attribute driving TTL, "" when disabled
	TTLStatus string // ENABLED | DISABLED | ENABLING | DISABLING
}

type GSI struct {
	Name      string
	HashKey   string
	HashType  string
	RangeKey  string
	RangeType string
}

// ItemSkeleton is the starting content for the add-item editor: this table's
// key attributes, empty but correctly typed, in key order.
//
// AWS's own console prefills the same thing, and the reason is not saving
// keystrokes. A PutItem missing a key attribute is refused, and DynamoDB's
// refusal talks about the key schema rather than naming the attribute you left
// out — so an empty box invites the one mistake the form is least able to help
// with. Prefilling makes the required shape something you edit rather than
// something you remember, and the types mean a numeric key does not arrive
// quoted.
//
// It is rendered into the textarea rather than set from JS so that it survives
// a form reset without a round trip, and so the editor comes up with it
// already in the buffer.
func (t *Table) ItemSkeleton() string {
	if t.HashKey == "" {
		return "{\n  \n}" // no key schema to describe; leave room to type
	}
	var b strings.Builder
	b.WriteString("{\n  ")
	b.WriteString(strconv.Quote(t.HashKey) + ": " + emptyLiteralFor(t.HashType))
	if t.RangeKey != "" {
		b.WriteString(",\n  " + strconv.Quote(t.RangeKey) + ": " + emptyLiteralFor(t.RangeType))
	}
	b.WriteString("\n}")
	return b.String()
}

// emptyLiteralFor is the empty JSON literal for a key attribute's type. B is
// base64 text on the wire, so it starts out looking like S does.
func emptyLiteralFor(attrType string) string {
	if attrType == "N" {
		return "0"
	}
	return `""`
}

// Item is one scanned row prepared for display.
type Item struct {
	PK      string
	SK      string
	Preview string            // truncated single-line JSON of the non-key attributes
	Attrs   map[string]string // per-attribute display values (feeds real columns)
	JSON    string            // full pretty plain-JSON
	KeyJSON string            // the primary-key AV map as JSON (for DeleteItem)
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

// CountTables is the cheap cardinality probe: one ListTables call, no
// per-table describes.
func (b *backend) CountTables(ctx context.Context) (int, error) {
	body, err := b.ddbCall(ctx, "ListTables", map[string]any{})
	if err != nil {
		return 0, err
	}
	var out struct {
		TableNames []string `json:"TableNames"`
	}
	json.Unmarshal(body, &out)
	return len(out.TableNames), nil
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
	// TTL lives behind its own call.
	if ttlBody, err := b.ddbCall(ctx, "DescribeTimeToLive", map[string]any{"TableName": name}); err == nil {
		var ttl struct {
			Description struct {
				Status        string `json:"TimeToLiveStatus"`
				AttributeName string `json:"AttributeName"`
			} `json:"TimeToLiveDescription"`
		}
		if json.Unmarshal(ttlBody, &ttl) == nil {
			t.TTLStatus = ttl.Description.Status
			if t.TTLStatus == "ENABLED" || t.TTLStatus == "ENABLING" {
				t.TTLAttr = ttl.Description.AttributeName
			}
		}
	}
	return t, nil
}

// SetTTL enables TTL on attr (empty attr disables it). UpdateTimeToLive requires
// the current attribute name when disabling, which we look up first.
func (b *backend) SetTTL(ctx context.Context, table, attr string) error {
	spec := map[string]any{"Enabled": attr != ""}
	if attr != "" {
		spec["AttributeName"] = attr
	} else {
		// Disabling: AWS wants the attribute currently in effect.
		if t, err := b.DescribeTable(ctx, table); err == nil && t.TTLAttr != "" {
			spec["AttributeName"] = t.TTLAttr
		} else {
			spec["AttributeName"] = "ttl"
		}
	}
	_, err := b.ddbCall(ctx, "UpdateTimeToLive", map[string]any{
		"TableName": table, "TimeToLiveSpecification": spec,
	})
	return err
}

// AddGSI adds a global secondary index to an existing table. The new key
// attributes must be declared alongside the index in the same UpdateTable call.
func (b *backend) AddGSI(ctx context.Context, table string, g GSICreate) error {
	ks := []map[string]string{{"AttributeName": g.HashKey, "KeyType": "HASH"}}
	defs := []map[string]string{{"AttributeName": g.HashKey, "AttributeType": g.HashType}}
	if g.RangeKey != "" {
		ks = append(ks, map[string]string{"AttributeName": g.RangeKey, "KeyType": "RANGE"})
		defs = append(defs, map[string]string{"AttributeName": g.RangeKey, "AttributeType": g.RangeType})
	}
	_, err := b.ddbCall(ctx, "UpdateTable", map[string]any{
		"TableName":            table,
		"AttributeDefinitions": defs,
		"GlobalSecondaryIndexUpdates": []map[string]any{{
			"Create": map[string]any{
				"IndexName": g.Name, "KeySchema": ks,
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		}},
	})
	return err
}

// DeleteGSI drops a global secondary index from a table.
func (b *backend) DeleteGSI(ctx context.Context, table, index string) error {
	_, err := b.ddbCall(ctx, "UpdateTable", map[string]any{
		"TableName": table,
		"GlobalSecondaryIndexUpdates": []map[string]any{{
			"Delete": map[string]any{"IndexName": index},
		}},
	})
	return err
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
		it.Attrs = map[string]string{}
		for k, v := range plain {
			if k != t.HashKey && k != t.RangeKey {
				rest[k] = v
				s := plainScalar(v)
				if len(s) > 48 {
					s = s[:48] + "…"
				}
				// the TTL attribute is a known epoch — show the date it means
				if k == t.TTLAttr {
					if sec, err := strconv.ParseInt(s, 10, 64); err == nil && sec > 0 {
						s += " · " + time.Unix(sec, 0).Local().Format("2006-01-02")
					}
				}
				it.Attrs[k] = s
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

// encodeCursor / decodeCursor turn a DynamoDB LastEvaluatedKey into an opaque
// string the browser can hand back as ExclusiveStartKey — real pagination.
func encodeCursor(m map[string]json.RawMessage) string {
	if len(m) == 0 {
		return ""
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) map[string]json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func (b *backend) ScanItems(ctx context.Context, t *Table, filter string, fvals map[string]any, fnames map[string]string, limit int, cursor string) ([]Item, string, ReadStats, error) {
	in := map[string]any{"TableName": t.Name, "Limit": limit}
	if strings.TrimSpace(filter) != "" {
		in["FilterExpression"] = filter
		if len(fvals) > 0 {
			in["ExpressionAttributeValues"] = fvals
		}
		if len(fnames) > 0 {
			in["ExpressionAttributeNames"] = fnames
		}
	}
	if esk := decodeCursor(cursor); esk != nil {
		in["ExclusiveStartKey"] = esk
	}
	body, err := b.ddbCall(ctx, "Scan", in)
	if err != nil {
		return nil, "", ReadStats{}, err
	}
	var out struct {
		Items            []map[string]json.RawMessage `json:"Items"`
		LastEvaluatedKey map[string]json.RawMessage   `json:"LastEvaluatedKey"`
		Count            int                          `json:"Count"`
		ScannedCount     int                          `json:"ScannedCount"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", ReadStats{}, err
	}
	stats := ReadStats{Count: out.Count, ScannedCount: out.ScannedCount}
	return b.itemsFromAV(t, out.Items), encodeCursor(out.LastEvaluatedKey), stats, nil
}

// ReadStats is what a read cost, as DynamoDB reports it.
//
// Count and ScannedCount are the same number until a filter is involved, and
// then the gap between them is the whole point: a filter runs AFTER the read,
// so "3 returned, 40,000 scanned" is a table scan you are paying for in full
// and a query you have not written yet. The console said "3 scanned", which
// was both wrong and the opposite of the warning.
type ReadStats struct {
	Count        int
	ScannedCount int
}

// Filtered reports whether the read examined more rows than it returned.
func (s ReadStats) Filtered() bool { return s.ScannedCount > s.Count }

// Wasted is the proportion of scanned rows the filter threw away, 0-100.
func (s ReadStats) Wasted() int {
	if s.ScannedCount <= 0 || s.ScannedCount <= s.Count {
		return 0
	}
	return (s.ScannedCount - s.Count) * 100 / s.ScannedCount
}

// QueryOpts describes a key-based query against the base table or a GSI.
type QueryOpts struct {
	Index       string // GSI name, or "" for the base table
	PKValue     string
	SKOp        string // "", "=", "<", "<=", ">", ">=", "begins_with", "between"
	SKValue     string
	SKValue2    string            // for "between"
	Filter      string            // optional FilterExpression on non-key attributes
	FilterVals  map[string]any    // :binding → typed AttributeValue for the filter
	FilterNames map[string]string // #alias → attribute name for the filter
	Limit       int
	Cursor      string // opaque pagination cursor (ExclusiveStartKey)
}

// QueryItems runs a Query, building the KeyConditionExpression from the chosen
// index's key schema. Names are aliased (#pk/#sk) to dodge reserved words.
func (b *backend) QueryItems(ctx context.Context, t *Table, o QueryOpts) ([]Item, string, ReadStats, error) {
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
		for k, v := range o.FilterVals {
			vals[k] = v // shares the map with :pk/:sk; user bindings named pk lose
		}
		for k, v := range o.FilterNames {
			names[k] = v
		}
	}
	if esk := decodeCursor(o.Cursor); esk != nil {
		in["ExclusiveStartKey"] = esk
	}
	body, err := b.ddbCall(ctx, "Query", in)
	if err != nil {
		return nil, "", ReadStats{}, err
	}
	var out struct {
		Items            []map[string]json.RawMessage `json:"Items"`
		LastEvaluatedKey map[string]json.RawMessage   `json:"LastEvaluatedKey"`
		Count            int                          `json:"Count"`
		ScannedCount     int                          `json:"ScannedCount"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", ReadStats{}, err
	}
	stats := ReadStats{Count: out.Count, ScannedCount: out.ScannedCount}
	return b.itemsFromAV(t, out.Items), encodeCursor(out.LastEvaluatedKey), stats, nil
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

// ---- point reads, batch reads, batch deletes, and in-place updates ----

// keyAV builds the typed primary-key AttributeValue map from the explorer's
// string inputs, using the table's declared key types.
func keyAV(t *Table, pk, sk string) map[string]any {
	key := map[string]any{t.HashKey: avTyped(t.HashType, pk)}
	if t.RangeKey != "" {
		key[t.RangeKey] = avTyped(t.RangeType, sk)
	}
	return key
}

// GetItem is the point read: the whole primary key in, zero or one item out.
func (b *backend) GetItem(ctx context.Context, t *Table, pk, sk string, consistent bool) ([]Item, error) {
	in := map[string]any{"TableName": t.Name, "Key": keyAV(t, pk, sk)}
	if consistent {
		in["ConsistentRead"] = true
	}
	body, err := b.ddbCall(ctx, "GetItem", in)
	if err != nil {
		return nil, err
	}
	var out struct {
		Item map[string]json.RawMessage `json:"Item"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, nil
	}
	return b.itemsFromAV(t, []map[string]json.RawMessage{out.Item}), nil
}

// parseKeys decodes the selection's KeyJSON strings (each row carries its own
// primary key as an AttributeValue map) back into wire-shaped keys.
func parseKeys(keys []string) ([]map[string]json.RawMessage, error) {
	out := make([]map[string]json.RawMessage, 0, len(keys))
	for _, k := range keys {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(k), &m); err != nil {
			return nil, fmt.Errorf("invalid item key: %w", err)
		}
		out = append(out, m)
	}
	return out, nil
}

// BatchGetItems re-reads the selected keys. Plain mode is BatchGetItem in
// chunks of 100 (its wire limit), retrying UnprocessedKeys. Snapshot mode is
// TransactGetItems — every key read at one consistent instant — and caps at
// 100 items total, because a snapshot cannot be faked across chunks.
func (b *backend) BatchGetItems(ctx context.Context, t *Table, keys []string, snapshot bool) ([]Item, error) {
	avKeys, err := parseKeys(keys)
	if err != nil {
		return nil, err
	}
	if snapshot {
		if len(avKeys) > 100 {
			return nil, fmt.Errorf("a transactional read holds at most 100 items; %d selected", len(avKeys))
		}
		tx := make([]map[string]any, 0, len(avKeys))
		for _, k := range avKeys {
			tx = append(tx, map[string]any{"Get": map[string]any{"TableName": t.Name, "Key": k}})
		}
		body, err := b.ddbCall(ctx, "TransactGetItems", map[string]any{"TransactItems": tx})
		if err != nil {
			return nil, err
		}
		var out struct {
			Responses []struct {
				Item map[string]json.RawMessage `json:"Item"`
			} `json:"Responses"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, err
		}
		avs := make([]map[string]json.RawMessage, 0, len(out.Responses))
		for _, r := range out.Responses {
			if r.Item != nil {
				avs = append(avs, r.Item)
			}
		}
		return b.itemsFromAV(t, avs), nil
	}
	var avs []map[string]json.RawMessage
	for start := 0; start < len(avKeys); start += 100 {
		pending := avKeys[start:min(start+100, len(avKeys))]
		for tries := 0; len(pending) > 0; tries++ {
			if tries == 5 {
				return nil, fmt.Errorf("%d keys still unprocessed after 5 attempts", len(pending))
			}
			body, err := b.ddbCall(ctx, "BatchGetItem", map[string]any{
				"RequestItems": map[string]any{t.Name: map[string]any{"Keys": pending}},
			})
			if err != nil {
				return nil, err
			}
			var out struct {
				Responses       map[string][]map[string]json.RawMessage `json:"Responses"`
				UnprocessedKeys map[string]struct {
					Keys []map[string]json.RawMessage `json:"Keys"`
				} `json:"UnprocessedKeys"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return nil, err
			}
			avs = append(avs, out.Responses[t.Name]...)
			pending = out.UnprocessedKeys[t.Name].Keys
		}
	}
	return b.itemsFromAV(t, avs), nil
}

// BatchDeleteItems deletes the selected keys. Plain mode is BatchWriteItem in
// chunks of 25 (its wire limit) — best-effort, each chunk independent. Atomic
// mode is a single TransactWriteItems: every delete commits or none do, which
// is why it refuses more than the transaction limit instead of chunking.
func (b *backend) BatchDeleteItems(ctx context.Context, table string, keys []string, atomic bool) (int, error) {
	avKeys, err := parseKeys(keys)
	if err != nil {
		return 0, err
	}
	if atomic {
		if len(avKeys) > 100 {
			return 0, fmt.Errorf("a transaction holds at most 100 items; %d selected", len(avKeys))
		}
		tx := make([]map[string]any, 0, len(avKeys))
		for _, k := range avKeys {
			tx = append(tx, map[string]any{"Delete": map[string]any{"TableName": table, "Key": k}})
		}
		if _, err := b.ddbCall(ctx, "TransactWriteItems", map[string]any{"TransactItems": tx}); err != nil {
			return 0, err
		}
		return len(avKeys), nil
	}
	deleted := 0
	for start := 0; start < len(avKeys); start += 25 {
		chunk := avKeys[start:min(start+25, len(avKeys))]
		pending := make([]any, 0, len(chunk))
		for _, k := range chunk {
			pending = append(pending, map[string]any{"DeleteRequest": map[string]any{"Key": k}})
		}
		for tries := 0; len(pending) > 0; tries++ {
			if tries == 5 {
				return deleted, fmt.Errorf("%d deletes still unprocessed after 5 attempts", len(pending))
			}
			body, err := b.ddbCall(ctx, "BatchWriteItem", map[string]any{
				"RequestItems": map[string]any{table: pending},
			})
			if err != nil {
				return deleted, err
			}
			var out struct {
				UnprocessedItems map[string][]json.RawMessage `json:"UnprocessedItems"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				return deleted, err
			}
			deleted += len(pending) - len(out.UnprocessedItems[table])
			pending = pending[:0]
			for _, raw := range out.UnprocessedItems[table] {
				pending = append(pending, raw)
			}
		}
	}
	return deleted, nil
}

// UpdateItemExpr applies an UpdateExpression to one item in place — the edit
// DynamoDB actually has, as opposed to the whole-item replace PutItem does.
// Values and name aliases arrive pre-parsed by the same bindings helper the
// explorer's filter expressions use.
func (b *backend) UpdateItemExpr(ctx context.Context, table, keyJSON, expr string, vals map[string]any, names map[string]string) error {
	var key map[string]json.RawMessage
	if err := json.Unmarshal([]byte(keyJSON), &key); err != nil {
		return fmt.Errorf("invalid item key: %w", err)
	}
	in := map[string]any{"TableName": table, "Key": key, "UpdateExpression": expr}
	if len(vals) > 0 {
		in["ExpressionAttributeValues"] = vals
	}
	if len(names) > 0 {
		in["ExpressionAttributeNames"] = names
	}
	_, err := b.ddbCall(ctx, "UpdateItem", in)
	return err
}
