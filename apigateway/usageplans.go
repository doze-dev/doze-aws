package apigateway

// Usage plans: which API stages a set of keys may call. The plan-key views
// (CreateUsagePlanKey, GetUsagePlanKeys, …) are derived from the plan's key
// list; there is no separate record.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"github.com/doze-dev/doze-aws/internal/awshttp"
)

var usagePlanBucket = []byte("usageplans")

// UsagePlan is one plan.
type UsagePlan struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Stages      []PlanStage       `json:"stages,omitempty"`
	Throttle    *Throttle         `json:"throttle,omitempty"`
	Quota       *Quota            `json:"quota,omitempty"`
	KeyIDs      []string          `json:"key_ids,omitempty"`
	Created     int64             `json:"created"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// PlanStage is one API stage a plan covers.
type PlanStage struct {
	APIID string `json:"api_id"`
	Stage string `json:"stage"`
}

// Throttle and Quota are stored and reported; nothing is metered locally.
type Throttle struct {
	Rate  float64 `json:"rate"`
	Burst int     `json:"burst"`
}

type Quota struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Period string `json:"period"` // DAY | WEEK | MONTH
}

func (p *UsagePlan) hasKey(id string) bool {
	for _, k := range p.KeyIDs {
		if k == id {
			return true
		}
	}
	return false
}

func (p *UsagePlan) removeKey(id string) {
	kept := p.KeyIDs[:0]
	for _, k := range p.KeyIDs {
		if k != id {
			kept = append(kept, k)
		}
	}
	p.KeyIDs = kept
}

func (p *UsagePlan) covers(apiID, stage string) bool {
	for _, st := range p.Stages {
		if st.APIID == apiID && st.Stage == stage {
			return true
		}
	}
	return false
}

func viewUsagePlan(p *UsagePlan) map[string]any {
	stages := []any{}
	for _, st := range p.Stages {
		stages = append(stages, map[string]any{"apiId": st.APIID, "stage": st.Stage})
	}
	v := map[string]any{"id": p.ID, "name": p.Name, "apiStages": stages, "tags": orEmptyMap(p.Tags)}
	putIfStr(v, "description", p.Description)
	if p.Throttle != nil {
		v["throttle"] = map[string]any{"rateLimit": p.Throttle.Rate, "burstLimit": p.Throttle.Burst}
	}
	if p.Quota != nil {
		v["quota"] = map[string]any{"limit": p.Quota.Limit, "offset": p.Quota.Offset, "period": p.Quota.Period}
	}
	return v
}

func viewUsagePlanKey(k *APIKey) map[string]any {
	return map[string]any{"id": k.ID, "type": "API_KEY", "value": k.Value, "name": k.Name}
}

// ---- store ----

func (s *Store) PutUsagePlan(p *UsagePlan) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(usagePlanBucket)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(p)
		return b.Put([]byte(p.ID), raw)
	})
}

func (s *Store) GetUsagePlan(id string) (*UsagePlan, error) {
	var out *UsagePlan
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usagePlanBucket)
		if b == nil || b.Get([]byte(id)) == nil {
			return errNotFound("Invalid Usage Plan ID specified")
		}
		var p UsagePlan
		if err := json.Unmarshal(b.Get([]byte(id)), &p); err != nil {
			return err
		}
		out = &p
		return nil
	})
	return out, err
}

func (s *Store) DeleteUsagePlan(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usagePlanBucket)
		if b == nil || b.Get([]byte(id)) == nil {
			return errNotFound("Invalid Usage Plan ID specified")
		}
		return b.Delete([]byte(id))
	})
}

// ListUsagePlans answers every plan, sorted by name.
func (s *Store) ListUsagePlans() ([]*UsagePlan, error) {
	var out []*UsagePlan
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usagePlanBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var p UsagePlan
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			out = append(out, &p)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

// ---- handlers ----

// routeUsagePlans serves /usageplans[/{id}[/keys[/{keyId}]]].
func (s *Server) routeUsagePlans(w http.ResponseWriter, r *http.Request, segs []string) *awshttp.APIError {
	switch {
	case len(segs) == 1 && r.Method == http.MethodPost:
		return s.createUsagePlan(w, r)
	case len(segs) == 1 && r.Method == http.MethodGet:
		return s.listUsagePlans(w, r)
	case len(segs) == 2:
		return s.routeUsagePlan(w, r, segs[1])
	case len(segs) >= 3 && segs[2] == "usage":
		return awshttp.Errf(501, "NotImplemented", "doze-aws does not meter requests, so there is no usage to report or reset")
	case len(segs) >= 3 && segs[2] == "keys":
		return s.routeUsagePlanKeys(w, r, segs[1], segs[3:])
	}
	return errNotFound("unknown usage plan path")
}

func (s *Server) routeUsagePlan(w http.ResponseWriter, r *http.Request, id string) *awshttp.APIError {
	switch r.Method {
	case http.MethodGet:
		p, err := s.store.GetUsagePlan(id)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewUsagePlan(p))
		return nil
	case http.MethodPatch:
		return s.patchUsagePlan(w, r, id)
	case http.MethodDelete:
		if err := s.store.DeleteUsagePlan(id); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(202)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a usage plan")
}

func (s *Server) createUsagePlan(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	var req struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Tags        map[string]string `json:"tags"`
		APIStages   []struct {
			APIID string `json:"apiId"`
			Stage string `json:"stage"`
		} `json:"apiStages"`
		Throttle *struct {
			RateLimit  float64 `json:"rateLimit"`
			BurstLimit int     `json:"burstLimit"`
		} `json:"throttle"`
		Quota *struct {
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
			Period string `json:"period"`
		} `json:"quota"`
	}
	if aerr := decode(r, &req); aerr != nil {
		return aerr
	}
	p := &UsagePlan{ID: s.store.newID(), Name: req.Name, Description: req.Description, Tags: req.Tags, Created: s.now().Unix()}
	for _, st := range req.APIStages {
		if _, err := s.store.Get(st.APIID); err != nil {
			return errBadRequest("Invalid API identifier specified %s", st.APIID)
		}
		p.Stages = append(p.Stages, PlanStage{APIID: st.APIID, Stage: st.Stage})
	}
	if req.Throttle != nil {
		p.Throttle = &Throttle{Rate: req.Throttle.RateLimit, Burst: req.Throttle.BurstLimit}
	}
	if req.Quota != nil {
		p.Quota = &Quota{Limit: req.Quota.Limit, Offset: req.Quota.Offset, Period: req.Quota.Period}
	}
	if err := s.store.PutUsagePlan(p); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 201, viewUsagePlan(p))
	return nil
}

func (s *Server) listUsagePlans(w http.ResponseWriter, r *http.Request) *awshttp.APIError {
	plans, err := s.store.ListUsagePlans()
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	keyID := r.URL.Query().Get("keyId")
	items := []any{}
	for _, p := range plans {
		if keyID != "" && !p.hasKey(keyID) {
			continue
		}
		items = append(items, viewUsagePlan(p))
	}
	writeJSON(w, 200, map[string]any{"item": items})
	return nil
}

func (s *Server) patchUsagePlan(w http.ResponseWriter, r *http.Request, id string) *awshttp.APIError {
	ops, aerr := decodePatch(r)
	if aerr != nil {
		return aerr
	}
	p, err := s.store.GetUsagePlan(id)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	for _, op := range ops {
		switch {
		case op.Path == "/name" && op.Op != "remove":
			p.Name = op.Value
		case op.Path == "/description" && op.Op != "remove":
			p.Description = op.Value
		case op.Path == "/apiStages":
			// "apiId:stage", added or removed.
			apiID, stage, _ := strings.Cut(op.Value, ":")
			kept := p.Stages[:0]
			for _, st := range p.Stages {
				if !(st.APIID == apiID && st.Stage == stage) {
					kept = append(kept, st)
				}
			}
			p.Stages = kept
			if op.Op == "add" || op.Op == "replace" {
				if _, err := s.store.Get(apiID); err != nil {
					return errBadRequest("Invalid API identifier specified %s", apiID)
				}
				p.Stages = append(p.Stages, PlanStage{APIID: apiID, Stage: stage})
			}
		case strings.HasPrefix(op.Path, "/throttle"):
			if op.Op == "remove" {
				p.Throttle = nil
				continue
			}
			if p.Throttle == nil {
				p.Throttle = &Throttle{}
			}
			switch op.Path {
			case "/throttle/rateLimit":
				p.Throttle.Rate, _ = strconv.ParseFloat(op.Value, 64)
			case "/throttle/burstLimit":
				p.Throttle.Burst = atoiOr(op.Value, 0)
			}
		case strings.HasPrefix(op.Path, "/quota"):
			if op.Op == "remove" {
				p.Quota = nil
				continue
			}
			if p.Quota == nil {
				p.Quota = &Quota{}
			}
			switch op.Path {
			case "/quota/limit":
				p.Quota.Limit = atoiOr(op.Value, 0)
			case "/quota/offset":
				p.Quota.Offset = atoiOr(op.Value, 0)
			case "/quota/period":
				p.Quota.Period = op.Value
			}
		default:
			return errBadRequest("Invalid patch path %s", op.Path)
		}
	}
	if err := s.store.PutUsagePlan(p); err != nil {
		return awshttp.AsAPIError(err)
	}
	writeJSON(w, 200, viewUsagePlan(p))
	return nil
}

// routeUsagePlanKeys serves /usageplans/{id}/keys[/{keyId}].
func (s *Server) routeUsagePlanKeys(w http.ResponseWriter, r *http.Request, planID string, rest []string) *awshttp.APIError {
	p, err := s.store.GetUsagePlan(planID)
	if err != nil {
		return awshttp.AsAPIError(err)
	}
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			var req struct {
				KeyID   string `json:"keyId"`
				KeyType string `json:"keyType"`
			}
			if aerr := decode(r, &req); aerr != nil {
				return aerr
			}
			if req.KeyType != "API_KEY" {
				return errBadRequest("Invalid key type %s: API_KEY is the only key type", req.KeyType)
			}
			k, err := s.store.GetAPIKey(req.KeyID)
			if err != nil {
				return awshttp.AsAPIError(err)
			}
			if p.hasKey(k.ID) {
				return errConflict("API Key already exists in the usage plan")
			}
			p.KeyIDs = append(p.KeyIDs, k.ID)
			if err := s.store.PutUsagePlan(p); err != nil {
				return awshttp.AsAPIError(err)
			}
			writeJSON(w, 201, viewUsagePlanKey(k))
			return nil
		case http.MethodGet:
			prefix := r.URL.Query().Get("name")
			items := []any{}
			for _, id := range p.KeyIDs {
				k, err := s.store.GetAPIKey(id)
				if err != nil || (prefix != "" && !strings.HasPrefix(k.Name, prefix)) {
					continue
				}
				items = append(items, viewUsagePlanKey(k))
			}
			writeJSON(w, 200, map[string]any{"item": items})
			return nil
		}
		return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on usage plan keys")
	}
	keyID := rest[0]
	if !p.hasKey(keyID) {
		return errNotFound("Invalid Usage Plan Key identifier specified")
	}
	switch r.Method {
	case http.MethodGet:
		k, err := s.store.GetAPIKey(keyID)
		if err != nil {
			return awshttp.AsAPIError(err)
		}
		writeJSON(w, 200, viewUsagePlanKey(k))
		return nil
	case http.MethodDelete:
		p.removeKey(keyID)
		if err := s.store.PutUsagePlan(p); err != nil {
			return awshttp.AsAPIError(err)
		}
		w.WriteHeader(202)
		return nil
	}
	return awshttp.Errf(405, "MethodNotAllowed", "unsupported method on a usage plan key")
}

// PlansCovering lists the plans that cover an API stage.
func (s *Store) PlansCovering(apiID, stage string) []*UsagePlan {
	plans, _ := s.ListUsagePlans()
	var out []*UsagePlan
	for _, p := range plans {
		if p.covers(apiID, stage) {
			out = append(out, p)
		}
	}
	return out
}
