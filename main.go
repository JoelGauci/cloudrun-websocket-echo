package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// Constants for WebSocket timeouts and limits
const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer (64 KB).
	maxMessageSize = 65536
)

// EchoResponse represents the JSON response returned to the WebSocket client.
type EchoResponse struct {
	Echo          string      `json:"echo"`
	Path          string      `json:"path,omitempty"`
	ReceivedAtUTC string      `json:"received_at_utc"`
	FormattedTime string      `json:"formatted_time"`
	Authenticated bool        `json:"authenticated"`
	UserEmail     string      `json:"user_email,omitempty"`
	PayloadJSON   interface{} `json:"payload_json,omitempty"`
}

// HealthResponse represents the response for the health check endpoint.
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// WebSocket upgrader configuration
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow origin verification based on ALLOWED_ORIGIN env var.
		// Defaults to allowing any origin if not configured.
		allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
		if allowedOrigin == "" || allowedOrigin == "*" {
			return true
		}
		origin := r.Header.Get("Origin")
		return origin == allowedOrigin
	},
}

// handleConnect handles WebSocket upgrade and message echoing on GET /connect
func handleConnect(w http.ResponseWriter, r *http.Request) {
	// Upgrade the HTTP GET request to a WebSocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading connection: %v", err)
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	writeMessageSafe := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(messageType, data)
	}

	clientAddr := r.RemoteAddr
	callerEmail := r.Header.Get("X-Goog-Authenticated-User-Email")
	hasAuth := r.Header.Get("Authorization") != "" || callerEmail != ""
	if callerEmail != "" {
		log.Printf("Client connected from: %s (caller: %s)", clientAddr, callerEmail)
	} else if hasAuth {
		log.Printf("Client connected from: %s (authenticated with Bearer token)", clientAddr)
	} else {
		log.Printf("Client connected from: %s", clientAddr)
	}

	// Set connection limits and timeouts
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Channel to signal reader termination to the ping ticker
	done := make(chan struct{})

	// Goroutine to send periodic ping messages to keep the connection alive.
	// This is important for Cloud Run to prevent intermediate proxy timeouts.
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read loop: receives messages from the client and echoes them back with timestamps
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure, websocket.CloseAbnormalClosure) {
				log.Printf("Error reading message from %s: %v", clientAddr, err)
			} else {
				log.Printf("Client disconnected: %s", clientAddr)
			}
			break
		}

		now := time.Now().UTC()
		rawMessage := string(message)
		log.Printf("Received message from %s: %s", clientAddr, rawMessage)

		// Construct the JSON response in English
		response := EchoResponse{
			Echo:          rawMessage,
			Path:          r.URL.Path,
			ReceivedAtUTC: now.Format(time.RFC3339),
			FormattedTime: now.Format("Monday, 02-Jan-2006 15:04:05 MST"),
			Authenticated: hasAuth,
			UserEmail:     callerEmail,
		}

		// If the incoming message is valid JSON, parse it for convenience
		var parsedJSON interface{}
		if err := json.Unmarshal(message, &parsedJSON); err == nil {
			response.PayloadJSON = parsedJSON
		}

		// Serialize response to JSON
		responseBytes, err := json.Marshal(response)
		if err != nil {
			log.Printf("Error serializing response: %v", err)
			continue
		}

		// Send JSON response back to the client safely
		if err := writeMessageSafe(messageType, responseBytes); err != nil {
			log.Printf("Error writing message to %s: %v", clientAddr, err)
			break
		}
	}

	close(done)
}

// handleHealthz handles HTTP GET /healthz for liveness/readiness probes
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	res := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	_ = json.NewEncoder(w).Encode(res)
}

// handleIndex serves the browser test client or upgrades any WebSocket request
func handleIndex(w http.ResponseWriter, r *http.Request) {
	// Accept WebSocket upgrade requests on any path (e.g. /connect, /ws, /custom/path)
	if websocket.IsWebSocketUpgrade(r) {
		handleConnect(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusOK)

	fmt.Fprint(w, indexHTML)
}

func main() {
	// Cloud Run injects the PORT environment variable (default: 8080)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", handleConnect)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/", handleIndex)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Channel to listen for OS interrupt signals for graceful shutdown
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on port %s", port)
		log.Printf("WebSocket endpoint: ws://localhost:%s/connect", port)
		log.Printf("Health check:      http://localhost:%s/healthz", port)
		log.Printf("Web Test Client:   http://localhost:%s/", port)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Block until SIGINT or SIGTERM is received
	sig := <-stopChan
	log.Printf("Signal %v received, shutting down gracefully...", sig)

	// Cloud Run provides up to 10 seconds to shut down upon receiving SIGTERM
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Server exited successfully.")
}

// Embedded HTML test client for testing directly from a web browser
const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Cloud Run WebSocket Echo Client</title>
  <style>
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
      margin: 0;
      padding: 24px;
      background: #f8fafc;
      color: #0f172a;
    }
    .container {
      max-width: 800px;
      margin: 0 auto;
      background: #ffffff;
      padding: 28px;
      border-radius: 12px;
      box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -2px rgba(0, 0, 0, 0.1);
    }
    h1 {
      margin-top: 0;
      font-size: 1.5rem;
      color: #1e293b;
    }
    .badge {
      display: inline-block;
      padding: 4px 10px;
      border-radius: 9999px;
      font-size: 0.85rem;
      font-weight: 600;
    }
    .disconnected { background: #fee2e2; color: #991b1b; }
    .connected { background: #dcfce7; color: #166534; }
    .connecting { background: #fef3c7; color: #92400e; }
    .controls {
      display: flex;
      gap: 8px;
      margin: 20px 0;
    }
    input[type="text"] {
      flex-grow: 1;
      padding: 10px 14px;
      border: 1px solid #cbd5e1;
      border-radius: 8px;
      font-size: 1rem;
    }
    button {
      padding: 10px 18px;
      border: none;
      border-radius: 8px;
      font-weight: 600;
      font-size: 0.95rem;
      cursor: pointer;
      background: #2563eb;
      color: #ffffff;
      transition: background 0.15s ease;
    }
    button:hover:not(:disabled) { background: #1d4ed8; }
    button:disabled { background: #94a3b8; cursor: not-allowed; }
    button.btn-secondary {
      background: #e2e8f0;
      color: #334155;
    }
    button.btn-secondary:hover:not(:disabled) { background: #cbd5e1; }
    #log {
      height: 340px;
      overflow-y: auto;
      background: #0f172a;
      color: #e2e8f0;
      padding: 16px;
      border-radius: 8px;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      font-size: 0.875rem;
      white-space: pre-wrap;
    }
    .entry-sent { color: #60a5fa; }
    .entry-received { color: #4ade80; }
    .entry-info { color: #94a3b8; font-style: italic; }
  </style>
</head>
<body>
  <div class="container">
    <h1>Cloud Run WebSocket Echo Tester</h1>
    <p>Target URL: <code id="targetUrlPreview">...</code> &bull; Status: <span id="status" class="badge disconnected">Disconnected</span></p>

    <div class="controls" style="align-items: center;">
      <label for="wsPathInput" style="font-size: 0.875rem; font-weight: 600; color: #475569; white-space: nowrap;">Path after base:</label>
      <input type="text" id="wsPathInput" value="/connect" placeholder="/connect" oninput="updateUrlPreview()" />
      <button id="btnConnect" onclick="toggleConnect()">Connect</button>
      <button class="btn-secondary" onclick="clearLog()">Clear Log</button>
    </div>

    <div class="controls">
      <input type="text" id="messageInput" placeholder="Type a message to send..." disabled onkeydown="if(event.key==='Enter') sendMessage()" />
      <button id="btnSend" onclick="sendMessage()" disabled>Send</button>
    </div>

    <div id="log"></div>
  </div>

  <script>
    let ws = null;
    const statusBadge = document.getElementById('status');
    const logBox = document.getElementById('log');
    const btnConnect = document.getElementById('btnConnect');
    const btnSend = document.getElementById('btnSend');
    const messageInput = document.getElementById('messageInput');
    const wsPathInput = document.getElementById('wsPathInput');
    const targetUrlPreview = document.getElementById('targetUrlPreview');

    function computeWsUrl() {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const basePath = window.location.pathname.replace(/\/$/, '');
      let subPath = wsPathInput.value.trim();
      if (subPath && !subPath.startsWith('/')) {
        subPath = '/' + subPath;
      }
      return protocol + '//' + window.location.host + basePath + subPath;
    }

    function updateUrlPreview() {
      targetUrlPreview.textContent = computeWsUrl();
    }

    updateUrlPreview();

    function appendLog(text, className) {
      const line = document.createElement('div');
      line.className = className;
      line.textContent = '[' + new Date().toLocaleTimeString() + '] ' + text;
      logBox.appendChild(line);
      logBox.scrollTop = logBox.scrollHeight;
    }

    function clearLog() {
      logBox.replaceChildren();
    }

    function toggleConnect() {
      if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
        ws.close();
        return;
      }

      const wsUrl = computeWsUrl();

      statusBadge.textContent = 'Connecting...';
      statusBadge.className = 'badge connecting';
      appendLog('Connecting to ' + wsUrl + '...', 'entry-info');

      ws = new WebSocket(wsUrl);

      ws.onopen = () => {
        statusBadge.textContent = 'Connected';
        statusBadge.className = 'badge connected';
        btnConnect.textContent = 'Disconnect';
        btnSend.disabled = false;
        messageInput.disabled = false;
        wsPathInput.disabled = true;
        messageInput.focus();
        appendLog('WebSocket connection established.', 'entry-info');
      };

      ws.onmessage = (event) => {
        try {
          const parsed = JSON.parse(event.data);
          appendLog('RECV: ' + JSON.stringify(parsed, null, 2), 'entry-received');
        } catch (e) {
          appendLog('RECV: ' + event.data, 'entry-received');
        }
      };

      ws.onclose = () => {
        statusBadge.textContent = 'Disconnected';
        statusBadge.className = 'badge disconnected';
        btnConnect.textContent = 'Connect';
        btnSend.disabled = true;
        messageInput.disabled = true;
        wsPathInput.disabled = false;
        appendLog('WebSocket connection closed.', 'entry-info');
        ws = null;
      };

      ws.onerror = (err) => {
        appendLog('WebSocket error encountered.', 'entry-info');
      };
    }

    function sendMessage() {
      if (!ws || ws.readyState !== WebSocket.OPEN) return;
      const text = messageInput.value.trim();
      if (!text) return;
      ws.send(text);
      appendLog('SENT: ' + text, 'entry-sent');
      messageInput.value = '';
    }
  </script>
</body>
</html>
`
