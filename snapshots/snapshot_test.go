package snapshots

import (
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func valid() SnapshotInput {
	return SnapshotInput{
		Year: ptr(2026), Month: ptr(10),
		NetWorth: ptr(-12000.004), Assets: ptr(388000.0), Debts: ptr(400000.0),
		MonthlyIncome: ptr(11333.33), MonthlyExpenses: ptr(5800.0),
		CreatedAt: ptr("2026-10-01T00:00:00.000Z"),
	}
}

func TestValidate(t *testing.T) {
	s, err := valid().Validate("2026-10", now)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "2026-10" || s.Year != 2026 || s.Month != 10 || s.NetWorth != -12000 || s.Debts != 400000 {
		t.Errorf("snapshot = %+v", s)
	}
	if ID(2026, 3) != "2026-03" {
		t.Errorf("ID = %q", ID(2026, 3))
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		modify func(in *SnapshotInput)
		want   string
	}{
		{"id isn't the month", "2026-09", func(in *SnapshotInput) {}, "id must be the snapshot's month, 2026-10"},
		{"future month", "2026-12", func(in *SnapshotInput) { in.Month = ptr(12) }, "must not be in the future"},
		{"month 13", "2026-13", func(in *SnapshotInput) { in.Month = ptr(13) }, "month must be between 1 and 12"},
		{"missing month", "2026-10", func(in *SnapshotInput) { in.Month = nil }, "year and month are required"},
		{"negative debts", "2026-10", func(in *SnapshotInput) { in.Debts = ptr(-1.0) }, "debts must be between 0"},
		{"missing income", "2026-10", func(in *SnapshotInput) { in.MonthlyIncome = nil }, "monthlyIncome is required"},
		{"huge net worth", "2026-10", func(in *SnapshotInput) { in.NetWorth = ptr(2e12) }, "netWorth must be between"},
	}
	for _, c := range cases {
		in := valid()
		c.modify(&in)
		if _, err := in.Validate(c.id, now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
