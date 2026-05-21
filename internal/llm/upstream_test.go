package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchUpstreamModels_HappyPath(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"gpt-4o-mini","object":"model"},
			{"id":"gpt-4o","object":"model"},
			{"id":"text-embedding-3-small","object":"model"}
		]}`)
	}))
	defer srv.Close()

	got, err := FetchUpstreamModels(Provider{ID: "test", BaseURL: srv.URL, APIKey: "sk-abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Endpoint und Bearer-Header pruefen.
	if gotPath != "/v1/models" {
		t.Errorf("path: got %q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-abc" {
		t.Errorf("auth: got %q, want %q", gotAuth, "Bearer sk-abc")
	}

	// Sortierung pruefen.
	want := []string{"gpt-4o", "gpt-4o-mini", "text-embedding-3-small"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

func TestFetchUpstreamModels_NoAPIKey(t *testing.T) {
	// Provider ohne Key (lokales Ollama-Setup): Authorization-Header darf
	// NICHT gesetzt sein.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization header set despite empty APIKey: %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"data":[{"id":"llama3"}]}`)
	}))
	defer srv.Close()

	got, err := FetchUpstreamModels(Provider{ID: "ollama", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "llama3" {
		t.Errorf("got %v, want [llama3]", got)
	}
}

func TestFetchUpstreamModels_TrailingSlashBaseURL(t *testing.T) {
	// Provider.BaseURL kann mit oder ohne trailing slash kommen — der Call
	// muss in beiden Faellen genau eine /v1/models-URL bauen.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path: got %q, want /v1/models", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	if _, err := FetchUpstreamModels(Provider{ID: "t", BaseURL: srv.URL + "/"}); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestFetchUpstreamModels_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	_, err := FetchUpstreamModels(Provider{ID: "t", BaseURL: srv.URL, APIKey: "bogus"})
	if err == nil {
		t.Fatal("expected error on HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code 401: %v", err)
	}
}

func TestFetchUpstreamModels_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json at all`)
	}))
	defer srv.Close()

	_, err := FetchUpstreamModels(Provider{ID: "t", BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode: %v", err)
	}
}

func TestFetchUpstreamModels_EmptyBaseURL(t *testing.T) {
	_, err := FetchUpstreamModels(Provider{ID: "t"})
	if err == nil {
		t.Fatal("expected error on empty BaseURL")
	}
}

func TestFetchUpstreamModels_FiltersEmptyIDs(t *testing.T) {
	// Defensives Filtern: ein Eintrag ohne id darf nicht als leerer String
	// ins Ergebnis sickern.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"a"},{"id":""},{"id":"b"}]}`)
	}))
	defer srv.Close()

	got, err := FetchUpstreamModels(Provider{ID: "t", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}
