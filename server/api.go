package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

type API struct {
	store *Store
	// hub is told when an entry is deleted, so viewers are sent away.
	hub *Hub
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entries", a.listEntries)
	mux.HandleFunc("POST /api/entries", a.createEntry)
	mux.HandleFunc("GET /api/entries/{id}", a.getEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", a.deleteEntry)
}

func (a *API) listEntries(w http.ResponseWriter, r *http.Request) {
	entries, err := a.store.ListEntries(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (a *API) createEntry(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.CreateEntry(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (a *API) getEntry(w http.ResponseWriter, r *http.Request) {
	e, err := a.store.GetEntry(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *API) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.store.DeleteEntry(r.Context(), id); err != nil {
		storeError(w, err)
		return
	}
	a.hub.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}

func storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "entry not found", http.StatusNotFound)
		return
	}
	serverError(w, err)
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("api: %v", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}
