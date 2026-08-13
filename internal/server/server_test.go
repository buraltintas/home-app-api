package server

import (
	"net/http/httptest"
	"testing"
)

func TestQueryFloatRejectsMalformedValue(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/stores/nearby?latitude=not-a-number", nil)
	if _, err := queryFloat(r, "latitude"); err == nil {
		t.Fatal("malformed coordinate accepted")
	}
}

func TestQueryFloatAllowsMissingValue(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/stores/nearby", nil)
	v, err := queryFloat(r, "latitude")
	if err != nil || v != nil {
		t.Fatalf("value=%v err=%v", v, err)
	}
}
