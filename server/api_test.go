package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mux := http.NewServeMux()
	(&API{store: store, hub: NewHub(store)}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func do(t *testing.T, method, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func decode[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEntryLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)

	res := do(t, "POST", srv.URL+"/api/entries")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d", res.StatusCode)
	}
	created := decode[Entry](t, res)
	if created.ID == "" || created.Title != "Untitled" {
		t.Fatalf("create: got %+v", created)
	}

	res = do(t, "GET", srv.URL+"/api/entries/"+created.ID)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d", res.StatusCode)
	}
	if got := decode[Entry](t, res); got != created {
		t.Fatalf("get: got %+v, want %+v", got, created)
	}

	res = do(t, "DELETE", srv.URL+"/api/entries/"+created.ID)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", res.StatusCode)
	}

	for _, method := range []string{"GET", "DELETE"} {
		res = do(t, method, srv.URL+"/api/entries/"+created.ID)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s after delete: status %d, want 404", method, res.StatusCode)
		}
	}
}

func TestListEmptyIsArrayAndNewestFirst(t *testing.T) {
	srv, store := newTestServer(t)

	res := do(t, "GET", srv.URL+"/api/entries")
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Fatalf("empty list: got %q, want []", got)
	}

	// Pin timestamps so ordering does not depend on the clock.
	ids := make([]string, 3)
	for i := range ids {
		e, err := store.CreateEntry(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = e.ID
		if _, err := store.db.Exec(`UPDATE entries SET updated_at = ? WHERE id = ?`, 1000*(i+1), e.ID); err != nil {
			t.Fatal(err)
		}
	}

	list := decode[[]Entry](t, do(t, "GET", srv.URL+"/api/entries"))
	if len(list) != 3 || list[0].ID != ids[2] || list[1].ID != ids[1] || list[2].ID != ids[0] {
		t.Fatalf("order: got %+v, want %v reversed", list, ids)
	}
}

func TestEntriesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.CreateEntry(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetEntry(t.Context(), e.ID)
	if err != nil || got != e {
		t.Fatalf("after reopen: got %+v, %v; want %+v", got, err, e)
	}
}
