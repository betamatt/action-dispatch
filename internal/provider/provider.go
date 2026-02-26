package provider

import "context"

// Runner represents a provisioned runner instance in a cloud provider.
type Runner struct {
	// ID is the provider-specific identifier (e.g. GCP instance name, EC2 instance ID).
	ID string

	// Name is the GitHub runner name, used for correlation.
	Name string

	// Pool is the pool this runner belongs to.
	Pool string

	// Status reflects the provider-level state of the instance.
	Status RunnerStatus

	// ProviderMeta holds provider-specific metadata (zone, region, etc.).
	ProviderMeta map[string]string
}

type RunnerStatus string

const (
	RunnerStatusProvisioning RunnerStatus = "provisioning"
	RunnerStatusRunning      RunnerStatus = "running"
	RunnerStatusTerminating  RunnerStatus = "terminating"
	RunnerStatusTerminated   RunnerStatus = "terminated"
)

// RunnerOpts contains everything a provider needs to create a runner instance.
type RunnerOpts struct {
	// Name for the runner (used as both instance name and GitHub runner name).
	Name string

	// Pool name this runner belongs to.
	Pool string

	// Labels are the GitHub runner labels.
	Labels []string

	// JITConfig is the base64-encoded JIT runner configuration from GitHub.
	// The runner binary uses this instead of a registration token.
	JITConfig string

	// RegistryToken is a short-lived token for authenticating to the container
	// registry (e.g. GHCR). For GitHub Apps this is the installation access token.
	// If empty, the startup script skips docker login (public images only).
	RegistryToken string
}

// Provider is the cloud-agnostic interface for managing runner instances.
// Each cloud (GCP, AWS, Azure) implements this to create/destroy VMs.
type Provider interface {
	// CreateRunner provisions a new runner instance. The provider should start
	// a VM that boots, installs/runs the GitHub runner binary with the JIT config,
	// and then self-terminates when the job completes.
	CreateRunner(ctx context.Context, opts RunnerOpts) (Runner, error)

	// DeleteRunner terminates a runner instance by its provider-specific ID.
	DeleteRunner(ctx context.Context, id string) error

	// ListRunners returns all runner instances managed by this provider.
	// Used by the reconciler to detect orphans and track state.
	ListRunners(ctx context.Context) ([]Runner, error)
}
