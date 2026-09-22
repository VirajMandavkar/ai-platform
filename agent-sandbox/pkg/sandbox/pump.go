package sandbox

import (
	"context"
	"io"
	"log"
	"os"

	"github.com/gorilla/websocket"
)

// PumpFromPTYToWebsocket continuously reads from the PTY and writes to the WebSocket.
func PumpFromPTYToWebsocket(ctx context.Context, ptmx *os.File, ws *websocket.Conn) {
	buf := make([]byte, 4096)
	
	// Since os.File.Read is blocking and ignores context, we'll run it in a separate goroutine
	// that communicates via a channel, so this function can exit when ctx is canceled.
	type readResult struct {
		n   int
		err error
	}
	resultCh := make(chan readResult)
	
	go func() {
		for {
			n, err := ptmx.Read(buf)
			resultCh <- readResult{n, err}
			if err != nil {
				break
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case res := <-resultCh:
			if res.err != nil {
				if res.err != io.EOF {
					log.Printf("PTY read error, breaking loop: %v", res.err)
				}
				return
			}
			err := ws.WriteMessage(websocket.TextMessage, buf[:res.n])
			if err != nil {
				log.Printf("WebSocket write error, breaking loop: %v", err)
				return
			}
		}
	}
}

// PumpFromWebsocketToPTY continuously reads keystrokes from the WebSocket and writes them to the PTY.
func PumpFromWebsocketToPTY(ws *websocket.Conn, ptmx *os.File) {
	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			break
		}

		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			_, err = ptmx.Write(p)
			if err != nil {
				log.Printf("PTY write error, breaking loop: %v", err)
				break
			}
		}
	}
}
