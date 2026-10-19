package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/coder/websocket"
)

// Handler serves the WebSocket endpoint at /ws and a health check at /healthz.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", m.serveWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (m *Manager) serveWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: m.cfg.OriginPatterns})
	if err != nil {
		return // Accept has already answered the client
	}
	ws.SetReadLimit(m.cfg.MaxReadBytes)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c, err := m.authenticate(ctx, ws)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, errBadMessage) {
			_ = ws.Close(codeAuthRequired, "sign in required")
		} else {
			_ = ws.Close(websocket.StatusTryAgainLater, "sign-in is unavailable, try again")
		}
		return
	}

	m.register(c)
	go c.writeLoop(ctx)
	c.enqueue(m.hello(c))
	m.Connected(c.ident.ID)

	m.readLoop(ctx, c)

	c.closeWith(websocket.StatusNormalClosure, "bye")
	if m.unregister(c) {
		m.Disconnected(c.ident.ID)
	}
}

// authenticate reads the first message, which must carry the player's token.
func (m *Manager) authenticate(ctx context.Context, ws *websocket.Conn) (*conn, error) {
	actx, cancel := context.WithTimeout(ctx, m.cfg.AuthTimeout)
	defer cancel()
	typ, data, err := ws.Read(actx)
	if err != nil {
		return nil, ErrUnauthorized
	}
	var in inbound
	if typ != websocket.MessageText || json.Unmarshal(data, &in) != nil || in.Type != "auth" || in.Token == "" {
		return nil, errBadMessage
	}
	ident, err := m.cfg.Auth.Authenticate(actx, in.Token)
	if err != nil {
		return nil, err
	}
	return newConn(ws, ident, m.cfg.SendQueue, m.cfg.RatePerSec, m.cfg.RateBurst, m.cfg.Now()), nil
}

func (m *Manager) readLoop(ctx context.Context, c *conn) {
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			c.closeWith(websocket.StatusUnsupportedData, "text only")
			return
		}
		if !c.rate.allow(m.cfg.Now()) {
			c.closeWith(websocket.StatusPolicyViolation, "too many messages")
			return
		}
		var in inbound
		if err := json.Unmarshal(data, &in); err != nil {
			c.enqueue(fail(nil, errBadMessage))
			continue
		}
		m.handle(c, in)
	}
}

// handle runs one request from a signed-in client.
func (m *Manager) handle(c *conn, in inbound) {
	user := c.ident.ID
	reply := func(err error) {
		if err != nil {
			c.enqueue(fail(in.ID, err))
			return
		}
		c.enqueue(ok(in.ID))
	}
	switch in.Type {
	case "ping":
		c.enqueue(encode(outbound{T: "pong", ID: in.ID}))
	case "list_rooms":
		c.enqueue(encode(outbound{T: "rooms", ID: in.ID, D: m.ListRooms()}))
	case "create_room":
		id, err := m.CreateRoom(user)
		if err != nil {
			reply(err)
			return
		}
		c.enqueue(encode(outbound{T: "room_created", ID: in.ID, D: map[string]string{"room": id}}))
	case "join":
		reply(m.Join(user, in.Room))
	case "spectate":
		reply(m.Spectate(user, in.Room))
	case "leave":
		reply(m.Leave(user))
	default:
		input, err := toInput(in)
		if err != nil {
			reply(err)
			return
		}
		reply(m.Do(user, input))
	}
}

// CloseAll disconnects every client, for shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	conns := make([]*conn, 0, len(m.conns))
	for _, c := range m.conns {
		conns = append(conns, c)
	}
	m.mu.Unlock()
	for _, c := range conns {
		c.closeWith(websocket.StatusGoingAway, "server restarting")
	}
}
