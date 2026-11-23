package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

const (
	// defaultMaxDocUnits caps a document at about 1 MB of text, counted in
	// UTF-16 code units because that is how the server holds and addresses it.
	defaultMaxDocUnits = 1_000_000

	// maxMessageBytes caps one incoming frame, sized so that an update the
	// size cap allows is never refused by the read limit instead: JSON can
	// spend up to six bytes on one code unit (a control character is
	// \u00XX), plus room for the rest of the message. The library default of
	// 32 KiB would drop the connection on a modest paste.
	maxMessageBytes = 6*defaultMaxDocUnits + 64<<10

	// clientSendBuffer is how many outgoing messages may queue for one
	// connection before it is dropped as too slow.
	clientSendBuffer = 256

	// closeNotFound is the application close code for an unknown entry.
	closeNotFound = websocket.StatusCode(4004)

	// closeReplaced is sent to a connection whose client (same clientID)
	// has opened a newer one.
	closeReplaced = websocket.StatusCode(4009)

	// closeDeleted is sent to every client of an entry that was deleted.
	closeDeleted = websocket.StatusCode(4010)

	// closeTooLarge is sent to a client whose update would have made the
	// document larger than the cap. The update was not applied.
	closeTooLarge = websocket.StatusCode(4013)
)

const (
	// Limits on the identity a client announces. Nothing is trusted: these
	// strings are relayed to every other viewer.
	maxIDBytes   = 64
	maxNameRunes = 40
)

var errShuttingDown = errors.New("server is shutting down")

var pongMessage = marshal(struct {
	Type string `json:"type"`
}{"pong"})

// valid reports whether m is well formed for its type.
func (m clientMessage) valid() bool {
	switch m.Type {
	case "update":
		return m.ClientID != "" && len(m.ClientID) <= maxIDBytes && len(m.Updates) > 0
	case "hello":
		return m.ClientID != "" && len(m.ClientID) <= maxIDBytes &&
			m.UserID != "" && len(m.UserID) <= maxIDBytes &&
			utf8.RuneCountInString(m.Name) <= maxNameRunes
	case "ping":
		return true
	case "cursor":
		return m.Pos != nil && *m.Pos >= 0 && m.Version >= 0
	}
	return false
}

// ServeWS handles /ws/entries/{id}: it joins the entry's room and then relays
// between the socket and the room until either side goes away.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.originPatterns,
	})
	if err != nil {
		return // Accept has already written the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxMessageBytes)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c := &Client{send: make(chan []byte, clientSendBuffer)}
	if epoch := r.URL.Query().Get("epoch"); epoch != "" {
		// A malformed version is treated as a plain join: the snapshot that
		// follows is always safe.
		if v, err := strconv.Atoi(r.URL.Query().Get("version")); err == nil {
			c.resume = &resumeRequest{epoch, v}
		}
	}
	var kicked atomic.Bool
	c.kick = func(code websocket.StatusCode, reason string) {
		if kicked.CompareAndSwap(false, true) {
			// Close blocks for the closing handshake; the room must not wait.
			go conn.Close(code, reason)
		}
	}

	room, err := h.Join(ctx, r.PathValue("id"), c)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			conn.Close(closeNotFound, "entry not found")
		case errors.Is(err, errShuttingDown):
			conn.Close(websocket.StatusGoingAway, "server shutting down")
		default:
			log.Printf("ws join: %v", err)
			conn.Close(websocket.StatusInternalError, "internal error")
		}
		return
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case msg := <-c.send:
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(h.pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pingCtx, cancelPing := context.WithTimeout(ctx, h.pingTimeout)
				err := conn.Ping(pingCtx)
				cancelPing()
				if err != nil {
					// The peer is gone or not answering. Cancelling ends the
					// read below, so the room hears about the departure.
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	limit := newLimiter(h.messageRate, h.messageBurst, time.Now())
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if !limit.allow(time.Now()) {
			conn.Close(websocket.StatusPolicyViolation, "too many messages")
			break
		}
		var m clientMessage
		if err := json.Unmarshal(data, &m); err != nil || !m.valid() {
			conn.Close(websocket.StatusInvalidFramePayloadData, "bad message")
			break
		}
		switch m.Type {
		case "update":
			room.update(c, m.ClientID, m.Version, m.Updates)
		case "hello":
			room.hello(c, m.ClientID, m.UserID, m.Name)
		case "ping":
			// Answered here, not by the room: it is about this connection.
			select {
			case c.send <- pongMessage:
			default: // the client is already being dropped as too slow
			}
		case "cursor":
			room.cursor(c, m.Version, *m.Pos)
		}
	}

	cancel()
	<-writerDone
	room.leave(c)
}
