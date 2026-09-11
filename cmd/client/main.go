package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	rawURL := flag.String("url", "ws://localhost:8080/connect", "WebSocket endpoint URL")
	msg := flag.String("msg", "Hello from WebSocket CLI client!", "Message to send")
	token := flag.String("token", "", "Google ID Token for Cloud Run authentication (Bearer token)")
	repeat := flag.Int("repeat", 1, "Number of times to send message")
	interval := flag.Duration("interval", 1*time.Second, "Interval between repeated messages")
	flag.Parse()

	u, err := url.Parse(*rawURL)
	if err != nil {
		log.Fatalf("Invalid URL: %v", err)
	}

	headers := make(http.Header)
	if *token != "" {
		headers.Set("Authorization", "Bearer "+*token)
	}

	log.Printf("Connecting to %s...", u.String())

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			log.Fatalf("Dial failed (HTTP status %d): %v", resp.StatusCode, err)
		}
		log.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	log.Println("Connected! Listening for responses...")

	done := make(chan struct{})

	// Goroutine to read incoming messages
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error / connection closed: %v", err)
				return
			}
			fmt.Printf("\n<<< RECEIVED FROM SERVER:\n%s\n\n", string(message))
		}
	}()

	// Channel for interrupt signal (Ctrl+C)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Send message(s)
	for i := 0; i < *repeat; i++ {
		select {
		case <-interrupt:
			log.Println("Interrupted, closing...")
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		default:
			sendText := *msg
			if *repeat > 1 {
				sendText = fmt.Sprintf("%s (message #%d)", *msg, i+1)
			}
			log.Printf(">>> SENDING: %s", sendText)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(sendText)); err != nil {
				log.Fatalf("Write error: %v", err)
			}

			if i < *repeat-1 {
				time.Sleep(*interval)
			}
		}
	}

	// Give time to receive last response
	time.Sleep(500 * time.Millisecond)

	// Clean shutdown
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}
