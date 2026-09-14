package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestHealthz verifies the /healthz endpoint returns 200 OK and expected JSON structure.
func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	handleHealthz(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if health.Status != "ok" {
		t.Fatalf("expected status 'ok', got '%s'", health.Status)
	}

	if _, err := time.Parse(time.RFC3339, health.Timestamp); err != nil {
		t.Fatalf("expected valid RFC3339 timestamp, got '%s'", health.Timestamp)
	}
}

// TestIndex verifies that GET / serves the HTML test client.
func TestIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handleIndex(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}

	contentType := res.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("expected Content-Type text/html, got %s", contentType)
	}
}

// TestWebSocketConnectEcho tests the WebSocket handshake on /connect and verifies echo output.
func TestWebSocketConnectEcho(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(handleConnect))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/connect"

	// Connect as client
	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close()
	defer resp.Body.Close()

	testMessage := "Hello Cloud Run WebSocket!"

	// Send message to server
	if err := conn.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	// Read echoed response
	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var echoRes EchoResponse
	if err := json.Unmarshal(message, &echoRes); err != nil {
		t.Fatalf("failed to unmarshal JSON response: %v. Raw: %s", err, string(message))
	}

	// Verify echoed content
	if echoRes.Echo != testMessage {
		t.Errorf("expected echo '%s', got '%s'", testMessage, echoRes.Echo)
	}

	// Verify UTC RFC3339 timestamp
	if parsedTime, err := time.Parse(time.RFC3339, echoRes.ReceivedAtUTC); err != nil {
		t.Errorf("invalid RFC3339 received_at_utc: %v", err)
	} else if time.Since(parsedTime) > 5*time.Second {
		t.Errorf("timestamp is too old: %v", parsedTime)
	}

	// Verify formatted English time
	if echoRes.FormattedTime == "" {
		t.Errorf("expected non-empty formatted_time")
	}
}

// TestWebSocketJSONPayload tests echoing structured JSON input.
func TestWebSocketJSONPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(handleConnect))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/connect"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close()

	jsonInput := `{"event":"ping","count":42}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(jsonInput)); err != nil {
		t.Fatalf("failed to write message: %v", err)
	}

	_, message, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var echoRes EchoResponse
	if err := json.Unmarshal(message, &echoRes); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if echoRes.PayloadJSON == nil {
		t.Fatalf("expected payload_json to be parsed, got nil")
	}
}

// TestWebSocketMultipleMessages verifies that the connection remains open across multiple sequential calls.
func TestWebSocketMultipleMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(handleConnect))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/connect"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WebSocket: %v", err)
	}
	defer conn.Close()

	for i := 1; i <= 5; i++ {
		msg := strings.Repeat("ping-", i)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("failed to write message #%d: %v", i, err)
		}

		_, respBytes, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read response #%d: %v", i, err)
		}

		var echoRes EchoResponse
		if err := json.Unmarshal(respBytes, &echoRes); err != nil {
			t.Fatalf("failed to unmarshal response #%d: %v", i, err)
		}
		if echoRes.Echo != msg {
			t.Errorf("message #%d: expected echo %q, got %q", i, msg, echoRes.Echo)
		}
	}
}

