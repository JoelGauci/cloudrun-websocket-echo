#!/usr/bin/env bash
# ==============================================================================
# Setup Regional Internal Application Load Balancer (WebSocket-compatible),
# Serverless NEG (Cloud Run), Private Service Connect Service Attachment,
# and Apigee Endpoint Attachment.
# ==============================================================================
set -euo pipefail

PROJECT_ID="${PROJECT_ID:-apigee-x-jog}"
REGION="${REGION:-europe-west1}"
VPC_NETWORK="${VPC_NETWORK:-vpc-customer-apigee-x}"
SUBNET="${SUBNET:-sub-customer-apigee-x}"
PROXY_SUBNET="${PROXY_SUBNET:-proxy-only-subnet-ew1}"
PSC_NAT_SUBNET="${PSC_NAT_SUBNET:-psc-nat-subnet-ws-echo}"
CLOUD_RUN_SERVICE="${CLOUD_RUN_SERVICE:-websocket-echo}"

NEG_NAME="websocket-echo-neg"
BACKEND_SERVICE_NAME="websocket-echo-ilb-backend"
URL_MAP_NAME="websocket-echo-ilb-urlmap"
CERT_NAME="websocket-echo-ilb-cert"
TARGET_HTTPS_PROXY_NAME="websocket-echo-ilb-https-proxy"
ILB_IP_NAME="websocket-echo-ilb-ip"
FORWARDING_RULE_NAME="websocket-echo-ilb-forwarding-rule"
SERVICE_ATTACHMENT_NAME="websocket-echo-service-attachment"
APIGEE_ENDPOINT_ATTACHMENT_ID="websocket-echo-ea"

echo "==> Project: ${PROJECT_ID}, Region: ${REGION}"

# ------------------------------------------------------------------------------
# 1. Subnets: Regional Managed Proxy Subnet & PSC NAT Subnet
# ------------------------------------------------------------------------------
echo "==> [1/10] Ensuring Proxy-only subnet (${PROXY_SUBNET}) exists..."
if ! gcloud compute networks subnets describe "${PROXY_SUBNET}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute networks subnets create "${PROXY_SUBNET}" \
    --network="${VPC_NETWORK}" \
    --region="${REGION}" \
    --range="10.129.0.0/23" \
    --purpose="REGIONAL_MANAGED_PROXY" \
    --role="ACTIVE" \
    --project="${PROJECT_ID}"
fi

echo "==> [2/10] Ensuring PSC NAT subnet (${PSC_NAT_SUBNET}) exists..."
if ! gcloud compute networks subnets describe "${PSC_NAT_SUBNET}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute networks subnets create "${PSC_NAT_SUBNET}" \
    --network="${VPC_NETWORK}" \
    --region="${REGION}" \
    --range="192.168.2.0/24" \
    --purpose="PRIVATE_SERVICE_CONNECT" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 2. Serverless NEG for Cloud Run
# ------------------------------------------------------------------------------
echo "==> [3/10] Creating Serverless NEG (${NEG_NAME}) for Cloud Run service (${CLOUD_RUN_SERVICE})..."
if ! gcloud compute network-endpoint-groups describe "${NEG_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute network-endpoint-groups create "${NEG_NAME}" \
    --region="${REGION}" \
    --network-endpoint-type="serverless" \
    --cloud-run-service="${CLOUD_RUN_SERVICE}" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 3. Regional Backend Service (INTERNAL_MANAGED, HTTPS)
#    Note: Serverless NEGs inherit their request/WebSocket timeout directly
#    from the Cloud Run service (--timeout=3600 on Cloud Run). Setting a custom
#    timeoutSec on the backend service itself is not supported with Serverless NEGs.
# ------------------------------------------------------------------------------
echo "==> [4/10] Creating Regional Backend Service (${BACKEND_SERVICE_NAME})..."
if ! gcloud compute backend-services describe "${BACKEND_SERVICE_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute backend-services create "${BACKEND_SERVICE_NAME}" \
    --load-balancing-scheme="INTERNAL_MANAGED" \
    --protocol="HTTPS" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
else
  # Ensure timeout is at default 30s so Serverless NEG can be attached
  gcloud compute backend-services update "${BACKEND_SERVICE_NAME}" \
    --timeout="30s" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
fi

# Add Serverless NEG backend if not already attached
EXISTING_BACKENDS=$(gcloud compute backend-services describe "${BACKEND_SERVICE_NAME}" --region="${REGION}" --project="${PROJECT_ID}" --format="value(backends)" 2>/dev/null || true)
if [[ -z "${EXISTING_BACKENDS}" ]]; then
  gcloud compute backend-services add-backend "${BACKEND_SERVICE_NAME}" \
    --network-endpoint-group="${NEG_NAME}" \
    --network-endpoint-group-region="${REGION}" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 4. Regional URL Map
# ------------------------------------------------------------------------------
echo "==> [5/10] Creating Regional URL Map (${URL_MAP_NAME})..."
if ! gcloud compute url-maps describe "${URL_MAP_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute url-maps create "${URL_MAP_NAME}" \
    --default-service="${BACKEND_SERVICE_NAME}" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 5. Self-Signed Regional SSL Certificate
# ------------------------------------------------------------------------------
echo "==> [6/10] Creating Self-Signed Regional SSL Certificate (${CERT_NAME})..."
if ! gcloud compute ssl-certificates describe "${CERT_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  TMP_DIR=$(mktemp -d)
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "${TMP_DIR}/key.pem" \
    -out "${TMP_DIR}/cert.pem" \
    -days 3650 \
    -subj "/CN=websocket-echo.internal" \
    -addext "subjectAltName=DNS:websocket-echo.internal,DNS:*.internal"
  gcloud compute ssl-certificates create "${CERT_NAME}" \
    --certificate="${TMP_DIR}/cert.pem" \
    --private-key="${TMP_DIR}/key.pem" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
  rm -rf "${TMP_DIR}"
fi

# ------------------------------------------------------------------------------
# 6. Regional Target HTTPS Proxy
# ------------------------------------------------------------------------------
echo "==> [7/10] Creating Regional Target HTTPS Proxy (${TARGET_HTTPS_PROXY_NAME})..."
if ! gcloud compute target-https-proxies describe "${TARGET_HTTPS_PROXY_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute target-https-proxies create "${TARGET_HTTPS_PROXY_NAME}" \
    --url-map="${URL_MAP_NAME}" \
    --url-map-region="${REGION}" \
    --ssl-certificates="${CERT_NAME}" \
    --ssl-certificates-region="${REGION}" \
    --region="${REGION}" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 7. Regional Internal IP & Forwarding Rule (INTERNAL_MANAGED)
# ------------------------------------------------------------------------------
echo "==> [8/10] Creating Regional Internal IP (${ILB_IP_NAME}) and Forwarding Rule (${FORWARDING_RULE_NAME})..."
if ! gcloud compute addresses describe "${ILB_IP_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute addresses create "${ILB_IP_NAME}" \
    --region="${REGION}" \
    --subnet="${SUBNET}" \
    --purpose="GCE_ENDPOINT" \
    --project="${PROJECT_ID}"
fi

if ! gcloud compute forwarding-rules describe "${FORWARDING_RULE_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute forwarding-rules create "${FORWARDING_RULE_NAME}" \
    --load-balancing-scheme="INTERNAL_MANAGED" \
    --network="${VPC_NETWORK}" \
    --subnet="${SUBNET}" \
    --address="${ILB_IP_NAME}" \
    --ports="443" \
    --region="${REGION}" \
    --target-https-proxy="${TARGET_HTTPS_PROXY_NAME}" \
    --target-https-proxy-region="${REGION}" \
    --project="${PROJECT_ID}"
fi

# ------------------------------------------------------------------------------
# 8. Private Service Connect Service Attachment (Producer)
# ------------------------------------------------------------------------------
echo "==> [9/10] Creating Private Service Connect Service Attachment (${SERVICE_ATTACHMENT_NAME})..."
if ! gcloud compute service-attachments describe "${SERVICE_ATTACHMENT_NAME}" --region="${REGION}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute service-attachments create "${SERVICE_ATTACHMENT_NAME}" \
    --region="${REGION}" \
    --producer-forwarding-rule="${FORWARDING_RULE_NAME}" \
    --connection-preference="ACCEPT_AUTOMATIC" \
    --nat-subnets="${PSC_NAT_SUBNET}" \
    --project="${PROJECT_ID}"
fi

SERVICE_ATTACHMENT_URI=$(gcloud compute service-attachments describe "${SERVICE_ATTACHMENT_NAME}" \
  --region="${REGION}" \
  --project="${PROJECT_ID}" \
  --format="value(selfLink)")

echo "Service Attachment URI: ${SERVICE_ATTACHMENT_URI}"

# ------------------------------------------------------------------------------
# 9. Apigee Endpoint Attachment (Consumer)
# ------------------------------------------------------------------------------
echo "==> [10/10] Creating Apigee Endpoint Attachment (${APIGEE_ENDPOINT_ATTACHMENT_ID})..."
TOKEN=$(gcloud auth application-default print-access-token 2>/dev/null || gcloud auth print-access-token)

EA_STATUS=$(curl -s -H "Authorization: Bearer ${TOKEN}" \
  "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/endpointAttachments/${APIGEE_ENDPOINT_ATTACHMENT_ID}" | jq -r '.state // empty')

if [[ -z "${EA_STATUS}" ]]; then
  echo "Triggering creation of Apigee Endpoint Attachment..."
  curl -s -X POST \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/endpointAttachments?endpointAttachmentId=${APIGEE_ENDPOINT_ATTACHMENT_ID}" \
    -d "{
      \"location\": \"${REGION}\",
      \"serviceAttachment\": \"projects/${PROJECT_ID}/regions/${REGION}/serviceAttachments/${SERVICE_ATTACHMENT_NAME}\"
    }"
fi

echo "Done! Waiting for Apigee Endpoint Attachment to reach ACTIVE state..."
