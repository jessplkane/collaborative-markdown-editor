package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	dbPath := "data.db"
	if p := os.Getenv("DB_PATH"); p != "" {
		dbPath = p
	}
	origins := parseOrigins(os.Getenv("ALLOWED_ORIGINS"))

	store, err := OpenStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	hub := NewHub(store)
	hub.originPatterns = origins
	if v := os.Getenv("RESUME_LOG_BYTES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("RESUME_LOG_BYTES must be a non-negative integer, got %q", v)
		}
		hub.historyLimit = n
	}
	if v := os.Getenv("MAX_DOC_UNITS"); v != "" {
		n, err := strconv.Atoi(v)
		// The websocket read limit is sized for the default, so the cap can
		// only be lowered.
		if err != nil || n < 1 || n > defaultMaxDocUnits {
			return fmt.Errorf("MAX_DOC_UNITS must be an integer from 1 to %d, got %q", defaultMaxDocUnits, v)
		}
		hub.maxDocUnits = n
	}

	mux := http.NewServeMux()
	(&API{store: store, hub: hub}).Register(mux)
	mux.HandleFunc("GET /ws/entries/{id}", hub.ServeWS)
	srv := &http.Server{Handler: withCORS(mux, origins)}

	// Bind before logging so "listening" is only printed when it is true.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("listening on %s (db: %s)", addr, dbPath)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	// Fly stops a machine about five seconds after SIGTERM.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpErr := srv.Shutdown(shutdownCtx)
	// Shutdown does not wait for hijacked (websocket) connections, so the
	// rooms are told separately. They must finish saving before the deferred
	// store.Close runs.
	if err := hub.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: rooms did not finish saving: %v", err)
	}
	if httpErr != nil && !errors.Is(httpErr, http.ErrServerClosed) {
		return httpErr
	}
	return nil
}
