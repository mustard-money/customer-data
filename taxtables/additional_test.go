package taxtables

import "testing"

func TestAdditionalTaxRates(t *testing.T) {
	for year, want := range map[int]float64{1974: 0, 1975: 0.10, 2026: 0.10} {
		if got := EarlyDistributionRate(year); got != want {
			t.Errorf("early %d: %v, want %v", year, got, want)
		}
	}
	for year, want := range map[int]float64{2003: 0, 2004: 0.10, 2010: 0.10, 2011: 0.20, 2026: 0.20} {
		if got := HSANonMedicalRate(year); got != want {
			t.Errorf("HSA %d: %v, want %v", year, got, want)
		}
	}
}
