package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Wire protocol, JSON text frames.
//
// The websocket URL may carry ?epoch=E&version=N, meaning "I last saw version
// N of the room instance E; continue from there". Versions restart at 0 with
// every room instance, so the epoch is what says N means anything to this
// room. If E matches, the history covers N and the gap fits the client's send
// buffer, the room replays the missed batches and then sends "resumed";
// otherwise it sends a snapshot as for a new client, and whatever the client
// had not yet had confirmed is lost.
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
//	{"type":"ping"}
//	    Liveness probe, answered with "pong". A browser cannot see websocket
//	    ping frames, and a connection through a proxy or a dead network can
//	    look open for a long time; the client closes one that goes quiet.
//
// server -> client
//
//	{"type":"snapshot","doc":"...","version":N,"epoch":"E"}
//	    Sent first when joining, unless the join resumed.
//	{"type":"resumed","version":N}
//	    Sent first when a resume succeeded, after the missed "updates"
//	    messages, which are the original batches with their original
//	    clientIDs. N is the current version; the client is caught up.
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
//	{"type":"pong"}
//	    The answer to "ping".
//
// Connections end with these close codes. A client reconnects after any of
// them except 1007, 1008, 1009 and the application codes below.
//
//	1001  the server is shutting down; come back
//	1007, 1008  the client sent something malformed or invalid
//	1009  a message over the read limit
//	1013  the client was too slow and was dropped; may try again
//	4004  closeNotFound: the entry does not exist
//	4009  closeReplaced: the same clientID opened a newer connection
//	4010  closeDeleted: the entry was deleted; nothing more was saved
//	4013  closeTooLarge: an update would have made the document larger than
//	      the cap (Hub.maxDocUnits, UTF-16 units); it was not applied. An
//	      entry already over the cap still opens and may shrink.

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
	Epoch   string `json:"epoch"`
}

type resumedMessage struct {
	Type    string `json:"type"`
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
	// kick closes the connection with a close code and reason. It may be
	// called repeatedly from any goroutine and must not block.
	kick func(code websocket.StatusCode, reason string)

	// resume is set before joining by a client that is reconnecting.
	resume *resumeRequest

	// Owned by the room's goroutine.
	seq      int    // join order, which keeps the viewer list stable
	clientID string // set by hello; empty until then
	userID   string
	name     string
}

type resumeRequest struct {
	epoch   string
	version int
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

// deleteMsg says the entry no longer exists; the room closes and discards
// whatever it had not saved.
type deleteMsg struct{}

// shutdownMsg asks the room to save and close every client.
type shutdownMsg struct{}

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

	// linger is how long an empty room stays in memory before it exits. A
	// client whose connection dropped is usually back within seconds, and
	// needs the room (its epoch and history) to resume rather than reload.
	linger time.Duration

	// historyLimit bounds the estimated size of the batches a room keeps for
	// replay; see history.
	historyLimit int

	// maxDocUnits caps a document, in UTF-16 code units. An update that would
	// make a document larger than this is refused; see handleUpdate.
	maxDocUnits int

	mu      sync.Mutex
	rooms   map[string]*Room
	closing bool // set by Shutdown: no new rooms
}

func NewHub(store *Store) *Hub {
	return &Hub{
		store:        store,
		debounce:     time.Second,
		maxWait:      5 * time.Second,
		rooms:        make(map[string]*Room),
		pingInterval: 20 * time.Second,
		pingTimeout:  10 * time.Second,
		linger:       30 * time.Second,
		historyLimit: 4 << 20,
		maxDocUnits:  defaultMaxDocUnits,
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
	if h.closing {
		return nil, errShuttingDown
	}
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
		epoch:   newEpoch(),
		reqs:    make(chan any),
		done:    make(chan struct{}),
		doc:     NewDoc(content),
		history: history{limit: h.historyLimit},
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

// Delete tells the entry's room, if there is one, that the entry is gone:
// its clients are closed with closeDeleted and nothing more is saved. Call it
// after the entry has been removed from the store. A room cannot be created
// for the entry in between: creating one reads the store while holding the
// hub's lock, so it either exists by now or finds the entry missing.
func (h *Hub) Delete(id string) {
	h.mu.Lock()
	r := h.rooms[id]
	h.mu.Unlock()
	if r != nil {
		r.post(deleteMsg{})
	}
}

// Shutdown saves every room and closes its clients with StatusGoingAway,
// which a client treats as "come back". New joins are refused from the start.
// It returns early with ctx's error if the rooms are not done in time; rooms
// still saving keep going, so close the store only after it has returned.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.closing = true
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	// Ask all first so the saves overlap, then wait.
	for _, r := range rooms {
		select {
		case r.reqs <- shutdownMsg{}:
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, r := range rooms {
		select {
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func newEpoch() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on supported platforms.
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Room is the single authority for one entry. One goroutine (run) owns all of
// its mutable state; everything else talks to it over reqs, so updates are
// sequenced by arrival order and no locks are needed.
//
// A room lives while it has clients, and for Hub.linger after the last one
// leaves. It saves when the last client leaves and again before it exits, then
// removes itself from the hub.
type Room struct {
	id    string
	hub   *Hub
	epoch string        // identifies this room instance; see the protocol notes
	reqs  chan any      // unbuffered: a completed send means run has the message
	done  chan struct{} // closed when run has exited

	// Owned by run.
	doc            Doc
	version        int
	history        history
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

// post hands the room a message that is not tied to a client, unless the
// room has already exited.
func (r *Room) post(msg any) {
	select {
	case r.reqs <- msg:
	case <-r.done:
	}
}

func (r *Room) run() {
	r.timer = time.NewTimer(time.Hour)
	r.timer.Stop()

	// While the room is empty and waiting for someone to come back.
	var lingerT *time.Timer
	var lingerC <-chan time.Time

	for {
		select {
		case msg := <-r.reqs:
			switch m := msg.(type) {
			case joinMsg:
				r.seq++
				m.c.seq = r.seq
				r.clients[m.c] = struct{}{}
				r.welcome(m.c)
			case leaveMsg:
				r.removeClient(m.c)
			case updateMsg:
				r.handleUpdate(m)
			case helloMsg:
				r.handleHello(m)
			case cursorMsg:
				r.handleCursor(m)
			case deleteMsg:
				// Nothing to save: the row is gone.
				r.dirty = false
				for c := range r.clients {
					r.drop(c, closeDeleted, "entry deleted")
				}
				r.finish()
				return
			case shutdownMsg:
				for c := range r.clients {
					r.drop(c, websocket.StatusGoingAway, "server shutting down")
				}
				r.finish()
				return
			}
		case <-r.flushC:
			r.flush()
		case <-lingerC:
			r.finish()
			return
		}
		r.sendViewers()

		switch {
		case len(r.clients) > 0 && lingerT != nil:
			lingerT.Stop()
			lingerT, lingerC = nil, nil
		case len(r.clients) == 0 && lingerT == nil:
			if r.hub.linger <= 0 {
				r.finish()
				return
			}
			// Persist now rather than relying on someone coming back.
			r.flush()
			lingerT = time.NewTimer(r.hub.linger)
			lingerC = lingerT.C
		}
	}
}

// finish saves and leaves the hub's map before closing done, so a room
// created for the next joiner loads this room's final content.
func (r *Room) finish() {
	r.flush()
	if r.dirty {
		log.Printf("room %s: final save failed, unsaved edits lost", r.id)
	}
	r.hub.remove(r)
	close(r.done)
}

// welcome sends a newly joined client what it needs to start: the batches it
// missed if it is resuming and they are all available, else a snapshot.
func (r *Room) welcome(c *Client) {
	if req := c.resume; req != nil && req.epoch == r.epoch {
		missed, ok := r.history.since(req.version, r.version)
		// Everything must fit the client's buffer: the room sends without
		// waiting, and a client dropped for being slow mid-replay would only
		// come back and be dropped again.
		if ok && len(missed)+1 <= cap(c.send)-len(c.send) {
			for _, b := range missed {
				r.send(c, marshal(updatesMessage{"updates", b.base, b.clientID, b.steps}))
			}
			r.send(c, marshal(resumedMessage{"resumed", r.version}))
			return
		}
	}
	r.send(c, marshal(snapshotMessage{"snapshot", r.doc.String(), r.version, r.epoch}))
}

func (r *Room) handleUpdate(m updateMsg) {
	if _, ok := r.clients[m.c]; !ok {
		return // dropped or left while the message was in flight
	}
	switch {
	case m.version > r.version:
		r.drop(m.c, websocket.StatusPolicyViolation, "version ahead of server")
	case m.version < r.version:
		r.send(m.c, marshal(rejectMessage{"reject", r.version}))
	default:
		newLen, err := r.doc.CheckSteps(m.steps)
		if err != nil {
			r.drop(m.c, websocket.StatusPolicyViolation, "invalid update")
			return
		}
		if newLen > r.hub.maxDocUnits && newLen > len(r.doc) {
			// Growth past the cap is refused; an entry that is already over it
			// (saved before the cap, or under a larger one) can still shrink.
			// Decided before applying, which writes into r.doc's storage. The
			// batch is not applied, and the client cannot be told to drop
			// just that batch, so the connection ends.
			r.drop(m.c, closeTooLarge, "document too large")
			return
		}
		base := r.version
		r.doc = r.doc.applyChecked(m.steps)
		r.version += len(m.steps)
		r.history.add(batch{base, m.clientID, m.steps})
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
	// A client that reconnects keeps its clientID, and the server may not have
	// noticed yet that its old connection is gone. Without this it would be
	// listed twice.
	for c := range r.clients {
		if c != m.c && c.clientID == m.clientID {
			r.drop(c, closeReplaced, "replaced by a newer connection")
		}
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
		r.drop(m.c, websocket.StatusPolicyViolation, "cursor before hello")
	case m.version > r.version:
		r.drop(m.c, websocket.StatusPolicyViolation, "version ahead of server")
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
		// Transient: the client may come back and catch up.
		r.drop(c, websocket.StatusTryAgainLater, "too slow")
	}
}

func (r *Room) drop(c *Client, code websocket.StatusCode, reason string) {
	r.removeClient(c)
	c.kick(code, reason)
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
