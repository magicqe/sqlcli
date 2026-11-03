package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"sqlcli/internal/config"
)

// TestClientLoads checks that Oracle Instant Client is found via libDir: the
// error must be a network/listener error, not DPI-1047 (library not found).
func TestClientLoads(t *testing.T) {
	if os.Getenv("ORACLE_HOME") == "" {
		t.Skip("ORACLE_HOME not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := Open(ctx, config.Connection{Name: "x", User: "u", Password: "p", ConnectString: "127.0.0.1:1/nosuch"}, Options{LibDir: os.Getenv("ORACLE_HOME"), DBMSOutput: true})
	t.Logf("err: %v", err)
	if err == nil {
		t.Fatal("expected an error")
	}
	if s := err.Error(); strings.Contains(s, "DPI-1047") {
		t.Fatalf("client library not loaded: %v", err)
	}
}
