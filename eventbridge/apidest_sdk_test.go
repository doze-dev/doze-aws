package eventbridge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awseb "github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// recorder is the HTTP endpoint an API destination posts to.
type recorder struct {
	mu       sync.Mutex
	requests []recorded
	fail     int // answer 500 this many times before 200
	token    string
}

type recorded struct {
	Method, Path, Query, Auth string
	Header                    http.Header
	Body                      string
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Path == "/oauth/token" {
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "cid" || form.Get("client_secret") != "shh" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": r.token, "expires_in": "3600"})
		return
	}
	r.requests = append(r.requests, recorded{Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery,
		Auth: req.Header.Get("Authorization"), Header: req.Header.Clone(), Body: string(body)})
	if r.fail > 0 {
		r.fail--
		w.WriteHeader(500)
		return
	}
	w.WriteHeader(200)
}

func (r *recorder) wait(t *testing.T, n int) []recorded {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.requests) >= n {
			out := append([]recorded(nil), r.requests...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("wanted %d deliveries, got %d", n, len(r.requests))
	return nil
}

func basicConn(ctx context.Context, t *testing.T, eb *awseb.Client, name string) string {
	t.Helper()
	out, err := eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name: aws.String(name), AuthorizationType: ebtypes.ConnectionAuthorizationTypeBasic,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			BasicAuthParameters: &ebtypes.CreateConnectionBasicAuthRequestParameters{Username: aws.String("alice"), Password: aws.String("s3cret")},
			InvocationHttpParameters: &ebtypes.ConnectionHttpParameters{
				HeaderParameters:      []ebtypes.ConnectionHeaderParameter{{Key: aws.String("X-Conn"), Value: aws.String("from-connection")}},
				QueryStringParameters: []ebtypes.ConnectionQueryStringParameter{{Key: aws.String("src"), Value: aws.String("conn")}},
				BodyParameters:        []ebtypes.ConnectionBodyParameter{{Key: aws.String("injected"), Value: aws.String("yes"), IsValueSecret: true}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	return aws.ToString(out.ConnectionArn)
}

func fire(ctx context.Context, t *testing.T, eb *awseb.Client, rule, destARN string, hp *ebtypes.HttpParameters) {
	t.Helper()
	if _, err := eb.PutRule(ctx, &awseb.PutRuleInput{Name: aws.String(rule), EventPattern: aws.String(`{"source":["app.` + rule + `"]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.PutTargets(ctx, &awseb.PutTargetsInput{Rule: aws.String(rule), Targets: []ebtypes.Target{
		{Id: aws.String("hook"), Arn: aws.String(destARN), HttpParameters: hp},
	}}); err != nil {
		t.Fatalf("PutTargets: %v", err)
	}
	if _, err := eb.PutEvents(ctx, &awseb.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{
		{Source: aws.String("app." + rule), DetailType: aws.String("Ping"), Detail: aws.String(`{"n":1}`)},
	}}); err != nil {
		t.Fatal(err)
	}
}

// A BASIC connection: the request carries the credential, the connection's
// header, query and body parameters, and the target's own HttpParameters,
// with `*` path segments filled in order.
func TestSDKAPIDestinationBasic(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	connARN := basicConn(ctx, t, eb, "basic")
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("hook"), ConnectionArn: aws.String(connARN), HttpMethod: ebtypes.ApiDestinationHttpMethodPut,
		InvocationEndpoint: aws.String(hook.URL + "/orders/*/items/*"),
	})
	if err != nil {
		t.Fatalf("CreateApiDestination: %v", err)
	}
	fire(ctx, t, eb, "basic", aws.ToString(dest.ApiDestinationArn), &ebtypes.HttpParameters{
		PathParameterValues:   []string{"o-1", "i 2"},
		HeaderParameters:      map[string]string{"X-Target": "from-target"},
		QueryStringParameters: map[string]string{"q": "t"},
	})
	got := rec.wait(t, 1)[0]
	if got.Method != "PUT" || got.Path != "/orders/o-1/items/i 2" {
		t.Errorf("got %s %s", got.Method, got.Path)
	}
	if got.Query != "q=t&src=conn" {
		t.Errorf("query %q", got.Query)
	}
	if !strings.HasPrefix(got.Auth, "Basic ") {
		t.Errorf("auth %q", got.Auth)
	}
	if got.Header.Get("X-Conn") != "from-connection" || got.Header.Get("X-Target") != "from-target" {
		t.Errorf("headers %v", got.Header)
	}
	var body map[string]any
	json.Unmarshal([]byte(got.Body), &body)
	if body["injected"] != "yes" || body["source"] != "app.basic" {
		t.Errorf("body %s", got.Body)
	}

	// Redaction: Describe reports the username but never the password, and a
	// secret body parameter without its value.
	d, err := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("basic")})
	if err != nil {
		t.Fatal(err)
	}
	if d.AuthParameters.BasicAuthParameters.Username == nil || aws.ToString(d.AuthParameters.BasicAuthParameters.Username) != "alice" {
		t.Errorf("username not reported")
	}
	if !strings.HasPrefix(aws.ToString(d.SecretArn), "arn:aws:secretsmanager:") {
		t.Errorf("SecretArn %q", aws.ToString(d.SecretArn))
	}
	for _, bp := range d.AuthParameters.InvocationHttpParameters.BodyParameters {
		if bp.Value != nil {
			t.Errorf("secret body parameter value leaked: %s", aws.ToString(bp.Value))
		}
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "s3cret") {
		t.Errorf("password leaked in Describe: %s", raw)
	}
}

// An API key connection puts the key in the named header; a 500 is retried
// once, and the OAuth flow fetches a bearer token from the connection's
// endpoint before the delivery.
func TestSDKAPIDestinationAPIKeyRetryAndOAuth(t *testing.T) {
	ctx := context.Background()
	eb, _ := startStack(t)
	rec := &recorder{fail: 1, token: "tok-123"}
	hook := httptest.NewServer(rec)
	defer hook.Close()

	key, err := eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name: aws.String("keyed"), AuthorizationType: ebtypes.ConnectionAuthorizationTypeApiKey,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			ApiKeyAuthParameters: &ebtypes.CreateConnectionApiKeyAuthRequestParameters{ApiKeyName: aws.String("X-Api-Key"), ApiKeyValue: aws.String("k-1")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("keyed"), ConnectionArn: key.ConnectionArn, HttpMethod: ebtypes.ApiDestinationHttpMethodPost,
		InvocationEndpoint: aws.String(hook.URL + "/keyed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	fire(ctx, t, eb, "keyed", aws.ToString(dest.ApiDestinationArn), nil)
	got := rec.wait(t, 2) // the 500, then the retry
	for _, r := range got {
		if r.Header.Get("X-Api-Key") != "k-1" || r.Path != "/keyed" {
			t.Errorf("request %+v", r)
		}
	}

	oauth, err := eb.CreateConnection(ctx, &awseb.CreateConnectionInput{
		Name: aws.String("oauth"), AuthorizationType: ebtypes.ConnectionAuthorizationTypeOauthClientCredentials,
		AuthParameters: &ebtypes.CreateConnectionAuthRequestParameters{
			OAuthParameters: &ebtypes.CreateConnectionOAuthRequestParameters{
				AuthorizationEndpoint: aws.String(hook.URL + "/oauth/token"), HttpMethod: ebtypes.ConnectionOAuthHttpMethodPost,
				ClientParameters: &ebtypes.CreateConnectionOAuthClientRequestParameters{ClientID: aws.String("cid"), ClientSecret: aws.String("shh")},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dest2, err := eb.CreateApiDestination(ctx, &awseb.CreateApiDestinationInput{
		Name: aws.String("oauth"), ConnectionArn: oauth.ConnectionArn, HttpMethod: ebtypes.ApiDestinationHttpMethodPost,
		InvocationEndpoint: aws.String(hook.URL + "/oauth-hook"),
	})
	if err != nil {
		t.Fatal(err)
	}
	fire(ctx, t, eb, "oauth", aws.ToString(dest2.ApiDestinationArn), nil)
	got = rec.wait(t, 3)
	if last := got[2]; last.Path != "/oauth-hook" || last.Auth != "Bearer tok-123" {
		t.Errorf("oauth delivery %+v", last)
	}

	// Lifecycle: the destination goes INACTIVE when its connection is
	// deleted; a deauthorized connection stops deliveries.
	if _, err := eb.DeleteConnection(ctx, &awseb.DeleteConnectionInput{Name: aws.String("keyed")}); err != nil {
		t.Fatal(err)
	}
	dd, err := eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("keyed")})
	if err != nil {
		t.Fatal(err)
	}
	if dd.ApiDestinationState != ebtypes.ApiDestinationStateInactive {
		t.Errorf("destination state after connection delete: %s", dd.ApiDestinationState)
	}
	if _, err := eb.DeauthorizeConnection(ctx, &awseb.DeauthorizeConnectionInput{Name: aws.String("oauth")}); err != nil {
		t.Fatal(err)
	}
	dc, _ := eb.DescribeConnection(ctx, &awseb.DescribeConnectionInput{Name: aws.String("oauth")})
	if dc.ConnectionState != ebtypes.ConnectionStateDeauthorized {
		t.Errorf("state after deauthorize: %s", dc.ConnectionState)
	}
	list, _ := eb.ListConnections(ctx, &awseb.ListConnectionsInput{ConnectionState: ebtypes.ConnectionStateDeauthorized})
	if len(list.Connections) != 1 || aws.ToString(list.Connections[0].Name) != "oauth" {
		t.Errorf("ListConnections by state: %+v", list.Connections)
	}
	dl, _ := eb.ListApiDestinations(ctx, &awseb.ListApiDestinationsInput{ConnectionArn: oauth.ConnectionArn})
	if len(dl.ApiDestinations) != 1 {
		t.Errorf("ListApiDestinations by connection: %d", len(dl.ApiDestinations))
	}
	if _, err := eb.UpdateApiDestination(ctx, &awseb.UpdateApiDestinationInput{Name: aws.String("oauth"), Description: aws.String("updated")}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.DeleteApiDestination(ctx, &awseb.DeleteApiDestinationInput{Name: aws.String("oauth")}); err != nil {
		t.Fatal(err)
	}
	if _, err := eb.DescribeApiDestination(ctx, &awseb.DescribeApiDestinationInput{Name: aws.String("oauth")}); err == nil {
		t.Error("deleted destination still describable")
	}
}
