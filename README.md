# Cloud Run WebSocket Echo Service

A lightweight, production-ready WebSocket echo microservice written in **Go** and optimized for **Google Cloud Run** running in **"Require authentication"** mode.

When a client connects to `GET /connect` via WebSocket and sends any message, the server echoes the message back in JSON format enriched with the current UTC timestamp, human-readable English date and time, and authentication status.

---

## Features

- **WebSocket Echo Handler (`GET /connect`)**:
  - Automatically echoes messages back to the client.
  - Generates an RFC3339 UTC timestamp and a human-readable English date/time.
  - Reports caller authentication status (`authenticated: true`, `user_email`).
  - Automatically parses JSON payloads if valid JSON is sent.
- **Embedded Web Tester (`GET /`)**:
  - Includes a built-in browser UI to test the WebSocket connection interactively.
- **Health Check Endpoint (`GET /healthz`)**:
  - Standard HTTP JSON health check for Cloud Run startup/liveness probes.
- **Secured with Cloud Run IAM ("Require authentication")**:
  - Requires a valid Google OIDC ID Token (`Authorization: Bearer <ID_TOKEN>`) on the WebSocket upgrade request.
  - Supports both standard Cloud Run URL audience and custom audiences (`gcloud` SDK client ID).
- **Built for Cloud Run**:
  - Listens on `PORT` environment variable (defaults to `8080`).
  - Implements **Ping/Pong keep-alive** heartbeats to prevent intermediate proxy timeout.
  - Supports **graceful shutdown** on `SIGTERM` / `SIGINT`.
  - Secure multi-stage Docker build using `gcr.io/distroless/static-debian12:nonroot`.
- **CLI Test Client (`cmd/client`)**:
  - Standalone Go CLI tool supporting `-token` for Cloud Run authentication, repeated messages, and response inspection.

---

## Project Structure

```
.
├── Dockerfile            # Multi-stage Docker build with Google Distroless non-root
├── .dockerignore         # Excludes local files from Docker context
├── .gitignore            # Git ignore rules for Go binaries and artifacts
├── go.mod                # Go module definition (Go 1.23+)
├── go.sum                # Go checksums (gorilla/websocket)
├── main.go               # WebSocket server, HTTP routes, HTML test page
├── main_test.go          # Automated unit tests for WebSocket and HTTP handlers
├── cmd/
│   └── client/
│       └── main.go       # Standalone CLI WebSocket test client with ID Token support
└── README.md             # Documentation and deployment guide
```

---

## Message Format

When you send a message (text or JSON) to `ws://.../connect` or `wss://.../connect`, the server responds with a JSON object:

```json
{
  "echo": "Hello Cloud Run!",
  "received_at_utc": "2026-09-11T14:40:00Z",
  "formatted_time": "Friday, 11-Sep-2026 14:40:00 UTC",
  "authenticated": true,
  "user_email": "admin@example.com"
}
```

If the sent message is valid JSON (e.g. `{"event": "status_update", "value": 42}`), the response also includes the parsed payload under `payload_json`:

```json
{
  "echo": "{\"event\": \"status_update\", \"value\": 42}",
  "received_at_utc": "2026-09-11T14:40:00Z",
  "formatted_time": "Friday, 11-Sep-2026 14:40:00 UTC",
  "authenticated": true,
  "user_email": "admin@example.com",
  "payload_json": {
    "event": "status_update",
    "value": 42
  }
}
```

---

## Local Development & Testing

### 1. Run the Server

```bash
go run .
```

The server will start listening on port `8080`:
```
2026/09/11 14:40:00 Server listening on port 8080
2026/09/11 14:40:00 WebSocket endpoint: ws://localhost:8080/connect
2026/09/11 14:40:00 Health check:      http://localhost:8080/healthz
2026/09/11 14:40:00 Web Test Client:   http://localhost:8080/
```

### 2. Test Using the Built-in Web Browser Client

Open your browser to:
[http://localhost:8080](http://localhost:8080)

Click **Connect**, type any message into the input field, and click **Send**.

### 3. Test Using the Included CLI Client

```bash
# Send a single message
go run ./cmd/client -msg "Hello from my terminal!"

# Send repeated messages at a 1-second interval
go run ./cmd/client -msg "Ping" -repeat 5 -interval 1s

# Pass custom HTTP headers (-H can be repeated multiple times)
go run ./cmd/client \
  -H "X-Custom-Header-1: Value1" \
  -H "X-Custom-Header-2: Value2" \
  -msg "Hello with custom headers!"
```

### 4. Run Automated Tests

```bash
go test -v ./...
```

---

## Deployment to Google Cloud Run ("Require authentication")

Cloud Run provides native support for WebSockets with zero configuration required for HTTP/1.1 and HTTP/2 protocol upgrades.

### Prerequisites

1. Install and configure the Google Cloud SDK (`gcloud`):
   ```bash
   gcloud auth login
   gcloud config set project YOUR_PROJECT_ID
   ```

2. Enable the required GCP APIs:
   ```bash
   gcloud services enable run.googleapis.com cloudbuild.googleapis.com
   ```

---

### Step 1: Deploy with Authentication Required

Deploy the service using `--no-allow-unauthenticated` to block public unauthenticated access:

```bash
gcloud run deploy websocket-echo \
  --source . \
  --region europe-west1 \
  --no-allow-unauthenticated \
  --timeout 3600 \
  --session-affinity
```

### Step 2: Grant Invoker Permissions (`roles/run.invoker`)

Only users or service accounts granted the `roles/run.invoker` IAM role can invoke the service:

```bash
# Grant access to a specific user
gcloud run services add-iam-policy-binding websocket-echo \
  --region europe-west1 \
  --member="user:your-email@example.com" \
  --role="roles/run.invoker"

# Or grant access to a service account
gcloud run services add-iam-policy-binding websocket-echo \
  --region europe-west1 \
  --member="serviceAccount:my-sa@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/run.invoker"
```

### Step 3: Configure Custom Audiences (For `gcloud` User Tokens)

By default, Cloud Run validates that the token audience matches the Cloud Run service URL. Because `gcloud auth print-identity-token` for personal user accounts issues tokens with the Google Cloud SDK client ID audience (`32555940559.apps.googleusercontent.com`), add this client ID to the service's custom audiences:

```bash
gcloud run services update websocket-echo \
  --region europe-west1 \
  --add-custom-audiences=32555940559.apps.googleusercontent.com
```

---

## Connecting to the Secured Cloud Run Service

When deployed in **"Require authentication"** mode, Google Front End (GFE) requires an **ID Token** in the `Authorization: Bearer <ID_TOKEN>` header during the WebSocket HTTP upgrade handshake (`GET /connect`). Requests without a valid token are rejected immediately.

### Option A: Using the CLI Client with an ID Token (Recommended)

```bash
# 1. Retrieve an ID token
TOKEN=$(gcloud auth print-identity-token)

# 2. Connect and send a message
go run ./cmd/client \
  -url "wss://<YOUR-CLOUD-RUN-URL>/connect" \
  -token "$TOKEN" \
  -msg "Hello secured Cloud Run!"
```

For service accounts:
```bash
TOKEN=$(gcloud auth print-identity-token --audiences="https://<YOUR-CLOUD-RUN-URL>")

go run ./cmd/client \
  -url "wss://<YOUR-CLOUD-RUN-URL>/connect" \
  -token "$TOKEN" \
  -msg "Hello from Service Account!"
```

### Option B: Using Native `curl` (curl 7.86+ / 8.x) for WebSockets

Modern versions of `curl` support WebSocket connections directly via the `ws://` and `wss://` schemes. You can stream a message through standard input (`stdin`) using the `-T -` option:

#### 1. Send Plain Text Message:
```bash
printf "Hello Cloud Run from curl!" | curl -s -N --max-time 2 -T - \
  -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
  "wss://<YOUR-CLOUD-RUN-URL>/connect"
```

**Example Response:**
```json
{
  "echo": "Hello Cloud Run from curl!",
  "received_at_utc": "2026-09-11T15:20:31Z",
  "formatted_time": "Friday, 11-Sep-2026 15:20:31 UTC",
  "authenticated": true
}
```

#### 2. Send JSON Payload:
```bash
echo '{"event": "ping", "data": 42}' | curl -s -N --max-time 2 -T - \
  -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
  "wss://<YOUR-CLOUD-RUN-URL>/connect"
```

**Example Response:**
```json
{
  "echo": "{\"event\": \"ping\", \"data\": 42}\n",
  "received_at_utc": "2026-09-11T15:20:39Z",
  "formatted_time": "Friday, 11-Sep-2026 15:20:39 UTC",
  "authenticated": true,
  "payload_json": {
    "data": 42,
    "event": "ping"
  }
}
```

#### Explanation of `curl` Flags:
- **`-s`** : Silent mode (hides download/upload progress meters).
- **`-N`** : Disables buffering (no-buffer), printing the server response immediately.
- **`-T -`** : Sends data read from standard input (`stdin`) as a WebSocket payload frame.
- **`--max-time 2`** : Terminates `curl` after 2 seconds (because WebSocket connections remain open indefinitely by default).
- **`-H "Authorization: Bearer ..."`** : Passes the Google OIDC ID token to satisfy Cloud Run's IAM requirement.
- **`-k`** *(Optional)* : Bypasses TLS certificate verification if connecting through an untrusted or self-signed certificate (e.g. `nip.io`).

---

### Option C: Using `curl` for the HTTP Health Check

```bash
curl -i -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
  https://<YOUR-CLOUD-RUN-URL>/healthz
```

---

### Option D: Using the Web Browser Client via `gcloud run proxy`

Standard browser JavaScript (`new WebSocket(url)`) does not allow setting custom HTTP headers such as `Authorization: Bearer`. To test from a web browser directly against a secured Cloud Run service, use the built-in `gcloud` local proxy which automatically injects your Google credentials:

```bash
gcloud run services proxy websocket-echo --region europe-west1 --port 8080
```

Then open [http://localhost:8080](http://localhost:8080) in your browser. The browser connects through the local proxy which forwards the request to Cloud Run with valid authentication headers.

---

## Private Southbound Architecture: Apigee → PSC → Regional ILB → Serverless NEG → Cloud Run

To isolate the Cloud Run service from the public internet (`--ingress=internal-and-cloud-load-balancing`) while exposing WebSockets through **Apigee X**, this project provisions a **Regional Internal Application Load Balancer (`INTERNAL_MANAGED`)** with a **Serverless NEG** and a **Private Service Connect (PSC) Service Attachment**:

```
[Client]
   │ wss://${APIGEE_HOST}/v1/wsecho/connect
   ▼
[Apigee X Runtime (europe-west1)]
   │ Southbound PSC Endpoint Attachment (websocket-echo-ea -> ${ENDPOINT_ATTACHMENT_HOST})
   │ Request Timeout: io.timeout.millis = 3600000 (3600s)
   ▼
[PSC Service Attachment (websocket-echo-service-attachment)]
   │ NAT Subnet: psc-nat-subnet-ws-echo
   ▼
[Regional Internal HTTPS Load Balancer (INTERNAL_MANAGED, Port 443)]
   │ Proxy Subnet: proxy-only-subnet-ew1
   │ Backend Service: websocket-echo-ilb-backend (HTTPS, inherits Cloud Run 3600s WebSocket timeout)
   ▼
[Serverless NEG (websocket-echo-neg)]
   ▼
[Cloud Run Service (websocket-echo, --ingress=internal-and-cloud-load-balancing)]
```

### Required Environment Variables

Before deploying or testing via the scripts, define your environment variables:

```bash
export PROJECT_ID="your-gcp-project-id"
export REGION="europe-west1"
export VPC_NETWORK="your-vpc-network"
export SUBNET="your-private-subnet"

# External hostname or IP of your Apigee X Environment Group / Load Balancer (e.g. api.example.com or <IP>.nip.io)
export APIGEE_HOST="your-apigee-hostname.example.com"

# Target Cloud Run service URL (used for Host header and Google OIDC token audience)
export CLOUD_RUN_URL="https://websocket-echo-xxxx.europe-west1.run.app"

# Google Cloud Service Account used by Apigee to authenticate to Cloud Run (must have roles/run.invoker)
export SERVICE_ACCOUNT="apigee-runtime-sa@${PROJECT_ID}.iam.gserviceaccount.com"
```

### Deployed Resources (`europe-west1`)

| Component | Resource Name | Details |
|---|---|---|
| **Cloud Run Service** | `websocket-echo` | `--ingress=internal-and-cloud-load-balancing`, `--timeout=3600`, `--session-affinity` |
| **Serverless NEG** | `websocket-echo-neg` | `SERVERLESS` endpoint type targeting `websocket-echo` |
| **Regional Backend Service** | `websocket-echo-ilb-backend` | `INTERNAL_MANAGED`, `HTTPS` protocol (inherits Cloud Run's 3600s WebSocket timeout) |
| **Regional URL Map** | `websocket-echo-ilb-urlmap` | Default backend: `websocket-echo-ilb-backend` |
| **Regional SSL Certificate** | `websocket-echo-ilb-cert` | Self-signed certificate for internal HTTPS (`websocket-echo.internal`) |
| **Regional Target HTTPS Proxy** | `websocket-echo-ilb-https-proxy` | Terminates internal TLS on the ILB |
| **Regional Forwarding Rule** | `websocket-echo-ilb-forwarding-rule` | Internal VIP (`:443`) in `${SUBNET}` |
| **PSC Service Attachment** | `websocket-echo-service-attachment` | Producer attachment (`ACCEPT_AUTOMATIC`) with NAT subnet `psc-nat-subnet-ws-echo` |
| **Apigee Endpoint Attachment** | `websocket-echo-ea` | Consumer attachment in `${REGION}` assigned a private PSC host IP (`${ENDPOINT_ATTACHMENT_HOST}`) |
| **Apigee Proxy Target** | `ws-echo` | Targets `https://${ENDPOINT_ATTACHMENT_HOST}` with `io.timeout.millis=3600000` (3600s) and `<GoogleIDToken>` authentication (`${SERVICE_ACCOUNT}`) |

### Automated Deployment Scripts & Terraform

All infrastructure and Apigee proxy configurations are automated in the `deploy/` directory:

- **Bash / gcloud Script (ILB + NEG + PSC + Endpoint Attachment)**:
  ```bash
  ./deploy/setup_ilb_psc_apigee.sh
  ```
- **Bash Script (Update Apigee Proxy `ws-echo` target to PSC Endpoint Attachment IP with 3600s timeout)**:
  ```bash
  ./deploy/update_apigee_proxy.sh
  ```
- **Terraform Configuration**:
  See [`deploy/terraform/main.tf`](deploy/terraform/main.tf) for declarative IaC provisioning.

---

## Testing via an API Gateway or Reverse Proxy (e.g., Apigee, Envoy)

When the service is published behind an API Gateway (such as **Apigee**) at `https://${APIGEE_HOST}/v1/wsecho`, the gateway terminates client TLS and routes privately over PSC to the Internal Load Balancer and Cloud Run:

### 1. Endpoints Overview

| Gateway Path | Target Route on Cloud Run | Description |
|---|---|---|
| `GET https://${APIGEE_HOST}/v1/wsecho` | `GET /` | Serves the interactive browser test client (`Cloud Run WebSocket Echo Tester`). |
| `GET wss://${APIGEE_HOST}/v1/wsecho/connect` | `GET /connect` | Default WebSocket upgrade and bidirectional echo endpoint. |
| `GET wss://${APIGEE_HOST}/v1/wsecho/<any-subpath>` | `GET /<any-subpath>` | Custom WebSocket upgrade path (all subpaths are accepted and echoed). |

### 2. Test WebSocket with Native `curl`

```bash
# Plain text echo
printf "Hello via Gateway!" | curl -k -s -N --max-time 2 -T - \
  "wss://${APIGEE_HOST}/v1/wsecho/connect"

# JSON payload echo
echo '{"status": "ok", "count": 1}' | curl -k -s -N --max-time 2 -T - \
  "wss://${APIGEE_HOST}/v1/wsecho/connect"
```

### 3. Test with the CLI Client

```bash
# Standard connection through Apigee
go run ./cmd/client \
  -url "wss://${APIGEE_HOST}/v1/wsecho/connect" \
  -insecure \
  -msg "Hello from CLI client through Apigee!"

# Passing custom headers (-H) through Apigee (e.g. X-Custom-Header or x-blocker)
go run ./cmd/client \
  -url "wss://${APIGEE_HOST}/v1/wsecho/connect" \
  -insecure \
  -H "header_name1: header_value1" \
  -H "header_name2: header_value2" \
  -msg "Hello with custom headers!"
```

### 4. Interactive Testing in Web Browser ("Cloud Run WebSocket Echo Tester")

Open the gateway root URL directly in any web browser:
```
https://${APIGEE_HOST}/v1/wsecho
```
- The UI automatically detects the current gateway base path (`/v1/wsecho` or `/v1/echo`).
- You can customize the **Path after base** field (default: `/connect`, e.g. `/connect`, `/ws`, `/custom/subpath`).
- Click **Connect**, enter your message in the input box, and click **Send**. The browser connects to `wss://${APIGEE_HOST}/v1/wsecho/<subpath>` and displays echoes with timestamps and the matched path in real-time.

---

## Cloud Run Configuration Key Concepts for WebSockets

### 1. Authentication & Handshake (`--no-allow-unauthenticated`)
- WebSocket connections start as an HTTP `GET /connect` request with `Upgrade: websocket`.
- Cloud Run Google Front End (GFE) inspects the `Authorization: Bearer <ID_TOKEN>` header on this initial handshake.
- Once authenticated, GFE allows the upgrade and maintains the bi-directional TCP tunnel.

### 2. Request Timeout (`--timeout`)
- **Default in Cloud Run**: 300 seconds (5 minutes).
- For WebSocket connections, the request timeout acts as the maximum lifetime of a single connection.
- Use `--timeout 3600` (up to 3600 seconds = 60 minutes) to allow long-lived connections. When the timeout expires, the connection closes and the client can automatically reconnect.

### 3. Session Affinity (`--session-affinity`)
- Enabled via `--session-affinity`.
- Cloud Run routes requests from the same client to the same container instance. While this echo service is stateless, session affinity is recommended if you later add in-memory state or room channels.

### 4. Concurrency (`--concurrency`)
- **Default**: 80 concurrent requests/connections per container instance (maximum 1000).
- Go's lightweight goroutines easily handle hundreds or thousands of simultaneous WebSocket connections per instance with minimal memory (~10–25 MB).

### 5. Keep-Alive / Heartbeat (Ping/Pong)
- Cloud Run terminates idle TCP connections if no packets are transmitted for more than 10–15 minutes.
- The Go server in `main.go` runs a background ticker sending WebSocket `Ping` frames every **54 seconds**. This guarantees the connection is not dropped by intermediate load balancers.

### 6. Secure Protocol (`wss://`)
- Cloud Run automatically provisions and terminates managed TLS certificates for all services.
- Always use `wss://<SERVICE_URL>/connect` (not `ws://`) when connecting to Cloud Run from the internet.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Port on which the HTTP server listens (injected automatically by Cloud Run). |
| `ALLOWED_ORIGIN` | `*` (any) | Allowed `Origin` header for WebSocket requests. Set to your frontend domain (e.g. `https://myfrontend.com`) in production to restrict cross-site connections. |
