package lambda

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/peercall"
)

// ---- aliases ----

func (s *Server) routeAliases(w http.ResponseWriter, r *http.Request, name string, segs []string) *awshttp.APIError {
	if len(segs) == 4 { // /functions/{name}/aliases
		switch r.Method {
		case http.MethodPost:
			var req struct {
				Name            string `json:"Name"`
				FunctionVersion string `json:"FunctionVersion"`
			}
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			f, err := s.store.Update(name, func(f *Function) error {
				if f.Aliases == nil {
					f.Aliases = map[string]string{}
				}
				f.Aliases[req.Name] = req.FunctionVersion
				return nil
			})
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 201, aliasView(f, req.Name, req.FunctionVersion))
			return nil
		case http.MethodGet:
			f, err := s.store.GetFunction(name)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			views := []any{}
			for alias, ver := range f.Aliases {
				if strings.HasPrefix(alias, "$") {
					continue
				}
				views = append(views, aliasView(f, alias, ver))
			}
			writeJSON(w, 200, map[string]any{"Aliases": views})
			return nil
		}
	}
	if len(segs) == 5 { // /functions/{name}/aliases/{alias}
		alias := segs[4]
		switch r.Method {
		case http.MethodGet:
			f, err := s.store.GetFunction(name)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			ver, ok := f.Aliases[alias]
			if !ok {
				return awshttp.Errf(404, "ResourceNotFoundException", "alias %s not found", alias)
			}
			writeJSON(w, 200, aliasView(f, alias, ver))
			return nil
		case http.MethodDelete:
			s.store.Update(name, func(f *Function) error {
				delete(f.Aliases, alias)
				return nil
			})
			w.WriteHeader(204)
			return nil
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported alias request")
}

func aliasView(f *Function, alias, version string) map[string]any {
	return map[string]any{
		"AliasArn":        f.ARN() + ":" + alias,
		"Name":            alias,
		"FunctionVersion": version,
	}
}

// ---- concurrency (Tier C) ----

func (s *Server) routeConcurrency(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPut:
		var req struct {
			ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		n := req.ReservedConcurrentExecutions
		if _, err := s.store.Update(name, func(f *Function) error { f.ReservedConcurrency = &n; return nil }); err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, map[string]any{"ReservedConcurrentExecutions": n})
		return nil
	case http.MethodGet:
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		out := map[string]any{}
		if f.ReservedConcurrency != nil {
			out["ReservedConcurrentExecutions"] = *f.ReservedConcurrency
		}
		writeJSON(w, 200, out)
		return nil
	case http.MethodDelete:
		s.store.Update(name, func(f *Function) error { f.ReservedConcurrency = nil; return nil })
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported concurrency request")
}

// ---- function URLs ----

func (s *Server) routeFunctionURL(w http.ResponseWriter, r *http.Request, name string) *awshttp.APIError {
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		f, err := s.store.Update(name, func(f *Function) error {
			if f.FunctionURL == "" {
				f.FunctionURL = "http://" + name + ".lambda-url.local/"
			}
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 201, map[string]any{
			"FunctionUrl": f.FunctionURL, "FunctionArn": f.ARN(), "AuthType": "NONE",
		})
		return nil
	case http.MethodGet:
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if f.FunctionURL == "" {
			return awshttp.Errf(404, "ResourceNotFoundException", "no function URL config for %s", name)
		}
		writeJSON(w, 200, map[string]any{"FunctionUrl": f.FunctionURL, "FunctionArn": f.ARN(), "AuthType": "NONE"})
		return nil
	case http.MethodDelete:
		s.store.Update(name, func(f *Function) error { f.FunctionURL = ""; return nil })
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported function-url request")
}

// ---- event invoke config (async destinations / retries) ----

// eventInvokeReq is the wire body for Put/UpdateFunctionEventInvokeConfig.
// Pointer fields distinguish "omitted" (leave as-is on Update) from an explicit
// value; DestinationConfig is captured raw and reused verbatim by
// routeDestination on async failure/success.
type eventInvokeReq struct {
	DestinationConfig        json.RawMessage `json:"DestinationConfig"`
	MaximumRetryAttempts     *int            `json:"MaximumRetryAttempts"`
	MaximumEventAgeInSeconds *int            `json:"MaximumEventAgeInSeconds"`
}

func (s *Server) eventInvokeView(f *Function) map[string]any {
	v := map[string]any{
		"FunctionArn": f.ARN() + ":$LATEST",
		// EventInvokeConfig models LastModified as a unix-timestamp number
		// (unlike GetFunction, which uses an ISO8601 string).
		"LastModified": s.now().Unix(),
	}
	if f.MaxRetryAttempts != nil {
		v["MaximumRetryAttempts"] = *f.MaxRetryAttempts
	}
	if f.MaxEventAgeSeconds != nil {
		v["MaximumEventAgeInSeconds"] = *f.MaxEventAgeSeconds
	}
	if len(f.Destinations) > 0 {
		v["DestinationConfig"] = json.RawMessage(f.Destinations)
	}
	return v
}

func (s *Server) routeEventInvokeConfig(w http.ResponseWriter, r *http.Request, name string, segs []string) *awshttp.APIError {
	// GET /event-invoke-config/list enumerates the (0 or 1) configs.
	if len(segs) == 5 && segs[4] == "list" && r.Method == http.MethodGet {
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		list := []any{}
		if f.HasEventInvokeCfg {
			list = append(list, s.eventInvokeView(f))
		}
		writeJSON(w, 200, map[string]any{"FunctionEventInvokeConfigs": list})
		return nil
	}
	if len(segs) != 4 {
		return awshttp.Errf(404, "ResourceNotFoundException", "unknown event-invoke-config path")
	}

	switch r.Method {
	// PUT fully replaces the config; POST merges (UpdateFunctionEventInvokeConfig).
	case http.MethodPut, http.MethodPost:
		var req eventInvokeReq
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		replace := r.Method == http.MethodPut
		f, err := s.store.Update(name, func(f *Function) error {
			if replace {
				f.Destinations, f.MaxRetryAttempts, f.MaxEventAgeSeconds = nil, nil, nil
			}
			if len(req.DestinationConfig) > 0 {
				f.Destinations = req.DestinationConfig
			}
			if req.MaximumRetryAttempts != nil {
				f.MaxRetryAttempts = req.MaximumRetryAttempts
			}
			if req.MaximumEventAgeInSeconds != nil {
				f.MaxEventAgeSeconds = req.MaximumEventAgeInSeconds
			}
			f.HasEventInvokeCfg = true
			return nil
		})
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, s.eventInvokeView(f))
		return nil
	case http.MethodGet:
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		if !f.HasEventInvokeCfg {
			return awshttp.Errf(404, "ResourceNotFoundException", "no event invoke config for %s", name)
		}
		writeJSON(w, 200, s.eventInvokeView(f))
		return nil
	case http.MethodDelete:
		if _, err := s.store.Update(name, func(f *Function) error {
			f.Destinations, f.MaxRetryAttempts, f.MaxEventAgeSeconds, f.HasEventInvokeCfg = nil, nil, nil, false
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported event-invoke-config request")
}

// ---- tags ----

func (s *Server) routeTags(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) < 3 {
		return awshttp.Errf(400, "InvalidParameterValueException", "tags requires a resource ARN")
	}
	arn := strings.Join(segs[2:], "/")
	name := arn[strings.LastIndex(arn, ":")+1:]
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Tags map[string]string `json:"Tags"`
		}
		if aerr := decode(r, &req); aerr != nil {
			return aerr
		}
		if _, err := s.store.Update(name, func(f *Function) error {
			if f.Tags == nil {
				f.Tags = map[string]string{}
			}
			for k, v := range req.Tags {
				f.Tags[k] = v
			}
			return nil
		}); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(204)
		return nil
	case http.MethodGet:
		f, err := s.store.GetFunction(name)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, map[string]any{"Tags": f.Tags})
		return nil
	case http.MethodDelete:
		keys := r.URL.Query()["tagKeys"]
		s.store.Update(name, func(f *Function) error {
			for _, k := range keys {
				delete(f.Tags, k)
			}
			return nil
		})
		w.WriteHeader(204)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported tags request")
}

// ---- event source mappings ----

func (s *Server) routeMappings(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	if len(segs) == 2 {
		switch r.Method {
		case http.MethodPost:
			return s.createMapping(w, r)
		case http.MethodGet:
			maps, _ := s.store.ListMappings()
			views := []any{}
			for i := range maps {
				views = append(views, mappingView(&maps[i]))
			}
			writeJSON(w, 200, map[string]any{"EventSourceMappings": views})
			return nil
		}
	}
	if len(segs) == 3 {
		uuid := segs[2]
		switch r.Method {
		case http.MethodGet:
			m, err := s.store.GetMapping(uuid)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 200, mappingView(m))
			return nil
		case http.MethodDelete:
			s.stopPoller(uuid)
			s.store.DeleteMapping(uuid)
			m := &EventSourceMapping{UUID: uuid, State: "Deleting"}
			writeJSON(w, 202, mappingView(m))
			return nil
		case http.MethodPut:
			return s.updateMapping(w, r, uuid)
		}
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported mapping request")
}

func (s *Server) createMapping(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req struct {
		FunctionName   string `json:"FunctionName"`
		EventSourceArn string `json:"EventSourceArn"`
		BatchSize      int    `json:"BatchSize"`
		Enabled        *bool  `json:"Enabled"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	if !strings.Contains(req.EventSourceArn, ":sqs:") {
		return awshttp.Errf(400, "InvalidParameterValueException",
			"doze-aws event source mappings support SQS sources; got %q", req.EventSourceArn)
	}
	fnName := req.FunctionName[strings.LastIndex(req.FunctionName, ":")+1:]
	if _, err := s.store.GetFunction(fnName); err != nil {
		return awshttp.AsAPIError(err)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	m := &EventSourceMapping{
		UUID: newUUID(), FunctionName: fnName, EventSourceArn: req.EventSourceArn,
		BatchSize: orInt(req.BatchSize, 10), Enabled: enabled,
		State: stateFor(enabled),
	}
	if err := s.store.PutMapping(m); err != nil {
		return awshttp.AsAPIError(err)
	}
	if enabled {
		s.startPoller(m)
	}
	writeJSON(w, 202, mappingView(m))
	return nil
}

func (s *Server) updateMapping(w http.ResponseWriter, r *http.Request, uuid string) *awshttp.APIError {
	var req struct {
		Enabled   *bool `json:"Enabled"`
		BatchSize int   `json:"BatchSize"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	m, err := s.store.GetMapping(uuid)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if req.BatchSize > 0 {
		m.BatchSize = req.BatchSize
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
		m.State = stateFor(m.Enabled)
	}
	s.store.PutMapping(m)
	s.stopPoller(uuid)
	if m.Enabled {
		s.startPoller(m)
	}
	writeJSON(w, 202, mappingView(m))
	return nil
}

func mappingView(m *EventSourceMapping) map[string]any {
	return map[string]any{
		"UUID":                  m.UUID,
		"FunctionArn":           "arn:aws:lambda:us-east-1:000000000000:function:" + m.FunctionName,
		"EventSourceArn":        m.EventSourceArn,
		"BatchSize":             m.BatchSize,
		"State":                 m.State,
		"StateTransitionReason": "User action",
	}
}

func stateFor(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

// esm is a running SQS poller for one mapping.
type esm struct {
	stopCh chan struct{}
	once   sync.Once
}

func (e *esm) stop() { e.once.Do(func() { close(e.stopCh) }) }

func (s *Server) startPoller(m *EventSourceMapping) {
	s.mu.Lock()
	if _, running := s.mappings[m.UUID]; running {
		s.mu.Unlock()
		return
	}
	poller := &esm{stopCh: make(chan struct{})}
	s.mappings[m.UUID] = poller
	s.mu.Unlock()

	queue := m.EventSourceArn[strings.LastIndex(m.EventSourceArn, ":")+1:]
	fnName := m.FunctionName
	batch := m.BatchSize
	go func() {
		for {
			select {
			case <-poller.stopCh:
				return
			default:
			}
			msgs, err := peercall.SQSReceive(s.peers, queue, batch, 2)
			if err != nil {
				select {
				case <-poller.stopCh:
					return
				case <-time.After(time.Second):
				}
				continue
			}
			if len(msgs) == 0 {
				continue
			}
			// Deliver the batch as an SQS event record set.
			records := make([]map[string]any, 0, len(msgs))
			for _, msg := range msgs {
				records = append(records, map[string]any{
					"messageId":      msg.MessageID,
					"receiptHandle":  msg.ReceiptHandle,
					"body":           msg.Body,
					"eventSource":    "aws:sqs",
					"eventSourceARN": m.EventSourceArn,
					"awsRegion":      "us-east-1",
				})
			}
			payload, _ := json.Marshal(map[string]any{"Records": records})
			f, err := s.store.GetFunction(fnName)
			if err != nil {
				continue
			}
			res, err := s.runInvoke(context.Background(), f, payload)
			if err == nil && res.FunctionErr == "" {
				// Success: delete the batch (delete-on-success semantics).
				for _, msg := range msgs {
					_ = peercall.SQSDelete(s.peers, queue, msg.ReceiptHandle)
				}
			}
			// On failure, leave the messages for the visibility-timeout retry.
		}
	}()
}

func (s *Server) stopPoller(uuid string) {
	s.mu.Lock()
	if p := s.mappings[uuid]; p != nil {
		p.stop()
		delete(s.mappings, uuid)
	}
	s.mu.Unlock()
}

func newUUID() string {
	var b [16]byte
	_, _ = readRand(b[:])
	return hexUUID(b)
}

func hexUUID(b [16]byte) string {
	const hexdigit = "0123456789abcdef"
	var out [36]byte
	j := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hexdigit[b[i]>>4]
		out[j+1] = hexdigit[b[i]&0xf]
		j += 2
	}
	return string(out[:])
}
