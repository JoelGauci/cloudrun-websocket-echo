package main

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type headerList []string

func (h *headerList) String() string {
	return strings.Join(*h, ", ")
}

func (h *headerList) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func main() {
	rawURL := flag.String("url", "ws://localhost:8080/connect", "WebSocket endpoint URL")
	msg := flag.String("msg", "Hello from WebSocket CLI client!", "Initial message to send (set empty to skip)")
	token := flag.String("token", "", "Google ID Token for Cloud Run authentication (Bearer token)")
	repeat := flag.Int("repeat", 1, "Number of times to send the initial message")
	interval := flag.Duration("interval", 1*time.Second, "Interval between repeated messages")
	closeAfter := flag.Bool("close", false, "Close connection immediately after sending initial message(s)")
	insecure := flag.Bool("insecure", false, "Skip TLS certificate verification (e.g. for self-signed certs like nip.io)")

	var customHeaders headerList
	flag.Var(&customHeaders, "H", "Custom HTTP header in 'Name: Value' format (can be specified multiple times)")
	flag.Var(&customHeaders, "header", "Alias for -H: Custom HTTP header in 'Name: Value' format")
	flag.Parse()

	u, err := url.Parse(*rawURL)
	if err != nil {
		log.Fatalf("Invalid URL: %v", err)
	}

	headers := make(http.Header)
	if *token != "" {
		headers.Set("Authorization", "Bearer "+*token)
	}
	for _, rawHeader := range customHeaders {
		parts := strings.SplitN(rawHeader, ":", 2)
		if len(parts) != 2 {
			log.Fatalf("Invalid header format %q: expected 'Header-Name: Header-Value'", rawHeader)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			log.Fatalf("Invalid header format %q: header name cannot be empty", rawHeader)
		}
		headers.Add(key, val)
		log.Printf("Added custom header: %s: %s", key, val)
	}

	dialer := *websocket.DefaultDialer
	if *insecure {
		dialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	log.Printf("Connecting to %s...", u.String())

	conn, resp, err := dialer.Dial(u.String(), headers)
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

	log.Println("Connected! Connection remains open (press Ctrl+C or type 'exit' to disconnect).")

	done := make(chan struct{})

	// Goroutine to read incoming messages
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Connection closed: %v", err)
				return
			}
			fmt.Printf("\n<<< RECEIVED FROM SERVER:\n%s\n> ", string(message))
		}
	}()

	// Channel for interrupt signal (Ctrl+C)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Send initial message(s)
	if *msg != "" && *repeat > 0 {
		for i := 0; i < *repeat; i++ {
			select {
			case <-interrupt:
				log.Println("Interrupted, closing...")
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
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
	}

	// If -close was explicitly requested, close after receiving the response
	if *closeAfter {
		time.Sleep(500 * time.Millisecond)
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		return
	}

	// Otherwise, keep connection open and read lines interactively from stdin
	inputChan := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputChan <- scanner.Text()
		}
		close(inputChan)
	}()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("Interrupt received, closing WebSocket connection gracefully...")
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		case line, ok := <-inputChan:
			if !ok {
				// stdin closed (e.g. piped input finished): wait for interrupt or server close
				<-interrupt
				return
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				fmt.Print("> ")
				continue
			}
			if trimmed == "exit" || trimmed == "quit" {
				log.Println("Closing connection...")
				_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			log.Printf(">>> SENDING: %s", trimmed)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(trimmed)); err != nil {
				log.Printf("Write error: %v", err)
				return
			}
		}
	}
}

