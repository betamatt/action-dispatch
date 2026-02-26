package gcp

import (
	"context"
	"fmt"

	"github.com/betamatt/action-dispatch/internal/config"
	"github.com/betamatt/action-dispatch/internal/provider"
)

// Provider implements the provider.Provider interface for Google Cloud Compute Engine.
type Provider struct {
	cfg *config.GCPConfig
	// TODO: add Compute Engine client
}

func New(cfg *config.GCPConfig) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("gcp config is required")
	}
	return &Provider{cfg: cfg}, nil
}

func (p *Provider) CreateRunner(ctx context.Context, opts provider.RunnerOpts) (provider.Runner, error) {
	// TODO: Create a Compute Engine instance with:
	// - startup script that downloads runner binary + runs with --jitconfig opts.JITConfig
	// - instance metadata for runner name, pool, labels
	// - machine type from pool config
	// - spot/preemptible if configured
	// - self-delete on shutdown (or shutdown script)
	return provider.Runner{}, fmt.Errorf("gcp provider not yet implemented")
}

func (p *Provider) DeleteRunner(ctx context.Context, id string) error {
	// TODO: Delete the Compute Engine instance by name/ID.
	return fmt.Errorf("gcp provider not yet implemented")
}

func (p *Provider) ListRunners(ctx context.Context) ([]provider.Runner, error) {
	// TODO: List Compute Engine instances filtered by a label (e.g. managed-by=action-dispatch).
	return nil, fmt.Errorf("gcp provider not yet implemented")
}
