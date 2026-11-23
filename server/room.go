package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"sync"
	"time"
)

// Wire protocol, JSON text frames.
//
// client -> server
//
//	{"type":"update","clientID":"c1","version":N,"updates":[{"changes":[...]}]}
//	    A batch of steps the client made on top of document version N.
//	{"type":"hello","clientID":"c1","userID":"u1","name":"Ada"}
//	    Says who this connection is. Sent once, right after the snapshot, and
//	    again if the name changes. The connection is listed as a viewer from
//	    its first hello.
//	{"type":"cursor","version":N,"pos":P}
//	    The cursor is at position P (UTF-16 units) of document version N. The
//	    server relays it unchanged; mapping it onto newer versions is up to
//	    the receiving browser. Only valid after a hello.
//
// server -> client
//
//	{"type":"snapshot","doc":"...","version":N}
//	    Sent once, first, when joining.
//	{"type":"updates","version":N,"clientID":"c1","updates":[...]}
//	    An accepted batch, based on version N. Broadcast to every client in the
//	    room including its author, whose clientID tells it the batch is confirmed.
//	{"type":"reject","version":N}
//	    The client's batch was based on a stale version. The updates that made it
//	    stale were sent before this message; N is the current version.
//	{"type":"viewers","viewers":[{"clientID":"c1","userID":"u1","name":"Ada"}]}
//	    Everyone in the room who has said hello, in join order, including the
//	    receiver. Sent whenever that set or a name changes.
//	{"type":"cursor","clientID":"c1","version":N,"pos":P}
//	    Another viewer's cursor, relayed to everyone but its owner. Because it
//	    is sent after every update the room had broadcast when it arrived, a
//	    receiver has always seen version N by the time it reads this.

type clientMessage struct {
	Type     string `json:"type"`
	ClientID string `json:"clientID"`
	Version  int    `json:"version"`
	Updates  []Step `json:"updates"`
	UserID   string `json:"userID"`
	Name     string `json:"name"`
	Pos      *int   `json:"pos"`
}

type snapshotMessage struct {
	Type    string `json:"type"`
	Doc     string `json:"doc"`
	Version int    `json:"version"`
}

type updatesMessage struct {
	Type     string `json:"type"`
	Version  int    `json:"version"`
	ClientID string `json:"clientID"`
	Updates  []Step `json:"updates"`
}

type rejectMessage struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
}

type viewer struct {
	ClientID string `json:"clientID"`
	UserID   string `json:"userID"`
	Name     string `json:"name"`
}

type viewersMessage struct {
	Type    string   `json:"type"`
	Viewers []viewer `json:"viewers"`
}

type cursorMessage struct {
	Type     string `json:"type"`
	ClientID string `json:"clientID"`
	Version  int    `json:"version"`
	Pos      int    `json:"pos"`
}

func marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Only fixed, marshalable message types reach here.
		panic(err)
	}
	return b
}

// Client is one connection in a room. The room only ever does a non-blocking
// send to it, so a slow connection cannot stall the room.
type Client struct {
	send chan []byte
	// kick closes the connection with a reason. It may be called repeatedly
	// from any goroutine and must not block.
	kick func(reason string)

	// Owned by the room's goroutine.
	seq      int    // join order, which keeps the viewer list stable
	clientID string // set by hello; empty until then
	userID   string
	name     string
}

type joinMsg struct{ c *Client }
type leaveMsg struct{ c *Client }
type helloMsg struct {
	c                      *Client
	clientID, userID, name string
}
type cursorMsg struct {
	c       *Client
	version int
	pos     int
}
type updateMsg struct {
	c        *Client
	clientID string
	version  int
	steps    []Step
}

// Hub owns the live rooms, at most one per entry.
type Hub struct {
	store *Store

	// debounce is how long after the last edit a room waits before saving;
	// maxWait bounds how long continuous typing can postpone a save.
	debounce time.Duration
	maxWait  time.Duration

	originPatterns []string

	// pingInterval is how often an idle connection is pinged; one that does
	// not answer within pingTimeout is closed, which removes it from the room.
	pingInterval time.Duration
	pingTimeout  time.Duration

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewHub(store *Store) *Hub {
	return &Hub{
		store:        store,
		debounce:     time.Second,
		maxWait:      5 * time.Second,
		rooms:        make(map[string]*Room),
		pingInterval: 20 * time.Second,
		pingTimeout:  10 * time.Second,
	}
}

// Join adds c to the room for entry id, creating the room if needed. The
// room sends c a snapshot as its first message. The error is ErrNotFound if
// the entry does not exist.
func (h *Hub) Join(ctx context.Context, id string, c *Client) (*Room, error) {
	for {
		r, err := h.room(ctx, id)
		if err != nil {
			return nil, err
		}
		if r.join(c) {
			return r, nil
		}
		// The room closed between lookup and join; look up (or create) again.
	}
}

func (h *Hub) room(ctx context.Context, id string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[id]; ok {
		return r, nil
	}
	content, err := h.store.GetContent(ctx, id)
	if err != nil {
		return nil, err
	}
	r := &Room{
		id:      id,
		hub:     h,
		reqs:    make(chan any),
		done:    make(chan struct{}),
		doc:     NewDoc(content),
		clients: make(map[*Client]struct{}),
	}
	h.rooms[id] = r
	go r.run()
	return r, nil
}

func (h *Hub) remove(r *Room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[r.id] == r {
		delete(h.rooms, r.id)
	}
}

// Room is the single authority for one entry. One goroutine (run) owns all of
// its mutable state; everything else talks to it over reqs, so updates are
// sequenced by arrival order and no locks are needed.
//
// A room lives while it has clients. When the last one leaves it saves, removes
// itself from the hub, and exits.
type Room struct {
	id   string
	hub  *Hub
	reqs chan any      // unbuffered: a completed send means run has the message
	done chan struct{} // closed when run has exited

	// Owned by run.
	doc            Doc
	version        int
	clients        map[*Client]struct{}
	seq            int // last join sequence number handed out
	viewersChanged bool
	dirty          bool
	dirtySince     time.Time
	timer          *time.Timer
	flushC         <-chan time.Time // timer.C while a save is scheduled, else nil
}

// join reports false if the room has already closed.
func (r *Room) join(c *Client) bool {
	select {
	case r.reqs <- joinMsg{c}:
		return true
	case <-r.done:
		return false
	}
}

func (r *Room) leave(c *Client) {
	select {
	case r.reqs <- leaveMsg{c}:
	case <-r.done:
	}
}

// update submits a batch of steps based on the given version.
func (r *Room) update(c *Client, clientID string, version int, steps []Step) {
	select {
	case r.reqs <- updateMsg{c, clientID, version, steps}:
	case <-r.done:
	}
}

// hello says who c is; the room lists it as a viewer from then on.
func (r *Room) hello(c *Client, clientID, userID, name string) {
	select {
	case r.reqs <- helloMsg{c, clientID, userID, name}:
	case <-r.done:
	}
}

// cursor relays c's cursor position, given in terms of document version.
func (r *Room) cursor(c *Client, version, pos int) {
	select {
	case r.reqs <- cursorMsg{c, version, pos}:
	case <-r.done:
	}
}

func (r *Room) run() {
	r.timer = time.NewTimer(time.Hour)
	r.timer.Stop()

	for {
		select {
		case msg := <-r.reqs:
			switch m := msg.(type) {
			case joinMsg:
				r.seq++
				m.c.seq = r.seq
				r.clients[m.c] = struct{}{}
				r.send(m.c, marshal(snapshotMessage{"snapshot", r.doc.String(), r.version}))
			case leaveMsg:
				r.removeClient(m.c)
			case updateMsg:
				r.handleUpdate(m)
			case helloMsg:
				r.handleHello(m)
			case cursorMsg:
				r.handleCursor(m)
			}
		case <-r.flushC:
			r.flush()
		}
		r.sendViewers()

		if len(r.clients) == 0 {
			// Save before leaving the hub's map, so a room created for the next
			// joiner loads this room's final content.
			r.flush()
			if r.dirty {
				log.Printf("room %s: final save failed, unsaved edits lost", r.id)
			}
			r.hub.remove(r)
			close(r.done)
			return
		}
	}
}

func (r *Room) handleUpdate(m updateMsg) {
	if _, ok := r.clients[m.c]; !ok {
		return // dropped or left while the message was in flight
	}
	switch {
	case m.version > r.version:
		r.drop(m.c, "version ahead of server")
	case m.version < r.version:
		r.send(m.c, marshal(rejectMessage{"reject", r.version}))
	default:
		doc, err := r.doc.ApplySteps(m.steps)
		if err != nil {
			r.drop(m.c, "invalid update")
			return
		}
		base := r.version
		r.doc = doc
		r.version += len(m.steps)
		data := marshal(updatesMessage{"updates", base, m.clientID, m.steps})
		for c := range r.clients {
			r.send(c, data)
		}
		r.markDirty()
	}
}

func (r *Room) handleHello(m helloMsg) {
	if _, ok := r.clients[m.c]; !ok {
		return
	}
	m.c.clientID, m.c.userID, m.c.name = m.clientID, m.userID, m.name
	r.viewersChanged = true
}

func (r *Room) handleCursor(m cursorMsg) {
	if _, ok := r.clients[m.c]; !ok {
		return
	}
	switch {
	case m.c.clientID == "":
		r.drop(m.c, "cursor before hello")
	case m.version > r.version:
		r.drop(m.c, "version ahead of server")
	default:
		data := marshal(cursorMessage{"cursor", m.c.clientID, m.version, m.pos})
		for c := range r.clients {
			if c != m.c {
				r.send(c, data)
			}
		}
	}
}

// sendViewers tells every client who is in the room. A send can drop a slow
// client, which changes the list again, hence the loop.
func (r *Room) sendViewers() {
	for r.viewersChanged {
		r.viewersChanged = false

		listed := make([]*Client, 0, len(r.clients))
		for c := range r.clients {
			if c.clientID != "" {
				listed = append(listed, c)
			}
		}
		slices.SortFunc(listed, func(a, b *Client) int { return a.seq - b.seq })
		viewers := make([]viewer, len(listed))
		for i, c := range listed {
			viewers[i] = viewer{c.clientID, c.userID, c.name}
		}

		data := marshal(viewersMessage{"viewers", viewers})
		for c := range r.clients {
			r.send(c, data)
		}
	}
}

// send queues data for c, dropping c if its buffer is full.
func (r *Room) send(c *Client, data []byte) {
	select {
	case c.send <- data:
	default:
		r.drop(c, "too slow")
	}
}

func (r *Room) drop(c *Client, reason string) {
	r.removeClient(c)
	c.kick(reason)
}

// removeClient takes c out of the room. If c was listed as a viewer, the
// list is sent again once the current message has been handled.
func (r *Room) removeClient(c *Client) {
	if _, ok := r.clients[c]; !ok {
		return
	}
	delete(r.clients, c)
	if c.clientID != "" {
		r.viewersChanged = true
	}
}

func (r *Room) markDirty() {
	now := time.Now()
	if !r.dirty {
		r.dirty = true
		r.dirtySince = now
	}
	delay := r.hub.debounce
	if left := r.hub.maxWait - now.Sub(r.dirtySince); left < delay {
		delay = max(left, 0)
	}
	r.timer.Reset(delay)
	r.flushC = r.timer.C
}

// flush saves the document if it has unsaved changes. It blocks the room for
// the duration of the write. On a failed save the document stays dirty and
// another attempt is scheduled.
func (r *Room) flush() {
	r.flushC = nil
	r.timer.Stop()
	if !r.dirty {
		return
	}

	content := r.doc.String()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := r.hub.store.SaveContent(ctx, r.id, content, deriveTitle(content))
	switch {
	case err == nil:
		r.dirty = false
	case errors.Is(err, ErrNotFound):
		log.Printf("room %s: entry no longer exists, discarding edits", r.id)
		r.dirty = false
	default:
		log.Printf("room %s: save failed: %v", r.id, err)
		r.timer.Reset(r.hub.maxWait)
		r.flushC = r.timer.C
	}
}
