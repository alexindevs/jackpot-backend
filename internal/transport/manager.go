package transport

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	mrand "math/rand/v2"
	"sync"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

var (
	errNotInRoom     = errors.New("you are not in a room")
	errAlreadyInRoom = errors.New("you are already in a room")
	errNoSuchRoom    = errors.New("no such room")
	errRateLimited   = errors.New("slow down")
)

// Config configures the transport.
type Config struct {
	Game           string        // the start.ng game name, e.g. "jackpot"
	Room           room.Config   // how rooms behave
	Auth           Authenticator // verifies players
	OriginPatterns []string      // hosts allowed to open a socket, e.g. "jackpot.start.ng"
	Now            func() time.Time

	// OnSignIn, if set, is told about every successful sign-in so player
	// names and looks can be saved. It must not block.
	OnSignIn func(Identity)

	// Log receives the server's operational log, including one line for every
	// connection that ends, with the reason. Defaults to discarding.
	Log *slog.Logger

	// HeartbeatInterval is how often the server sends each client a
	// {"t":"heartbeat"} message. Browsers can't see WebSocket pings, so this
	// is what lets a page notice a dead connection: if nothing at all has
	// arrived for a couple of intervals, reconnect.
	HeartbeatInterval time.Duration

	// A request that carries a "key" is run once: a retry with the same key
	// within IdempotencyTTL gets the first reply back. At most IdempotencyMax
	// keys are remembered per player.
	IdempotencyTTL time.Duration
	IdempotencyMax int

	// StatsInterval is how often a summary of disconnect reasons is logged.
	StatsInterval time.Duration

	// PingInterval is how often the server pings each client. Proxies drop
	// connections that stay silent, and a lobby can be quiet for minutes. A
	// client that doesn't answer within PingTimeout is disconnected.
	PingInterval time.Duration
	PingTimeout  time.Duration

	AuthTimeout  time.Duration // how long a new socket has to authenticate
	SendQueue    int           // per-connection outgoing queue; a full queue drops the connection
	MaxReadBytes int64
	RatePerSec   float64 // sustained client messages per second
	RateBurst    int
}

func (c *Config) setDefaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Log == nil {
		c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 20 * time.Second
	}
	if c.IdempotencyTTL == 0 {
		c.IdempotencyTTL = 10 * time.Minute
	}
	if c.IdempotencyMax == 0 {
		c.IdempotencyMax = 64
	}
	if c.StatsInterval == 0 {
		c.StatsInterval = 10 * time.Minute
	}
	if c.PingInterval == 0 {
		c.PingInterval = 25 * time.Second
	}
	if c.PingTimeout == 0 {
		c.PingTimeout = 10 * time.Second
	}
	if c.AuthTimeout == 0 {
		c.AuthTimeout = 5 * time.Second
	}
	if c.SendQueue == 0 {
		c.SendQueue = 256
	}
	if c.MaxReadBytes == 0 {
		c.MaxReadBytes = 8 << 10
	}
	if c.RatePerSec == 0 {
		c.RatePerSec = 20
	}
	if c.RateBurst == 0 {
		c.RateBurst = 40
	}
}

type managed struct {
	id     string
	runner *room.Runner
	cancel context.CancelFunc
}

// Manager owns every room and every connection.
type Manager struct {
	cfg Config
	ctx context.Context
	wg  sync.WaitGroup

	mu       sync.Mutex
	rooms    map[string]*managed
	userRoom map[engine.PlayerID]string // who is in which room (as player or spectator)
	conns    map[engine.PlayerID]*conn
	idents   map[engine.PlayerID]Identity
	idem     map[engine.PlayerID]*idemCache
	pausedBy map[engine.PlayerID]map[string]bool // paused games each player can go back to
	known    map[engine.PlayerID]Identity        // profiles of players in paused games, who may not be signed in

	statsMu sync.Mutex
	stats   map[string]int64 // disconnects by reason since the last summary
}

// NewManager starts a manager. Rooms stop when ctx ends.
func NewManager(ctx context.Context, cfg Config) *Manager {
	cfg.setDefaults()
	m := &Manager{
		cfg:      cfg,
		ctx:      ctx,
		rooms:    map[string]*managed{},
		userRoom: map[engine.PlayerID]string{},
		conns:    map[engine.PlayerID]*conn{},
		idents:   map[engine.PlayerID]Identity{},
		idem:     map[engine.PlayerID]*idemCache{},
		pausedBy: map[engine.PlayerID]map[string]bool{},
		known:    map[engine.PlayerID]Identity{},
		stats:    map[string]int64{},
	}
	m.wg.Add(1)
	go m.statsLoop()
	return m
}

// idemFor is a player's idempotency cache.
func (m *Manager) idemFor(user engine.PlayerID) *idemCache {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.idem[user]
	if c == nil {
		c = newIdemCache(m.cfg.IdempotencyTTL, m.cfg.IdempotencyMax)
		m.idem[user] = c
	}
	return c
}

// countDisconnect adds one to a reason's tally.
func (m *Manager) countDisconnect(reason string) {
	m.statsMu.Lock()
	m.stats[reason]++
	m.statsMu.Unlock()
}

// statsLoop logs a summary of why connections ended, every StatsInterval, so
// patterns (lots of network drops, lots of slow clients) show up without
// reading every line.
func (m *Manager) statsLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.cfg.StatsInterval)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.statsMu.Lock()
			summary := m.stats
			m.stats = map[string]int64{}
			m.statsMu.Unlock()
			if len(summary) == 0 {
				continue
			}
			attrs := make([]any, 0, len(summary)*2+2)
			attrs = append(attrs, "window", m.cfg.StatsInterval.String())
			for reason, n := range summary {
				attrs = append(attrs, reason, n)
			}
			m.cfg.Log.Info("disconnect summary", attrs...)
		}
	}
}

// Wait blocks until every room's loop has finished.
func (m *Manager) Wait() { m.wg.Wait() }

func cryptoSeed() (uint64, uint64) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("no randomness available: " + err.Error())
	}
	return binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])
}

const roomIDAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no look-alikes

func newRoomID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("no randomness available: " + err.Error())
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = roomIDAlphabet[int(v)%len(roomIDAlphabet)]
	}
	return string(out)
}

// --- connections ---

// register makes c the user's connection, replacing any earlier one (which is
// told it was superseded). It reports whether there was an earlier one.
func (m *Manager) register(c *conn) {
	m.mu.Lock()
	old := m.conns[c.ident.ID]
	m.conns[c.ident.ID] = c
	m.idents[c.ident.ID] = c.ident
	m.mu.Unlock()
	if old != nil {
		old.closeWith(codeAnotherTab, "joined from another tab", "replaced")
	}
}

// unregister forgets c if it is still the user's connection, and reports
// whether it was.
func (m *Manager) unregister(c *conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conns[c.ident.ID] != c {
		return false
	}
	delete(m.conns, c.ident.ID)
	m.gcIdentLocked(c.ident.ID)
	return true
}

// gcIdentLocked forgets an identity nobody needs any more.
func (m *Manager) gcIdentLocked(id engine.PlayerID) {
	if m.conns[id] == nil {
		if _, inRoom := m.userRoom[id]; !inRoom {
			delete(m.idents, id)
			delete(m.idem, id)
		}
	}
}

func (m *Manager) roomOf(user engine.PlayerID) (*managed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.userRoom[user]
	if !ok {
		return nil, false
	}
	return m.rooms[id], true
}

// Connected is called when a user's socket is authenticated. If they were in
// a room, the room is told they are back and sends them a snapshot.
func (m *Manager) Connected(user engine.PlayerID) {
	mr, inRoom := m.roomOf(user)
	if !inRoom {
		return
	}
	if mr == nil {
		m.dropMapping(user)
		return
	}
	if err := mr.runner.Submit(user, room.Connect{}); err != nil {
		m.dropMapping(user) // they are no longer part of that room
	}
}

// Disconnected is called when a user's socket goes away.
func (m *Manager) Disconnected(user engine.PlayerID) {
	if mr, ok := m.roomOf(user); ok && mr != nil {
		_ = mr.runner.Submit(user, room.Disconnect{})
		if m.pausedNow(mr) {
			// A paused game they had gone back to is theirs to find again.
			m.mu.Lock()
			if m.userRoom[user] == mr.id {
				delete(m.userRoom, user)
				m.addPausedLocked(user, mr.id)
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) dropMapping(user engine.PlayerID) {
	m.mu.Lock()
	delete(m.userRoom, user)
	m.gcIdentLocked(user)
	m.mu.Unlock()
}

// --- rooms ---

// CreateRoom opens a room with user in it. short asks for a short game.
func (m *Manager) CreateRoom(user engine.PlayerID, short bool) (string, error) {
	m.mu.Lock()
	if _, in := m.userRoom[user]; in {
		m.mu.Unlock()
		return "", errAlreadyInRoom
	}
	id := newRoomID()
	for m.rooms[id] != nil {
		id = newRoomID()
	}
	m.userRoom[user] = id
	m.mu.Unlock()

	a, b := cryptoSeed()
	var opts []room.Option
	if short {
		opts = append(opts, room.Short())
	}
	r, initial := room.New(m.cfg.Room, id, user, mrand.New(mrand.NewPCG(a, b)), m.cfg.Now(), opts...)
	m.launch(id, r, initial)
	return id, nil
}

// roomEnded forgets a room whose loop has stopped, and everyone in it.
func (m *Manager) roomEnded(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rooms, id)
	for user, set := range m.pausedBy {
		delete(set, id)
		if len(set) == 0 {
			delete(m.pausedBy, user)
		}
	}
	for user, rid := range m.userRoom {
		if rid == id {
			delete(m.userRoom, user)
			m.gcIdentLocked(user)
		}
	}
}

// Join, Spectate and Leave move a user in and out of a room.
func (m *Manager) Join(user engine.PlayerID, roomID string) error {
	return m.enter(user, roomID, room.Join{})
}

func (m *Manager) Spectate(user engine.PlayerID, roomID string) error {
	return m.enter(user, roomID, room.Spectate{})
}

func (m *Manager) enter(user engine.PlayerID, roomID string, in room.Input) error {
	m.mu.Lock()
	if _, already := m.userRoom[user]; already {
		m.mu.Unlock()
		return errAlreadyInRoom
	}
	mr := m.rooms[roomID]
	if mr == nil {
		m.mu.Unlock()
		return errNoSuchRoom
	}
	m.userRoom[user] = roomID // claimed before the room answers, so a double click can't enter twice
	m.mu.Unlock()

	if err := mr.runner.Submit(user, in); err != nil {
		m.dropMapping(user)
		return err
	}
	return nil
}

func (m *Manager) Leave(user engine.PlayerID) error {
	mr, in := m.roomOf(user)
	if !in {
		return errNotInRoom
	}
	if mr == nil {
		m.dropMapping(user)
		return errNotInRoom
	}
	if err := mr.runner.Submit(user, room.Leave{}); err != nil && !errors.Is(err, room.ErrClosed) {
		return err
	}
	m.dropMapping(user)
	return nil
}

// Do runs a room input for a user in their current room.
func (m *Manager) Do(user engine.PlayerID, in room.Input) error {
	mr, inRoom := m.roomOf(user)
	if !inRoom || mr == nil {
		return errNotInRoom
	}
	return mr.runner.Submit(user, in)
}

// ListRooms lists the rooms that are running, newest information first from
// each room's own loop.
func (m *Manager) ListRooms() []room.Info {
	m.mu.Lock()
	rooms := make([]*managed, 0, len(m.rooms))
	for _, mr := range m.rooms {
		rooms = append(rooms, mr)
	}
	m.mu.Unlock()

	var infos []room.Info
	for _, mr := range rooms {
		var info room.Info
		if err := mr.runner.Inspect(func(r *room.Room) { info = r.Info() }); err == nil && !info.Paused {
			infos = append(infos, info)
		}
	}
	sortInfos(infos)
	return infos
}

func sortInfos(infos []room.Info) {
	for i := 1; i < len(infos); i++ {
		for j := i; j > 0 && infos[j].ID < infos[j-1].ID; j-- {
			infos[j], infos[j-1] = infos[j-1], infos[j]
		}
	}
}

// --- delivery ---

// deliver sends a room's outputs to the right connections. It runs on the
// room's own goroutine and must not block: connections have queues, and a
// connection that can't keep up is dropped instead of slowing the game.
func (m *Manager) deliver(roomID string, outs []room.Out) {
	for _, o := range outs {
		switch v := o.Msg.(type) {
		case room.MemberLeft:
			m.mu.Lock()
			if m.userRoom[v.Player] == roomID {
				delete(m.userRoom, v.Player)
				m.gcIdentLocked(v.Player)
			}
			m.mu.Unlock()
		case room.RoomClosed:
			m.roomEnded(roomID)
		case room.Paused:
			m.onPaused(roomID, o.To, v.Players)
		}

		m.mu.Lock()
		c := m.conns[o.To]
		var profiles []Identity
		if c != nil {
			for _, id := range profilesIn(o.Msg) {
				if ident, ok := m.identLocked(id); ok {
					profiles = append(profiles, ident)
				}
			}
		}
		m.mu.Unlock()
		if c == nil {
			continue
		}
		for _, p := range profiles {
			c.enqueue(encode(outbound{T: "profile", D: p}))
		}
		c.enqueue(encodeOut(o))
	}
}

// profilesIn lists the players a message mentions that the client may need a
// name and look for.
func profilesIn(msg room.Message) []engine.PlayerID {
	switch v := msg.(type) {
	case room.Snapshot:
		ids := make([]engine.PlayerID, 0, len(v.Players))
		for _, p := range v.Players {
			ids = append(ids, p.ID)
		}
		return ids
	case room.MemberJoined:
		return []engine.PlayerID{v.Player}
	case room.PartnerInvite:
		return []engine.PlayerID{v.From}
	case room.RoomInvite:
		return []engine.PlayerID{v.From}
	}
	return nil
}

func (m *Manager) hello(c *conn) []byte {
	return encode(outbound{T: "hello", D: map[string]any{
		"you":        c.ident,
		"serverTime": m.cfg.Now(),
	}})
}

func (m *Manager) roomsMessage() []byte {
	return encode(outbound{T: "rooms", D: m.ListRooms()})
}
