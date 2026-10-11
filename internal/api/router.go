package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mj52951/evm-tx-sender/internal/job"
)

const requestTimeout = 30 * time.Second

// New builds the fully-mounted HTTP router. Returned as http.Handler so the
// caller doesn't need to import chi.
func New(services map[uint64]job.Service, registry *job.JobRegistry) http.Handler {
	handler := &Handler{services: services, registry: registry}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(requestTimeout))

	router.Get("/healthz", healthz)
	router.Route("/v1", func(r chi.Router) {
		r.Post("/transactions", handler.submit)
		r.Get("/transactions/{jobId}", handler.get)
	})
	return router
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message})
}
