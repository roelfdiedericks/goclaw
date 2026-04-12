package http

import (
	"bufio"
	"context"
	"fmt"
	"net"
	stdhttp "net/http"
	"testing"
	"time"

	"github.com/roelfdiedericks/goclaw/internal/delivery"
	"github.com/roelfdiedericks/goclaw/internal/user"
)

func TestDeliverSystemMessageEmitsSystemEvent(t *testing.T) {
	u := &user.User{ID: "owner", HTTPPasswordHash: "hash"}
	conn := &SSEConnection{
		Events: make(chan SSEEvent, 1),
		Done:   make(chan struct{}),
	}
	sess := &SSESession{
		SessionID:  "sess1",
		UserID:     u.ID,
		User:       u,
		activeConn: conn,
	}
	ch := &HTTPChannel{
		sessions: map[string]*SSESession{
			"sess1": sess,
		},
	}

	err := ch.DeliverSystemMessage(context.Background(), u, delivery.SystemMessage{
		Kind:    delivery.SystemKindStatus,
		Title:   "Status",
		Content: "Running cron...",
	})
	if err != nil {
		t.Fatalf("DeliverSystemMessage returned error: %v", err)
	}

	select {
	case event := <-conn.Events:
		if event.Event != "system" {
			t.Fatalf("expected system event, got %q", event.Event)
		}
		payload, ok := event.Data.(map[string]string)
		if !ok {
			t.Fatalf("expected map[string]string payload, got %#v", event.Data)
		}
		if payload["message"] != "**[Status]**\n\nRunning cron..." {
			t.Fatalf("unexpected system payload: %#v", payload)
		}
	default:
		t.Fatalf("expected a queued SSE system event")
	}
}

func TestServerStopClosesActiveSSEConnection(t *testing.T) {
	conn := &SSEConnection{
		Events: make(chan SSEEvent, 1),
		Done:   make(chan struct{}),
	}
	sess := &SSESession{
		SessionID:  "sess-stop",
		UserID:     "owner",
		User:       &user.User{ID: "owner", HTTPPasswordHash: "hash"},
		activeConn: conn,
	}

	ready := make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &Server{
		channel:    &HTTPChannel{sessions: map[string]*SSESession{"sess-stop": sess}},
		running:    true,
		instanceID: "test",
	}
	server.server = &stdhttp.Server{
		Handler: stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprint(w, ": ready\n\n"); err != nil {
				return
			}
			if f, ok := w.(stdhttp.Flusher); ok {
				f.Flush()
			}
			close(ready)
			select {
			case <-conn.Done:
				return
			case <-r.Context().Done():
				return
			}
		}),
	}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		_ = server.server.Serve(ln)
	}()

	resp, err := stdhttp.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read initial sse line: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler not ready")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- server.Stop()
	}()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Server.Stop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Server.Stop timed out with active SSE connection")
	}
}
