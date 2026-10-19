package taxtables

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedDataParses(t *testing.T) {
	d, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if d.LastActualYear() < 2026 {
		t.Errorf("last actual year %d", d.LastActualYear())
	}
	states := 0
	var federal *Jurisdiction
	for _, j := range d.Jurisdictions {
		if j.Kind == "state" {
			states++
		}
		if j.ID == "us-federal" {
			federal = j
		}
	}
	if states != 51 {
		t.Errorf("%d states, want 50 + DC", states)
	}
	if federal == nil {
		t.Fatal("no federal data")
	}
	// 2026: Rev. Proc. 2025-32, as amended by the One Big Beautiful Bill Act.
	f := federal.Years["2026"]
	if f.Deduction.Single != 16_100 || *f.Deduction.MarriedJoint != 32_200 {
		t.Errorf("2026 deduction %+v", f.Deduction)
	}
	if top := f.Schedules.Single[len(f.Schedules.Single)-1]; top.From != 640_600 || top.Rate != 0.37 {
		t.Errorf("2026 top bracket %+v", top)
	}
	if p := d.Payroll[2026]; p.WageBase != 184_500 || p.SocialSecurityRate != 0.062 {
		t.Errorf("2026 payroll %+v", p)
	}
}

func TestProjection(t *testing.T) {
	base := &Tax{
		Type: "brackets",
		Schedules: &ByStatus[[]Bracket]{Single: []Bracket{
			{From: 0, Rate: 0.1}, {From: 12_400, Rate: 0.12}, {From: 50_400, Rate: 0.22},
		}},
		Deduction: &ByStatus[float64]{Single: 16_100},
		Source:    "https://example.gov",
	}
	p := ProjectTax(base, 1)
	// 12,400 × 1.023 = 12,685.2 → 12,650; 16,100 × 1.023 = 16,470.3 → 16,450.
	if got := p.Schedules.Single[1].From; got != 12_650 {
		t.Errorf("bracket %v", got)
	}
	if p.Deduction.Single != 16_450 {
		t.Errorf("deduction %v", p.Deduction.Single)
	}
	if p.Schedules.Single[0].From != 0 || p.Schedules.Single[2].Rate != 0.22 {
		t.Errorf("rates and the zero bracket stay: %+v", p.Schedules.Single)
	}
	if base.Schedules.Single[1].From != 12_400 {
		t.Error("projection changed its base")
	}
	// Ten years: 12,400 × 1.023^10 = 15,571.6 → 15,550.
	if got := ProjectTax(base, 10).Schedules.Single[1].From; got != 15_550 {
		t.Errorf("ten years: %v", got)
	}
	// Wage base: 184,500 × 1.023 = 188,743.5 → 188,700 (a multiple of $300).
	if got := ProjectPayroll(&Payroll{WageBase: 184_500}, 1).WageBase; got != 188_700 {
		t.Errorf("wage base %v", got)
	}
}

func TestExpand(t *testing.T) {
	d, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range d.Jurisdictions {
		if j.ID != "us-federal" {
			continue
		}
		years := d.Expand(j)
		last := years[len(years)-1]
		if last.Year != ProjectThrough || !last.Projected {
			t.Errorf("last year %+v", last)
		}
		for i := 1; i < len(years); i++ {
			if years[i].Year != years[i-1].Year+1 {
				t.Fatalf("gap between %d and %d", years[i-1].Year, years[i].Year)
			}
		}
	}
	payroll := d.ExpandPayroll()
	if payroll[len(payroll)-1].Year != ProjectThrough {
		t.Errorf("payroll ends %d", payroll[len(payroll)-1].Year)
	}
}

func TestParseRejectsBadData(t *testing.T) {
	payroll := `{"years":{"2026":{"socialSecurityRate":0.062,"wageBase":1,"medicareRate":0.0145,"additionalMedicareRate":0,"source":"https://x.gov"}}}`
	federal := func(year string) string {
		return `{"id":"us-federal","kind":"federal","years":{` + year + `}}`
	}
	cases := map[string]struct{ id, file, want string }{
		"unsorted brackets": {"us-federal", federal(`"2026":{"type":"brackets","schedules":{"single":[{"from":0,"rate":0.1},{"from":0,"rate":0.2}]},"source":"https://x.gov"}`), "ascend"},
		"no source":         {"us-federal", federal(`"2026":{"type":"none"}`), "source"},
		"rate as percent":   {"us-federal", federal(`"2026":{"type":"flat","rate":5,"source":"https://x.gov"}`), "rate"},
		"none with a rate":  {"us-federal", federal(`"2026":{"type":"none","rate":0.1,"source":"https://x.gov"}`), "none takes"},
		"unknown field":     {"us-federal", federal(`"2026":{"type":"none","rat":0.1,"source":"https://x.gov"}`), "unknown field"},
		"bad year":          {"us-federal", federal(`"26":{"type":"none","source":"https://x.gov"}`), "4 digits"},
		"state without region": {"us-state-CA",
			`{"id":"us-state-CA","kind":"state","state":"CA","years":{"2026":{"type":"none","source":"https://x.gov"}}}`, "region"},
		"id not the file name": {"us-state-NV", federal(`"2026":{"type":"none","source":"https://x.gov"}`), "file name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{
				"data/jurisdictions/" + c.id + ".json": {Data: []byte(c.file)},
				"data/payroll.json":                    {Data: []byte(payroll)},
				"data/zips.json":                       {Data: []byte(`{"jurisdictions":{}}`)},
			}
			_, err := Parse(fsys)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}
