package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestParseOrigins(t *testing.T) {
	got := parseOrigins(" a.com, *.b.com ,,localhost:5173 ")
	if want := []string{"a.com", "*.b.com", "localhost:5173"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := parseOrigins(""); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestOriginAllowed(t *testing.T) {
	patterns := []string{"app.example.com", "*.preview.example.com", "localhost:5173"}
	tests := map[string]bool{
		"https://app.example.com":          true,
		"https://APP.example.com":          true,
		"https://pr-7.preview.example.com": true,
		"http://localhost:5173":            true,
		"http://localhost:3000":            false,
		"https://app.example.com.evil.io":  false,
		"https://example.com":              false,
		"null":                             false,
		"":                                 false,
	}
	for origin, want := range tests {
		if got := originAllowed(origin, patterns); got != want {
			t.Errorf("originAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestWithCORS(t *testing.T) {
	reached := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusTeapot)
	})
	h := withCORS(next, []string{"app.example.com"})

	do := func(method, origin string, extra ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/entries", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		for i := 0; i+1 < len(extra); i += 2 {
			req.Header.Set(extra[i], extra[i+1])
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("allowed origin is echoed back", func(t *testing.T) {
		rec := do("GET", "https://app.example.com")
		if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || rec.Code != http.StatusTeapot {
			t.Fatalf("code %d, headers %v", rec.Code, rec.Header())
		}
	})
	t.Run("preflight is answered without reaching the handler", func(t *testing.T) {
		before := reached
		rec := do("OPTIONS", "https://app.example.com", "Access-Control-Request-Method", "DELETE")
		if rec.Code != http.StatusNoContent || reached != before {
			t.Fatalf("code %d, reached %d->%d", rec.Code, before, reached)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") == "" || rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Fatalf("missing headers: %v", rec.Header())
		}
	})
	t.Run("other origin gets no CORS headers", func(t *testing.T) {
		rec := do("GET", "https://evil.example.org")
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("headers %v", rec.Header())
		}
	})
	t.Run("preflight from other origin is not answered", func(t *testing.T) {
		rec := do("OPTIONS", "https://evil.example.org", "Access-Control-Request-Method", "DELETE")
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Code == http.StatusNoContent {
			t.Fatalf("code %d headers %v", rec.Code, rec.Header())
		}
	})
	t.Run("same-origin request without Origin passes through", func(t *testing.T) {
		if rec := do("GET", ""); rec.Code != http.StatusTeapot {
			t.Fatalf("code %d", rec.Code)
		}
	})
	t.Run("no patterns means no wrapping", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		withCORS(next, nil).ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("headers %v", rec.Header())
		}
	})
}
