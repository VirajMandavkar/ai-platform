package sandbox

import (
	"io"
	"log"
	"os"

	"github.com/gorilla/websocket"
)

// PumpFromPTYToWebsocket continuously reads from the PTY and writes to the WebSocket.
func PumpFromPTYToWebsocket(ptmx *os.File, ws *websocket.Conn) {
	buf := make([]byte, 4096)

	for {
		n, err := ptmx.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("PTY read error, breaking loop: %v", err)
			}
			break
		}

		err = ws.WriteMessage(websocket.TextMessage, buf[:n])
		if err != nil {
			log.Printf("WebSocket write error, breaking loop: %v", err)
			break
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
