package taxtables

// Additional taxes on taking money out of tax-advantaged accounts early.
// They're set in statute, not indexed, so they're kept here as rules by year
// rather than as transcribed data.

// EarlyDistributionRate is the additional tax on retirement plan and IRA
// distributions before age 59½: 10% of the taxable amount. IRAs have owed it
// since 1975 (ERISA §2002, former IRC §408(f)); the Tax Reform Act of 1986
// (§1123, IRC §72(t)) extended it to employer plans from 1987. Before 1987
// employer plans didn't owe it; callers don't tell the two apart, so earlier
// years apply it to both (an overstatement for 1975–1986 plan withdrawals).
// Governmental 457(b) plans are exempt; callers leave them out.
func EarlyDistributionRate(year int) float64 {
	if year >= 1975 {
		return 0.10
	}
	return 0
}

// HSANonMedicalRate is the additional tax on HSA distributions not used for
// medical expenses before age 65 (IRC §223(f)(4)): 10% from 2004, when HSAs
// began (Medicare Prescription Drug, Improvement, and Modernization Act of
// 2003), and 20% from 2011 (Affordable Care Act §9004).
func HSANonMedicalRate(year int) float64 {
	switch {
	case year >= 2011:
		return 0.20
	case year >= 2004:
		return 0.10
	}
	return 0
}
