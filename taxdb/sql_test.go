package taxdb

import (
	"bytes"
	"flag"
	"os"
	"testing"

	"github.com/mustard-money/customer-data/taxtables"
)

var update = flag.Bool("update", false, "rewrite db/sql/default-data.sql from the tax data")

const defaultDataFile = "../db/sql/default-data.sql"

// TestDefaultData checks that db/sql/default-data.sql holds what the tax data
// makes today (run with -update to rewrite it after the data changes).
func TestDefaultData(t *testing.T) {
	d, err := taxtables.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := WriteSQL(&got, d); err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(defaultDataFile, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(defaultDataFile)
	if err != nil {
		t.Fatalf("%v (generate it with: go test ./taxdb -run TestDefaultData -update)", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("%s is out of date with the tax data; regenerate it with: make default-data", defaultDataFile)
	}
}

func TestCopyText(t *testing.T) {
	s := "a\\b\tc\nd"
	for _, c := range []struct {
		in   any
		want string
	}{
		{nil, `\N`},
		{(*string)(nil), `\N`},
		{&s, `a\\b\tc\nd`},
		{[]byte(`{"x":"y"}`), `{"x":"y"}`},
		{true, "t"},
		{int16(2026), "2026"},
		{0.062, "0.062"},
		{(*float64)(nil), `\N`},
	} {
		got, err := copyText(c.in)
		if err != nil || got != c.want {
			t.Errorf("copyText(%#v) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}
