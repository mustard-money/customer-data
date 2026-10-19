package server

import "testing"

func TestItemSelfEmployment(t *testing.T) {
	srv := newTestServer(t)
	body := func(selfEmployment string) string {
		return `{"category":"income","name":"Consulting","amount":2000,"frequency":"monthly","incomeType":"1099nec",` +
			selfEmployment + `"createdAt":"2026-10-11T06:20:39.033Z"}`
	}

	r := do(t, srv, "PUT", "/api/v1/items/consulting", body(`"selfEmployment":true,`))
	expectStatus(t, r, 201)
	if got := r.json(t)["selfEmployment"]; got != true {
		t.Errorf("selfEmployment = %v, want true", got)
	}
	if got := do(t, srv, "GET", "/api/v1/items/consulting", "").json(t)["selfEmployment"]; got != true {
		t.Errorf("stored selfEmployment = %v, want true", got)
	}

	// Saving it unchecked (false, or left out) clears it.
	r = do(t, srv, "PUT", "/api/v1/items/consulting", body(`"selfEmployment":false,`))
	expectStatus(t, r, 200)
	if _, ok := r.json(t)["selfEmployment"]; ok {
		t.Errorf("false should be omitted: %s", r.body)
	}

	expectError(t, do(t, srv, "PUT", "/api/v1/items/rent",
		`{"category":"expenses","name":"Rent","amount":2000,"frequency":"monthly","selfEmployment":true,"createdAt":"2026-10-11T06:20:39.033Z"}`),
		400, "selfEmployment must be omitted")
}
