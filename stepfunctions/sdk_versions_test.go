package stepfunctions_test

// Versions and aliases through the real SDK: the immutable snapshots
// PublishStateMachineVersion mints, the aliases that route StartExecution to
// them by weight, and the typed errors each refusal must decode to.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

const versionRole = "arn:aws:iam::000000000000:role/StepFunctions"

// passDef is a one-state machine whose output names the version it came
// from, so an execution's output says which snapshot ran.
func passDef(result string) string {
	return fmt.Sprintf(`{"StartAt":"A","States":{"A":{"Type":"Pass","Result":%q,"End":true}}}`, result)
}

func createMachine(t *testing.T, c *awssfn.Client, name, def string) string {
	t.Helper()
	out, err := c.CreateStateMachine(context.Background(), &awssfn.CreateStateMachineInput{
		Name: aws.String(name), Definition: aws.String(def), RoleArn: aws.String(versionRole),
	})
	if err != nil {
		t.Fatalf("CreateStateMachine: %v", err)
	}
	return aws.ToString(out.StateMachineArn)
}

func publish(t *testing.T, c *awssfn.Client, arn string) string {
	t.Helper()
	out, err := c.PublishStateMachineVersion(context.Background(), &awssfn.PublishStateMachineVersionInput{
		StateMachineArn: aws.String(arn),
	})
	if err != nil {
		t.Fatalf("PublishStateMachineVersion: %v", err)
	}
	return aws.ToString(out.StateMachineVersionArn)
}

func updateDef(t *testing.T, c *awssfn.Client, arn, def string) {
	t.Helper()
	if _, err := c.UpdateStateMachine(context.Background(), &awssfn.UpdateStateMachineInput{
		StateMachineArn: aws.String(arn), Definition: aws.String(def),
	}); err != nil {
		t.Fatalf("UpdateStateMachine: %v", err)
	}
}

func createAlias(t *testing.T, c *awssfn.Client, name string, routes ...sfntypes.RoutingConfigurationListItem) string {
	t.Helper()
	out, err := c.CreateStateMachineAlias(context.Background(), &awssfn.CreateStateMachineAliasInput{
		Name: aws.String(name), RoutingConfiguration: routes,
	})
	if err != nil {
		t.Fatalf("CreateStateMachineAlias(%s): %v", name, err)
	}
	return aws.ToString(out.StateMachineAliasArn)
}

func route(versionARN string, weight int32) sfntypes.RoutingConfigurationListItem {
	return sfntypes.RoutingConfigurationListItem{StateMachineVersionArn: aws.String(versionARN), Weight: weight}
}

func TestSDKVersionPublishIsIdempotent(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)
	arn := createMachine(t, c, "versioned", passDef("v1"))

	v1 := publish(t, c, arn)
	if v1 != arn+":1" {
		t.Fatalf("first version ARN = %q, want %q", v1, arn+":1")
	}
	// Same revision, same version: a deploy that changed nothing must not
	// mint a new number.
	if again := publish(t, c, arn); again != v1 {
		t.Errorf("publishing the same revision twice gave %q, want %q", again, v1)
	}

	updateDef(t, c, arn, passDef("v2"))
	desc, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	// A stale revisionId is the optimistic check failing.
	_, err = c.PublishStateMachineVersion(ctx, &awssfn.PublishStateMachineVersionInput{
		StateMachineArn: aws.String(arn), RevisionId: aws.String("stale"),
	})
	var conflict *sfntypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("stale revisionId: got %v, want ConflictException", err)
	}
	v2out, err := c.PublishStateMachineVersion(ctx, &awssfn.PublishStateMachineVersionInput{
		StateMachineArn: aws.String(arn), RevisionId: desc.RevisionId, Description: aws.String("second"),
	})
	if err != nil {
		t.Fatalf("publish with matching revisionId: %v", err)
	}
	v2 := aws.ToString(v2out.StateMachineVersionArn)
	if v2 != arn+":2" {
		t.Fatalf("second version ARN = %q", v2)
	}

	// DescribeStateMachine on a version ARN is the snapshot, not the machine.
	vd, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(v1)})
	if err != nil {
		t.Fatalf("DescribeStateMachine(%s): %v", v1, err)
	}
	if aws.ToString(vd.Definition) != passDef("v1") {
		t.Errorf("version 1 definition = %s, want the v1 snapshot", aws.ToString(vd.Definition))
	}
	if aws.ToString(vd.StateMachineArn) != v1 || aws.ToString(vd.Name) != "versioned" {
		t.Errorf("version describe: arn=%q name=%q", aws.ToString(vd.StateMachineArn), aws.ToString(vd.Name))
	}
	vd2, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(v2)})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(vd2.Description) != "second" {
		t.Errorf("version 2 description = %q, want %q", aws.ToString(vd2.Description), "second")
	}
	if _, err := c.DescribeStateMachine(ctx, &awssfn.DescribeStateMachineInput{StateMachineArn: aws.String(arn + ":9")}); err == nil {
		t.Error("describing a version that was never published succeeded")
	}

	// publish on Create and Update mint versions too, and answer the ARN.
	cr, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
		Name: aws.String("born-published"), Definition: aws.String(passDef("x")), RoleArn: aws.String(versionRole),
		Publish: true, VersionDescription: aws.String("initial"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(cr.StateMachineVersionArn); !strings.HasSuffix(got, ":born-published:1") {
		t.Errorf("CreateStateMachine publish=true answered version %q", got)
	}
	up, err := c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: cr.StateMachineArn, Definition: aws.String(passDef("y")), Publish: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(up.StateMachineVersionArn); !strings.HasSuffix(got, ":born-published:2") {
		t.Errorf("UpdateStateMachine publish=true answered version %q", got)
	}
	// versionDescription without publish is a contradiction AWS refuses.
	_, err = c.UpdateStateMachine(ctx, &awssfn.UpdateStateMachineInput{
		StateMachineArn: cr.StateMachineArn, RoleArn: aws.String(versionRole), VersionDescription: aws.String("nope"),
	})
	var validation *sfntypes.ValidationException
	if !errors.As(err, &validation) {
		t.Errorf("versionDescription without publish: got %v, want ValidationException", err)
	}

	_, err = c.PublishStateMachineVersion(ctx, &awssfn.PublishStateMachineVersionInput{
		StateMachineArn: aws.String(strings.Replace(arn, "versioned", "nobody", 1)),
	})
	var missing *sfntypes.StateMachineDoesNotExist
	if !errors.As(err, &missing) {
		t.Errorf("publishing an unknown machine: got %v, want StateMachineDoesNotExist", err)
	}
}

func TestSDKVersionListOrderAndPagination(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)
	arn := createMachine(t, c, "listed", passDef("v1"))
	var want []string
	for i := 1; i <= 3; i++ {
		updateDef(t, c, arn, passDef(fmt.Sprintf("v%d", i)))
		want = append([]string{publish(t, c, arn)}, want...) // newest first
	}

	all, err := c.ListStateMachineVersions(ctx, &awssfn.ListStateMachineVersionsInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range all.StateMachineVersions {
		got = append(got, aws.ToString(v.StateMachineVersionArn))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ListStateMachineVersions = %v, want newest first %v", got, want)
	}

	var paged []string
	var token *string
	for pages := 0; ; pages++ {
		out, err := c.ListStateMachineVersions(ctx, &awssfn.ListStateMachineVersionsInput{
			StateMachineArn: aws.String(arn), MaxResults: 2, NextToken: token,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range out.StateMachineVersions {
			paged = append(paged, aws.ToString(v.StateMachineVersionArn))
		}
		if out.NextToken == nil {
			if pages != 1 {
				t.Errorf("3 versions at maxResults=2 took %d pages, want 2", pages+1)
			}
			break
		}
		token = out.NextToken
	}
	if strings.Join(paged, ",") != strings.Join(want, ",") {
		t.Errorf("paged = %v, want %v", paged, want)
	}
}

func TestSDKAliasLifecycle(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)
	arn := createMachine(t, c, "aliased", passDef("v1"))
	v1 := publish(t, c, arn)
	updateDef(t, c, arn, passDef("v2"))
	v2 := publish(t, c, arn)

	prod := createAlias(t, c, "PROD", route(v1, 100))
	if prod != arn+":PROD" {
		t.Fatalf("alias ARN = %q, want %q", prod, arn+":PROD")
	}
	// Same request again is the idempotent create; a different routing on
	// the same name is a conflict.
	if again := createAlias(t, c, "PROD", route(v1, 100)); again != prod {
		t.Errorf("idempotent create answered %q", again)
	}
	_, err := c.CreateStateMachineAlias(ctx, &awssfn.CreateStateMachineAliasInput{
		Name: aws.String("PROD"), RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{route(v2, 100)},
	})
	var conflict *sfntypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Errorf("re-creating PROD with different routing: got %v, want ConflictException", err)
	}

	d, err := c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String(prod)})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(d.Name) != "PROD" || len(d.RoutingConfiguration) != 1 ||
		aws.ToString(d.RoutingConfiguration[0].StateMachineVersionArn) != v1 || d.RoutingConfiguration[0].Weight != 100 {
		t.Errorf("DescribeStateMachineAlias = %+v", d)
	}

	if _, err := c.UpdateStateMachineAlias(ctx, &awssfn.UpdateStateMachineAliasInput{
		StateMachineAliasArn: aws.String(prod), Description: aws.String("moved to 2"),
		RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{route(v2, 100)},
	}); err != nil {
		t.Fatalf("UpdateStateMachineAlias: %v", err)
	}
	d, err = c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String(prod)})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(d.Description) != "moved to 2" || aws.ToString(d.RoutingConfiguration[0].StateMachineVersionArn) != v2 {
		t.Errorf("after update: %+v", d)
	}

	list, err := c.ListStateMachineAliases(ctx, &awssfn.ListStateMachineAliasesInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.StateMachineAliases) != 1 || aws.ToString(list.StateMachineAliases[0].StateMachineAliasArn) != prod {
		t.Errorf("ListStateMachineAliases = %+v", list.StateMachineAliases)
	}
	// Given a version ARN, the list is the aliases routing to it.
	byV1, err := c.ListStateMachineAliases(ctx, &awssfn.ListStateMachineAliasesInput{StateMachineArn: aws.String(v1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(byV1.StateMachineAliases) != 0 {
		t.Errorf("aliases of version 1 after PROD moved to 2 = %+v, want none", byV1.StateMachineAliases)
	}

	// A version an alias routes to cannot be deleted.
	_, err = c.DeleteStateMachineVersion(ctx, &awssfn.DeleteStateMachineVersionInput{StateMachineVersionArn: aws.String(v2)})
	if !errors.As(err, &conflict) {
		t.Errorf("deleting the version PROD routes to: got %v, want ConflictException", err)
	}
	if _, err := c.DeleteStateMachineVersion(ctx, &awssfn.DeleteStateMachineVersionInput{StateMachineVersionArn: aws.String(v1)}); err != nil {
		t.Errorf("deleting the unreferenced version 1: %v", err)
	}
	if _, err := c.DeleteStateMachineAlias(ctx, &awssfn.DeleteStateMachineAliasInput{StateMachineAliasArn: aws.String(prod)}); err != nil {
		t.Fatalf("DeleteStateMachineAlias: %v", err)
	}
	_, err = c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String(prod)})
	var notFound *sfntypes.ResourceNotFound
	if !errors.As(err, &notFound) {
		t.Errorf("describing a deleted alias: got %v, want ResourceNotFound", err)
	}
	if _, err := c.DeleteStateMachineVersion(ctx, &awssfn.DeleteStateMachineVersionInput{StateMachineVersionArn: aws.String(v2)}); err != nil {
		t.Errorf("deleting version 2 once the alias is gone: %v", err)
	}
	// Numbers stay monotonic across deletes: the next publish is 3, not 1.
	updateDef(t, c, arn, passDef("v3"))
	if v3 := publish(t, c, arn); v3 != arn+":3" {
		t.Errorf("publish after deleting 1 and 2 gave %q, want :3", v3)
	}

	updateDef(t, c, arn, passDef("v4"))
	v4 := publish(t, c, arn)

	t.Run("refusals", func(t *testing.T) {
		var validation *sfntypes.ValidationException
		_, err := c.CreateStateMachineAlias(ctx, &awssfn.CreateStateMachineAliasInput{
			Name: aws.String("7"), RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{route(arn+":3", 100)},
		})
		if !errors.As(err, &validation) {
			t.Errorf("integer alias name: got %v, want ValidationException", err)
		}
		_, err = c.CreateStateMachineAlias(ctx, &awssfn.CreateStateMachineAliasInput{
			Name: aws.String("lopsided"), RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{route(arn+":3", 60), route(v4, 50)},
		})
		if !errors.As(err, &validation) {
			t.Errorf("weights summing to 110: got %v, want ValidationException", err)
		}
		_, err = c.CreateStateMachineAlias(ctx, &awssfn.CreateStateMachineAliasInput{
			Name: aws.String("dangling"), RoutingConfiguration: []sfntypes.RoutingConfigurationListItem{route(arn+":42", 100)},
		})
		if !errors.As(err, &notFound) {
			t.Errorf("routing to an unpublished version: got %v, want ResourceNotFound", err)
		}
		_, err = c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String("nonsense")})
		var invalid *sfntypes.InvalidArn
		if !errors.As(err, &invalid) {
			t.Errorf("garbage alias ARN: got %v, want InvalidArn", err)
		}
		_, err = c.ListStateMachineAliases(ctx, &awssfn.ListStateMachineAliasesInput{
			StateMachineArn: aws.String(strings.Replace(arn, "aliased", "nobody", 1)),
		})
		var missing *sfntypes.StateMachineDoesNotExist
		if !errors.As(err, &missing) {
			t.Errorf("listing aliases of an unknown machine: got %v, want StateMachineDoesNotExist", err)
		}
	})

	// DeleteStateMachine takes the versions and aliases with it.
	createAlias(t, c, "DOOMED", route(arn+":3", 100))
	if _, err := c.DeleteStateMachine(ctx, &awssfn.DeleteStateMachineInput{StateMachineArn: aws.String(arn)}); err != nil {
		t.Fatal(err)
	}
	_, err = c.DescribeStateMachineAlias(ctx, &awssfn.DescribeStateMachineAliasInput{StateMachineAliasArn: aws.String(arn + ":DOOMED")})
	if !errors.As(err, &notFound) {
		t.Errorf("alias survived DeleteStateMachine: %v", err)
	}
	left, err := c.ListStateMachineVersions(ctx, &awssfn.ListStateMachineVersionsInput{StateMachineArn: aws.String(arn)})
	if err != nil {
		t.Fatal(err)
	}
	if len(left.StateMachineVersions) != 0 {
		t.Errorf("versions survived DeleteStateMachine: %+v", left.StateMachineVersions)
	}
}

// TestSDKExecutionsRunTheAddressedVersion: a version ARN runs its frozen
// snapshot no matter what the machine says today, an alias picks a version
// by weight, and the execution records how it was addressed.
func TestSDKExecutionsRunTheAddressedVersion(t *testing.T) {
	ctx := context.Background()
	c := sfnClient(t)
	arn := createMachine(t, c, "routed", passDef("v1"))
	v1 := publish(t, c, arn)
	updateDef(t, c, arn, passDef("v2"))
	v2 := publish(t, c, arn)
	updateDef(t, c, arn, passDef("head"))

	run := func(target string) *awssfn.DescribeExecutionOutput {
		t.Helper()
		started, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: aws.String(target)})
		if err != nil {
			t.Fatalf("StartExecution(%s): %v", target, err)
		}
		return waitForStatus(t, c, aws.ToString(started.ExecutionArn), sfntypes.ExecutionStatusSucceeded)
	}

	t.Run("version ARN", func(t *testing.T) {
		d := run(v1)
		if aws.ToString(d.Output) != `"v1"` {
			t.Errorf("output = %s, want the v1 snapshot's", aws.ToString(d.Output))
		}
		if aws.ToString(d.StateMachineVersionArn) != v1 || d.StateMachineAliasArn != nil {
			t.Errorf("version=%q alias=%v", aws.ToString(d.StateMachineVersionArn), d.StateMachineAliasArn)
		}
		// The execution belongs to the machine, not the version.
		if aws.ToString(d.StateMachineArn) != arn {
			t.Errorf("stateMachineArn = %q, want the unqualified %q", aws.ToString(d.StateMachineArn), arn)
		}
		frozen, err := c.DescribeStateMachineForExecution(ctx, &awssfn.DescribeStateMachineForExecutionInput{ExecutionArn: d.ExecutionArn})
		if err != nil {
			t.Fatal(err)
		}
		if aws.ToString(frozen.Definition) != passDef("v1") {
			t.Errorf("DescribeStateMachineForExecution = %s, want the v1 snapshot", aws.ToString(frozen.Definition))
		}
	})
	t.Run("bare machine", func(t *testing.T) {
		d := run(arn)
		if aws.ToString(d.Output) != `"head"` || d.StateMachineVersionArn != nil {
			t.Errorf("output=%s version=%v", aws.ToString(d.Output), d.StateMachineVersionArn)
		}
	})
	t.Run("alias 100/0", func(t *testing.T) {
		pinned := createAlias(t, c, "PINNED", route(v2, 100), route(v1, 0))
		for i := 0; i < 10; i++ {
			d := run(pinned)
			if aws.ToString(d.Output) != `"v2"` {
				t.Fatalf("run %d through a 100/0 alias produced %s", i, aws.ToString(d.Output))
			}
			if aws.ToString(d.StateMachineVersionArn) != v2 || aws.ToString(d.StateMachineAliasArn) != pinned {
				t.Fatalf("version=%q alias=%q", aws.ToString(d.StateMachineVersionArn), aws.ToString(d.StateMachineAliasArn))
			}
		}
	})
	t.Run("alias 50/50", func(t *testing.T) {
		split := createAlias(t, c, "CANARY", route(v1, 50), route(v2, 50))
		seen := map[string]int{}
		for i := 0; i < 40; i++ {
			seen[aws.ToString(run(split).StateMachineVersionArn)]++
		}
		if seen[v1] == 0 || seen[v2] == 0 {
			t.Errorf("40 starts through a 50/50 alias split %v; both versions should run", seen)
		}
		// ListExecutions narrowed to the alias sees only its own.
		byAlias, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{StateMachineArn: aws.String(split)})
		if err != nil {
			t.Fatal(err)
		}
		if len(byAlias.Executions) != 40 {
			t.Errorf("ListExecutions(alias) = %d, want 40", len(byAlias.Executions))
		}
		for _, e := range byAlias.Executions {
			if aws.ToString(e.StateMachineAliasArn) != split || e.StateMachineVersionArn == nil {
				t.Fatalf("list item carries alias=%q version=%v", aws.ToString(e.StateMachineAliasArn), e.StateMachineVersionArn)
			}
		}
		byV1, err := c.ListExecutions(ctx, &awssfn.ListExecutionsInput{StateMachineArn: aws.String(v1)})
		if err != nil {
			t.Fatal(err)
		}
		// The direct v1 run plus every canary start that landed on v1.
		if len(byV1.Executions) != seen[v1]+1 {
			t.Errorf("ListExecutions(v1) = %d, want %d", len(byV1.Executions), seen[v1]+1)
		}
	})
	t.Run("unknown alias", func(t *testing.T) {
		_, err := c.StartExecution(ctx, &awssfn.StartExecutionInput{StateMachineArn: aws.String(arn + ":NOPE")})
		var missing *sfntypes.StateMachineDoesNotExist
		if !errors.As(err, &missing) {
			t.Errorf("starting through an unknown alias: got %v, want StateMachineDoesNotExist", err)
		}
	})
}
