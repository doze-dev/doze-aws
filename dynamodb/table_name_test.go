package dynamodb_test

// A table name is 3 to 255 characters from a-z A-Z 0-9 _ - . .
//
// The model states it on the shape TableName (length 3..255, pattern
// ^[a-zA-Z0-9_.-]+$), which every member that names a table being made or
// described targets (TableDescription.TableName, TableCreationParameters,
// RestoreTableFromBackup.TargetTableName, ...); CreateTable's own member targets
// TableArn (1..1024) only because it may also carry an ARN. The naming-rules page
// says the same in words. The create accepted any string ("a", "bad name!", 300
// characters) and then could not describe the table with a name its own model
// forbids. Direction before: more forgiving. Code ValidationException, 400; the
// message follows AWS's validation style and is `memory`, so only its member and
// the rule are held.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
)

func createNamed(c *awsddb.Client, name string) error {
	_, err := c.CreateTable(context.Background(), &awsddb.CreateTableInput{
		TableName:            aws.String(name),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	return err
}

func TestATableNameFollowsTheModelsShape(t *testing.T) {
	c := ddbClient(t)
	for _, tc := range []struct{ what, name, rule string }{
		{"one character", "a", "length greater than or equal to 3"},
		{"two characters", "ab", "length greater than or equal to 3"},
		{"256 characters", strings.Repeat("a", 256), "length less than or equal to 255"},
		{"a space", "bad name", "regular expression pattern"},
		{"an exclamation mark", "bad!name", "regular expression pattern"},
		{"a slash", "a/b/c", "regular expression pattern"},
		{"a colon, as in an ARN", "arn:aws:dynamodb:us-east-1:000000000000:table/orders", "regular expression pattern"},
		{"non-ASCII letters", "tablé", "regular expression pattern"},
	} {
		err := createNamed(c, tc.name)
		var api smithy.APIError
		if err == nil || !errors.As(err, &api) || api.ErrorCode() != "ValidationException" {
			t.Errorf("%s (%q): got %v, want ValidationException", tc.what, tc.name, err)
			continue
		}
		if !strings.Contains(api.ErrorMessage(), "tableName") || !strings.Contains(api.ErrorMessage(), tc.rule) {
			t.Errorf("%s: the message does not name the member and the rule %q: %s", tc.what, tc.rule, api.ErrorMessage())
		}
	}
	// Nothing was made by any of them.
	out, err := c.ListTables(context.Background(), &awsddb.ListTablesInput{})
	if err != nil || len(out.TableNames) != 0 {
		t.Errorf("refused creates left tables behind: %v, %v", out, err)
	}
	// The edges are valid: 3 characters, 255, and every allowed punctuation.
	for _, name := range []string{"abc", strings.Repeat("a", 255), "A-b_c.9", "...", "---", "___"} {
		if err := createNamed(c, name); err != nil {
			t.Errorf("%q is a valid table name: %v", name, err)
		}
	}
}
