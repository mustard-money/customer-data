package taxtables

import (
	"errors"
	"math"
	"testing"
)

func TestTablesFor(t *testing.T) {
	d, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	tb := NewTables(d)
	first, lastActual, last := tb.Years()
	if first != 1970 || lastActual < 2026 || last != ProjectThrough {
		t.Errorf("years %d %d %d", first, lastActual, last)
	}

	// NYC: federal, New York State, then New York City (a full-coverage local).
	l, err := tb.For("10001", 2026)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, layer := range l.Layers {
		ids = append(ids, layer.ID)
	}
	if len(ids) != 3 || ids[0] != "us-federal" || ids[1] != "us-state-NY" || ids[2] != "us-local-nyc" || l.Layers[2].Coverage != "full" {
		t.Errorf("10001: %v", l.Layers)
	}

	// No ZIP: federal alone. Bad or unmapped ZIPs are errors.
	if l, err := tb.For("", 2026); err != nil || len(l.Layers) != 1 {
		t.Errorf("no ZIP: %v %v", l, err)
	}
	for _, zip := range []string{"1234", "00000"} {
		if _, err := tb.For(zip, 2026); !errors.Is(err, ErrUnknownZip) {
			t.Errorf("%q: %v", zip, err)
		}
	}

	// A projected year comes from the last actual one, indexed.
	p, err := tb.For("", 2030)
	if err != nil || !p.Layers[0].Projected {
		t.Fatalf("2030: %v %v", p, err)
	}
	if got, want := p.Layers[0].Tax.Deduction.Single, indexed(16_100, 2030-lastActual, 50); got != want {
		t.Errorf("2030 deduction %v, want %v", got, want)
	}
	if _, err := tb.For("", 2101); err == nil {
		t.Error("2101 has no tables")
	}

	// A state year that hasn't been transcribed is reported, not treated as no tax.
	if l, err := tb.For("84111", 1974); err != nil || l.StateMissing != "Utah" || len(l.Layers) != 1 {
		t.Errorf("Utah 1974: %+v %v", l, err)
	}
}

func TestPayrollAndCompute(t *testing.T) {
	d, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	tb := NewTables(d)
	p, projected := tb.Payroll(2026)
	if projected || p.WageBase != 184_500 {
		t.Errorf("2026 payroll %+v", p)
	}
	if p, projected := tb.Payroll(2027); projected || p.WageBase != 192_300 {
		t.Errorf("2027 payroll %v %+v", projected, p)
	}
	// 192,300 × 1.023 = 196,722.9, rounded down to a multiple of $300.
	if p, projected := tb.Payroll(2028); !projected || p.WageBase != 196_500 {
		t.Errorf("2028 payroll %v %+v", projected, p)
	}
	ss, medicare := p.PayrollParts(250_000, Single)
	// 6.2% up to the wage base; 1.45% on all, plus 0.9% over $200,000.
	if math.Abs(ss-0.062*184_500) > 1e-9 || math.Abs(medicare-(0.0145*250_000+0.009*50_000)) > 1e-9 {
		t.Errorf("payroll %v %v", ss, medicare)
	}
	if se := p.SelfEmploymentTax(300, 0, Single); se.Total != 0 {
		t.Errorf("under $400 of net earnings owes nothing: %+v", se)
	}
	fed, _ := tb.Federal(2026)
	b := Compute([]Layer{fed}, p, 150_000, 150_000, Single)
	if math.Abs(b.IncomeTax-24_734) > 1e-6 || math.Abs(b.Total-(24_734+9_300+2_175)) > 1e-6 {
		t.Errorf("150k single 2026: %+v", b)
	}
}
