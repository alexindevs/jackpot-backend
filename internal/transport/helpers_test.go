package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alexindevs/jackpot-backend/internal/room"
)

func newTestServer(t *testing.T, mod func(*Config)) (*Manager, string) {
	t.Helper()
	cfg := Config{Game: "jackpot", Room: room.DefaultConfig(), Auth: Dev{}}
	if mod != nil {
		mod(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := NewManager(ctx, cfg)
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(func() {
		m.CloseAll()
		cancel()
		srv.Close()
		m.Wait()
	})
	return m, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

type msg = map[string]any

type client struct {
	t      *testing.T
	ws     *websocket.Conn
	mu     sync.Mutex
	msgs   []msg
	closed chan struct{}
	code   websocket.StatusCode
}

// dialRaw connects without authenticating.
func dialRaw(t *testing.T, url string, header http.Header) (*client, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, err
	}
	c := &client{t: t, ws: ws, closed: make(chan struct{})}
	go c.readLoop()
	t.Cleanup(func() { _ = ws.CloseNow() })
	return c, nil
}

// dial connects and signs in as the dev user name.
func dial(t *testing.T, url, name string) *client {
	t.Helper()
	c, err := dialRaw(t, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.send(msg{"type": "auth", "token": "dev:" + name})
	return c
}

func (c *client) readLoop() {
	for {
		_, data, err := c.ws.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			c.code = websocket.CloseStatus(err)
			c.mu.Unlock()
			close(c.closed)
			return
		}
		var m msg
		if json.Unmarshal(data, &m) == nil {
			c.mu.Lock()
			c.msgs = append(c.msgs, m)
			c.mu.Unlock()
		}
	}
}

func (c *client) send(v any) {
	c.t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

func (c *client) closeCode() websocket.StatusCode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.code
}

func (c *client) waitClosed() websocket.StatusCode {
	c.t.Helper()
	select {
	case <-c.closed:
		return c.closeCode()
	case <-time.After(3 * time.Second):
		c.t.Fatal("the connection was not closed")
		return 0
	}
}

func (c *client) find(match func(msg) bool) (msg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.msgs {
		if match(m) {
			return m, true
		}
	}
	return nil, false
}

func (c *client) count(match func(msg) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.msgs {
		if match(m) {
			n++
		}
	}
	return n
}

// wait returns the first message (old or new) that matches.
func (c *client) wait(desc string, match func(msg) bool) msg {
	c.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m, ok := c.find(match); ok {
			return m
		}
		time.Sleep(2 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t.Fatalf("timed out waiting for %s; got %d messages: %v", desc, len(c.msgs), summarise(c.msgs))
	return nil
}

func summarise(ms []msg) []string {
	var out []string
	for _, m := range ms {
		s, _ := m["t"].(string)
		if k, ok := m["kind"].(string); ok {
			s += "/" + k
		}
		out = append(out, s)
	}
	return out
}

func isT(t string) func(msg) bool { return func(m msg) bool { return m["t"] == t } }

func isEvent(kind string) func(msg) bool {
	return func(m msg) bool { return m["t"] == "game_event" && m["kind"] == kind }
}

func data(m msg) msg {
	d, _ := m["d"].(map[string]any)
	return d
}

// request sends a request with an id and waits for its ok or error reply.
func (c *client) request(v msg) msg {
	c.t.Helper()
	id := float64(time.Now().UnixNano())
	v["id"] = id
	c.send(v)
	return c.wait("a reply to "+v["type"].(string), func(m msg) bool {
		return m["id"] == id && (m["t"] == "ok" || m["t"] == "error" || m["t"] == "room_created" || m["t"] == "rooms")
	})
}

func (c *client) mustOK(v msg) {
	c.t.Helper()
	if r := c.request(v); r["t"] == "error" {
		c.t.Fatalf("%v failed: %v", v["type"], data(r))
	}
}

func eventually(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

// startedGame signs in four players, puts them in one room, pairs them,
// starts the game and gets every round going.
type game struct {
	m       *Manager
	url     string
	room    string
	players map[string]*client
}

var names = []string{"alice", "bob", "carol", "dave"}

func startedGame(t *testing.T, mod func(*Config)) *game {
	t.Helper()
	m, url := newTestServer(t, mod)
	g := &game{m: m, url: url, players: map[string]*client{}}
	for _, n := range names {
		g.players[n] = dial(t, url, n)
		g.players[n].wait("hello", isT("hello"))
	}
	created := g.players["alice"].request(msg{"type": "create_room"})
	g.room = data(created)["room"].(string)
	for _, n := range names[1:] {
		g.players[n].mustOK(msg{"type": "join", "room": g.room})
	}
	g.players["alice"].mustOK(msg{"type": "invite_partner", "to": "bob"})
	g.players["bob"].mustOK(msg{"type": "accept_partner", "from": "alice"})
	g.players["carol"].mustOK(msg{"type": "invite_partner", "to": "dave"})
	g.players["dave"].mustOK(msg{"type": "accept_partner", "from": "carol"})
	g.players["alice"].mustOK(msg{"type": "start_now"})
	for _, n := range names {
		g.players[n].wait("chat_started", isT("chat_started"))
	}
	for _, n := range names {
		g.players[n].mustOK(msg{"type": "ready", "ready": true})
	}
	for _, n := range names {
		g.players[n].wait("round_started", isEvent("round_started"))
		g.players[n].wait("hand_update", isT("hand_update"))
	}
	return g
}

// hand reads a player's latest hand update.
func (c *client) hand() []int {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []int
	for _, m := range c.msgs {
		if m["t"] == "hand_update" {
			out = nil
			for _, v := range data(m)["hand"].([]any) {
				out = append(out, int(v.(float64)))
			}
		}
	}
	return out
}

func sum(h []int) int {
	n := 0
	for _, v := range h {
		n += v
	}
	return n
}
