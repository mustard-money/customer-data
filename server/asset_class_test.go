package server

import "testing"

func TestItemAssetClassRoundTrip(t *testing.T) {
	srv := newTestServer(t)
	r := do(t, srv, "PUT", "/api/v1/items/muni",
		`{"category":"investments","name":"Muni fund","amount":50000,"account":"taxable","assetClass":"fixedIncome","incomeRate":0.035,"taxExempt":{"federal":true,"amt":true},"createdAt":"2026-10-12T06:20:39.033Z"}`)
	expectStatus(t, r, 201)
	got := do(t, srv, "GET", "/api/v1/items/muni", "").json(t)
	exempt, _ := got["taxExempt"].(map[string]any)
	if got["assetClass"] != "fixedIncome" || got["incomeRate"] != 0.035 || exempt["federal"] != true || exempt["amt"] != true || exempt["state"] != nil {
		t.Errorf("stored %v", got)
	}
}
