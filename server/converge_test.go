package main

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/coder/websocket"
)

var tokenRE = regexp.MustCompile(`<[ab]\d+(?:😀)?>`)

// editor is a websocket client that, like the browser, keeps a local copy of
// the document, has at most one batch in flight, and sends another once the
// last one is confirmed or rejected. The server sends the updates that made a
// batch stale before rejecting it, so after a rejection the local copy is
// current and a new edit can be made against it. (The browser rebases its
// pending edit instead; that is covered by the Playwright test.)
type editor struct {
	conn     *websocket.Conn
	clientID string
	rng      *rand.Rand
	tokens   []string // edits still to make, one insert each

	doc      Doc
	version  int
	inFlight bool
	rejects  int
}

// run makes every edit and keeps reading until the document reaches
// finalVersion, which means every client's edits have been applied here.
func (e *editor) run(t *testing.T, finalVersion int) {
	m := readServerMsg(t, e.conn)
	if m.Type != "snapshot" {
		t.Errorf("%s: first message is %q", e.clientID, m.Type)
		return
	}
	e.doc = Doc(utf16.Encode([]rune(m.Doc)))
	e.version = m.Version

	for e.version < finalVersion || e.inFlight || len(e.tokens) > 0 {
		if !e.inFlight && len(e.tokens) > 0 {
			writeUpdate(t, e.conn, e.clientID, e.version, insert(e.position(), e.tokens[0]))
			e.inFlight = true
		}

		m := readServerMsg(t, e.conn)
		switch m.Type {
		case "updates":
			if m.Version != e.version {
				t.Errorf("%s: updates based on %d, have %d", e.clientID, m.Version, e.version)
				return
			}
			doc, err := e.doc.ApplySteps(m.Updates)
			if err != nil {
				t.Errorf("%s: %v", e.clientID, err)
				return
			}
			e.doc = doc
			e.version += len(m.Updates)
			if m.ClientID == e.clientID {
				e.inFlight = false
				e.tokens = e.tokens[1:]
			}
		case "reject":
			e.inFlight = false
			e.rejects++
		default:
			t.Errorf("%s: unexpected %q", e.clientID, m.Type)
			return
		}
	}
}

// position picks a random place in the document that does not fall between
// the halves of a surrogate pair.
func (e *editor) position() int {
	pos := e.rng.IntN(len(e.doc) + 1)
	if pos > 0 && pos < len(e.doc) && e.doc[pos-1] >= 0xD800 && e.doc[pos-1] < 0xDC00 {
		pos++ // after a high surrogate comes its low half
	}
	return pos
}

// Two real websocket clients type into one entry at the same time, each
// sending without waiting for the other. They must end with the same text as
// each other and as the server, and no edit may be lost or duplicated.
func TestTwoClientsTypingConcurrentlyConverge(t *testing.T) {
	const perClient = 100

	srv, store := newWSServer(t)
	id := newEntry(t, store, "start 😀 end")

	editors := make([]*editor, 2)
	for i := range editors {
		name := string(rune('a' + i))
		e := &editor{
			conn:     mustDial(t, srv, id),
			clientID: name,
			rng:      rand.New(rand.NewPCG(uint64(i), 1)),
		}
		for n := range perClient {
			e.tokens = append(e.tokens, token(name, n))
		}
		editors[i] = e
	}

	// Each edit is one step, so the final version counts all of them.
	finalVersion := 2 * perClient
	var wg sync.WaitGroup
	for _, e := range editors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.run(t, finalVersion)
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}

	a, b := editors[0].doc.String(), editors[1].doc.String()
	if a != b {
		t.Fatalf("clients diverged:\n a: %q\n b: %q", a, b)
	}

	// A fresh connection sees what the server holds.
	snap := readServerMsg(t, mustDial(t, srv, id))
	if snap.Doc != a || snap.Version != finalVersion {
		t.Fatalf("server has version %d %q, clients have %q", snap.Version, snap.Doc, a)
	}

	// Edits land inside each other's text, so peel tokens off from the inside
	// out. What is left must be the original document, and each edit must
	// have been found exactly once.
	found := map[string]int{}
	rest := a
	for {
		next := tokenRE.ReplaceAllStringFunc(rest, func(s string) string {
			found[s]++
			return ""
		})
		if next == rest {
			break
		}
		rest = next
	}
	if rest != "start 😀 end" {
		t.Fatalf("original text damaged: %q", rest)
	}
	for _, e := range editors {
		for n := range perClient {
			if got := found[token(e.clientID, n)]; got != 1 {
				t.Errorf("edit %q found %d times", token(e.clientID, n), got)
			}
		}
	}
	t.Logf("rejections: a=%d b=%d", editors[0].rejects, editors[1].rejects)
}

// token is the text of one edit. Every n gives a string that is not a
// substring of another, and every third one holds an astral character so
// UTF-16 positions are exercised.
func token(name string, n int) string {
	if n%3 == 0 {
		return fmt.Sprintf("<%s%d😀>", name, n)
	}
	return fmt.Sprintf("<%s%d>", name, n)
}
