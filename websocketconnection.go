package signalr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

type webSocketConnection struct {
	ConnectionBase
	conn         *websocket.Conn
	transferMode TransferMode
	wdMx         sync.Mutex
	dog          *watchDog
	watchDogChan chan *watchDog
}

func newWebSocketConnection(ctx context.Context, connectionID string, conn *websocket.Conn) *webSocketConnection {
	w := &webSocketConnection{
		conn:         conn,
		watchDogChan: make(chan *watchDog, 1),
		ConnectionBase: ConnectionBase{
			ctx:          ctx,
			connectionID: connectionID,
		},
	}
	log.Printf("%p: newWebSocketConnection: ConnectionID: %v", w, connectionID)
	go w.setupWatchDog(ctx)
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
	err = w.conn.Write(w.changeWatchDog(), messageType, p)
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
	msgType, data, err := w.conn.Read(w.changeWatchDog())
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

// setupWatchDog starts the common watchDog for Read and Write. The watchDog stops the connection (aka closes the Websocket)
// when the last timeout has elapsed. If changeWatchDog is called before the last timeout has elapsed,
// the watchDog will restart waiting for the new timeout. If timeout is set to 0, it will not wait at all.
func (w *webSocketConnection) setupWatchDog(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case newDog := <-w.watchDogChan:
			w.wdMx.Lock()
			if w.dog != nil {
				if w.dog.timer != nil && !w.dog.timer.Stop() {
					go func(c <-chan time.Time) {
						<-c
					}(w.dog.timer.C)
				}
				w.dog.Cancel()
			}
			w.dog = newDog
			if w.dog != nil {
				go w.dog.BarkOrDie()
			}
			w.wdMx.Unlock()
		}
	}
}

// changeWatchDog changes the common watchDog for Read and Write.
// the watchDog will stop waiting for the last set timeout and wait for the new timeout.
func (w *webSocketConnection) changeWatchDog() context.Context {
	ctx := w.ctx
	if w.timeout > 0 {
		var dog *watchDog
		ctx, dog = newWatchDog(w.ctx, w.timeout)
		w.watchDogChan <- dog
	} else {
		w.watchDogChan <- nil
	}
	return ctx
}

type watchDog struct {
	// After this, the dog will bark
	timer      *time.Timer
	cancelChan chan struct{}
	bark       context.CancelFunc
}

func newWatchDog(ctx context.Context, timeout time.Duration) (context.Context, *watchDog) {
	dog := &watchDog{
		timer:      time.NewTimer(timeout),
		cancelChan: make(chan struct{}),
	}
	var dogCtx context.Context
	dogCtx, dog.bark = context.WithCancel(ctx)
	return dogCtx, dog
}

func (d *watchDog) Cancel() {
	close(d.cancelChan)
}

func (d *watchDog) BarkOrDie() {
	select {
	case <-d.cancelChan:
	case <-d.timer.C:
		d.bark()
	}
}

func (w *webSocketConnection) TransferMode() TransferMode {
	return w.transferMode
}

func (w *webSocketConnection) SetTransferMode(transferMode TransferMode) {
	w.transferMode = transferMode
}
