package transport

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Close codes. 4401 and 4409 follow start.ng's own multiplayer convention.
const (
	codeAuthRequired websocket.StatusCode = 4401
	codeAnotherTab   websocket.StatusCode = 4409
)

// conn is one authenticated socket. Everything the server sends goes through
// a bounded queue: a connection that can't keep up is dropped, so a slow
// client can never hold up a room.
type conn struct {
	ws     *websocket.Conn
	closer closer
	ident  Identity
	send   chan []byte
	done   chan struct{}
	once   sync.Once
	rate   bucket

	why      atomic.Value // string: why the server closed this connection, if it did
	rx, tx   atomic.Int64 // messages received from and sent to the client
	lastRead atomic.Int64 // unix nanoseconds of the last message received
}

type closer interface {
	Close(code websocket.StatusCode, reason string) error
}

func newConn(ws *websocket.Conn, ident Identity, queue int, rate float64, burst int, now time.Time) *conn {
	return &conn{
		ws:     ws,
		closer: ws,
		ident:  ident,
		send:   make(chan []byte, queue),
		done:   make(chan struct{}),
		rate:   newBucket(rate, burst, now),
	}
}

// enqueue queues a message without ever blocking.
func (c *conn) enqueue(b []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.send <- b:
		return true
	default:
		c.closeWith(websocket.StatusPolicyViolation, "too slow", "too_slow")
		return false
	}
}

// closeWith closes the socket, once. why is a short machine-readable reason
// that ends up in the disconnect log ("replaced", "too_slow", ...).
func (c *conn) closeWith(code websocket.StatusCode, text, why string) {
	c.once.Do(func() {
		c.why.Store(why)
		close(c.done)
		go func() { _ = c.closer.Close(code, text) }()
	})
}

// closedBecause is the reason the server closed the connection, or "".
func (c *conn) closedBecause() string {
	s, _ := c.why.Load().(string)
	return s
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case b := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.closeWith(websocket.StatusInternalError, "write failed", "write_failed")
				return
			}
			c.tx.Add(1)
		}
	}
}

// bucket is a token bucket: rate tokens per second, up to burst.
type bucket struct {
	rate, burst, tokens float64
	last                time.Time
}

func newBucket(rate float64, burst int, now time.Time) bucket {
	return bucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

func (b *bucket) allow(now time.Time) bool {
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
