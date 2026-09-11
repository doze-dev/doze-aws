// price-calculator — Go on provided.al2023.
//
// Second step of the order-fulfilment workflow: turn a validated basket into
// the money. Delivery banding, the multibuy rule, VAT at the line level and
// the rounding that has to happen exactly once, at the end.
//
// Money is the reason this one is Go: integer pence throughout, no float
// arithmetic anywhere near a total a customer will be charged.
//
// It speaks the Lambda Runtime API directly rather than importing
// aws-lambda-go — the same wire protocol a provided.al2023 bootstrap uses, and
// stdlib only, so there is nothing to fetch before it builds.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	vatRateBasisPoints = 2000 // 20%
	freeDeliveryOver   = 4000 // pence
	standardDelivery   = 395
	expressDelivery    = 695
)

type line struct {
	SKU          string  `json:"sku"`
	Name         string  `json:"name"`
	Quantity     int     `json:"quantity"`
	UnitPriceGbp float64 `json:"unitPriceGbp"`
	VATable      bool    `json:"vatable"`
	Multibuy     string  `json:"multibuy,omitempty"` // "3for2"
}

type order struct {
	OrderID   string `json:"orderId"`
	Lines     []line `json:"lines"`
	Delivery  string `json:"deliverySpeed"`
	Untouched map[string]json.RawMessage
}

type pricedLine struct {
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	Quantity   int    `json:"quantity"`
	UnitPence  int    `json:"unitPence"`
	GrossPence int    `json:"grossPence"`
	SavedPence int    `json:"savedPence,omitempty"`
	VATPence   int    `json:"vatPence"`
}

// pence converts a decimal price to integer pence without going near a float
// total. The input is a JSON number because that is what the basket carries;
// this is the one place it is allowed to be one.
func pence(gbp float64) int { return int(gbp*100 + 0.5) }

func priceOrder(raw map[string]any) map[string]any {
	b, _ := json.Marshal(raw)
	var o order
	_ = json.Unmarshal(b, &o)

	var priced []pricedLine
	subtotal, vat, saved := 0, 0, 0
	for _, l := range o.Lines {
		unit := pence(l.UnitPriceGbp)
		charged := l.Quantity
		// 3-for-2: every third item of that line is free.
		if strings.EqualFold(l.Multibuy, "3for2") {
			charged = l.Quantity - l.Quantity/3
		}
		gross := unit * charged
		lineSaved := unit*l.Quantity - gross
		lineVAT := 0
		if l.VATable {
			// VAT on the discounted line, rounded half-up, per line — which is
			// what a receipt has to show, and why it is not computed on the
			// order total.
			lineVAT = (gross*vatRateBasisPoints + 5000) / 10000
		}
		subtotal += gross
		vat += lineVAT
		saved += lineSaved
		priced = append(priced, pricedLine{
			SKU: l.SKU, Name: l.Name, Quantity: l.Quantity,
			UnitPence: unit, GrossPence: gross, SavedPence: lineSaved, VATPence: lineVAT,
		})
	}

	delivery := standardDelivery
	switch {
	case strings.EqualFold(o.Delivery, "express"):
		delivery = expressDelivery
	case subtotal >= freeDeliveryOver:
		delivery = 0
	}
	total := subtotal + vat + delivery

	log.Printf("priced %s: %d lines, subtotal %dp, vat %dp, delivery %dp, total %dp",
		o.OrderID, len(priced), subtotal, vat, delivery, total)

	raw["pricing"] = map[string]any{
		"lines":         priced,
		"subtotalPence": subtotal,
		"vatPence":      vat,
		"deliveryPence": delivery,
		"savedPence":    saved,
		"totalPence":    total,
		"totalGbp":      fmt.Sprintf("%d.%02d", total/100, total%100),
		"currency":      "GBP",
		"pricedBy":      "price-calculator (go, integer pence)",
	}
	return raw
}

func main() {
	api := os.Getenv("AWS_LAMBDA_RUNTIME_API")
	if api == "" {
		log.Fatal("AWS_LAMBDA_RUNTIME_API is not set; this binary only runs as a Lambda")
	}
	base := "http://" + api + "/2018-06-01/runtime/invocation/"
	for {
		resp, err := http.Get(base + "next")
		if err != nil {
			log.Printf("next: %v", err)
			continue
		}
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var in map[string]any
		if err := json.Unmarshal(body, &in); err != nil {
			post(base+id+"/error", map[string]any{
				"errorType": "InvalidEvent", "errorMessage": err.Error()})
			continue
		}
		out, _ := json.Marshal(priceOrder(in))
		http.Post(base+id+"/response", "application/json", bytes.NewReader(out))
	}
}

func post(url string, v any) {
	b, _ := json.Marshal(v)
	http.Post(url, "application/json", bytes.NewReader(b))
}
