package cloudformation

// Export of DynamoDB tables: the key schema, secondary indexes and TTL.

import (
	"github.com/doze-dev/doze-aws/provision"
)

func emitTables(s *provision.Stack, add func(prefix, name, typ string, props map[string]any)) {
	for _, name := range sortedNames(s.Tables) {
		t := s.Tables[name]
		props := map[string]any{"TableName": name, "BillingMode": "PAY_PER_REQUEST"}
		attrs, keySchema := keyBlocks(t.Key)
		for idxName, gsi := range t.GSIs {
			gAttrs, gKeys := keyBlocks(gsi.Key)
			attrs = mergeAttrs(attrs, gAttrs)
			g := map[string]any{"IndexName": idxName, "KeySchema": gKeys}
			proj := map[string]any{"ProjectionType": orDefault(gsi.Projection, "ALL")}
			if len(gsi.Include) > 0 {
				proj["NonKeyAttributes"] = gsi.Include
			}
			g["Projection"] = proj
			props["GlobalSecondaryIndexes"] = appendAny(props["GlobalSecondaryIndexes"], g)
		}
		props["AttributeDefinitions"] = attrs
		props["KeySchema"] = keySchema
		if t.TTL != "" {
			props["TimeToLiveSpecification"] = map[string]any{"AttributeName": t.TTL, "Enabled": true}
		}
		if t.DeletionProtection != nil && *t.DeletionProtection {
			props["DeletionProtectionEnabled"] = true
		}
		putTags(props, t.Tags)
		add("Table", name, "AWS::DynamoDB::Table", props)
	}
}
