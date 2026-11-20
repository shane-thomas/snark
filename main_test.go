package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelloHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	response := httptest.NewRecorder()

	HomepageHandler(response, req)

	if response.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	expected := "Hello!"
	if response.Body.String() != expected {
		t.Errorf("expected body %q, got %q", expected, response.Body.String())
	}

}
