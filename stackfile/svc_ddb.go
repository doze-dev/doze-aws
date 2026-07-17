package stackfile

// DynamoDB apply + export: tables, GSIs/LSIs, TTL, and deletion protection.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/awsident"
)

// ---- tables ----

func applyTables(ctx context.Context, c *client, s *Stack, rep *Report) error {
	for _, name := range sortedNames(s.Tables) {
		t := s.Tables[name]
		if out, err := c.ddb(ctx, "DescribeTable", map[string]any{"TableName": name}); err == nil {
			if err := convergeTable(ctx, c, rep, name, t, out); err != nil {
				return fmt.Errorf("table %q: %w", name, err)
			}
			continue
		} else if !notFound(err) {
			return fmt.Errorf("table %q: %w", name, err)
		}

		hash, rng, _ := parseKey(t.Key) // validated at parse time
		defs := map[string]string{hash.Name: hash.Type}
		schema := []map[string]string{{"AttributeName": hash.Name, "KeyType": "HASH"}}
		if rng != nil {
			defs[rng.Name] = rng.Type
			schema = append(schema, map[string]string{"AttributeName": rng.Name, "KeyType": "RANGE"})
		}
		var gsis []map[string]any
		for _, gname := range sortedNames(t.GSIs) {
			g := t.GSIs[gname]
			gh, gr, _ := parseKey(g.Key)
			defs[gh.Name] = gh.Type
			ks := []map[string]string{{"AttributeName": gh.Name, "KeyType": "HASH"}}
			if gr != nil {
				defs[gr.Name] = gr.Type
				ks = append(ks, map[string]string{"AttributeName": gr.Name, "KeyType": "RANGE"})
			}
			gsis = append(gsis, map[string]any{
				"IndexName": gname, "KeySchema": ks,
				"Projection": projectionWire(g.Projection, g.Include),
			})
		}
		var lsis []map[string]any
		for _, lname := range sortedNames(t.LSIs) {
			l := t.LSIs[lname]
			lr, _, _ := parseKey(l.Key) // single "attr:TYPE", validated at parse time
			defs[lr.Name] = lr.Type
			lsis = append(lsis, map[string]any{
				"IndexName": lname,
				"KeySchema": []map[string]string{
					{"AttributeName": hash.Name, "KeyType": "HASH"},
					{"AttributeName": lr.Name, "KeyType": "RANGE"},
				},
				"Projection": projectionWire(l.Projection, l.Include),
			})
		}
		var attrs []map[string]string
		for _, n := range sortedNames(defs) {
			attrs = append(attrs, map[string]string{"AttributeName": n, "AttributeType": defs[n]})
		}
		in := map[string]any{
			"TableName": name, "AttributeDefinitions": attrs, "KeySchema": schema,
			"BillingMode": "PAY_PER_REQUEST",
		}
		if len(gsis) > 0 {
			in["GlobalSecondaryIndexes"] = gsis
		}
		if len(lsis) > 0 {
			in["LocalSecondaryIndexes"] = lsis
		}
		if t.DeletionProtection != nil {
			in["DeletionProtectionEnabled"] = *t.DeletionProtection
		}
		if len(t.Tags) > 0 {
			in["Tags"] = tagList(t.Tags, "Key", "Value")
		}
		if _, err := c.ddb(ctx, "CreateTable", in); err != nil {
			return fmt.Errorf("table %q: %w", name, err)
		}
		if t.TTL != "" {
			if _, err := c.ddb(ctx, "UpdateTimeToLive", map[string]any{
				"TableName":               name,
				"TimeToLiveSpecification": map[string]any{"Enabled": true, "AttributeName": t.TTL},
			}); err != nil {
				return fmt.Errorf("table %q ttl: %w", name, err)
			}
		}
		rep.add("created", "table/"+name, "")
	}
	return nil
}

// convergeTable updates what an existing table can cheaply change: missing
// GSIs (UpdateTable backfills synchronously), TTL, deletion protection, and
// tags. The key schema and LSIs are create-time-only, so they are left alone.
func convergeTable(ctx context.Context, c *client, rep *Report, name string, t Table, describe []byte) error {
	var d struct {
		Table struct {
			GlobalSecondaryIndexes    []struct{ IndexName string }
			DeletionProtectionEnabled bool
		}
	}
	json.Unmarshal(describe, &d)
	var changes []string

	live := map[string]bool{}
	for _, g := range d.Table.GlobalSecondaryIndexes {
		live[g.IndexName] = true
	}
	for _, gname := range sortedNames(t.GSIs) {
		if live[gname] {
			continue
		}
		g := t.GSIs[gname]
		gh, gr, _ := parseKey(g.Key)
		defs := []map[string]string{{"AttributeName": gh.Name, "AttributeType": gh.Type}}
		ks := []map[string]string{{"AttributeName": gh.Name, "KeyType": "HASH"}}
		if gr != nil {
			defs = append(defs, map[string]string{"AttributeName": gr.Name, "AttributeType": gr.Type})
			ks = append(ks, map[string]string{"AttributeName": gr.Name, "KeyType": "RANGE"})
		}
		if _, err := c.ddb(ctx, "UpdateTable", map[string]any{
			"TableName": name, "AttributeDefinitions": defs,
			"GlobalSecondaryIndexUpdates": []map[string]any{{"Create": map[string]any{
				"IndexName": gname, "KeySchema": ks,
				"Projection": projectionWire(g.Projection, g.Include),
			}}},
		}); err != nil {
			return fmt.Errorf("gsi %q: %w", gname, err)
		}
		changes = append(changes, "gsi "+gname)
	}

	if t.DeletionProtection != nil && *t.DeletionProtection != d.Table.DeletionProtectionEnabled {
		if _, err := c.ddb(ctx, "UpdateTable", map[string]any{
			"TableName": name, "DeletionProtectionEnabled": *t.DeletionProtection,
		}); err != nil {
			return fmt.Errorf("deletion protection: %w", err)
		}
		changes = append(changes, "deletion protection")
	}

	if t.TTL != "" {
		enabled := false
		if out, err := c.ddb(ctx, "DescribeTimeToLive", map[string]any{"TableName": name}); err == nil {
			var ttl struct {
				TimeToLiveDescription struct{ AttributeName, TimeToLiveStatus string }
			}
			json.Unmarshal(out, &ttl)
			enabled = ttl.TimeToLiveDescription.TimeToLiveStatus == "ENABLED" &&
				ttl.TimeToLiveDescription.AttributeName == t.TTL
		}
		if !enabled {
			if _, err := c.ddb(ctx, "UpdateTimeToLive", map[string]any{
				"TableName":               name,
				"TimeToLiveSpecification": map[string]any{"Enabled": true, "AttributeName": t.TTL},
			}); err != nil {
				return fmt.Errorf("ttl: %w", err)
			}
			changes = append(changes, "ttl")
		}
	}

	if len(t.Tags) > 0 {
		if _, err := c.ddb(ctx, "TagResource", map[string]any{
			"ResourceArn": tableARN(name), "Tags": tagList(t.Tags, "Key", "Value"),
		}); err != nil {
			return fmt.Errorf("tags: %w", err)
		}
	}

	if len(changes) > 0 {
		rep.add("updated", "table/"+name, strings.Join(changes, ", "))
	} else {
		rep.add("skipped", "table/"+name, "exists (key schema is immutable)")
	}
	return nil
}

// projectionWire renders a gsi/lsi projection for CreateTable/UpdateTable.
func projectionWire(p string, include []string) map[string]any {
	out := map[string]any{"ProjectionType": "ALL"}
	if p != "" {
		out["ProjectionType"] = p
	}
	if p == "INCLUDE" {
		out["NonKeyAttributes"] = include
	}
	return out
}

func tableARN(name string) string { return awsident.ARN("dynamodb", "table/"+name) }

func exportTables(ctx context.Context, c *client, s *Stack) error {
	out, err := c.ddb(ctx, "ListTables", map[string]any{})
	if err != nil {
		return err
	}
	var lst struct {
		TableNames []string `json:"TableNames"`
	}
	json.Unmarshal(out, &lst)
	if len(lst.TableNames) > 0 {
		s.Tables = map[string]Table{}
	}
	for _, name := range lst.TableNames {
		out, err := c.ddb(ctx, "DescribeTable", map[string]any{"TableName": name})
		if err != nil {
			continue
		}
		type indexWire struct {
			IndexName string
			KeySchema []struct {
				AttributeName, KeyType string
			}
			Projection struct {
				ProjectionType   string
				NonKeyAttributes []string
			}
		}
		var d struct {
			Table struct {
				KeySchema []struct {
					AttributeName, KeyType string
				}
				AttributeDefinitions []struct {
					AttributeName, AttributeType string
				}
				GlobalSecondaryIndexes    []indexWire
				LocalSecondaryIndexes     []indexWire
				DeletionProtectionEnabled bool
			}
		}
		json.Unmarshal(out, &d)
		types := map[string]string{}
		for _, ad := range d.Table.AttributeDefinitions {
			types[ad.AttributeName] = ad.AttributeType
		}
		keyOf := func(schema []struct{ AttributeName, KeyType string }) string {
			var hash, rng string
			for _, ks := range schema {
				part := ks.AttributeName + ":" + types[ks.AttributeName]
				if ks.KeyType == "HASH" {
					hash = part
				} else {
					rng = part
				}
			}
			if rng != "" {
				return hash + " " + rng
			}
			return hash
		}
		projOf := func(ix indexWire) (string, []string) {
			if p := ix.Projection.ProjectionType; p != "" && p != "ALL" {
				return p, ix.Projection.NonKeyAttributes
			}
			return "", nil
		}
		t := Table{Key: keyOf(d.Table.KeySchema)}
		for _, g := range d.Table.GlobalSecondaryIndexes {
			if t.GSIs == nil {
				t.GSIs = map[string]GSI{}
			}
			proj, incl := projOf(g)
			t.GSIs[g.IndexName] = GSI{Key: keyOf(g.KeySchema), Projection: proj, Include: incl}
		}
		for _, l := range d.Table.LocalSecondaryIndexes {
			if t.LSIs == nil {
				t.LSIs = map[string]LSI{}
			}
			sortKey := ""
			for _, ks := range l.KeySchema {
				if ks.KeyType == "RANGE" {
					sortKey = ks.AttributeName + ":" + types[ks.AttributeName]
				}
			}
			proj, incl := projOf(l)
			t.LSIs[l.IndexName] = LSI{Key: sortKey, Projection: proj, Include: incl}
		}
		if d.Table.DeletionProtectionEnabled {
			v := true
			t.DeletionProtection = &v
		}
		if out, err := c.ddb(ctx, "ListTagsOfResource", map[string]any{"ResourceArn": tableARN(name)}); err == nil {
			var lt struct {
				Tags []struct{ Key, Value string }
			}
			json.Unmarshal(out, &lt)
			for _, tag := range lt.Tags {
				if t.Tags == nil {
					t.Tags = map[string]string{}
				}
				t.Tags[tag.Key] = tag.Value
			}
		}
		if out, err := c.ddb(ctx, "DescribeTimeToLive", map[string]any{"TableName": name}); err == nil {
			var ttl struct {
				TimeToLiveDescription struct {
					AttributeName, TimeToLiveStatus string
				}
			}
			json.Unmarshal(out, &ttl)
			if ttl.TimeToLiveDescription.TimeToLiveStatus == "ENABLED" {
				t.TTL = ttl.TimeToLiveDescription.AttributeName
			}
		}
		s.Tables[name] = t
	}
	return nil
}
