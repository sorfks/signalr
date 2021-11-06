package signalr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/teivah/onecontext"
	"nhooyr.io/websocket"
)

type webSocketConnection struct {
	ConnectionBase
	conn         *websocket.Conn
	transferMode TransferMode
	watchDogChan chan dogFood
}

func newWebSocketConnection(parentContext context.Context, requestContext context.Context, connectionID string, conn *websocket.Conn) *webSocketConnection {
	ctx, _ := onecontext.Merge(parentContext, requestContext)
	w := &webSocketConnection{
		conn:         conn,
		watchDogChan: make(chan dogFood, 1),
		ConnectionBase: ConnectionBase{
			ctx:          ctx,
			connectionID: connectionID,
		},
	}
	log.Printf("%p: newWebSocketConnection: ConnectionID: %v", w, connectionID)
	go w.watchDog(ctx)
	return w
}

var eofCounter int = 0

func (w *webSocketConnection) Write(p []byte) (n int, err error) {
	log.Printf("%p: w.Write", w)
	if err := w.Context().Err(); err != nil {
		return 0, fmt.Errorf("webSocketConnection canceled: %w", w.ctx.Err())
	}
	messageType := websocket.MessageText
	if w.transferMode == BinaryTransferMode {
		messageType = websocket.MessageBinary
	}
	err = w.conn.Write(w.resetWatchDog(), messageType, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *webSocketConnection) Read(p []byte) (n int, err error) {
	log.Printf("%p: w.Read", w)
	if err := w.Context().Err(); err != nil {
		return 0, fmt.Errorf("webSocketConnection canceled: %w", w.ctx.Err())
	}
	msgType, data, err := w.conn.Read(w.resetWatchDog())
	log.Printf("%p: w.Read: msg: %v, err: %v, len(data): %d", w, msgType, err, len(data))
	if err != nil {
		if errors.Is(err, io.EOF) {
			log.Printf("%p: w.Read: error is EOF", w)
		}
		if strings.Contains(err.Error(), "EOF") {
			eofCounter++
			if eofCounter >= 100 {
				panic("zzz")
			}
		}

		return 0, err
	}
	return bytes.NewReader(data).Read(p)
}

// resetWatchDog resets the common watchDog for Read and Write.
// the watchDog will stop waiting for the last set timeout and wait for the new timeout.
func (w *webSocketConnection) resetWatchDog() context.Context {
	log.Printf("%p: w.resetWatchDog", w)
	ctx := w.ctx
	food := dogFood{timeout: w.timeout}
	if w.timeout > 0 {
		ctx, food.bark = context.WithCancel(w.ctx)
	}
	w.watchDogChan <- food
	return ctx
}

// dogFood is used to reset the watchDog
type dogFood struct {
	// After this, the dog will bark
	timeout time.Duration
	bark    context.CancelFunc
}

// watchDog is the common watchDog for Read and Write. It stops the connection (aka closes the Websocket)
// when the last timeout has elapsed. If resetWatchDog is called before the last timeout has elapsed,
// the watchDog will restart waiting for the new timeout. If timeout is set to 0, it will not wait at all.
func (w *webSocketConnection) watchDog(ctx context.Context) {
	log.Printf("%p: w.watchDog", w)
	var timer *time.Timer
	var cancelTimeoutChan chan struct{}
	for {
		select {
		case <-ctx.Done():
			return
		case food := <-w.watchDogChan:
			if timer != nil {
				if !timer.Stop() {
					go func() {
						log.Printf("%p: w.watchDog - 95", w)
						<-timer.C
					}()
				}
				go func() {
					log.Printf("%p: w.watchDog - 99", w)
					cancelTimeoutChan <- struct{}{}
				}()
			}
			if food.timeout != 0 {
				timer = time.NewTimer(food.timeout)
				cancelTimeoutChan = make(chan struct{}, 1)
				go func() {
					log.Printf("%p: w.watchDog - 106", w)
					select {
					case <-cancelTimeoutChan:
					case <-timer.C:
						food.bark()
					}
				}()
			} else {
				log.Printf("%p: w.watchDog - 115", w)
				timer = nil
			}
		}
	}
}

func (w *webSocketConnection) TransferMode() TransferMode {
	return w.transferMode
}

func (w *webSocketConnection) SetTransferMode(transferMode TransferMode) {
	w.transferMode = transferMode
}
