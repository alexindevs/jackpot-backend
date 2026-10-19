package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

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
	opened := m.cfg.Now()

	c, err := m.authenticate(ctx, ws)
	if err != nil {
		reason := "auth_failed"
		switch {
		case errors.Is(err, errAuthTimeout):
			reason = "auth_timeout"
		case errors.Is(err, errBadMessage):
			reason = "auth_bad_message"
		case !errors.Is(err, ErrUnauthorized):
			reason = "auth_unavailable"
		}
		m.countDisconnect(reason)
		m.cfg.Log.Info("client rejected", "reason", reason, "after", m.cfg.Now().Sub(opened).Round(time.Millisecond).String())
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, errBadMessage) {
			_ = ws.Close(codeAuthRequired, "sign in required")
		} else {
			_ = ws.Close(websocket.StatusTryAgainLater, "sign-in is unavailable, try again")
		}
		return
	}

	if m.cfg.OnSignIn != nil {
		m.cfg.OnSignIn(c.ident)
	}
	m.register(c)
	go c.writeLoop(ctx)
	go m.keepAlive(ctx, c)
	c.lastRead.Store(m.cfg.Now().UnixNano())
	c.enqueue(m.hello(c))
	m.Connected(c.ident.ID)

	readErr := m.readLoop(ctx, c)

	// Work out why it ended, for the log. If the server closed it, we know
	// exactly; otherwise it was the client or the network.
	reason, code := c.closedBecause(), -1
	if reason == "" {
		reason, code = classifyReadError(readErr)
	}
	m.countDisconnect(reason)
	now := m.cfg.Now()
	m.cfg.Log.Info("client disconnected",
		"player", string(c.ident.ID),
		"reason", reason,
		"code", code,
		"connected_for", now.Sub(opened).Round(time.Second).String(),
		"idle_for", now.Sub(time.Unix(0, c.lastRead.Load())).Round(time.Second).String(),
		"received", c.rx.Load(),
		"sent", c.tx.Load(),
	)

	c.closeWith(websocket.StatusNormalClosure, "bye", reason)
	if m.unregister(c) {
		m.Disconnected(c.ident.ID)
	}
}

// classifyReadError says why a client's connection ended when the server did
// not close it. A close frame with a normal code means the client chose to
// leave (closed the tab, navigated away, signed out). No close frame at all
// means the connection was cut: the network dropped.
func classifyReadError(err error) (reason string, code int) {
	status := websocket.CloseStatus(err)
	switch {
	case status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway:
		return "client_closed", int(status)
	case status != -1:
		return "client_closed_abnormally", int(status)
	case errors.Is(err, context.Canceled):
		return "server_stopping", -1
	case err == nil:
		return "client_closed", -1
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "EOF"), strings.Contains(msg, "reset"), strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "closed network connection"), strings.Contains(msg, "timed out"), strings.Contains(msg, "timeout"):
		return "network_drop", -1
	}
	return "error", -1
}

// keepAlive pings the client on a schedule and sends it a heartbeat message.
// The ping keeps proxies from treating a quiet connection as dead, and a
// client that has silently gone away is dropped instead of lingering. The
// heartbeat is for the page: browsers can't observe pings, so without a
// message of its own the page can't tell a quiet connection from a dead one.
func (m *Manager) keepAlive(ctx context.Context, c *conn) {
	ping := time.NewTicker(m.cfg.PingInterval)
	defer ping.Stop()
	beat := time.NewTicker(m.cfg.HeartbeatInterval)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-beat.C:
			c.enqueue(encode(outbound{T: "heartbeat", D: map[string]any{"serverTime": m.cfg.Now()}}))
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, m.cfg.PingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.closeWith(websocket.StatusGoingAway, "no answer to ping", "ping_timeout")
				return
			}
		}
	}
}

var errAuthTimeout = errors.New("sign-in timed out")

// authenticate reads the first message, which must carry the player's token.
func (m *Manager) authenticate(ctx context.Context, ws *websocket.Conn) (*conn, error) {
	actx, cancel := context.WithTimeout(ctx, m.cfg.AuthTimeout)
	defer cancel()
	typ, data, err := ws.Read(actx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errAuthTimeout
		}
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

// readLoop handles messages until the connection ends, and returns the error
// that ended it.
func (m *Manager) readLoop(ctx context.Context, c *conn) error {
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		c.rx.Add(1)
		c.lastRead.Store(m.cfg.Now().UnixNano())
		if typ != websocket.MessageText {
			c.closeWith(websocket.StatusUnsupportedData, "text only", "bad_frame")
			return nil
		}
		if !c.rate.allow(m.cfg.Now()) {
			c.closeWith(websocket.StatusPolicyViolation, "too many messages", "rate_limited")
			return nil
		}
		var in inbound
		if err := json.Unmarshal(data, &in); err != nil {
			c.enqueue(encode(outbound{T: "error", D: errorBody{Code: errCode(errBadMessage), Message: errBadMessage.Error()}}))
			continue
		}
		m.handle(c, in)
	}
}

const maxKeyLen = 64

// handle runs one request from a signed-in client. A request with a "key" is
// remembered per player: if the same key comes again (a retry after a network
// drop, say, possibly on a new connection) the first reply is sent again and
// nothing is run twice.
func (m *Manager) handle(c *conn, in inbound) {
	if len(in.Key) > maxKeyLen {
		c.enqueue(encodeReply(in.ID, errReply(errBadMessage)))
		return
	}
	if in.Key == "" || in.Type == "ping" || in.Type == "list_rooms" || in.Type == "my_games" {
		c.enqueue(encodeReply(in.ID, m.process(c, in)))
		return
	}

	cache := m.idemFor(c.ident.ID)
	cache.mu.Lock() // also serialises a retry that races the original on another connection
	defer cache.mu.Unlock()
	now := m.cfg.Now()
	rep, seen := cache.get(in.Key, now)
	if !seen {
		rep = m.process(c, in)
		cache.put(in.Key, rep, now)
	}
	c.enqueue(encodeReply(in.ID, rep))
}

func encodeReply(id json.RawMessage, r reply) []byte {
	return encode(outbound{T: r.t, ID: id, D: r.d})
}

func errReply(err error) reply {
	return reply{t: "error", d: errorBody{Code: errCode(err), Message: err.Error()}}
}

func okOrError(err error) reply {
	if err != nil {
		return errReply(err)
	}
	return reply{t: "ok"}
}

// process does what a request asks and says how to answer it.
func (m *Manager) process(c *conn, in inbound) reply {
	user := c.ident.ID
	switch in.Type {
	case "ping":
		return reply{t: "pong"}
	case "list_rooms":
		return reply{t: "rooms", d: m.ListRooms()}
	case "create_room":
		id, err := m.CreateRoom(user)
		if err != nil {
			return errReply(err)
		}
		return reply{t: "room_created", d: map[string]string{"room": id}}
	case "join":
		return okOrError(m.Join(user, in.Room))
	case "spectate":
		return okOrError(m.Spectate(user, in.Room))
	case "leave":
		return okOrError(m.Leave(user))
	case "my_games":
		return reply{t: "my_games", d: m.MyGames(user)}
	case "resume_game":
		return okOrError(m.ResumeGame(user, in.Room))
	}
	input, err := toInput(in)
	if err != nil {
		return errReply(err)
	}
	return okOrError(m.Do(user, input))
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
		c.closeWith(websocket.StatusGoingAway, "server restarting", "server_shutdown")
	}
}
