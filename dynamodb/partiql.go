package dynamodb

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doze-dev/doze-aws/internal/awshttp"
	"github.com/doze-dev/doze-aws/internal/ddb/store"
)

// PartiQL support (ExecuteStatement / BatchExecuteStatement / ExecuteTransaction).
// A statement is parsed and translated into the equivalent classic operation,
// then run through the existing item handlers — so PartiQL inherits the same
// storage, key encoding, and conditional semantics. Supported shapes:
//
//	INSERT INTO "table" VALUE {'pk': ?, 'attr': 'literal', ...}
//	SELECT * FROM "table" WHERE "pk" = ? [AND "sk" = ?]     (full key -> GetItem, else Scan+filter)
//	UPDATE "table" SET "a" = ?, "b" = ? WHERE "pk" = ? [AND "sk" = ?]
//	DELETE FROM "table" WHERE "pk" = ? [AND "sk" = ?]
//
// Values are '?' positional parameters (bound from Parameters, which are
// AttributeValues), single-quoted strings, numbers, or true/false/null.

// executeStatement handles the ExecuteStatement action.
func (s *Server) executeStatement(body []byte) (any, *awshttp.APIError) {
	var req struct {
		Statement  string            `json:"Statement"`
		Parameters []json.RawMessage `json:"Parameters"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	return s.runPartiQL(req.Statement, req.Parameters)
}

// batchExecuteStatement handles BatchExecuteStatement (a list of statements).
func (s *Server) batchExecuteStatement(body []byte) (any, *awshttp.APIError) {
	var req struct {
		Statements []struct {
			Statement  string            `json:"Statement"`
			Parameters []json.RawMessage `json:"Parameters"`
		} `json:"Statements"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	responses := make([]map[string]any, 0, len(req.Statements))
	for _, st := range req.Statements {
		out, aerr := s.runPartiQL(st.Statement, st.Parameters)
		if aerr != nil {
			responses = append(responses, map[string]any{"Error": map[string]any{"Code": aerr.Code, "Message": aerr.Message}})
			continue
		}
		resp := map[string]any{}
		if m, ok := out.(map[string]any); ok {
			if items, ok := m["Items"].([]any); ok && len(items) > 0 {
				resp["Item"] = items[0]
			}
		}
		responses = append(responses, resp)
	}
	return map[string]any{"Responses": responses}, nil
}

// executeTransaction handles ExecuteTransaction — the write statements run as a
// classic TransactWriteItems by translating each and delegating.
func (s *Server) executeTransaction(body []byte) (any, *awshttp.APIError) {
	var req struct {
		TransactStatements []struct {
			Statement  string            `json:"Statement"`
			Parameters []json.RawMessage `json:"Parameters"`
		} `json:"TransactStatements"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, awshttp.Errf(400, "SerializationException", "%v", err)
	}
	// For local semantics, run each statement in order (best-effort atomicity is
	// bounded to per-item ops here; full cross-item rollback is a follow-on).
	responses := make([]map[string]any, 0, len(req.TransactStatements))
	for _, st := range req.TransactStatements {
		if _, aerr := s.runPartiQL(st.Statement, st.Parameters); aerr != nil {
			return nil, aerr
		}
		responses = append(responses, map[string]any{})
	}
	return map[string]any{"Responses": responses}, nil
}

// runPartiQL parses a single statement, binds parameters, translates it to a
// classic operation, and returns the ExecuteStatement-shaped result.
func (s *Server) runPartiQL(statement string, params []json.RawMessage) (any, *awshttp.APIError) {
	st, aerr := parsePartiQL(statement)
	if aerr != nil {
		return nil, aerr
	}
	binder := &paramBinder{params: params}
	switch st.kind {
	case stInsert:
		item, aerr := st.values.attributeMap(binder)
		if aerr != nil {
			return nil, aerr
		}
		reqBody, _ := json.Marshal(map[string]any{"TableName": st.table, "Item": item})
		if _, aerr := s.putItem(reqBody); aerr != nil {
			return nil, aerr
		}
		return map[string]any{"Items": []any{}}, nil

	case stDelete:
		key, aerr := st.where.attributeMap(binder)
		if aerr != nil {
			return nil, aerr
		}
		reqBody, _ := json.Marshal(map[string]any{"TableName": st.table, "Key": key})
		if _, aerr := s.deleteItem(reqBody); aerr != nil {
			return nil, aerr
		}
		return map[string]any{"Items": []any{}}, nil

	case stUpdate:
		key, aerr := st.where.attributeMap(binder)
		if aerr != nil {
			return nil, aerr
		}
		setNames, setVals, setExpr, aerr := st.set.updateExpr(binder)
		if aerr != nil {
			return nil, aerr
		}
		reqBody, _ := json.Marshal(map[string]any{
			"TableName":                 st.table,
			"Key":                       key,
			"UpdateExpression":          setExpr,
			"ExpressionAttributeNames":  setNames,
			"ExpressionAttributeValues": setVals,
		})
		if _, aerr := s.updateItem(reqBody); aerr != nil {
			return nil, aerr
		}
		return map[string]any{"Items": []any{}}, nil

	case stSelect:
		return s.partiqlSelect(st, binder)
	}
	return nil, awshttp.Errf(400, "ValidationException", "unsupported statement")
}

// partiqlSelect translates a SELECT: a full-primary-key equality WHERE becomes a
// GetItem; anything else becomes a Scan with a filter expression.
func (s *Server) partiqlSelect(st *partiqlStmt, binder *paramBinder) (any, *awshttp.APIError) {
	t, err := s.store.GetTable(st.table)
	if err != nil {
		return nil, awshttp.AsAPIError(err)
	}
	whereAttrs := st.where.attrNames()
	if coversKey(t, whereAttrs) {
		key, aerr := st.where.attributeMap(binder)
		if aerr != nil {
			return nil, aerr
		}
		reqBody, _ := json.Marshal(map[string]any{"TableName": st.table, "Key": key})
		out, aerr := s.getItem(reqBody)
		if aerr != nil {
			return nil, aerr
		}
		items := []any{}
		if m, ok := out.(map[string]any); ok {
			if item, ok := m["Item"]; ok && item != nil {
				items = append(items, item)
			}
		}
		return map[string]any{"Items": items}, nil
	}
	// Non-key WHERE: Scan with an equality filter over the conditions.
	names := map[string]string{}
	vals := map[string]json.RawMessage{}
	var clauses []string
	for i, c := range st.where {
		n := fmt.Sprintf("#p%d", i)
		v := fmt.Sprintf(":p%d", i)
		names[n] = c.attr
		av, aerr := binder.value(c.value)
		if aerr != nil {
			return nil, aerr
		}
		vals[v] = av
		clauses = append(clauses, n+" = "+v)
	}
	scanBody := map[string]any{"TableName": st.table}
	if len(clauses) > 0 {
		scanBody["FilterExpression"] = strings.Join(clauses, " AND ")
		scanBody["ExpressionAttributeNames"] = names
		scanBody["ExpressionAttributeValues"] = vals
	}
	reqBody, _ := json.Marshal(scanBody)
	return s.scan(reqBody)
}

// coversKey reports whether the WHERE attributes are exactly the table's primary
// key (hash, and range if present).
func coversKey(t *store.Table, attrs map[string]bool) bool {
	need := []string{t.Hash.Name}
	if t.Range != nil {
		need = append(need, t.Range.Name)
	}
	if len(attrs) != len(need) {
		return false
	}
	for _, k := range need {
		if !attrs[k] {
			return false
		}
	}
	return true
}
