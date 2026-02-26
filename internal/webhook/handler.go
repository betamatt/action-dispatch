package webhook

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/betamatt/action-dispatch/internal/dispatcher"
	gogithub "github.com/google/go-github/v68/github"
)

// Handler receives GitHub webhook events and dispatches them.
type Handler struct {
	webhookSecret []byte
	dispatcher    *dispatcher.Dispatcher
	logger        *slog.Logger
}

func NewHandler(webhookSecret []byte, d *dispatcher.Dispatcher, logger *slog.Logger) *Handler {
	return &Handler{
		webhookSecret: webhookSecret,
		dispatcher:    d,
		logger:        logger,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	payload, err := gogithub.ValidatePayload(r, h.webhookSecret)
	if err != nil {
		h.logger.Warn("invalid webhook payload", "error", err)
		http.Error(w, "invalid payload", http.StatusUnauthorized)
		return
	}

	event, err := gogithub.ParseWebHook(gogithub.WebHookType(r), payload)
	if err != nil {
		h.logger.Warn("failed to parse webhook", "error", err)
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}

	switch e := event.(type) {
	case *gogithub.WorkflowJobEvent:
		h.handleWorkflowJob(r.Context(), w, e)
	default:
		h.logger.Debug("ignoring unhandled event type", "type", gogithub.WebHookType(r))
		w.WriteHeader(http.StatusOK)
	}
}

func (h *Handler) handleWorkflowJob(ctx context.Context, w http.ResponseWriter, e *gogithub.WorkflowJobEvent) {
	action := e.GetAction()
	job := e.GetWorkflowJob()

	logger := h.logger.With(
		"action", action,
		"job_id", job.GetID(),
		"repo", e.GetRepo().GetFullName(),
		"labels", job.Labels,
	)

	switch action {
	case "queued":
		logger.Info("job queued, requesting runner")
		err := h.dispatcher.HandleQueued(ctx, &dispatcher.JobEvent{
			Owner:  e.GetRepo().GetOwner().GetLogin(),
			Repo:   e.GetRepo().GetName(),
			Labels: job.Labels,
		})
		if err != nil {
			logger.Error("failed to handle queued job", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

	case "in_progress":
		logger.Info("job in progress", "runner_name", job.GetRunnerName())

	case "completed":
		logger.Info("job completed", "runner_name", job.GetRunnerName(), "conclusion", job.GetConclusion())
		err := h.dispatcher.HandleCompleted(ctx, &dispatcher.CompletedEvent{
			RunnerName: job.GetRunnerName(),
		})
		if err != nil {
			logger.Error("failed to handle completed job", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}
