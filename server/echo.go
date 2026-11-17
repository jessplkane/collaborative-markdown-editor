package main

import (
	"log"
	"net/http"

	"github.com/coder/websocket"
)

// handleEcho is a slice-0 wiring check: it reflects every text frame back.
// It is removed once the real entry websocket exists.
func handleEcho(w http.ResponseWriter, r *http.Request) {
	// Default origin check: Origin host must equal Host. The Vite dev proxy
	// preserves Host, so the browser stays same-origin end to end.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("echo accept: %v", err)
		return
	}
	defer c.CloseNow()

	ctx := r.Context()
	for {
		typ, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		if err := c.Write(ctx, typ, msg); err != nil {
			return
		}
	}
}
