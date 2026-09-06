package stepfunctions_test

// The service integrations against the real embedded stack: a machine that
// writes to the local DynamoDB through the optimized integration, reads it
// back through aws-sdk:dynamodb, round-trips a parameter through aws-sdk:ssm,
// and emits an event with events:putEvents — every hop a real peercall into
// the sibling service, not a stub. What the stub tests cannot prove is that
// the targets, JSON versions and member spellings in sdkServices are the
// ones the services in this repo actually accept; this does.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"

	dozeaws "github.com/doze-dev/doze-aws"
	"github.com/doze-dev/doze-aws/awsident"
)

func TestSDKServiceIntegrationsAgainstTheRealStack(t *testing.T) {
	ctx := context.Background()
	if testing.Short() {
		t.Skip("stands up a full stack")
	}
	stack, err := dozeaws.NewStack(dozeaws.StackConfig{DataDir: t.TempDir(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stack.Close() })
	ts := httptest.NewServer(stack.Handler())
	t.Cleanup(ts.Close)
	cfg := aws.Config{
		Region: awsident.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			awsident.AccessKeyID, awsident.SecretAccessKey, ""),
	}
	c := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ddb := awsddb.NewFromConfig(cfg, func(o *awsddb.Options) { o.BaseEndpoint = aws.String(ts.URL) })

	if _, err := ddb.CreateTable(ctx, &awsddb.CreateTableInput{
		TableName:            aws.String("orders"),
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	}); err != nil {
		t.Fatal(err)
	}

	def := `{"StartAt":"Put","States":{
	  "Put":{"Type":"Task","Resource":"arn:aws:states:::dynamodb:putItem",
	    "Parameters":{"TableName":"orders","Item":{"pk":{"S.$":"$.id"},"status":{"S":"placed"}}},
	    "ResultPath":null,"Next":"Get"},
	  "Get":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:dynamodb:getItem",
	    "Parameters":{"TableName":"orders","Key":{"pk":{"S.$":"$.id"}}},
	    "ResultSelector":{"status.$":"$.Item.status.S"},"ResultPath":"$.row","Next":"PutParam"},
	  "PutParam":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:ssm:putParameter",
	    "Parameters":{"Name":"/orders/last","Type":"String","Value.$":"$.id","Overwrite":true},
	    "ResultPath":"$.putParam","Next":"GetParam"},
	  "GetParam":{"Type":"Task","Resource":"arn:aws:states:::aws-sdk:ssm:getParameter",
	    "Parameters":{"Name":"/orders/last"},
	    "ResultSelector":{"value.$":"$.Parameter.Value"},"ResultPath":"$.param","Next":"Emit"},
	  "Emit":{"Type":"Task","Resource":"arn:aws:states:::events:putEvents",
	    "Parameters":{"Entries":[{"Source":"orders","DetailType":"OrderPlaced","Detail":{"id.$":"$.id"}}]},
	    "ResultPath":"$.emitted","End":true}}}`
	created, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("integrations"), Definition: aws.String(def),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/StepFunctions"),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{
		StateMachineArn: created.StateMachineArn, Input: aws.String(`{"id":"o-77"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)

	var out struct {
		Row      struct{ Status string } `json:"row"`
		PutParam struct{ Version int }   `json:"putParam"`
		Param    struct{ Value string }  `json:"param"`
		Emitted  struct {
			Entries          []struct{ EventId string }
			FailedEntryCount int
		} `json:"emitted"`
	}
	if err := json.Unmarshal([]byte(aws.ToString(done.Output)), &out); err != nil {
		t.Fatalf("output %s: %v", aws.ToString(done.Output), err)
	}
	if out.Row.Status != "placed" {
		t.Errorf("aws-sdk:dynamodb:getItem should read what dynamodb:putItem wrote, got %s", aws.ToString(done.Output))
	}
	if out.PutParam.Version != 1 || out.Param.Value != "o-77" {
		t.Errorf("ssm putParameter/getParameter round trip wrong: %s", aws.ToString(done.Output))
	}
	if len(out.Emitted.Entries) != 1 || out.Emitted.Entries[0].EventId == "" || out.Emitted.FailedEntryCount != 0 {
		t.Errorf("events:putEvents result wrong: %s", aws.ToString(done.Output))
	}

	// And the item really is in the table, as the SDK sees it.
	got, err := ddb.GetItem(ctx, &awsddb.GetItemInput{
		TableName: aws.String("orders"),
		Key:       map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: "o-77"}},
	})
	if err != nil || got.Item == nil {
		t.Fatalf("GetItem after the machine ran: %v %v", got, err)
	}
}
