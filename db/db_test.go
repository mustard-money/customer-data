package db

import (
	"context"
	"strings"
	"testing"
)

func TestConnectErrorNamesTheServer(t *testing.T) {
	// Nothing listens on port 1, so the ping fails at once.
	_, err := Connect(context.Background(), "postgres://mustard:s3cret@127.0.0.1:1/mustard?sslmode=disable")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, `connect to database "mustard" at 127.0.0.1:1 as user "mustard": `) {
		t.Errorf("error = %q", msg)
	}
	if strings.Contains(msg, "s3cret") {
		t.Errorf("error leaks the password: %q", msg)
	}
}
