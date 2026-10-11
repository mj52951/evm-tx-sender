package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/mj52951/evm-tx-sender/internal/job"
)

type Handler struct {
	service *job.Service
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	var req SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	jobReq, err := req.ToJobRequest()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	createdJob, err := h.service.Submit(r.Context(), jobReq)
	if err != nil {
		switch {
		case errors.Is(err, job.ErrUnknownSender):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, job.ErrBusy):
			writeError(w, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, err.Error())
		default:
			slog.Error("submit failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	status := http.StatusCreated
	if createdJob.State == job.StateFailed {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, NewJobResponse(createdJob))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	fetchedJob, ok := h.service.Get(jobID)
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, NewJobResponse(fetchedJob))
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
