package dispatcher

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/betamatt/action-dispatch/internal/config"
	"github.com/betamatt/action-dispatch/internal/github"
	"github.com/betamatt/action-dispatch/internal/provider"
)

// Dispatcher decides when to provision and terminate runners.
type Dispatcher struct {
	cfg       *config.Config
	providers map[string]provider.Provider
	registrar *github.Registrar
	logger    *slog.Logger
}

func New(cfg *config.Config, providers map[string]provider.Provider, registrar *github.Registrar, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		cfg:       cfg,
		providers: providers,
		registrar: registrar,
		logger:    logger,
	}
}

// JobEvent represents a workflow_job queued event.
type JobEvent struct {
	Owner  string
	Repo   string
	Labels []string
}

// CompletedEvent represents a workflow_job completed event.
type CompletedEvent struct {
	RunnerName string
}

// HandleQueued is called when a workflow_job is queued. It finds a matching pool,
// generates a JIT config, and provisions a runner.
func (d *Dispatcher) HandleQueued(ctx context.Context, event *JobEvent) error {
	pool := d.cfg.MatchPool(event.Labels)
	if pool == nil {
		d.logger.Info("no matching pool for labels", "labels", event.Labels)
		return nil
	}

	prov, ok := d.providers[pool.Provider]
	if !ok {
		return fmt.Errorf("unknown provider %q for pool %q", pool.Provider, pool.Name)
	}

	// Check capacity.
	runners, err := prov.ListRunners(ctx)
	if err != nil {
		return fmt.Errorf("listing runners for pool %q: %w", pool.Name, err)
	}

	poolRunners := countPoolRunners(runners, pool.Name)
	if poolRunners >= pool.MaxRunners {
		d.logger.Warn("pool at capacity", "pool", pool.Name, "current", poolRunners, "max", pool.MaxRunners)
		return nil
	}

	// Generate a unique runner name.
	name := fmt.Sprintf("ad-%s-%d", pool.Name, time.Now().UnixMilli())

	// Get JIT config from GitHub.
	jit, err := d.registrar.GenerateJITConfig(ctx, event.Owner, event.Repo, name, pool.Labels)
	if err != nil {
		return fmt.Errorf("generating JIT config: %w", err)
	}

	d.logger.Info("provisioning runner",
		"name", name,
		"pool", pool.Name,
		"runner_id", jit.RunnerID,
	)

	// Provision the cloud instance.
	runner, err := prov.CreateRunner(ctx, provider.RunnerOpts{
		Name:      name,
		Pool:      pool.Name,
		Labels:    pool.Labels,
		JITConfig: jit.EncodedJITConfig,
	})
	if err != nil {
		// Best-effort: remove the GitHub runner registration since we failed to provision.
		_ = d.registrar.RemoveRunner(ctx, event.Owner, event.Repo, jit.RunnerID)
		return fmt.Errorf("creating runner instance: %w", err)
	}

	d.logger.Info("runner provisioned", "name", name, "id", runner.ID)
	return nil
}

// HandleCompleted is called when a workflow_job completes. For JIT runners,
// the runner process exits after one job, so the VM should self-terminate.
// This is a safety net to clean up if that doesn't happen.
func (d *Dispatcher) HandleCompleted(ctx context.Context, event *CompletedEvent) error {
	if event.RunnerName == "" {
		return nil
	}

	// Find which provider owns this runner by checking all providers.
	for provName, prov := range d.providers {
		runners, err := prov.ListRunners(ctx)
		if err != nil {
			d.logger.Warn("failed to list runners for cleanup", "provider", provName, "error", err)
			continue
		}

		for _, r := range runners {
			if r.Name == event.RunnerName {
				d.logger.Info("cleaning up completed runner", "name", r.Name, "provider", provName)
				if err := prov.DeleteRunner(ctx, r.ID); err != nil {
					return fmt.Errorf("deleting runner %s: %w", r.ID, err)
				}
				return nil
			}
		}
	}

	d.logger.Debug("runner not found for cleanup (may have self-terminated)", "name", event.RunnerName)
	return nil
}

func countPoolRunners(runners []provider.Runner, pool string) int {
	count := 0
	for _, r := range runners {
		if strings.EqualFold(r.Pool, pool) && r.Status != provider.RunnerStatusTerminated {
			count++
		}
	}
	return count
}
