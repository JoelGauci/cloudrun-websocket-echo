# Cloud Run WebSocket Echo Service

A lightweight, production-ready WebSocket echo microservice written in **Go** and optimized for **Google Cloud Run**.

When a client connects to `GET /connect` via WebSocket and sends any message, the server echoes the message back in JSON format enriched with the current UTC timestamp and human-readable English date and time.

---

## Features

- **WebSocket Echo Handler (`GET /connect`)**:
  - Automatically echoes messages back to the client.
  - Generates an RFC3339 UTC timestamp and a human-readable English date/time.
  - Automatically parses JSON payloads if valid JSON is sent.
- **Embedded Web Tester (`GET /`)**:
  - Includes a built-in browser UI to test the WebSocket connection interactively without installing third-party tools.
- **Health Check Endpoint (`GET /healthz`)**:
  - Standard HTTP JSON health check for Cloud Run startup/liveness probes.
- **Built for Cloud Run**:
  - Listens on `PORT` environment variable (defaults to `8080`).
  - Implements **Ping/Pong keep-alive** heartbeats to prevent intermediate proxy timeout.
  - Supports **graceful shutdown** on `SIGTERM` / `SIGINT`.
  - Secure multi-stage Docker build using `gcr.io/distroless/static-debian12:nonroot`.
- **CLI Test Client (`cmd/client`)**:
  - A handy Go CLI tool to test connections, repeat messages, and inspect responses.

---

## Project Structure

```
.
├── Dockerfile            # Multi-stage Docker build with Google Distroless non-root
├── .dockerignore         # Excludes local files from Docker context
├── .gitignore            # Git ignore rules for Go binaries and artifacts
├── go.mod                # Go module definition
├── go.sum                # Go checksums (gorilla/websocket)
├── main.go               # WebSocket server, HTTP routes, HTML test page
├── main_test.go          # Automated unit tests for WebSocket and HTTP handlers
├── cmd/
│   └── client/
│       └── main.go       # Standalone CLI WebSocket test client
└── README.md             # Documentation and deployment guide
```

---

## Message Format

When you send a message (text or JSON) to `ws://.../connect` or `wss://.../connect`, the server responds with a JSON object:

```json
{
  "echo": "Hello Cloud Run!",
  "received_at_utc": "2026-09-11T14:40:00Z",
  "formatted_time": "Friday, 11-Sep-2026 14:40:00 UTC"
}
```

If the sent message is valid JSON (e.g. `{"event": "status_update", "value": 42}`), the response also includes the parsed payload under `payload_json`:

```json
{
  "echo": "{\"event\": \"status_update\", \"value\": 42}",
  "received_at_utc": "2026-09-11T14:40:00Z",
  "formatted_time": "Friday, 11-Sep-2026 14:40:00 UTC",
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

### 2. Test Using the Web Browser Client

Open your browser to:
[http://localhost:8080](http://localhost:8080)

Click **Connect**, type any message into the input field, and click **Send**. You will see the outgoing message and the incoming JSON response formatted in real-time.

### 3. Test Using the Included CLI Client

In a separate terminal window:

```bash
# Send a single message
go run ./cmd/client -msg "Hello from my terminal!"

# Send repeated messages at a 1-second interval
go run ./cmd/client -msg "Ping" -repeat 5 -interval 1s

# Connect to a remote / Cloud Run endpoint
go run ./cmd/client -url "wss://<YOUR-CLOUD-RUN-SERVICE-URL>/connect" -msg "Hello Cloud Run!"
```

### 4. Test Using Third-Party Tools

Using `wscat`:
```bash
npm install -g wscat
wscat -c ws://localhost:8080/connect
> Hello!
< {"echo":"Hello!","received_at_utc":"2026-09-11T14:40:00Z","formatted_time":"Friday, 11-Sep-2026 14:40:00 UTC"}
```

Using `curl` for the health check:
```bash
curl -i http://localhost:8080/healthz
```

### 5. Run Automated Tests

```bash
go test -v ./...
```

---

## Deployment to Google Cloud Run

Cloud Run provides native support for WebSockets with zero configuration required for basic HTTP/1.1 and HTTP/2 protocol upgrades.

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

### Deployment Option 1: Direct Source Deploy (Recommended)

Google Cloud Run can build and deploy the container image directly from source files using Cloud Build in a single command:

```bash
gcloud run deploy websocket-echo \
  --source . \
  --region europe-west1 \
  --allow-unauthenticated \
  --timeout 3600 \
  --session-affinity
```

---

### Deployment Option 2: Build with Artifact Registry and Deploy

If you prefer building and managing your own container images:

1. **Create an Artifact Registry repository** (skip if you already have one):
   ```bash
   gcloud artifacts repositories create cloud-run-source-deploy \
     --repository-format=docker \
     --location=europe-west1 \
     --description="Docker repository for Cloud Run services"
   ```

2. **Build and push the container image with Cloud Build**:
   ```bash
   PROJECT_ID=$(gcloud config get-value project)
   IMAGE_URI="europe-west1-docker.pkg.dev/${PROJECT_ID}/cloud-run-source-deploy/websocket-echo:latest"

   gcloud builds submit --tag "${IMAGE_URI}"
   ```

3. **Deploy the image to Cloud Run**:
   ```bash
   gcloud run deploy websocket-echo \
     --image "${IMAGE_URI}" \
     --region europe-west1 \
     --allow-unauthenticated \
     --timeout 3600 \
     --session-affinity
   ```

---

## Cloud Run Configuration Key Concepts for WebSockets

When running WebSockets on Cloud Run, keep the following settings in mind:

### 1. Request Timeout (`--timeout`)
- **Default in Cloud Run**: 300 seconds (5 minutes).
- For WebSocket connections, the Cloud Run request timeout acts as the maximum lifetime of a single connection.
- Use `--timeout 3600` (up to 3600 seconds = 60 minutes) to allow long-lived connections. When the timeout expires, the connection will close and the client should automatically reconnect.

### 2. Session Affinity (`--session-affinity`)
- Enabled via `--session-affinity`.
- Cloud Run routes requests from the same client to the same container instance. While this echo service is stateless, session affinity is useful if you later add in-memory state, user sessions, or room channels.

### 3. Concurrency (`--concurrency`)
- **Default**: 80 concurrent requests/connections per container instance (maximum 1000).
- Go's lightweight goroutines easily handle hundreds or thousands of simultaneous WebSocket connections per instance with minimal memory (~10–25 MB).

### 4. Keep-Alive / Heartbeat (Ping/Pong)
- Cloud Run terminates idle TCP connections if no packets are transmitted for more than 10–15 minutes.
- The Go server in `main.go` runs a background ticker sending WebSocket `Ping` frames every **54 seconds**. This guarantees the connection is not dropped by intermediate load balancers.

### 5. Secure Protocol (`wss://`)
- Cloud Run automatically provisions and terminates managed TLS certificates for all services.
- Always use `wss://<SERVICE_URL>/connect` (not `ws://`) when connecting to Cloud Run from the internet.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Port on which the HTTP server listens (injected automatically by Cloud Run). |
| `ALLOWED_ORIGIN` | `*` (any) | Allowed `Origin` header for WebSocket requests. Set to your frontend domain (e.g. `https://myfrontend.com`) in production to restrict cross-site connections. |
