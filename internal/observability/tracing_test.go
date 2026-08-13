package observability

import (
	"context"
	"testing"
)

func TestTracingDisabledNeedsNoCollector(t *testing.T) {
	shutdown, err := SetupTracing(context.Background(), false, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTracingEnabledRequiresEndpoint(t *testing.T) {
	if _, err := SetupTracing(context.Background(), true, "", "test"); err == nil {
		t.Fatal("enabled tracing accepted an empty endpoint")
	}
}
