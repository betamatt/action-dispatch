package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/betamatt/action-dispatch/internal/config"
	"github.com/betamatt/action-dispatch/internal/provider"
	"golang.org/x/oauth2/google"
)

const (
	computeBasePath = "https://compute.googleapis.com/compute/v1"
	computeScope    = "https://www.googleapis.com/auth/compute"

	// Container-Optimized OS image. GCE resolves family to the latest stable.
	cosImageURL = "projects/cos-cloud/global/images/family/cos-stable"

	// DefaultRunnerImage is the official GitHub Actions runner Docker image.
	// It's minimal (runner binary + Docker on Debian). Users should extend it
	// or provide their own image with additional tools their workflows need.
	DefaultRunnerImage = "ghcr.io/actions/actions-runner:latest"

	// Labels applied to all managed instances for filtering.
	managedByLabel = "managed-by"
	managedByValue = "action-dispatch"
	poolLabel      = "pool"
	runnerLabel    = "runner-name"
)

// Provider implements provider.Provider for Google Cloud Compute Engine.
// It creates COS VMs that pull and run the user's Docker image.
type Provider struct {
	cfg    *config.GCPConfig
	client *http.Client
}

func New(ctx context.Context, cfg *config.GCPConfig) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("gcp config is required")
	}
	applyDefaults(cfg)

	client, err := google.DefaultClient(ctx, computeScope)
	if err != nil {
		return nil, fmt.Errorf("creating authenticated client: %w", err)
	}

	return &Provider{cfg: cfg, client: client}, nil
}

// NewWithClient creates a Provider with a caller-supplied HTTP client (for testing).
func NewWithClient(cfg *config.GCPConfig, client *http.Client) (*Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("gcp config is required")
	}
	applyDefaults(cfg)
	return &Provider{cfg: cfg, client: client}, nil
}

func applyDefaults(cfg *config.GCPConfig) {
	if cfg.RunnerImage == "" {
		cfg.RunnerImage = DefaultRunnerImage
	}
}

func (p *Provider) CreateRunner(ctx context.Context, opts provider.RunnerOpts) (provider.Runner, error) {
	diskSizeGB := int64(p.cfg.DiskSizeGB)
	if diskSizeGB == 0 {
		diskSizeGB = 50
	}

	instance := gceInstance{
		Name:        opts.Name,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", p.cfg.Zone, p.cfg.MachineType),
		Disks: []gceAttachedDisk{
			{
				InitializeParams: &gceDiskInitParams{
					DiskSizeGb:  diskSizeGB,
					SourceImage: cosImageURL,
				},
				AutoDelete: true,
				Boot:       true,
				Type:       "PERSISTENT",
			},
		},
		NetworkInterfaces: []gceNetworkInterface{
			p.buildNetworkInterface(),
		},
		Labels: map[string]string{
			managedByLabel: managedByValue,
			poolLabel:      sanitizeLabel(opts.Pool),
			runnerLabel:    sanitizeLabel(opts.Name),
		},
		Metadata: &gceMetadata{
			Items: []gceMetadataItem{
				{Key: "startup-script", Value: startupScript(p.cfg.RunnerImage, opts)},
			},
		},
	}

	if p.cfg.ServiceAccount != "" {
		instance.ServiceAccounts = []gceServiceAccount{
			{
				Email:  p.cfg.ServiceAccount,
				Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
			},
		}
	}

	if p.cfg.Spot {
		instance.Scheduling = &gceScheduling{
			ProvisioningModel:         "SPOT",
			OnHostMaintenance:         "TERMINATE",
			AutomaticRestart:          false,
			InstanceTerminationAction: "DELETE",
		}
	}

	url := fmt.Sprintf("%s/projects/%s/zones/%s/instances", computeBasePath, p.cfg.Project, p.cfg.Zone)
	op, err := p.doRequest(ctx, "POST", url, instance)
	if err != nil {
		return provider.Runner{}, fmt.Errorf("inserting instance %s: %w", opts.Name, err)
	}

	if err := p.waitZoneOp(ctx, op.Name); err != nil {
		return provider.Runner{}, fmt.Errorf("waiting for instance %s creation: %w", opts.Name, err)
	}

	return provider.Runner{
		ID:     opts.Name,
		Name:   opts.Name,
		Pool:   opts.Pool,
		Status: provider.RunnerStatusProvisioning,
		ProviderMeta: map[string]string{
			"project": p.cfg.Project,
			"zone":    p.cfg.Zone,
		},
	}, nil
}

func (p *Provider) DeleteRunner(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/projects/%s/zones/%s/instances/%s", computeBasePath, p.cfg.Project, p.cfg.Zone, id)
	op, err := p.doRequest(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("deleting instance %s: %w", id, err)
	}

	if err := p.waitZoneOp(ctx, op.Name); err != nil {
		return fmt.Errorf("waiting for instance %s deletion: %w", id, err)
	}

	return nil
}

func (p *Provider) ListRunners(ctx context.Context) ([]provider.Runner, error) {
	filter := fmt.Sprintf("labels.%s=%s", managedByLabel, managedByValue)
	url := fmt.Sprintf("%s/projects/%s/zones/%s/instances?filter=%s",
		computeBasePath, p.cfg.Project, p.cfg.Zone, filter)

	var runners []provider.Runner
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := p.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("listing instances: %w", err)
		}

		var list gceInstanceList
		if err := decodeResponse(resp, &list); err != nil {
			return nil, fmt.Errorf("listing instances: %w", err)
		}

		for _, inst := range list.Items {
			runners = append(runners, provider.Runner{
				ID:     inst.Name,
				Name:   inst.Labels[runnerLabel],
				Pool:   inst.Labels[poolLabel],
				Status: mapGCEStatus(inst.Status),
				ProviderMeta: map[string]string{
					"project": p.cfg.Project,
					"zone":    p.cfg.Zone,
				},
			})
		}
		url = list.NextPageToken
		if url != "" {
			url = fmt.Sprintf("%s/projects/%s/zones/%s/instances?filter=%s&pageToken=%s",
				computeBasePath, p.cfg.Project, p.cfg.Zone, filter, url)
		}
	}

	return runners, nil
}

func (p *Provider) waitZoneOp(ctx context.Context, opName string) error {
	url := fmt.Sprintf("%s/projects/%s/zones/%s/operations/%s/wait",
		computeBasePath, p.cfg.Project, p.cfg.Zone, opName)

	for {
		req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
		if err != nil {
			return err
		}

		resp, err := p.client.Do(req)
		if err != nil {
			return fmt.Errorf("waiting for operation %s: %w", opName, err)
		}

		var op gceOperation
		if err := decodeResponse(resp, &op); err != nil {
			return fmt.Errorf("waiting for operation %s: %w", opName, err)
		}

		if op.Status == "DONE" {
			if op.Error != nil && len(op.Error.Errors) > 0 {
				return fmt.Errorf("operation %s failed: %s", opName, op.Error.Errors[0].Message)
			}
			return nil
		}

		// The /wait endpoint blocks server-side for up to 2 minutes,
		// so we only need a short sleep as a fallback.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func (p *Provider) doRequest(ctx context.Context, method, url string, body any) (*gceOperation, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	var op gceOperation
	if err := decodeResponse(resp, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

func (p *Provider) buildNetworkInterface() gceNetworkInterface {
	ni := gceNetworkInterface{
		AccessConfigs: []gceAccessConfig{
			{
				Name:        "External NAT",
				NetworkTier: "STANDARD",
				Type:        "ONE_TO_ONE_NAT",
			},
		},
	}

	if p.cfg.Network != "" {
		ni.Network = p.cfg.Network
	} else {
		ni.Network = "global/networks/default"
	}

	if p.cfg.Subnet != "" {
		ni.Subnetwork = p.cfg.Subnet
	}

	return ni
}

func decodeResponse(resp *http.Response, v any) error {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(v)
}

// startupScript generates a script that runs on COS to pull and execute
// the runner Docker image with the JIT config.
func startupScript(image string, opts provider.RunnerOpts) string {
	// COS comes with Docker pre-installed. The script:
	// 1. Authenticates to GHCR if a registry token is provided
	// 2. Pulls the runner image
	// 3. Runs it with the JIT config as an env var
	// 4. Shuts down the VM when the container exits
	//
	// The Docker image is expected to:
	// - Have the GitHub Actions runner installed
	// - Read ACTIONS_RUNNER_INPUT_JITCONFIG and call: ./run.sh --jitconfig "$ACTIONS_RUNNER_INPUT_JITCONFIG"
	// - Exit when the job completes (JIT runners are ephemeral by nature)

	var loginBlock string
	if opts.RegistryToken != "" {
		// GitHub App installation tokens authenticate to GHCR as "x-access-token".
		loginBlock = fmt.Sprintf(`
# Authenticate to GHCR using the GitHub App installation token.
echo '%s' | docker login ghcr.io -u x-access-token --password-stdin
`, opts.RegistryToken)
	}

	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail

# Log to serial console for debugging.
exec > >(tee /dev/ttyS0) 2>&1

echo "action-dispatch: starting runner %s for pool %s"
%s
# Pull the runner image. Retry a few times since COS networking may take a moment.
for i in 1 2 3 4 5; do
    if docker pull %s; then
        break
    fi
    echo "action-dispatch: docker pull attempt $i failed, retrying in ${i}s..."
    sleep "$i"
done

# Run the container. Pass the JIT config so the runner registers and picks up exactly one job.
docker run --rm \
    --name github-runner \
    -e ACTIONS_RUNNER_INPUT_JITCONFIG='%s' \
    -e RUNNER_NAME='%s' \
    %s

echo "action-dispatch: runner exited, shutting down VM"
shutdown -h now
`, opts.Name, opts.Pool, loginBlock, image, opts.JITConfig, opts.Name, image)
}

func mapGCEStatus(status string) provider.RunnerStatus {
	switch status {
	case "PROVISIONING", "STAGING":
		return provider.RunnerStatusProvisioning
	case "RUNNING":
		return provider.RunnerStatusRunning
	case "STOPPING", "SUSPENDING":
		return provider.RunnerStatusTerminating
	case "TERMINATED", "SUSPENDED":
		return provider.RunnerStatusTerminated
	default:
		return provider.RunnerStatusRunning
	}
}

// sanitizeLabel ensures a value is valid as a GCE label (lowercase, alphanumeric, hyphens).
func sanitizeLabel(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
