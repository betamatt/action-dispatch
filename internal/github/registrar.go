package github

import (
	"context"
	"fmt"

	gogithub "github.com/google/go-github/v68/github"
)

// Registrar handles GitHub runner registration via JIT configs.
type Registrar struct {
	client *gogithub.Client
}

// NewRegistrar creates a Registrar using the provided GitHub client.
// The client should be authenticated as a GitHub App installation.
func NewRegistrar(client *gogithub.Client) *Registrar {
	return &Registrar{client: client}
}

// JITConfig holds the response from generating a JIT runner configuration.
type JITConfig struct {
	// EncodedJITConfig is the base64-encoded config passed to the runner binary.
	EncodedJITConfig string

	// RunnerID is the GitHub-assigned runner ID.
	RunnerID int64
}

// GenerateJITConfig calls the GitHub API to generate a just-in-time runner
// configuration. This returns a one-time-use encoded config that the runner
// binary consumes via `./run.sh --jitconfig <encoded>`.
func (r *Registrar) GenerateJITConfig(ctx context.Context, owner, repo, name string, labels []string) (*JITConfig, error) {
	req := &gogithub.GenerateJITConfigRequest{
		Name:          name,
		RunnerGroupID: 1, // Default runner group
		Labels:        labels,
	}

	jitConfig, resp, err := r.client.Actions.GenerateRepoJITConfig(ctx, owner, repo, req)
	if err != nil {
		return nil, fmt.Errorf("generating JIT config for %s/%s: %w", owner, repo, err)
	}
	defer resp.Body.Close()

	return &JITConfig{
		EncodedJITConfig: jitConfig.GetEncodedJITConfig(),
		RunnerID:         jitConfig.GetRunner().GetID(),
	}, nil
}

// GenerateOrgJITConfig is the org-level equivalent of GenerateJITConfig.
func (r *Registrar) GenerateOrgJITConfig(ctx context.Context, org, name string, labels []string) (*JITConfig, error) {
	req := &gogithub.GenerateJITConfigRequest{
		Name:          name,
		RunnerGroupID: 1,
		Labels:        labels,
	}

	jitConfig, resp, err := r.client.Actions.GenerateOrgJITConfig(ctx, org, req)
	if err != nil {
		return nil, fmt.Errorf("generating JIT config for org %s: %w", org, err)
	}
	defer resp.Body.Close()

	return &JITConfig{
		EncodedJITConfig: jitConfig.GetEncodedJITConfig(),
		RunnerID:         jitConfig.GetRunner().GetID(),
	}, nil
}

// RemoveRunner deletes a runner registration from GitHub.
func (r *Registrar) RemoveRunner(ctx context.Context, owner, repo string, runnerID int64) error {
	resp, err := r.client.Actions.RemoveRunner(ctx, owner, repo, runnerID)
	if err != nil {
		return fmt.Errorf("removing runner %d from %s/%s: %w", runnerID, owner, repo, err)
	}
	defer resp.Body.Close()
	return nil
}
