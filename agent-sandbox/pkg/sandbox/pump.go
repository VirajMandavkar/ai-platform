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
	type readResult struct {
		data []byte
		err  error
	}
	resultCh := make(chan readResult, 1)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// Copy data to a new slice to avoid race with next Read
				data := make([]byte, n)
				copy(data, buf[:n])
				resultCh <- readResult{data: data}
			}
			if err != nil {
				resultCh <- readResult{err: err}
				return
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
			if err := ws.WriteMessage(websocket.BinaryMessage, res.data); err != nil {
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
