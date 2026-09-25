package acp

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

// A persistent host replays a long turn's journal in one burst on reconnect.
// That burst must wait for the consumer instead of overflowing the SDK's
// 1024-notification queue, which closes the connection and wedges the host.
func TestReplayBurstBackpressuresInsteadOfClosingConnection(t *testing.T) {
	const frames = eventBuffer + 2000
	stdout, provider := io.Pipe()
	proc := &process{
		stdin: discardWriteCloser{}, stdout: stdout,
		stop:      func() error { return stdout.Close() },
		terminate: func() error { return stdout.Close() },
	}
	conv := newConversation(proc, slog.New(slog.NewTextHandler(io.Discard, nil)), "", nil, nil)
	defer conv.Close()

	go func() {
		frame := []byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"}}}}` + "\n")
		for i := 0; i < frames; i++ {
			if _, err := provider.Write(frame); err != nil {
				return
			}
		}
	}()

	// Nothing consumes events yet, exactly like a reconnect before the controller
	// starts projecting. Give the SDK every chance to overflow.
	deadline := time.Now().Add(5 * time.Second)
	for len(conv.events) < eventBuffer && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	select {
	case <-conv.conn.Done():
		t.Fatal("SDK connection closed during replay burst")
	default:
	}

	got := 0
	timeout := time.After(10 * time.Second)
	for got < frames {
		select {
		case event, ok := <-conv.events:
			if !ok {
				t.Fatalf("events closed after %d of %d deltas", got, frames)
			}
			if event.Kind == ports.ChatEventMessageDelta {
				got++
			}
		case <-timeout:
			t.Fatalf("received %d of %d deltas", got, frames)
		}
	}
}
