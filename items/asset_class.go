package items

import (
	"math"
	"slices"
	"strings"

	"github.com/mustard-money/customer-data/fields"
)

// Asset classes of a brokerage ("taxable") investment. Equities grow at the
// expected return; fixed income keeps its value and pays IncomeRate, which
// may be exempt from some levels of tax. Must match AssetClasses in the
// website's src/data/asset-classes.ts and the items_asset_class_valid
// constraint.
const (
	AssetEquities    = "equities"
	AssetFixedIncome = "fixedIncome"
)

// AssetClasses lists the asset classes in display order.
var AssetClasses = []string{AssetEquities, AssetFixedIncome}

// maxIncomeRate bounds a fixed-income holding's yearly income rate (50%).
const maxIncomeRate = 0.5

// TaxExempt marks the levels of tax a fixed-income holding's interest is
// exempt from. AMT is recorded for the alternative minimum tax, which isn't
// computed yet.
type TaxExempt struct {
	Federal bool `json:"federal,omitempty"`
	AMT     bool `json:"amt,omitempty"`
	State   bool `json:"state,omitempty"`
	Local   bool `json:"local,omitempty"`
}

func (e TaxExempt) any() bool { return e.Federal || e.AMT || e.State || e.Local }

type assetClass struct {
	class      *string // only fixedIncome is stored: unset means equities
	incomeRate *float64
	taxExempt  *TaxExempt
}

// validateAssetClass checks assetClass, incomeRate and taxExempt: only for a
// brokerage ("taxable") investment; fixed income needs an income rate and
// has no expected return (it doesn't appreciate); equities take neither an
// income rate nor exemptions.
func (in ItemInput) validateAssetClass(account *string) (assetClass, error) {
	brokerage := *in.Category == CategoryInvestments && account != nil && *account == "taxable"
	if in.AssetClass == nil {
		if in.IncomeRate != nil || in.TaxExempt != nil {
			return assetClass{}, fields.Invalid("incomeRate and taxExempt need assetClass fixedIncome")
		}
		return assetClass{}, nil
	}
	if !brokerage {
		return assetClass{}, fields.Invalid("assetClass must be omitted or null unless category is investments and account is taxable")
	}
	if !slices.Contains(AssetClasses, *in.AssetClass) {
		return assetClass{}, fields.Invalid("assetClass must be one of: %s", strings.Join(AssetClasses, ", "))
	}
	if *in.AssetClass == AssetEquities {
		if in.IncomeRate != nil || in.TaxExempt != nil {
			return assetClass{}, fields.Invalid("incomeRate and taxExempt need assetClass fixedIncome")
		}
		return assetClass{}, nil
	}
	if in.ReturnRate != nil {
		return assetClass{}, fields.Invalid("returnRate must be omitted or null for fixedIncome (it doesn't appreciate)")
	}
	if in.IncomeRate == nil || math.IsNaN(*in.IncomeRate) || *in.IncomeRate < 0 || *in.IncomeRate > maxIncomeRate {
		return assetClass{}, fields.Invalid("incomeRate is required for fixedIncome, between 0 and %g", maxIncomeRate)
	}
	class, rate := AssetFixedIncome, *in.IncomeRate
	out := assetClass{class: &class, incomeRate: &rate}
	if in.TaxExempt != nil && in.TaxExempt.any() {
		e := *in.TaxExempt
		out.taxExempt = &e
	}
	return out, nil
}
