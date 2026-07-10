package dynamodb

// Item operation handlers: CRUD, Query/Scan, batches, transactions.

import (
	"encoding/json"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/expr"
	"github.com/doze-dev/doze-aws/internal/ddb/item"
	"github.com/doze-dev/doze-aws/internal/ddb/store"
)

// exprCommon carries the expression-related request members.
type exprCommon struct {
	ConditionExpression                 string                     `json:"ConditionExpression"`
	FilterExpression                    string                     `json:"FilterExpression"`
	KeyConditionExpression              string                     `json:"KeyConditionExpression"`
	UpdateExpression                    string                     `json:"UpdateExpression"`
	ProjectionExpression                string                     `json:"ProjectionExpression"`
	ExpressionAttributeNames            map[string]string          `json:"ExpressionAttributeNames"`
	ExpressionAttributeValues           map[string]json.RawMessage `json:"ExpressionAttributeValues"`
	ReturnValues                        string                     `json:"ReturnValues"`
	ReturnValuesOnConditionCheckFailure string                     `json:"ReturnValuesOnConditionCheckFailure"`
}

// env builds the expression environment from the request members.
func (c *exprCommon) env() (*expr.Env, *awshttp.APIError) {
	vals := map[string]item.Value{}
	for k, raw := range c.ExpressionAttributeValues {
		v, aerr := item.FromJSON(raw)
		if aerr != nil {
			return nil, aerr
		}
		vals[k] = v
	}
	return expr.NewEnv(c.ExpressionAttributeNames, vals), nil
}

// cond parses the ConditionExpression (nil when absent).
func (c *exprCommon) cond(env *expr.Env) (*store.Cond, *awshttp.APIError) {
	if c.ConditionExpression == "" {
		return nil, nil
	}
	parsed, aerr := expr.ParseCondition(c.ConditionExpression, env)
	if aerr != nil {
		return nil, aerr
	}
	return &store.Cond{
		Expr:      parsed,
		Env:       env,
		ReturnOld: c.ReturnValuesOnConditionCheckFailure == "ALL_OLD",
	}, nil
}

func itemsOrEmpty(it item.Item) any {
	if it == nil {
		return nil
	}
	return json.RawMessage(item.ItemJSON(it))
}

func (s *Server) putItem(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string          `json:"TableName"`
		Item      json.RawMessage `json:"Item"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	env, aerr := req.env()
	if aerr != nil {
		return nil, aerr
	}
	cond, aerr := req.cond(env)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, aerr
	}
	old, err := s.store.PutItem(req.TableName, req.Item, cond)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		out["Attributes"] = itemsOrEmpty(old)
	}
	return out, nil
}

func (s *Server) getItem(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string          `json:"TableName"`
		Key       json.RawMessage `json:"Key"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	it, err := s.store.GetItem(req.TableName, req.Key)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{}
	if it != nil {
		if req.ProjectionExpression != "" {
			env, aerr := req.env()
			if aerr != nil {
				return nil, aerr
			}
			proj, aerr := expr.ParseProjection(req.ProjectionExpression, env)
			if aerr != nil {
				return nil, aerr
			}
			if aerr := env.CheckAllUsed(); aerr != nil {
				return nil, aerr
			}
			it = proj.Apply(it)
		}
		out["Item"] = itemsOrEmpty(it)
	}
	return out, nil
}

func (s *Server) deleteItem(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string          `json:"TableName"`
		Key       json.RawMessage `json:"Key"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	env, aerr := req.env()
	if aerr != nil {
		return nil, aerr
	}
	cond, aerr := req.cond(env)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, aerr
	}
	old, err := s.store.DeleteItem(req.TableName, req.Key, cond, req.ReturnValues)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{}
	if req.ReturnValues == "ALL_OLD" && old != nil {
		out["Attributes"] = itemsOrEmpty(old)
	}
	return out, nil
}

func (s *Server) updateItem(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName string          `json:"TableName"`
		Key       json.RawMessage `json:"Key"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	env, aerr := req.env()
	if aerr != nil {
		return nil, aerr
	}
	var upd *expr.Update
	if req.UpdateExpression != "" {
		upd, aerr = expr.ParseUpdate(req.UpdateExpression, env)
		if aerr != nil {
			return nil, aerr
		}
	}
	cond, aerr := req.cond(env)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, aerr
	}
	old, new_, err := s.store.UpdateItem(req.TableName, req.Key, upd, cond)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	out := map[string]any{}
	switch req.ReturnValues {
	case "ALL_OLD":
		if old != nil {
			out["Attributes"] = itemsOrEmpty(old)
		}
	case "ALL_NEW":
		out["Attributes"] = itemsOrEmpty(new_)
	case "UPDATED_OLD":
		if old != nil {
			out["Attributes"] = itemsOrEmpty(diffAttrs(old, new_, old))
		}
	case "UPDATED_NEW":
		out["Attributes"] = itemsOrEmpty(diffAttrs(old, new_, new_))
	}
	return out, nil
}

// diffAttrs returns from's values for top-level attributes that changed
// between old and new — the UPDATED_OLD/UPDATED_NEW views.
func diffAttrs(old, new_, from item.Item) item.Item {
	changed := item.Item{}
	names := map[string]bool{}
	for k := range old {
		names[k] = true
	}
	for k := range new_ {
		names[k] = true
	}
	for k := range names {
		ov, oOK := old[k]
		nv, nOK := new_[k]
		if oOK != nOK || (oOK && !item.Equal(ov, nv)) {
			if v, ok := from[k]; ok {
				changed[k] = v
			}
		}
	}
	return changed
}

func (s *Server) query(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName         string          `json:"TableName"`
		IndexName         string          `json:"IndexName"`
		Limit             int             `json:"Limit"`
		ScanIndexForward  *bool           `json:"ScanIndexForward"`
		ExclusiveStartKey json.RawMessage `json:"ExclusiveStartKey"`
		Select            string          `json:"Select"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	if req.KeyConditionExpression == "" {
		return nil, awshttp.Errf(400, "ValidationException", "KeyConditionExpression is required")
	}
	env, aerr := req.env()
	if aerr != nil {
		return nil, aerr
	}
	kc, aerr := expr.ParseKeyCondition(req.KeyConditionExpression, env)
	if aerr != nil {
		return nil, aerr
	}
	var filter *expr.Cond
	if req.FilterExpression != "" {
		filter, aerr = expr.ParseCondition(req.FilterExpression, env)
		if aerr != nil {
			return nil, aerr
		}
	}
	var proj *expr.Projection
	if req.ProjectionExpression != "" {
		proj, aerr = expr.ParseProjection(req.ProjectionExpression, env)
		if aerr != nil {
			return nil, aerr
		}
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, aerr
	}
	forward := true
	if req.ScanIndexForward != nil {
		forward = *req.ScanIndexForward
	}
	res, err := s.store.Query(store.QueryInput{
		Table: req.TableName, Index: req.IndexName,
		KeyCond: kc, Filter: filter, Forward: forward,
		Limit: req.Limit, StartKey: req.ExclusiveStartKey,
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return renderPage(res, proj, req.Select), nil
}

func (s *Server) scan(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TableName         string          `json:"TableName"`
		IndexName         string          `json:"IndexName"`
		Limit             int             `json:"Limit"`
		ExclusiveStartKey json.RawMessage `json:"ExclusiveStartKey"`
		Segment           int             `json:"Segment"`
		TotalSegments     int             `json:"TotalSegments"`
		Select            string          `json:"Select"`
		exprCommon
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	env, aerr := req.env()
	if aerr != nil {
		return nil, aerr
	}
	var filter *expr.Cond
	if req.FilterExpression != "" {
		filter, aerr = expr.ParseCondition(req.FilterExpression, env)
		if aerr != nil {
			return nil, aerr
		}
	}
	var proj *expr.Projection
	if req.ProjectionExpression != "" {
		proj, aerr = expr.ParseProjection(req.ProjectionExpression, env)
		if aerr != nil {
			return nil, aerr
		}
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, aerr
	}
	res, err := s.store.Scan(store.ScanInput{
		Table: req.TableName, Index: req.IndexName, Filter: filter,
		Limit: req.Limit, StartKey: req.ExclusiveStartKey,
		Segment: req.Segment, TotalSegments: req.TotalSegments,
	})
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	return renderPage(res, proj, req.Select), nil
}

func renderPage(res *store.QueryOutput, proj *expr.Projection, sel string) map[string]any {
	out := map[string]any{
		"Count":        res.Count,
		"ScannedCount": res.ScannedCount,
	}
	if sel != "COUNT" {
		items := make([]json.RawMessage, 0, len(res.Items))
		for _, it := range res.Items {
			if proj != nil {
				it = proj.Apply(it)
			}
			items = append(items, item.ItemJSON(it))
		}
		out["Items"] = items
	}
	if res.LastEvaluatedKey != nil {
		out["LastEvaluatedKey"] = json.RawMessage(item.ItemJSON(res.LastEvaluatedKey))
	}
	return out
}

// ---- batches ----

func (s *Server) batchGet(body []byte) (any, *awshttp.APIError) {
	var req struct {
		RequestItems map[string]struct {
			Keys                     []json.RawMessage `json:"Keys"`
			ProjectionExpression     string            `json:"ProjectionExpression"`
			ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
		} `json:"RequestItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	total := 0
	for _, spec := range req.RequestItems {
		total += len(spec.Keys)
	}
	if total == 0 || total > 100 {
		return nil, awshttp.Errf(400, "ValidationException", "BatchGetItem accepts 1-100 keys, got %d", total)
	}
	responses := map[string][]json.RawMessage{}
	for table, spec := range req.RequestItems {
		var proj *expr.Projection
		if spec.ProjectionExpression != "" {
			env := expr.NewEnv(spec.ExpressionAttributeNames, nil)
			var aerr *awshttp.APIError
			proj, aerr = expr.ParseProjection(spec.ProjectionExpression, env)
			if aerr != nil {
				return nil, aerr
			}
		}
		out := []json.RawMessage{}
		for _, key := range spec.Keys {
			it, err := s.store.GetItem(table, key)
			if err != nil {
				return nil, awshttp.AsAPIError(err)
			}
			if it == nil {
				continue
			}
			if proj != nil {
				it = proj.Apply(it)
			}
			out = append(out, item.ItemJSON(it))
		}
		responses[table] = out
	}
	return map[string]any{"Responses": responses, "UnprocessedKeys": map[string]any{}}, nil
}

func (s *Server) batchWrite(body []byte) (any, *awshttp.APIError) {
	var req struct {
		RequestItems map[string][]struct {
			PutRequest *struct {
				Item json.RawMessage `json:"Item"`
			} `json:"PutRequest"`
			DeleteRequest *struct {
				Key json.RawMessage `json:"Key"`
			} `json:"DeleteRequest"`
		} `json:"RequestItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	total := 0
	for _, ops := range req.RequestItems {
		total += len(ops)
	}
	if total == 0 || total > 25 {
		return nil, awshttp.Errf(400, "ValidationException", "BatchWriteItem accepts 1-25 requests, got %d", total)
	}
	for table, ops := range req.RequestItems {
		for _, op := range ops {
			switch {
			case op.PutRequest != nil:
				if _, err := s.store.PutItem(table, op.PutRequest.Item, nil); err != nil {
					return nil, awshttp.AsAPIError(err)
				}
			case op.DeleteRequest != nil:
				if _, err := s.store.DeleteItem(table, op.DeleteRequest.Key, nil, "NONE"); err != nil {
					return nil, awshttp.AsAPIError(err)
				}
			default:
				return nil, awshttp.Errf(400, "ValidationException", "each request must be a PutRequest or DeleteRequest")
			}
		}
	}
	return map[string]any{"UnprocessedItems": map[string]any{}}, nil
}

// ---- transactions ----

type txCommonWire struct {
	TableName                           string                     `json:"TableName"`
	ConditionExpression                 string                     `json:"ConditionExpression"`
	ExpressionAttributeNames            map[string]string          `json:"ExpressionAttributeNames"`
	ExpressionAttributeValues           map[string]json.RawMessage `json:"ExpressionAttributeValues"`
	ReturnValuesOnConditionCheckFailure string                     `json:"ReturnValuesOnConditionCheckFailure"`
}

func (w *txCommonWire) build(updateSrc string) (env *expr.Env, cond *store.Cond, upd *expr.Update, aerr *awshttp.APIError) {
	vals := map[string]item.Value{}
	for k, raw := range w.ExpressionAttributeValues {
		v, verr := item.FromJSON(raw)
		if verr != nil {
			return nil, nil, nil, verr
		}
		vals[k] = v
	}
	env = expr.NewEnv(w.ExpressionAttributeNames, vals)
	if updateSrc != "" {
		upd, aerr = expr.ParseUpdate(updateSrc, env)
		if aerr != nil {
			return nil, nil, nil, aerr
		}
	}
	if w.ConditionExpression != "" {
		parsed, aerr := expr.ParseCondition(w.ConditionExpression, env)
		if aerr != nil {
			return nil, nil, nil, aerr
		}
		cond = &store.Cond{Expr: parsed, Env: env, ReturnOld: w.ReturnValuesOnConditionCheckFailure == "ALL_OLD"}
	}
	if aerr := env.CheckAllUsed(); aerr != nil {
		return nil, nil, nil, aerr
	}
	return env, cond, upd, nil
}

func (s *Server) transactWrite(body []byte) (any, *awshttp.APIError) {
	var req struct {
		ClientRequestToken string `json:"ClientRequestToken"`
		TransactItems      []struct {
			Put *struct {
				txCommonWire
				Item json.RawMessage `json:"Item"`
			} `json:"Put"`
			Update *struct {
				txCommonWire
				Key              json.RawMessage `json:"Key"`
				UpdateExpression string          `json:"UpdateExpression"`
			} `json:"Update"`
			Delete *struct {
				txCommonWire
				Key json.RawMessage `json:"Key"`
			} `json:"Delete"`
			ConditionCheck *struct {
				txCommonWire
				Key json.RawMessage `json:"Key"`
			} `json:"ConditionCheck"`
		} `json:"TransactItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	var ops []store.TxWriteOp
	for _, ti := range req.TransactItems {
		switch {
		case ti.Put != nil:
			_, cond, _, aerr := ti.Put.build("")
			if aerr != nil {
				return nil, aerr
			}
			ops = append(ops, store.TxWriteOp{
				Table: ti.Put.TableName, Put: ti.Put.Item, Cond: cond,
				ReturnOld: ti.Put.ReturnValuesOnConditionCheckFailure == "ALL_OLD",
			})
		case ti.Update != nil:
			_, cond, upd, aerr := ti.Update.build(ti.Update.UpdateExpression)
			if aerr != nil {
				return nil, aerr
			}
			if upd == nil {
				return nil, awshttp.Errf(400, "ValidationException", "transact Update requires an UpdateExpression")
			}
			ops = append(ops, store.TxWriteOp{
				Table: ti.Update.TableName, UpdateKey: ti.Update.Key, Update: upd, Cond: cond,
				ReturnOld: ti.Update.ReturnValuesOnConditionCheckFailure == "ALL_OLD",
			})
		case ti.Delete != nil:
			_, cond, _, aerr := ti.Delete.build("")
			if aerr != nil {
				return nil, aerr
			}
			ops = append(ops, store.TxWriteOp{
				Table: ti.Delete.TableName, DeleteKey: ti.Delete.Key, Cond: cond,
				ReturnOld: ti.Delete.ReturnValuesOnConditionCheckFailure == "ALL_OLD",
			})
		case ti.ConditionCheck != nil:
			_, cond, _, aerr := ti.ConditionCheck.build("")
			if aerr != nil {
				return nil, aerr
			}
			if cond == nil {
				return nil, awshttp.Errf(400, "ValidationException", "ConditionCheck requires a ConditionExpression")
			}
			ops = append(ops, store.TxWriteOp{
				Table: ti.ConditionCheck.TableName, CheckKey: ti.ConditionCheck.Key, Cond: cond,
				ReturnOld: ti.ConditionCheck.ReturnValuesOnConditionCheckFailure == "ALL_OLD",
			})
		default:
			return nil, awshttp.Errf(400, "ValidationException", "each TransactItem must have exactly one operation")
		}
	}
	err := s.store.TransactWrite(ops, req.ClientRequestToken, store.RequestHash(body))
	if err != nil {
		if tc, ok := err.(*store.ErrTransactionCanceled); ok {
			return nil, transactionCanceled(tc)
		}
		return nil, awshttp.AsAPIError(err)
	}
	return map[string]any{}, nil
}

// transactionCanceled renders the TransactionCanceledException with reasons.
func transactionCanceled(tc *store.ErrTransactionCanceled) *awshttp.APIError {
	e := awshttp.Errf(400, "TransactionCanceledException",
		"Transaction cancelled, please refer cancellation reasons for specific reasons")
	raw, _ := json.Marshal(tc.Reasons)
	e.Extra = map[string]json.RawMessage{"CancellationReasons": raw}
	return e
}

func (s *Server) transactGet(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TransactItems []struct {
			Get struct {
				TableName                string            `json:"TableName"`
				Key                      json.RawMessage   `json:"Key"`
				ProjectionExpression     string            `json:"ProjectionExpression"`
				ExpressionAttributeNames map[string]string `json:"ExpressionAttributeNames"`
			} `json:"Get"`
		} `json:"TransactItems"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	var gets []struct {
		Table string
		Key   json.RawMessage
	}
	for _, ti := range req.TransactItems {
		gets = append(gets, struct {
			Table string
			Key   json.RawMessage
		}{ti.Get.TableName, ti.Get.Key})
	}
	items, err := s.store.TransactGet(gets)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	responses := make([]map[string]any, len(items))
	for i, it := range items {
		entry := map[string]any{}
		if it != nil {
			view := it
			if pe := req.TransactItems[i].Get.ProjectionExpression; pe != "" {
				env := expr.NewEnv(req.TransactItems[i].Get.ExpressionAttributeNames, nil)
				proj, aerr := expr.ParseProjection(pe, env)
				if aerr != nil {
					return nil, aerr
				}
				view = proj.Apply(it)
			}
			entry["Item"] = json.RawMessage(item.ItemJSON(view))
		}
		responses[i] = entry
	}
	return map[string]any{"Responses": responses}, nil
}
