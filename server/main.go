package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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

	mux := http.NewServeMux()
	(&API{store: store}).Register(mux)
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
