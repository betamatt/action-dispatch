package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GitHub GitHubConfig `yaml:"github"`
	Pools  []Pool       `yaml:"pools"`
}

type GitHubConfig struct {
	AppID          int64  `yaml:"app_id"`
	PrivateKeyPath string `yaml:"private_key_path"`
	WebhookSecret  string `yaml:"webhook_secret"`
}

type Pool struct {
	Name       string   `yaml:"name"`
	Labels     []string `yaml:"labels"`
	Provider   string   `yaml:"provider"`
	MaxRunners int      `yaml:"max_runners"`
	IdleRunners int     `yaml:"idle_runners"`

	// Provider-specific config, keyed by provider name.
	GCP *GCPConfig `yaml:"gcp,omitempty"`
}

type GCPConfig struct {
	Project        string `yaml:"project"`
	Zone           string `yaml:"zone"`
	MachineType    string `yaml:"machine_type"`
	DiskSizeGB     int    `yaml:"disk_size_gb"`
	Spot           bool   `yaml:"spot"`
	Network        string `yaml:"network"`
	Subnet         string `yaml:"subnet"`
	ServiceAccount string `yaml:"service_account"`

	// RunnerImage is the Docker image that contains the GitHub Actions runner.
	// The VM boots Container-Optimized OS and runs this image.
	RunnerImage string `yaml:"runner_image"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.GitHub.AppID == 0 {
		return fmt.Errorf("github.app_id is required")
	}
	if c.GitHub.PrivateKeyPath == "" {
		return fmt.Errorf("github.private_key_path is required")
	}

	if len(c.Pools) == 0 {
		return fmt.Errorf("at least one pool is required")
	}

	for i, p := range c.Pools {
		if p.Name == "" {
			return fmt.Errorf("pools[%d].name is required", i)
		}
		if len(p.Labels) == 0 {
			return fmt.Errorf("pools[%d].labels is required", i)
		}
		if p.Provider == "" {
			return fmt.Errorf("pools[%d].provider is required", i)
		}
		if p.MaxRunners <= 0 {
			return fmt.Errorf("pools[%d].max_runners must be > 0", i)
		}
	}

	return nil
}

// MatchPool finds the first pool whose labels are a subset of the requested labels.
func (c *Config) MatchPool(requestedLabels []string) *Pool {
	labelSet := make(map[string]struct{}, len(requestedLabels))
	for _, l := range requestedLabels {
		labelSet[l] = struct{}{}
	}

	for i, p := range c.Pools {
		match := true
		for _, pl := range p.Labels {
			if _, ok := labelSet[pl]; !ok {
				match = false
				break
			}
		}
		if match {
			return &c.Pools[i]
		}
	}
	return nil
}
