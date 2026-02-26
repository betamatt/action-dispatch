#!/usr/bin/env bash
#
# Deploy action-dispatch to Cloud Run.
#
# Prerequisites:
#   - gcloud CLI authenticated
#   - A service account with roles:
#       compute.instanceAdmin.v1  (create/delete GCE VMs)
#       iam.serviceAccountUser    (attach SA to VMs)
#   - Config file stored in Secret Manager
#
# Usage:
#   ./deploy/cloud-run.sh
#
# Environment variables (override defaults):
#   PROJECT         GCP project ID
#   REGION          Cloud Run region (default: us-central1)
#   SERVICE_NAME    Cloud Run service name (default: action-dispatch)
#   SERVICE_ACCOUNT Service account email for the Cloud Run service
#   CONFIG_SECRET   Secret Manager secret name for config.yaml (default: action-dispatch-config)
#   IMAGE           Pre-built image to deploy (skips docker build if set)

set -euo pipefail

PROJECT="${PROJECT:?PROJECT is required}"
REGION="${REGION:-us-central1}"
SERVICE_NAME="${SERVICE_NAME:-action-dispatch}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:?SERVICE_ACCOUNT is required}"
CONFIG_SECRET="${CONFIG_SECRET:-action-dispatch-config}"
IMAGE="${IMAGE:-}"

REPO="${REGION}-docker.pkg.dev/${PROJECT}/action-dispatch"

# Build and push if no pre-built image provided.
if [[ -z "$IMAGE" ]]; then
    IMAGE="${REPO}/action-dispatch:$(git rev-parse --short HEAD)"

    echo "==> Ensuring Artifact Registry repository exists"
    gcloud artifacts repositories describe action-dispatch \
        --project="$PROJECT" \
        --location="$REGION" \
        --format="value(name)" 2>/dev/null || \
    gcloud artifacts repositories create action-dispatch \
        --project="$PROJECT" \
        --location="$REGION" \
        --repository-format=docker

    echo "==> Building and pushing ${IMAGE}"
    gcloud builds submit . \
        --project="$PROJECT" \
        --tag="$IMAGE" \
        --quiet
fi

echo "==> Deploying ${SERVICE_NAME} to Cloud Run"
gcloud run deploy "$SERVICE_NAME" \
    --project="$PROJECT" \
    --region="$REGION" \
    --image="$IMAGE" \
    --service-account="$SERVICE_ACCOUNT" \
    --port=8080 \
    --args="--config=/secrets/config.yaml" \
    --set-secrets="/secrets/config.yaml=${CONFIG_SECRET}:latest" \
    --min-instances=0 \
    --max-instances=3 \
    --cpu=1 \
    --memory=256Mi \
    --timeout=30s \
    --no-cpu-throttling \
    --allow-unauthenticated \
    --quiet

URL=$(gcloud run services describe "$SERVICE_NAME" \
    --project="$PROJECT" \
    --region="$REGION" \
    --format="value(status.url)")

echo "==> Deployed to ${URL}"
echo "    Webhook endpoint: ${URL}/webhook"
echo "    Health check:     ${URL}/healthz"
