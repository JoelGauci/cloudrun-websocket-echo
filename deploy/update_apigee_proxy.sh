#!/usr/bin/env bash
# ==============================================================================
# Update Apigee Proxy (ws-echo) to route through the Private Service Connect
# Endpoint Attachment (https://<ENDPOINT_ATTACHMENT_HOST>) to the Regional ILB,
# with a 3600-second (3,600,000 ms) request/WebSocket timeout matching Cloud Run.
# ==============================================================================
set -euo pipefail

PROJECT_ID="${PROJECT_ID:?ERROR: Please export PROJECT_ID (e.g. export PROJECT_ID=your-project-id)}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:?ERROR: Please export SERVICE_ACCOUNT (e.g. export SERVICE_ACCOUNT=my-sa@your-project-id.iam.gserviceaccount.com)}"
CLOUD_RUN_URL="${CLOUD_RUN_URL:?ERROR: Please export CLOUD_RUN_URL (e.g. export CLOUD_RUN_URL=https://your-cloud-run-service-url)}"
PROXY_NAME="${PROXY_NAME:-ws-echo}"
ENV_NAME="${ENV_NAME:-prod}"
ENDPOINT_ATTACHMENT_ID="${ENDPOINT_ATTACHMENT_ID:-websocket-echo-ea}"

CLOUD_RUN_HOST="${CLOUD_RUN_URL#https://}"
CLOUD_RUN_HOST="${CLOUD_RUN_HOST%%/*}"

TOKEN=$(gcloud auth application-default print-access-token 2>/dev/null || gcloud auth print-access-token)

echo "==> Fetching Apigee Endpoint Attachment host IP (${ENDPOINT_ATTACHMENT_ID})..."
EA_HOST=$(curl -s -H "Authorization: Bearer ${TOKEN}" \
  "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/endpointAttachments/${ENDPOINT_ATTACHMENT_ID}" | jq -r '.host')

if [[ -z "${EA_HOST}" || "${EA_HOST}" == "null" ]]; then
  echo "ERROR: Could not retrieve host IP for Endpoint Attachment ${ENDPOINT_ATTACHMENT_ID}" >&2
  exit 1
fi
echo "==> Endpoint Attachment Host IP: ${EA_HOST}"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

mkdir -p "${TMP_DIR}/apiproxy/policies" "${TMP_DIR}/apiproxy/proxies" "${TMP_DIR}/apiproxy/targets"

cat <<'EOF' > "${TMP_DIR}/apiproxy/ws-echo.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<APIProxy revision="1" name="ws-echo">
  <DisplayName>ws-echo</DisplayName>
  <Description>WebSocket Echo Proxy via Southbound PSC Endpoint Attachment and Regional ILB</Description>
  <ProxyEndpoints>
    <ProxyEndpoint>default</ProxyEndpoint>
  </ProxyEndpoints>
  <TargetEndpoints>
    <TargetEndpoint>default</TargetEndpoint>
  </TargetEndpoints>
</APIProxy>
EOF

cat <<'EOF' > "${TMP_DIR}/apiproxy/policies/AM-RemoveHeaders.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<AssignMessage continueOnError="false" enabled="true" name="AM-RemoveHeaders">
  <Remove>
    <Headers>
      <Header name="Connection"/>
      <Header name="Upgrade"/>
    </Headers>
  </Remove>
  <IgnoreUnresolvedVariables>true</IgnoreUnresolvedVariables>
  <AssignTo createNew="false" transport="http" type="request"/>
</AssignMessage>
EOF

cat <<EOF > "${TMP_DIR}/apiproxy/policies/AM-SetTargetHost.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<AssignMessage continueOnError="false" enabled="true" name="AM-SetTargetHost">
  <Set>
    <Headers>
      <Header name="Host">${CLOUD_RUN_HOST}</Header>
    </Headers>
  </Set>
  <IgnoreUnresolvedVariables>true</IgnoreUnresolvedVariables>
  <AssignTo createNew="false" transport="http" type="request"/>
</AssignMessage>
EOF

cat <<'EOF' > "${TMP_DIR}/apiproxy/proxies/default.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<ProxyEndpoint name="default">
  <Description/>
  <FaultRules/>
  <PreFlow name="PreFlow">
    <Request/>
    <Response/>
  </PreFlow>
  <PostFlow name="PostFlow">
    <Request/>
    <Response/>
  </PostFlow>
  <Flows/>
  <HTTPProxyConnection>
    <BasePath>/v1/wsecho</BasePath>
    <Properties>
      <Property name="io.timeout.millis">3600000</Property>
    </Properties>
  </HTTPProxyConnection>
  <RouteRule name="default">
    <TargetEndpoint>default</TargetEndpoint>
  </RouteRule>
</ProxyEndpoint>
EOF

cat <<EOF > "${TMP_DIR}/apiproxy/targets/default.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<TargetEndpoint name="default">
  <Description/>
  <FaultRules/>
  <PreFlow name="PreFlow">
    <Request>
      <Step>
        <Name>AM-SetTargetHost</Name>
      </Step>
    </Request>
    <Response/>
  </PreFlow>
  <PostFlow name="PostFlow">
    <Request/>
    <Response/>
  </PostFlow>
  <Flows>
    <Flow name="Blocker">
      <Description/>
      <Request>
        <Step>
          <Name>AM-RemoveHeaders</Name>
        </Step>
      </Request>
      <Response/>
      <Condition>request.header.x-blocker = "true"</Condition>
    </Flow>
  </Flows>
  <HTTPTargetConnection>
    <Properties>
      <Property name="io.timeout.millis">3600000</Property>
    </Properties>
    <SSLInfo>
      <Enabled>true</Enabled>
      <IgnoreValidationErrors>true</IgnoreValidationErrors>
    </SSLInfo>
    <URL>https://${EA_HOST}</URL>
    <Authentication>
      <GoogleIDToken>
        <Audience>${CLOUD_RUN_URL}</Audience>
      </GoogleIDToken>
    </Authentication>
  </HTTPTargetConnection>
</TargetEndpoint>
EOF

(cd "${TMP_DIR}" && zip -qr ws-echo.zip apiproxy)

echo "==> Uploading new revision of ${PROXY_NAME} to Apigee..."
NEW_REV=$(curl -s -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  -F "file=@${TMP_DIR}/ws-echo.zip" \
  "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/apis?action=import&name=${PROXY_NAME}" | jq -r '.revision')

echo "==> Imported Revision: ${NEW_REV}"

echo "==> Deploying Revision ${NEW_REV} to environment ${ENV_NAME} with Service Account ${SERVICE_ACCOUNT}..."
curl -s -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/environments/${ENV_NAME}/apis/${PROXY_NAME}/revisions/${NEW_REV}/deployments?override=true&serviceAccount=${SERVICE_ACCOUNT}"

echo ""
echo "==> Waiting for deployment to become READY..."
for i in {1..20}; do
  STATE=$(curl -s -H "Authorization: Bearer ${TOKEN}" \
    "https://apigee.googleapis.com/v1/organizations/${PROJECT_ID}/environments/${ENV_NAME}/apis/${PROXY_NAME}/revisions/${NEW_REV}/deployments" | jq -r '.state // empty')
  echo "Deployment state: ${STATE}"
  if [[ "${STATE}" == "READY" ]]; then
    echo "==> Deployment READY!"
    break
  fi
  sleep 3
done
