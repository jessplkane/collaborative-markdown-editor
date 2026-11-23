package main

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// parseOrigins splits a comma-separated list of origin host patterns, such as
// "my-app.netlify.app,*.example.com,localhost:5173". Patterns are matched with
// path.Match against the host (including port) of an Origin header, the same
// convention coder/websocket uses for its OriginPatterns.
func parseOrigins(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func originAllowed(origin string, patterns []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), strings.ToLower(u.Host)); ok {
			return true
		}
	}
	return false
}

// withCORS lets browsers on an allowed origin call the API cross-origin, which
// is needed when the frontend is hosted separately from this server. Requests
// from other origins get no CORS headers, so the browser blocks them. Same-origin
// requests are unaffected.
func withCORS(next http.Handler, patterns []string) http.Handler {
	if len(patterns) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The response depends on Origin whether or not it is allowed, so a
		// cache must key on it either way: otherwise a response with the
		// allowed origin's headers could be served to another origin, or one
		// without them to the allowed origin.
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin != "" && originAllowed(origin, patterns) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
