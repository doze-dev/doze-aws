package eventbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/trace"
)

// Delivery to an API destination: a real HTTP request, built from the
// destination's endpoint (with `*` path parameters filled from the target),
// the connection's invocation parameters, the target's HttpParameters, and
// the connection's credential. It runs on its own goroutine so PutEvents
// never waits on the network, with one retry a second later on a transport
// error, a 429 or a 5xx — AWS retries for a day with backoff and a DLQ; one
// retry is the honest local budget, and a failure is logged with the rule,
// the destination and the status.

const destinationTimeout = 5 * time.Second

// oauthToken is a cached bearer token for a connection.
type oauthToken struct {
	value   string
	expires time.Time
}

// dispatchAPIDestination resolves the target's destination and connection
// and delivers in the background.
func (s *Server) dispatchAPIDestination(ctx context.Context, rule Rule, target Target, payload []byte) {
	name, _, ok := destinationFromARN(target.ARN)
	if !ok {
		s.logf("eventbridge: rule %s target %s: %s is not an api-destination ARN", rule.Name, target.ID, target.ARN)
		return
	}
	dest, err := s.store.GetApiDestination(name)
	if err != nil {
		s.logf("eventbridge: rule %s target %s: %v", rule.Name, target.ID, err)
		return
	}
	if dest.State != "ACTIVE" {
		s.logf("eventbridge: rule %s target %s: api destination %s is %s", rule.Name, target.ID, name, dest.State)
		return
	}
	connName, _, _ := destinationConnection(dest.ConnectionARN)
	conn, err := s.store.GetConnection(connName)
	if err != nil {
		s.logf("eventbridge: rule %s target %s: %v", rule.Name, target.ID, err)
		return
	}
	if conn.State != "AUTHORIZED" {
		s.logf("eventbridge: rule %s target %s: connection %s is %s", rule.Name, target.ID, connName, conn.State)
		return
	}
	go s.deliverHTTP(context.WithoutCancel(ctx), rule, target, dest, conn, payload)
}

// buildRequest assembles the request: URL, parameters, body and auth.
func (s *Server) buildRequest(ctx context.Context, dest *ApiDestination, conn *Connection, target Target, payload []byte) (*http.Request, error) {
	endpoint := dest.Endpoint
	var hp HTTPParameters
	if target.HttpParameters != nil {
		hp = *target.HttpParameters
	}
	for _, v := range hp.PathParameterValues {
		if !strings.Contains(endpoint, "*") {
			break
		}
		endpoint = strings.Replace(endpoint, "*", url.PathEscape(v), 1)
	}
	if strings.Contains(endpoint, "*") {
		return nil, fmt.Errorf("endpoint %s has a * path parameter the target did not fill", dest.Endpoint)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for _, p := range conn.Invocation.Query {
		q.Set(p.Key, p.Value)
	}
	for k, v := range hp.QueryStringParameters {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	body := payload
	if len(conn.Invocation.Body) > 0 {
		// Body parameters merge into a JSON object; anything else is sent
		// as is, with a note in the log.
		var obj map[string]any
		if json.Unmarshal(payload, &obj) == nil && obj != nil {
			for _, p := range conn.Invocation.Body {
				obj[p.Key] = p.Value
			}
			body, _ = json.Marshal(obj)
		} else {
			s.logf("eventbridge: connection %s has body parameters but the event is not a JSON object; sent without them", conn.Name)
		}
	}
	req, err := http.NewRequestWithContext(ctx, dest.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for _, p := range conn.Invocation.Header {
		req.Header.Set(p.Key, p.Value)
	}
	for k, v := range hp.HeaderParameters {
		req.Header.Set(k, v)
	}
	if err := s.applyAuth(ctx, req, conn); err != nil {
		return nil, err
	}
	return req, nil
}

// applyAuth adds the connection's credential.
func (s *Server) applyAuth(ctx context.Context, req *http.Request, conn *Connection) error {
	switch {
	case conn.Basic != nil:
		req.SetBasicAuth(conn.Basic.Username, conn.Basic.Password)
	case conn.APIKey != nil:
		req.Header.Set(conn.APIKey.Name, conn.APIKey.Value)
	case conn.OAuth != nil:
		token, err := s.fetchToken(ctx, conn)
		if err != nil {
			return fmt.Errorf("oauth token for connection %s: %w", conn.Name, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return nil
}

// fetchToken runs the client-credentials grant against the connection's
// authorization endpoint, caching the token until it expires.
func (s *Server) fetchToken(ctx context.Context, conn *Connection) (string, error) {
	s.tokenMu.Lock()
	if t, ok := s.tokens[conn.ARN()]; ok && s.now().Before(t.expires) {
		s.tokenMu.Unlock()
		return t.value, nil
	}
	s.tokenMu.Unlock()

	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {conn.OAuth.ClientID}, "client_secret": {conn.OAuth.ClientSecret}}
	for _, p := range conn.OAuth.Params.Body {
		form.Set(p.Key, p.Value)
	}
	endpoint, err := url.Parse(conn.OAuth.Endpoint)
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	for _, p := range conn.OAuth.Params.Query {
		q.Set(p.Key, p.Value)
	}
	endpoint.RawQuery = q.Encode()
	method := conn.OAuth.Method
	if method == "" {
		method = http.MethodPost
	}
	var req *http.Request
	if method == http.MethodGet {
		q := endpoint.Query()
		for k, vs := range form {
			q.Set(k, vs[0])
		}
		endpoint.RawQuery = q.Encode()
		req, err = http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, endpoint.String(), strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return "", err
	}
	for _, p := range conn.OAuth.Params.Header {
		req.Header.Set(p.Key, p.Value)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("authorization endpoint answered %s", resp.Status)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if json.Unmarshal(raw, &tok) != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("authorization endpoint did not return an access_token")
	}
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	s.tokenMu.Lock()
	s.tokens[conn.ARN()] = oauthToken{value: tok.AccessToken, expires: s.now().Add(ttl - 30*time.Second)}
	s.tokenMu.Unlock()
	return tok.AccessToken, nil
}

// forgetToken drops a cached token after the connection changed.
func (s *Server) forgetToken(connARN string) {
	s.tokenMu.Lock()
	delete(s.tokens, connARN)
	s.tokenMu.Unlock()
}

// deliverHTTP sends the request, once more after a second on a transport
// error, 429 or 5xx, and logs the outcome.
func (s *Server) deliverHTTP(ctx context.Context, rule Rule, target Target, dest *ApiDestination, conn *Connection, payload []byte) {
	host := dest.Endpoint
	if u, err := url.Parse(dest.Endpoint); err == nil {
		host = u.Host
	}
	err := trace.Step(ctx, trace.Event{Service: "http", Action: dest.Method, Resource: host, Via: "events:" + rule.Name},
		func(ctx context.Context) error {
			var last error
			for attempt := 1; attempt <= 2; attempt++ {
				actx, cancel := context.WithTimeout(ctx, destinationTimeout)
				req, err := s.buildRequest(actx, dest, conn, target, payload)
				if err != nil {
					cancel()
					return err
				}
				resp, err := s.http.Do(req)
				if err == nil {
					io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
					resp.Body.Close()
					if resp.StatusCode != 429 && resp.StatusCode/100 != 5 {
						cancel()
						if resp.StatusCode/100 != 2 {
							return fmt.Errorf("%s answered %s", dest.Name, resp.Status)
						}
						return nil
					}
					last = fmt.Errorf("%s answered %s", dest.Name, resp.Status)
				} else {
					last = err
				}
				cancel()
				if attempt == 1 {
					time.Sleep(time.Second)
				}
			}
			return last
		})
	if err != nil {
		s.logf("eventbridge: rule %s target %s -> api destination %s: %v", rule.Name, target.ID, dest.Name, err)
	}
}

// tokenCache is the Server's OAuth token cache.
type tokenCache struct {
	tokenMu sync.Mutex
	tokens  map[string]oauthToken
}
