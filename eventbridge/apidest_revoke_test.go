package eventbridge_test

// Revoking a credential must stop traffic.
//
// dispatchAPIDestination has five guards — the target ARN is not a
// destination, the destination is gone, the destination is not ACTIVE, the
// connection is gone, the connection is not AUTHORIZED — and every one of
// them was dead in the coverage profile. The existing test asserts the STATE
// transitions (a deauthorized connection reports DEAUTHORIZED, a destination
// goes INACTIVE when its connection is deleted) and then never fires another
// event, so nothing established that the state is load-bearing rather than
// cosmetic.
//
// That is the security-shaped half of the feature. An operator who
// deauthorizes a connection because its credential leaked has done nothing at
// all if deliveries continue.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// stays asserts no further delivery arrives for a while.
func (r *recorder) stays(t *testing.T, n int) {
	t.Helper()
	time.Sleep(600 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) != n {
		t.Fatalf("delivery count moved from %d to %d after the credential was revoked", n, len(r.requests))
	}
}

// TestDeauthorizedConnectionStopsDelivering: the credential is revoked and
// the endpoint must go quiet.
func TestDeauthorizedConnectionStopsDelivering(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	connARN := basicConn(ctx, t, eb, "revoked")
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("revoked-hook"), ConnectionArn: aws.String(connARN),
		HttpMethod: ebtypes.ApiDestinationHttpMethodPost, InvocationEndpoint: aws.String(hook.URL + "/hook"),
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	destARN := aws.ToString(dest.ApiDestinationArn)

	// It works before the revocation, or the silence below proves nothing.
	fire(ctx, t, eb, "revoked", destARN, nil)
	rec.wait(t, 1)

	if _, err := eb.DeauthorizeConnection(ctx, &awseb.DeauthorizeConnectionInput{Name: aws.String("revoked")}); err != nil {
		t.Fatalf("DeauthorizeConnection: %v", err)
	}
	conn, err := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("revoked")})
	if err != nil || conn.ConnectionState != ebtypes.ConnectionStateDeauthorized {
		t.Fatalf("connection state = %v err=%v", conn.ConnectionState, err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.revoked"), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":2}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	rec.stays(t, 1)
}

// TestInactiveDestinationStopsDelivering: deleting a connection cascades its
// destinations to INACTIVE, and an INACTIVE destination delivers nothing.
func TestInactiveDestinationStopsDelivering(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	connARN := basicConn(ctx, t, eb, "cascade")
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("cascade-hook"), ConnectionArn: aws.String(connARN),
		HttpMethod: ebtypes.ApiDestinationHttpMethodPost, InvocationEndpoint: aws.String(hook.URL + "/hook"),
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	fire(ctx, t, eb, "cascade", aws.ToString(dest.ApiDestinationArn), nil)
	rec.wait(t, 1)

	if _, err := eb.DeleteConnection(ctx, &awseb.DeleteConnectionInput{Name: aws.String("cascade")}); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	got, err := eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("cascade-hook")})
	if err != nil || got.ApiDestinationState != ebtypes.ApiDestinationStateInactive {
		t.Fatalf("destination state = %v err=%v", got.ApiDestinationState, err)
	}

	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.cascade"), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":2}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	rec.stays(t, 1)
}

// TestDeletedDestinationStopsDelivering: a rule left pointing at a
// destination that no longer exists is logged, not delivered and not a panic.
func TestDeletedDestinationStopsDelivering(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	connARN := basicConn(ctx, t, eb, "doomed")
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("doomed-hook"), ConnectionArn: aws.String(connARN),
		HttpMethod: ebtypes.ApiDestinationHttpMethodPost, InvocationEndpoint: aws.String(hook.URL + "/hook"),
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	fire(ctx, t, eb, "doomed", aws.ToString(dest.ApiDestinationArn), nil)
	rec.wait(t, 1)

	if _, err := eb.DeleteApiDestination(ctx, &awseb.DeleteApiDestinationInput{Name: aws.String("doomed-hook")}); err != nil {
		t.Fatalf("DeleteApiDestination: %v", err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.doomed"), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":2}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	rec.stays(t, 1)
}

// TestReauthorizedConnectionDeliversAgain: the revocation is reversible, so
// the silence above is the state and not a one-way latch.
func TestReauthorizedConnectionDeliversAgain(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	connARN := basicConn(ctx, t, eb, "back")
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("back-hook"), ConnectionArn: aws.String(connARN),
		HttpMethod: ebtypes.ApiDestinationHttpMethodPost, InvocationEndpoint: aws.String(hook.URL + "/hook"),
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	destARN := aws.ToString(dest.ApiDestinationArn)
	fire(ctx, t, eb, "back", destARN, nil)
	rec.wait(t, 1)

	if _, err := eb.DeauthorizeConnection(ctx, &awseb.DeauthorizeConnectionInput{Name: aws.String("back")}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.back"), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":2}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	rec.stays(t, 1)

	// Supplying the credential again re-authorizes it.
	if _, err := eb.UpdateConnection(ctx, &awseb.UpdateConnectionInput{
		Name: aws.String("back"), AuthorizationType: ebtypes.ConnectionAuthorizationTypeBasic,
		AuthParameters: &ebtypes.UpdateConnectionAuthRequestParameters{
			BasicAuthParameters: &ebtypes.UpdateConnectionBasicAuthRequestParameters{
				Username: aws.String("alice"), Password: aws.String("s3cret-2")},
		},
	}); err != nil {
		t.Fatalf("UpdateConnection: %v", err)
	}
	conn, err := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("back")})
	if err != nil || conn.ConnectionState != ebtypes.ConnectionStateAuthorized {
		t.Fatalf("connection state after update = %v err=%v", conn.ConnectionState, err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app.back"), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":3}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, 2)
}
