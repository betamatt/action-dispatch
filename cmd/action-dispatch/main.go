package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/betamatt/action-dispatch/internal/config"
	"github.com/betamatt/action-dispatch/internal/dispatcher"
	ghclient "github.com/betamatt/action-dispatch/internal/github"
	"github.com/betamatt/action-dispatch/internal/provider"
	"github.com/betamatt/action-dispatch/internal/provider/gcp"
	"github.com/betamatt/action-dispatch/internal/webhook"
	gogithub "github.com/google/go-github/v68/github"
)

func main() {
	var (
		configPath = flag.String("config", "config.yaml", "path to config file")
		addr       = flag.String("addr", ":8080", "listen address")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Build providers from pool configs.
	providers, err := buildProviders(cfg)
	if err != nil {
		logger.Error("failed to build providers", "error", err)
		os.Exit(1)
	}

	// Build the GitHub client. In production this would be a GitHub App
	// installation client; for now we use a placeholder.
	// TODO: implement GitHub App auth (private key → JWT → installation token)
	ghClient := gogithub.NewClient(nil)
	registrar := ghclient.NewRegistrar(ghClient)

	// Wire up the dispatcher.
	d := dispatcher.New(cfg, providers, registrar, logger)

	// Wire up the webhook handler.
	handler := webhook.NewHandler([]byte(cfg.GitHub.WebhookSecret), d, logger)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("starting server", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}

func buildProviders(cfg *config.Config) (map[string]provider.Provider, error) {
	providers := make(map[string]provider.Provider)

	for _, pool := range cfg.Pools {
		if _, exists := providers[pool.Provider]; exists {
			continue
		}

		switch pool.Provider {
		case "gcp":
			p, err := gcp.New(pool.GCP)
			if err != nil {
				return nil, fmt.Errorf("building gcp provider for pool %q: %w", pool.Name, err)
			}
			providers["gcp"] = p
		default:
			return nil, fmt.Errorf("unknown provider %q in pool %q", pool.Provider, pool.Name)
		}
	}

	return providers, nil
}
