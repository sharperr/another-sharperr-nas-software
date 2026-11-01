package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerURL(t *testing.T) {
	got, err := serverURL("")
	if err != nil || got != defaultServerURL {
		t.Fatalf("serverURL default = %q, %v", got, err)
	}
	got, err = serverURL("https://nas.example/asns/")
	if err != nil || got != "https://nas.example/asns" {
		t.Fatalf("serverURL custom = %q, %v", got, err)
	}
	for _, invalid := range []string{"ftp://nas.example", "https://user@nas.example", "http://nas.example?q=1", "/relative"} {
		if _, err := serverURL(invalid); err == nil {
			t.Errorf("serverURL(%q) should be rejected", invalid)
		}
	}
}

func TestCheckHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Version: "0.1.0"})
	}))
	defer server.Close()

	var output strings.Builder
	if err := checkHealth(context.Background(), server.URL, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ASNS is healthy (version 0.1.0)") {
		t.Fatalf("unexpected health output: %q", output.String())
	}
}

func TestHelpAndUnknownCommand(t *testing.T) {
	var output, errors strings.Builder
	if err := runWith([]string{"help"}, &output, &errors, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "asns launch") || !strings.Contains(output.String(), "asns health") {
		t.Fatalf("help output omitted commands: %q", output.String())
	}
	if err := runWith([]string{"unknown"}, &output, &errors, ""); err == nil {
		t.Fatal("unknown command should fail")
	}
}

func TestHealthRejectsUnhealthyResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"starting","version":"0.1.0"}`))
	}))
	defer server.Close()
	var output strings.Builder
	if err := checkHealth(context.Background(), server.URL, &output); err == nil {
		t.Fatal("unhealthy response should fail")
	}
}
