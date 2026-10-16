package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/somewhere-tech/sessions/runtime/internal/proto"
	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestTerminalStreamReleasesSlotBeforeWritingFrame(t *testing.T) {
	for _, kind := range []proto.EventKind{proto.EventExit, proto.EventRunnerLost} {
		t.Run(string(kind), func(t *testing.T) {
			peers := make(chan *wsPeer, 1)
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				peers <- &wsPeer{connection: conn}
				<-stop
			}))
			defer server.Close()
			defer close(stop)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseNow()
			peer := <-peers
			peer.writes.Lock()
			unlocked := false
			defer func() {
				if !unlocked {
					peer.writes.Unlock()
				}
			}()
			events := make(chan proto.Event, 1)
			events <- proto.Event{Kind: kind}
			released, finished := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(finished)
				streamAttachment(ctx, peer, state.Attachment{Events: events}, streamOptions{
					releaseTerminal: func() { close(released) },
				})
			}()
			select {
			case <-released:
			case <-ctx.Done():
				t.Fatal("terminal slot release waited for frame write")
			}
			peer.writes.Unlock()
			unlocked = true
			if _, _, err := client.Read(ctx); err != nil {
				t.Fatal(err)
			}
			<-finished
		})
	}
}
