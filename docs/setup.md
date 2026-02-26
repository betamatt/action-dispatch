# Setup guide

This guide walks through deploying action-dispatch from scratch on Google Cloud
with a GitHub App for webhook delivery and runner registration.

## Prerequisites

- A Google Cloud project with billing enabled.
- The `gcloud` CLI installed and authenticated.
- A GitHub organization (or repository) where you want self-hosted runners.
- Docker (for local builds) or Cloud Build enabled in your GCP project.

---

## 1. Create a GitHub App

action-dispatch uses a
[GitHub App](https://docs.github.com/en/apps/creating-github-apps) for
authentication. The App receives webhooks and calls the GitHub API to register
runners.

### Create the App

1. Go to your organization's settings → Developer settings → GitHub Apps →
   **New GitHub App**.
2. Fill in:
   - **Name**: `action-dispatch` (or any name).
   - **Homepage URL**: your Cloud Run URL (can update later).
   - **Webhook URL**: leave blank for now (you'll set this after deploying).
   - **Webhook secret**: generate a strong secret. Save it — you'll need it for
     the config file.
     ```bash
     openssl rand -hex 32
     ```

### Set permissions

Under **Permissions**, grant:

| Permission | Access | Why |
|---|---|---|
| **Administration** | Read & write | Register and remove runners |
| **Actions** | Read-only | Read workflow job metadata |

### Subscribe to events

Under **Subscribe to events**, check:

- **Workflow job** — this is the only event action-dispatch needs.

### Generate a private key

After creating the App:
1. Go to the App's settings page.
2. Scroll to **Private keys** → **Generate a private key**.
3. Save the `.pem` file. You'll store this in Secret Manager.

### Install the App

1. Go to the App's settings → **Install App**.
2. Install it on your organization (or specific repositories).
3. Note the **Installation ID** from the URL after installing:
   `https://github.com/organizations/{org}/settings/installations/{installation_id}`

### Record these values

You need:
- **App ID** — shown on the App's settings page.
- **Installation ID** — from the installation URL.
- **Private key** — the `.pem` file you downloaded.
- **Webhook secret** — the secret you generated.

---

## 2. Set up GCP resources

### Enable APIs

```bash
PROJECT=my-project

gcloud services enable \
    compute.googleapis.com \
    run.googleapis.com \
    artifactregistry.googleapis.com \
    secretmanager.googleapis.com \
    cloudbuild.googleapis.com \
    --project="$PROJECT"
```

### Create a service account

This service account is used by the Cloud Run service to manage GCE instances.

```bash
SA_NAME=action-dispatch
SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"

gcloud iam service-accounts create "$SA_NAME" \
    --display-name="action-dispatch runner manager" \
    --project="$PROJECT"
```

### Grant IAM roles

```bash
# Create and delete GCE instances
gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:${SA_EMAIL}" \
    --role="roles/compute.instanceAdmin.v1"

# Attach service accounts to VMs
gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:${SA_EMAIL}" \
    --role="roles/iam.serviceAccountUser"

# Read config from Secret Manager
gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:${SA_EMAIL}" \
    --role="roles/secretmanager.secretAccessor"
```

### (Optional) Create a runner service account

If your workflows need to access GCP resources (Artifact Registry, GCS, etc.),
create a separate service account for the runner VMs:

```bash
gcloud iam service-accounts create runner \
    --display-name="GitHub Actions runner" \
    --project="$PROJECT"

# Example: let runners pull from Artifact Registry
gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:runner@${PROJECT}.iam.gserviceaccount.com" \
    --role="roles/artifactregistry.reader"
```

Reference this in your pool config as `service_account`.

---

## 3. Configure action-dispatch

Create a config file based on [config.example.yaml](../config.example.yaml):

```yaml
github:
  app_id: 12345                          # Your GitHub App ID
  installation_id: 67890                 # Your installation ID
  private_key_path: /secrets/app.pem     # Path inside the container
  webhook_secret: your-webhook-secret    # The secret from step 1

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
      # service_account: runner@my-project.iam.gserviceaccount.com
```

### Store the config in Secret Manager

```bash
gcloud secrets create action-dispatch-config \
    --data-file=config.yaml \
    --project="$PROJECT"
```

### Store the GitHub App private key

```bash
gcloud secrets create action-dispatch-app-key \
    --data-file=your-app.pem \
    --project="$PROJECT"
```

### Update config to reference both secrets

In your config, set `private_key_path: /secrets/app.pem`. The deploy script
mounts the config secret; you'll mount the private key secret as well (see
deployment section).

---

## 4. Deploy to Cloud Run

### Option A: Use the deploy script

```bash
PROJECT=my-project \
SERVICE_ACCOUNT=action-dispatch@my-project.iam.gserviceaccount.com \
./deploy/cloud-run.sh
```

The script:
1. Creates an Artifact Registry repository (if needed).
2. Builds the Docker image via Cloud Build.
3. Deploys to Cloud Run with the config mounted from Secret Manager.

### Option B: Manual deployment

Build and push:

```bash
REGION=us-central1
IMAGE="${REGION}-docker.pkg.dev/${PROJECT}/action-dispatch/action-dispatch:latest"

docker build -t "$IMAGE" .
docker push "$IMAGE"
```

Deploy:

```bash
gcloud run deploy action-dispatch \
    --project="$PROJECT" \
    --region="$REGION" \
    --image="$IMAGE" \
    --service-account="action-dispatch@${PROJECT}.iam.gserviceaccount.com" \
    --port=8080 \
    --args="--config=/secrets/config.yaml" \
    --set-secrets="/secrets/config.yaml=action-dispatch-config:latest" \
    --set-secrets="/secrets/app.pem=action-dispatch-app-key:latest" \
    --min-instances=0 \
    --max-instances=3 \
    --cpu=1 \
    --memory=256Mi \
    --timeout=30s \
    --no-cpu-throttling \
    --allow-unauthenticated
```

Note the service URL from the output.

### Verify the deployment

```bash
URL=$(gcloud run services describe action-dispatch \
    --project="$PROJECT" \
    --region="$REGION" \
    --format="value(status.url)")

curl "${URL}/healthz"
# Expected: ok
```

---

## 5. Connect GitHub to action-dispatch

1. Go to your GitHub App's settings.
2. Set **Webhook URL** to `https://<your-service>.run.app/webhook`.
3. Ensure **Webhook secret** matches what's in your config.
4. Set **Webhook status** to Active.

### Test the integration

Create a workflow that targets your pool's labels:

```yaml
# .github/workflows/test-runner.yml
name: Test self-hosted runner
on: push
jobs:
  test:
    runs-on: [self-hosted, linux, x64]
    steps:
      - run: echo "Hello from action-dispatch!"
      - run: uname -a
```

Push the workflow. You should see:
1. A `workflow_job queued` event in your GitHub App's delivery log.
2. A GCE instance appear in your project (named `ad-default-{timestamp}`).
3. The job complete in GitHub Actions.
4. The GCE instance disappear.

### Troubleshooting

**Webhook delivery failing (4xx/5xx)**:
- Check Cloud Run logs: `gcloud run services logs read action-dispatch`
- Verify the webhook secret matches.
- Ensure the service is publicly accessible (`--allow-unauthenticated`).

**VM created but runner never picks up the job**:
- Check the VM's serial console in the GCP Console for startup script output.
- Verify the runner image exists and is accessible.
- Check that the JIT config is valid (not expired, correct labels).

**VM not being created**:
- Check Cloud Run logs for capacity or API errors.
- Verify the service account has `compute.instanceAdmin.v1`.
- Check GCE quotas in the target zone.

**VM not being deleted after job completes**:
- The completed webhook handler is a safety net. Check Cloud Run logs.
- Spot instances with `instanceTerminationAction: DELETE` are cleaned up by GCE.
- For on-demand instances, the startup script calls `shutdown -h now`.

---

## 6. Adding more pools

### ARM64 pool

Use a `t2a-*` machine type. The provider automatically selects the ARM64 COS
image and the default runner image is multi-arch:

```yaml
pools:
  - name: arm
    labels: [self-hosted, linux, arm64]
    provider: gcp
    max_runners: 5
    idle_runners: 0
    gcp:
      project: my-project
      zone: us-central1-a
      machine_type: t2a-standard-4
      spot: true
      disk_size_gb: 50
```

```yaml
jobs:
  build-arm:
    runs-on: [self-hosted, linux, arm64]
```

### High-memory pool

```yaml
pools:
  - name: highmem
    labels: [self-hosted, linux, x64, highmem]
    provider: gcp
    max_runners: 3
    idle_runners: 0
    gcp:
      project: my-project
      zone: us-central1-a
      machine_type: n2-highmem-8
      spot: false           # on-demand for reliability
      disk_size_gb: 200
```

```yaml
jobs:
  heavy-build:
    runs-on: [self-hosted, linux, x64, highmem]
```

### Custom runner image

```yaml
pools:
  - name: custom
    labels: [self-hosted, linux, x64, custom]
    provider: gcp
    max_runners: 10
    idle_runners: 0
    gcp:
      project: my-project
      zone: us-central1-a
      machine_type: e2-standard-4
      runner_image: ghcr.io/my-org/actions-runner:v2
      spot: true
      disk_size_gb: 50
```

---

## 7. Updating the config

Update the secret in Secret Manager and redeploy:

```bash
# Update config
gcloud secrets versions add action-dispatch-config \
    --data-file=config.yaml \
    --project="$PROJECT"

# Redeploy (Cloud Run picks up the latest secret version)
gcloud run services update action-dispatch \
    --project="$PROJECT" \
    --region="$REGION"
```

---

## 8. Custom networking

By default, runner VMs are created in the `default` VPC with an ephemeral
public IP. For production, use a custom VPC:

```yaml
gcp:
  network: projects/my-project/global/networks/runners-vpc
  subnet: projects/my-project/regions/us-central1/subnetworks/runners-subnet
```

If your VMs don't need public IPs (e.g., you have Cloud NAT for egress),
you'll need to modify the provider to skip the access config. This is not
yet configurable — contributions welcome.
