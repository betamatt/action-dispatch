# Architecture

## Overview

action-dispatch is a stateless webhook-driven service that provisions ephemeral
GitHub Actions runners on Google Cloud Compute Engine. It receives `workflow_job`
events from GitHub, creates a VM for each queued job, and tears it down when the
job completes.

```
┌──────────┐  webhook POST   ┌──────────────────┐  GCE REST API  ┌──────────────┐
│  GitHub   │───────────────→│  action-dispatch  │──────────────→│  GCE (COS VM) │
│           │                │  (Cloud Run)      │               │  + Docker     │
│  workflow │  completed     │                   │  delete       │  + Runner     │
│  _job     │───────────────→│                   │──────────────→│               │
└──────────┘                 └──────────────────┘               └──────────────┘
                                    │
                                    │ GitHub API
                                    ↓
                             ┌──────────────┐
                             │  GitHub API   │
                             │  - JIT config │
                             │  - Tokens     │
                             └──────────────┘
```

## Components

### Webhook handler (`internal/webhook`)

Receives HTTP POST requests from GitHub, validates the HMAC-SHA256 signature
using the configured webhook secret, parses the `workflow_job` event payload,
and routes it to the dispatcher.

Only the `workflow_job` event type is processed. Other events are acknowledged
with 200 OK and ignored.

### Dispatcher (`internal/dispatcher`)

The dispatcher contains the core provisioning logic. It handles two event
actions:

**`queued`** — a new job needs a runner:
1. Match the job's requested labels against configured pools (subset match).
2. Check capacity — if the pool is at `max_runners`, drop the event.
3. Generate a unique runner name: `ad-{pool}-{timestamp}`.
4. Call the GitHub API to generate a JIT (just-in-time) runner configuration.
5. Mint a short-lived installation token for GHCR authentication.
6. Ask the cloud provider to create a runner VM.
7. On failure, clean up the GitHub runner registration.

**`completed`** — a job finished:
1. Search all providers for a runner matching the completed runner name.
2. Delete the VM if found.
3. This is a safety net — runners normally self-terminate.

### GitHub registrar (`internal/github`)

Wraps the GitHub API client for runner registration operations:

- **GenerateJITConfig** — creates a one-time-use encoded configuration that the
  runner binary consumes via `./run.sh --jitconfig <encoded>`. The runner
  registers itself, picks up exactly one job, and then its registration expires.
- **CreateInstallationToken** — mints a short-lived GitHub App installation
  token. This token is used by the runner VM to authenticate `docker login` to
  GHCR for pulling private runner images.
- **RemoveRunner** — deletes a runner registration from GitHub. Used for cleanup
  when VM creation fails.

### Provider interface (`internal/provider`)

A cloud-agnostic interface that any cloud provider must implement:

```go
type Provider interface {
    CreateRunner(ctx context.Context, opts RunnerOpts) (Runner, error)
    DeleteRunner(ctx context.Context, id string) error
    ListRunners(ctx context.Context) ([]Runner, error)
}
```

`RunnerOpts` carries everything a provider needs: runner name, pool, labels,
JIT config (base64-encoded), and an optional registry token for private images.

### GCP provider (`internal/provider/gcp`)

The GCP implementation creates Compute Engine instances running Container-
Optimized OS (COS). It calls the GCE REST API directly using `net/http` with
Application Default Credentials via `golang.org/x/oauth2/google`.

Key behaviors:
- Instances are labeled with `managed-by=action-dispatch` for filtering.
- Operations (insert, delete) use the GCE `/wait` endpoint for completion.
- Spot instances are configured with `instanceTerminationAction: DELETE` so
  preempted VMs are automatically cleaned up by GCE.

---

## Runner lifecycle

This is the full sequence from a workflow being triggered to the runner VM being
deleted. Every job gets its own dedicated, clean VM.

### Phase 1: Job queued

```
Developer pushes code
  → GitHub evaluates workflow YAML
  → Job needs `runs-on: [self-hosted, linux, x64]`
  → GitHub sends `workflow_job` webhook with action=queued
```

action-dispatch receives the webhook and:

1. **Validates** the HMAC signature against the configured webhook secret.
2. **Matches a pool** — finds the first pool whose labels are a subset of the
   job's requested labels. For example, a pool with labels
   `[self-hosted, linux, x64]` matches a job requesting
   `[self-hosted, linux, x64, gpu]`.
3. **Checks capacity** — counts active (non-terminated) runners in the pool. If
   at `max_runners`, the event is dropped and GitHub will retry.
4. **Generates a JIT config** — calls the GitHub API to create a just-in-time
   runner registration. This returns a base64-encoded blob that the runner binary
   consumes. JIT runners are inherently ephemeral: they handle one job and then
   their registration expires.
5. **Mints an installation token** — creates a short-lived GitHub App
   installation token for GHCR authentication. This is best-effort; if it fails,
   the runner can still pull public images.
6. **Creates a GCE instance** — sends an `instances.insert` request to the
   Compute Engine API and waits for the operation to complete.

### Phase 2: VM boots and runner starts

The GCE instance boots Container-Optimized OS, which has Docker pre-installed.
The startup script (injected via instance metadata) runs:

```bash
# 1. Authenticate to GHCR (if token provided)
echo "$TOKEN" | docker login ghcr.io -u x-access-token --password-stdin

# 2. Pull the runner image (with retry)
docker pull ghcr.io/actions/actions-runner:latest

# 3. Run the container with JIT config
docker run --rm \
    -e ACTIONS_RUNNER_INPUT_JITCONFIG='<encoded>' \
    -e RUNNER_NAME='ad-default-1709123456789' \
    ghcr.io/actions/actions-runner:latest

# 4. Shut down when container exits
shutdown -h now
```

Inside the container, the GitHub Actions runner binary:
1. Reads `ACTIONS_RUNNER_INPUT_JITCONFIG` from the environment.
2. Registers itself with GitHub using the JIT config.
3. Picks up the waiting job.
4. Executes the job's steps.
5. Exits (JIT runners are single-use by design).

### Phase 3: Job completes, VM terminates

When the runner process exits, the Docker container stops, and the startup
script calls `shutdown -h now`. The VM stops.

Concurrently, GitHub sends a `workflow_job` webhook with `action=completed`.
action-dispatch receives this and:

1. Searches all providers for a runner matching the completed runner's name.
2. If found, calls `DeleteRunner` to terminate the instance.
3. If not found (VM already shut down and was cleaned up), logs and moves on.

This completed handler is a **safety net**. The normal path is the VM shutting
itself down. The webhook-triggered deletion handles cases where:
- The startup script's `shutdown` command failed.
- The VM is stuck in a running state.
- The container exited but the VM didn't stop.

### Phase 4: Cleanup

For non-spot instances, the GCE instance remains in a TERMINATED state after
shutdown. The `DeleteRunner` call from the completed handler removes it. The
boot disk is auto-deleted with the instance.

For spot instances configured with `instanceTerminationAction: DELETE`, GCE
automatically deletes the instance when it stops or is preempted, so no
further cleanup is needed.

### Lifecycle timeline

```
t=0s     GitHub sends workflow_job (queued)
t=0.1s   action-dispatch validates webhook, matches pool
t=0.5s   JIT config generated, installation token minted
t=1s     GCE instance.insert API call made
t=5-15s  VM boots (COS is minimal, boots fast)
t=15-30s Docker pulls runner image (depends on image size and caching)
t=30s    Runner registers with GitHub, picks up job
t=30s+   Job executes (varies by workload)
t=end    Runner exits, container stops
t=end+1s startup script calls shutdown -h now
t=end+2s GitHub sends workflow_job (completed)
t=end+3s action-dispatch calls instances.delete (safety net)
```

---

## Spot instance handling

When `spot: true` is configured, VMs are created as
[Spot VMs](https://cloud.google.com/compute/docs/instances/spot):

- **Cost**: up to 60-91% cheaper than on-demand.
- **Preemption**: GCE can terminate the VM at any time.
- **Configuration**: `onHostMaintenance: TERMINATE`,
  `instanceTerminationAction: DELETE`, `automaticRestart: false`.
- **On preemption**: GCE deletes the instance automatically. The GitHub job
  will fail and can be retried (either manually or via workflow retry logic).

Spot is appropriate for CI workloads that are tolerant of occasional preemption.
For time-sensitive or long-running jobs, use on-demand instances.

---

## Architecture decisions

### COS + Docker vs custom VM images

We use Container-Optimized OS as a thin Docker host instead of building custom
VM images with Packer. Trade-offs:

| | COS + Docker | Custom VM image |
|---|---|---|
| **Image maintenance** | None — Google maintains COS, you maintain a Dockerfile | You maintain Packer builds |
| **Boot time** | ~15s boot + ~15s pull | ~15s boot (tools pre-baked) |
| **Reproducibility** | Dockerfile is versioned, deterministic | Packer config is versioned |
| **Flexibility** | Any tools, any base image | Tied to VM image format |
| **First-run cost** | Docker pull on every VM | No pull needed |

The Docker pull cost can be mitigated by using a smaller runner image or by
pre-pulling images to a GCE disk snapshot (future optimization).

### Stateless design

action-dispatch has no database and no persistent state. All runner tracking is
done by querying the cloud provider's API (filtered by the `managed-by` label).
This means:

- **No single point of failure** — Cloud Run can scale to zero and restart
  without losing state.
- **No data migration** — upgrades are just redeploying a new image.
- **Eventual consistency** — there's a window between VM creation and the
  completed webhook where we rely on the cloud API to report accurate state.

### Direct REST API vs SDK

The GCP provider calls the Compute Engine REST API directly with `net/http`
instead of using the `cloud.google.com/go/compute` SDK. This keeps the
dependency tree small and avoids the SDK's heavy transitive dependencies.

---

## Security model

### Webhook validation

Every incoming webhook is verified using HMAC-SHA256 with the configured
webhook secret. Invalid signatures are rejected with 401.

### Runner isolation

Each job runs in its own VM with its own boot disk. There is no shared state
between runners. The VM is deleted after the job completes.

### Credentials on the runner VM

The runner VM receives two sensitive values via the startup script (instance
metadata):

1. **JIT config** — a one-time-use token that expires after the runner
   registers. It cannot be reused.
2. **GHCR token** — a short-lived GitHub App installation token (1 hour TTL).
   Used only for `docker login` to pull the runner image.

Neither credential persists beyond the VM's lifetime. Instance metadata is only
accessible from within the VM.

### Service account scope

The Cloud Run service account needs minimal permissions:
- `compute.instanceAdmin.v1` — create and delete VMs.
- `iam.serviceAccountUser` — attach a service account to VMs.
- `secretmanager.secretAccessor` — read the config from Secret Manager.

The runner VMs can have their own service account with permissions scoped to
what workflows need (e.g., Artifact Registry read for Docker images).

---

## Pool matching

Pools are matched by label subset. A pool matches a job if **all** of the pool's
labels appear in the job's `runs-on` labels.

```yaml
# Pool config
pools:
  - name: gpu
    labels: [self-hosted, linux, gpu]
```

```yaml
# Workflow
jobs:
  train:
    runs-on: [self-hosted, linux, gpu, x64]  # matches: gpu pool labels are a subset
  build:
    runs-on: [self-hosted, linux, x64]        # no match: missing "gpu"
```

The first matching pool wins. Order your pools from most specific to least
specific.

---

## Scaling limits

- **max_runners** — per-pool cap on concurrent runners. Checked by listing
  active GCE instances with the pool's label.
- **Cloud Run concurrency** — the webhook handler processes requests
  concurrently. Multiple queued events can be handled in parallel.
- **GCE API quotas** — instance creation is subject to GCE project quotas
  (CPUs, IP addresses, instances per zone).

If a pool is at capacity, the queued event is dropped. GitHub will eventually
time out the job (default: 6 hours) and can be configured to retry.
