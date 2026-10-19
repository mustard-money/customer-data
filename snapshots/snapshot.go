// Package snapshots implements the monthly snapshot resource: the customer's
// net worth (assets and debts) and monthly income and expenses as they stood
// in a month, recorded by the app so the Home graph can show history. One
// snapshot per month; its id is the month ("2026-10").
package snapshots

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/mustard-money/customer-data/fields"
)

// Snapshot is one month's figures. Amounts are dollars; Debts and the monthly
// amounts are never negative, NetWorth (Assets − Debts) can be.
type Snapshot struct {
	ID              string
	Year, Month     int
	NetWorth        float64
	Assets          float64
	Debts           float64
	MonthlyIncome   float64
	MonthlyExpenses float64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type snapshotJSON struct {
	ID              string  `json:"id"`
	Year            int     `json:"year"`
	Month           int     `json:"month"`
	NetWorth        float64 `json:"netWorth"`
	Assets          float64 `json:"assets"`
	Debts           float64 `json:"debts"`
	MonthlyIncome   float64 `json:"monthlyIncome"`
	MonthlyExpenses float64 `json:"monthlyExpenses"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

// MarshalJSON renders the snapshot in the API's camelCase shape.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(snapshotJSON{
		ID: s.ID, Year: s.Year, Month: s.Month,
		NetWorth: s.NetWorth, Assets: s.Assets, Debts: s.Debts,
		MonthlyIncome: s.MonthlyIncome, MonthlyExpenses: s.MonthlyExpenses,
		CreatedAt: fields.FormatTime(s.CreatedAt), UpdatedAt: fields.FormatTime(s.UpdatedAt),
	})
}

// SnapshotInput is the PUT request body.
type SnapshotInput struct {
	ID              *string  `json:"id"`
	Year            *int     `json:"year"`
	Month           *int     `json:"month"`
	NetWorth        *float64 `json:"netWorth"`
	Assets          *float64 `json:"assets"`
	Debts           *float64 `json:"debts"`
	MonthlyIncome   *float64 `json:"monthlyIncome"`
	MonthlyExpenses *float64 `json:"monthlyExpenses"`
	CreatedAt       *string  `json:"createdAt"`
	// UpdatedAt is accepted so clients can send back a full snapshot, but it
	// is ignored: the server sets updatedAt on every write.
	UpdatedAt *string `json:"updatedAt"`
}

// maxAmount bounds every figure (a trillion dollars either way).
const maxAmount = 1e12

// ID returns the id of the snapshot for a month: "2026-10".
func ID(year, month int) string { return fmt.Sprintf("%04d-%02d", year, month) }

// Validate checks a PUT body against the path id and returns the snapshot to
// store. now bounds the month: a snapshot can't be for a month that hasn't
// started anywhere on Earth. Errors are *fields.ValidationError.
func (in SnapshotInput) Validate(pathID string, now time.Time) (Snapshot, error) {
	if err := fields.ValidateBodyID(in.ID, pathID); err != nil {
		return Snapshot{}, err
	}
	if in.Year == nil || in.Month == nil {
		return Snapshot{}, fields.Invalid("year and month are required")
	}
	year, month := *in.Year, *in.Month
	if month < 1 || month > 12 {
		return Snapshot{}, fields.Invalid("month must be between 1 and 12")
	}
	latest := now.UTC().Add(14 * time.Hour) // UTC+14, the earliest time zone
	if year < 1900 || year*12+month > latest.Year()*12+int(latest.Month()) {
		return Snapshot{}, fields.Invalid("the snapshot's month must not be in the future")
	}
	if pathID != ID(year, month) {
		return Snapshot{}, fields.Invalid("id must be the snapshot's month, %s", ID(year, month))
	}
	s := Snapshot{ID: pathID, Year: year, Month: month}
	for _, f := range []struct {
		name        string
		in          *float64
		out         *float64
		canNegative bool
	}{
		{"netWorth", in.NetWorth, &s.NetWorth, true},
		{"assets", in.Assets, &s.Assets, false},
		{"debts", in.Debts, &s.Debts, false},
		{"monthlyIncome", in.MonthlyIncome, &s.MonthlyIncome, false},
		{"monthlyExpenses", in.MonthlyExpenses, &s.MonthlyExpenses, false},
	} {
		if f.in == nil {
			return Snapshot{}, fields.Invalid("%s is required", f.name)
		}
		v := *f.in
		low := 0.0
		if f.canNegative {
			low = -maxAmount
		}
		if math.IsNaN(v) || v < low || v > maxAmount {
			return Snapshot{}, fields.Invalid("%s must be between %.0f and %.0f", f.name, low, maxAmount)
		}
		*f.out = math.Round(v*100) / 100
	}
	createdAt, err := fields.CreatedAt(in.CreatedAt)
	if err != nil {
		return Snapshot{}, err
	}
	s.CreatedAt = createdAt
	return s, nil
}
