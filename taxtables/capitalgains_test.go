package taxtables

import (
	"math"
	"strings"
	"testing"
	"testing/fstest"
)

func fptr(v float64) *float64 { return &v }

// A made-up federal schedule (no deduction) with round numbers, so the
// expected taxes below can be worked out by hand.
var testFederal = &Tax{
	Type: "brackets",
	Schedules: &ByStatus[[]Bracket]{Single: []Bracket{
		{From: 0, Rate: 0.1}, {From: 10_000, Rate: 0.2}, {From: 50_000, Rate: 0.4}, {From: 100_000, Rate: 0.6},
	}},
	Source: "https://example.gov",
}

func TestFederalTax(t *testing.T) {
	cases := []struct {
		name            string
		cg              *CapitalGains
		ordinary, gains float64
		total, onGains  float64
	}{
		// Ordinary 15,000 taxes 1,000 + 1,000. The gain fills 15,000–25,000:
		// 0% to 20,000, then 15%.
		{"dollar schedule", &CapitalGains{Method: "brackets", Schedules: &ByStatus[[]Bracket]{
			Single: []Bracket{{From: 0, Rate: 0}, {From: 20_000, Rate: 0.15}, {From: 60_000, Rate: 0.2}},
		}}, 15_000, 10_000, 2_750, 750},
		// Tiers: 5% until the 40% bracket starts (50,000), then 15%.
		// Ordinary 40,000 taxes 7,000; the gain fills 40,000–60,000.
		{"tiers tied to the ordinary brackets", &CapitalGains{Method: "brackets", Tiers: []CapitalGainsTier{
			{Rate: 0.05, From: fptr(0)}, {Rate: 0.15, FromOrdinaryRate: fptr(0.4)},
		}}, 40_000, 20_000, 9_000, 2_000},
		// 20% on 40,000–50,000, then 40% capped at 28%.
		{"maximum rate", &CapitalGains{Method: "maxRate", MaxRate: fptr(0.28)}, 40_000, 20_000, 11_800, 4_800},
		// Half the gain (10,000) is income at 20%.
		{"exclusion", &CapitalGains{Method: "exclusion", ExcludedShare: fptr(0.5)}, 40_000, 20_000, 9_000, 2_000},
		// In the 60% bracket half the gain costs 30%; the alternative tax
		// (25% on the first 50,000) is less.
		{"alternative tax", &CapitalGains{Method: "exclusion", ExcludedShare: fptr(0.5),
			AlternativeRate: fptr(0.25), AlternativeLimit: &ByStatus[float64]{Single: 50_000}}, 120_000, 20_000, 46_000, 5_000},
		// 1981: the alternative rate on all of the gain (no limit).
		{"alternative tax without a limit", &CapitalGains{Method: "exclusion", ExcludedShare: fptr(0.6),
			AlternativeRate: fptr(0.2)}, 120_000, 100_000, 41_000 + 20_000, 20_000},
		{"no preference", &CapitalGains{Method: "ordinary"}, 40_000, 20_000, 13_000, 6_000},
		{"no rules for the year", nil, 40_000, 20_000, 13_000, 6_000},
		{"no gains", &CapitalGains{Method: "maxRate", MaxRate: fptr(0.28)}, 40_000, 0, 7_000, 0},
		// A preference never costs more than ordinary rates would: the gain
		// fits in the 10% bracket, below the 15% preferential rate.
		{"never above ordinary", &CapitalGains{Method: "brackets", Schedules: &ByStatus[[]Bracket]{
			Single: []Bracket{{From: 0, Rate: 0.15}},
		}}, 0, 5_000, 500, 500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			total, onGains := FederalTax(testFederal, c.cg, c.ordinary, c.gains, Single)
			if math.Abs(total-c.total) > 1e-6 || math.Abs(onGains-c.onGains) > 1e-6 {
				t.Errorf("got %v (gains %v), want %v (gains %v)", total, onGains, c.total, c.onGains)
			}
		})
	}
}

func TestNetInvestmentIncome(t *testing.T) {
	cg := &CapitalGains{NetInvestmentIncomeTax: &NetInvestmentIncomeTax{
		Rate: 0.038, Threshold: ByStatus[float64]{Single: 200_000, MarriedJoint: fptr(250_000)},
	}}
	for _, c := range []struct {
		nii, magi float64
		status    Status
		want      float64
	}{
		{50_000, 230_000, Single, 0.038 * 30_000}, // the excess over the threshold
		{10_000, 230_000, Single, 0.038 * 10_000}, // all of the investment income
		{50_000, 230_000, MarriedJoint, 0},        // under the joint threshold
		{50_000, 180_000, Single, 0},
	} {
		if got := cg.NetInvestmentIncome(c.nii, c.magi, c.status); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if (*CapitalGains)(nil).NetInvestmentIncome(1, 1e9, Single) != 0 {
		t.Error("no rules, no tax")
	}
}

func TestProjectCapitalGains(t *testing.T) {
	base := &CapitalGains{Method: "brackets", Schedules: &ByStatus[[]Bracket]{
		Single: []Bracket{{From: 0, Rate: 0}, {From: 48_350, Rate: 0.15}},
	}, NetInvestmentIncomeTax: &NetInvestmentIncomeTax{Rate: 0.038, Threshold: ByStatus[float64]{Single: 200_000}}}
	p := ProjectCapitalGains(base, 4)
	if got, want := p.Schedules.Single[1].From, indexed(48_350, 4, bracketRounding); got != want {
		t.Errorf("threshold %v, want %v", got, want)
	}
	if p.NetInvestmentIncomeTax.Threshold.Single != 200_000 || base.Schedules.Single[1].From != 48_350 {
		t.Error("NIIT thresholds aren't indexed, and the base is unchanged")
	}
}

func TestParseCapitalGains(t *testing.T) {
	payroll := `{"years":{"2026":{"socialSecurityRate":0.062,"wageBase":1,"medicareRate":0.0145,"additionalMedicareRate":0,"source":"https://x.gov"}}}`
	federal := `{"id":"us-federal","kind":"federal","years":{"2026":{"type":"brackets","schedules":{"single":[{"from":0,"rate":0.1},{"from":100,"rate":0.25}]},"source":"https://x.gov"}}}`
	parse := func(cg string) error {
		_, err := Parse(fstest.MapFS{
			"data/jurisdictions/us-federal.json": {Data: []byte(federal)},
			"data/payroll.json":                  {Data: []byte(payroll)},
			"data/zips.json":                     {Data: []byte(`{"jurisdictions":{}}`)},
			"data/capital-gains.json":            {Data: []byte(`{"years":{"2026":` + cg + `}}`)},
		})
		return err
	}
	if err := parse(`{"method":"brackets","tiers":[{"rate":0,"from":0},{"rate":0.15,"fromOrdinaryRate":0.25}],"source":"https://x.gov"}`); err != nil {
		t.Errorf("valid tiers: %v", err)
	}
	for name, c := range map[string]struct{ cg, want string }{
		"tier rate not in the schedule": {`{"method":"brackets","tiers":[{"rate":0,"from":0},{"rate":0.15,"fromOrdinaryRate":0.28}],"source":"https://x.gov"}`, "no ordinary bracket"},
		"no source":                     {`{"method":"ordinary"}`, "source"},
		"unknown method":                {`{"method":"flat","source":"https://x.gov"}`, "method"},
		"exclusion without a share":     {`{"method":"exclusion","source":"https://x.gov"}`, "excludedShare"},
		"half an alternative tax":       {`{"method":"exclusion","excludedShare":0.5,"alternativeLimit":{"single":50000},"source":"https://x.gov"}`, "alternativeRate"},
		"tiers and schedules":           {`{"method":"brackets","tiers":[{"rate":0,"from":0}],"schedules":{"single":[{"from":0,"rate":0}]},"source":"https://x.gov"}`, "not both"},
		"rate as percent":               {`{"method":"maxRate","maxRate":28,"source":"https://x.gov"}`, "maxRate"},
		"unknown field":                 {`{"method":"ordinary","rate":0.1,"source":"https://x.gov"}`, "unknown field"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := parse(c.cg); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}
