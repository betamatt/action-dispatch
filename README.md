# action-dispatch

Ephemeral GitHub Actions self-hosted runner provisioner for Google Cloud.

action-dispatch listens for GitHub webhook events and dynamically creates
short-lived GCE virtual machines to run jobs. Each VM boots
[Container-Optimized OS](https://cloud.google.com/container-optimized-os/docs),
pulls a Docker image containing the GitHub Actions runner, executes exactly one
job, and shuts itself down.

## How it works

```
GitHub (workflow_job queued)
  → action-dispatch (Cloud Run)
    → registers JIT runner via GitHub API
    → creates GCE VM (Container-Optimized OS)
      → VM boots, pulls runner Docker image
      → runner picks up one job, runs it, exits
      → VM shuts down
  → action-dispatch (workflow_job completed)
    → deletes VM if still running (safety net)
```

Runners are fully ephemeral — every job gets a clean VM. There is no persistent
state, no shared runner infrastructure, and no image to maintain (unless you want
to customize the runner image).

## Quick start

1. [Create a GitHub App](docs/setup.md#1-create-a-github-app) with
   `workflow_job` webhook events and `administration:write` permission.
2. [Set up GCP resources](docs/setup.md#2-set-up-gcp-resources) — service
   account, IAM roles, Secret Manager.
3. [Write a config file](docs/setup.md#3-configure-action-dispatch) and store it
   in Secret Manager.
4. [Deploy to Cloud Run](docs/setup.md#4-deploy-to-cloud-run).
5. Point your GitHub App's webhook URL to
   `https://<your-service>.run.app/webhook`.

See [docs/setup.md](docs/setup.md) for the full walkthrough.

## Configuration

```yaml
github:
  app_id: 12345
  installation_id: 67890
  private_key_path: /secrets/app.pem
  webhook_secret: your-webhook-secret

pools:
  - name: default
    labels: [self-hosted, linux, x64]
    provider: gcp
    max_runners: 10
    idle_runners: 0
    gcp:
      project: my-project
      zone: us-central1-a
      machine_type: e2-standard-4
      spot: true
      disk_size_gb: 50
```

Use the pool in a workflow:

```yaml
jobs:
  build:
    runs-on: [self-hosted, linux, x64]
```

See [config.example.yaml](config.example.yaml) for all options including ARM64
pools, custom networks, and service accounts.

## Runner images

By default, pools use
[`ghcr.io/actions/actions-runner:latest`](https://github.com/actions/runner/pkgs/container/actions-runner) —
the official GitHub Actions runner Docker image. It's multi-arch (amd64 + arm64)
and contains the runner binary, Docker, and container hooks.

The default image is minimal. If your workflows need additional tools, extend it:

```dockerfile
FROM ghcr.io/actions/actions-runner:latest
RUN apt-get update && apt-get install -y \
    build-essential git-lfs jq curl unzip zstd
```

Set `runner_image` in the pool config to use your custom image:

```yaml
gcp:
  runner_image: ghcr.io/my-org/actions-runner:latest
```

Private GHCR images are supported — action-dispatch authenticates using GitHub
App installation tokens automatically.

## Architecture

See [docs/architecture.md](docs/architecture.md) for details on component
design, the runner lifecycle, spot instance handling, and security
considerations.

## Development

```bash
# Build
go build ./cmd/action-dispatch

# Run locally
./action-dispatch --config=config.yaml --addr=:8080

# Run tests
go test ./...
```

## License

See [LICENSE](LICENSE).
