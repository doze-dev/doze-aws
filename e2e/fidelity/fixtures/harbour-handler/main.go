// Command bootstrap is the fidelity harness's stand-in for the two Lambda
// functions of the made-up shop "harbour": the order validator that a queue's
// event source mapping calls, and the price calculator an app invokes directly.
// It speaks the Lambda Runtime API like AWS's provided.al2 bootstrap does, and
// does what the functions in the design boards do, for real:
//
//   - An SQS batch (a Records array) is the validator's. Each record is an
//     order; the handler prints the three lines the Wire-Inspect board shows and
//     writes the order to the harbour-orders table through the endpoint the
//     runtime injected (AWS_ENDPOINT_URL), so that call arrives on the wire as
//     its own call, the way a function's SDK calls do.
//   - {"price": …} with no "unit_price" is the price calculator's, and it
//     raises the KeyError the Main board shows, through the Runtime API's error
//     endpoint, so the invoke is a 200 with X-Amz-Function-Error.
//
// Nothing here is for the console to read: it is what the functions, run
// locally, print and raise.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	if api == "" {
		fmt.Fprintln(os.Stderr, "bootstrap: AWS_LAMBDA_RUNTIME_API not set")
		os.Exit(1)
	}
	base := "http://" + api + "/2018-06-01/runtime/invocation/"
	client := &http.Client{}
	for {
		id, event, err := next(client, base)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var in struct {
			Records []struct {
				Body string `json:"body"`
			} `json:"Records"`
			Price *json.RawMessage `json:"price"`
			Unit  *json.RawMessage `json:"unit_price"`
		}
		_ = json.Unmarshal(event, &in)
		switch {
		case len(in.Records) > 0:
			for _, r := range in.Records {
				validate(r.Body)
			}
			respond(client, base, id, []byte(`{"ok":true}`))
		case in.Price != nil && in.Unit == nil:
			// price["unit_price"] on a dict with no such key.
			fail(client, base, id, "KeyError", "'unit_price'")
		default:
			respond(client, base, id, event)
		}
	}
}

// validate is the order validator: three lines, and the order in the table.
func validate(body string) {
	var o struct {
		OrderID string `json:"orderId"`
		Lines   int    `json:"lines"`
	}
	if json.Unmarshal([]byte(body), &o) != nil || o.OrderID == "" {
		fmt.Printf("skipping a message that is not an order: %s\n", body)
		return
	}
	fmt.Printf("validating %s\n", o.OrderID)
	fmt.Printf("%d lines, all in stock\n", o.Lines)
	if put(o.OrderID, o.Lines) {
		fmt.Println("wrote harbour-orders")
	}
}

// put writes the order to the harbour-orders table: a DynamoDB PutItem against
// the endpoint doze-aws gave this process.
func put(orderID string, lines int) bool {
	ep := os.Getenv("AWS_ENDPOINT_URL")
	if ep == "" {
		return false
	}
	item, _ := json.Marshal(map[string]any{
		"TableName": "harbour-orders",
		"Item": map[string]any{
			"orderId": map[string]string{"S": orderID},
			"lines":   map[string]string{"N": fmt.Sprint(lines)},
		},
	})
	req, err := http.NewRequest(http.MethodPost, ep, bytes.NewReader(item))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

func next(client *http.Client, base string) (id string, event json.RawMessage, err error) {
	resp, err := client.Get(base + "next")
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	id = resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	if id == "" {
		return "", nil, fmt.Errorf("bootstrap: response missing Lambda-Runtime-Aws-Request-Id")
	}
	return id, json.RawMessage(body), nil
}

func respond(client *http.Client, base, id string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, base+id+"/response", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

func fail(client *http.Client, base, id, typ, why string) {
	body, _ := json.Marshal(map[string]string{"errorMessage": why, "errorType": typ})
	req, err := http.NewRequest(http.MethodPost, base+id+"/error", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Lambda-Runtime-Function-Error-Type", "Unhandled")
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}
