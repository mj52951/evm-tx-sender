package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mj52951/evm-tx-sender/internal/sender"
)

const requestTimeout = 30 * time.Second

// New builds the fully-mounted HTTP router. Returned as http.Handler so the
// caller doesn't need to import chi.
func New(service *sender.Service) http.Handler {
	handler := &Handler{service: service}

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
