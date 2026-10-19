package transport

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// capLog is a slog handler that keeps what it is given, for assertions.
type capLog struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (l *capLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *capLog) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *capLog) WithGroup(string) slog.Handler            { return l }
func (l *capLog) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
	l.mu.Lock()
	l.recs = append(l.recs, m)
	l.mu.Unlock()
	return nil
}

func (l *capLog) find(match func(map[string]any) bool) (map[string]any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.recs {
		if match(r) {
			return r, true
		}
	}
	return nil, false
}

func (l *capLog) wait(t *testing.T, what string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	return l.waitFor(t, 3*time.Second, what, match)
}

func (l *capLog) waitFor(t *testing.T, d time.Duration, what string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if r, ok := l.find(match); ok {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Fatalf("timed out waiting for log: %s; log so far: %v", what, l.recs)
	return nil
}

func disconnectWith(reason string) func(map[string]any) bool {
	return func(r map[string]any) bool {
		return (r["msg"] == "client disconnected" || r["msg"] == "client rejected") && r["reason"] == reason
	}
}

func logged(l *capLog) func(*Config) {
	return func(c *Config) { c.Log = slog.New(l) }
}

// --- heartbeat ---

func TestTheServerSendsHeartbeatsSoAPageCanTellADeadConnectionFromAQuietOne(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) { c.HeartbeatInterval = 20 * time.Millisecond })
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	eventually(t, "several heartbeats", func() bool { return c.count(isT("heartbeat")) >= 3 })
	hb := c.wait("a heartbeat", isT("heartbeat"))
	if _, ok := data(hb)["serverTime"].(string); !ok {
		t.Errorf("a heartbeat should carry the server time: %v", hb)
	}
}

// --- idempotency keys ---

func TestARetryWithTheSameKeyDoesNotRunTwice(t *testing.T) {
	g := startedGame(t, nil)
	alice, bob := g.players["alice"], g.players["bob"]
	holder := ""
	for n, c := range g.players {
		if sum(c.hand()) == 5 {
			holder = n
		}
	}
	target := "carol"
	if holder == "carol" {
		target = "dave"
	}
	raised := func(c *client) int { return c.count(isEvent("suspicion_raised")) }

	first := alice.request(msg{"type": "suspect", "target": target, "key": "k-suspect-1"})
	if first["t"] != "ok" {
		t.Fatalf("first attempt: %v", first)
	}
	g.players[target].wait("the prompt", isEvent("reveal_requested"))
	g.players[target].mustOK(msg{"type": "reveal", "types": []int{0, 1}}) // answered: a keyless repeat would now charge again

	// The reply to the first attempt never reached alice, so she retries with the same key.
	retry := alice.request(msg{"type": "suspect", "target": target, "key": "k-suspect-1"})
	if retry["t"] != "ok" {
		t.Errorf("the retry should be answered like the original: %v", retry)
	}
	time.Sleep(50 * time.Millisecond)
	if n := raised(bob); n != 1 {
		t.Errorf("the suspicion ran %d times, want once", n)
	}

	// A new key is a new request.
	alice.request(msg{"type": "suspect", "target": target, "key": "k-suspect-2"})
	eventually(t, "a second suspicion", func() bool { return raised(bob) == 2 })
}

func TestARetryAfterReconnectingIsStillRecognised(t *testing.T) {
	_, url := newTestServer(t, nil)
	a1 := dial(t, url, "alice")
	a1.wait("hello", isT("hello"))
	first := a1.request(msg{"type": "create_room", "key": "make-room-1"})
	room1 := data(first)["room"].(string)
	_ = a1.ws.Close(websocket.StatusNormalClosure, "network dropped")

	a2 := dial(t, url, "alice")
	a2.wait("hello", isT("hello"))
	again := a2.request(msg{"type": "create_room", "key": "make-room-1"})
	if again["t"] != "room_created" || data(again)["room"] != room1 {
		t.Errorf("a retry on a new connection should get the same room back: %v", again)
	}
	// Without the key it would have been refused.
	if r := a2.request(msg{"type": "create_room"}); data(r)["code"] != "already_in_room" {
		t.Errorf("a keyless repeat: %v", r)
	}
}

func TestKeysBelongToOnePlayer(t *testing.T) {
	_, url := newTestServer(t, nil)
	alice, bob := dial(t, url, "alice"), dial(t, url, "bob")
	alice.wait("hello", isT("hello"))
	bob.wait("hello", isT("hello"))
	a := alice.request(msg{"type": "create_room", "key": "same-key"})
	b := bob.request(msg{"type": "create_room", "key": "same-key"})
	if b["t"] != "room_created" || data(a)["room"] == data(b)["room"] {
		t.Errorf("bob using alice's key must get his own room: %v %v", a, b)
	}
}

func TestRepliesToARetryCarryTheRetrysOwnRequestId(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	c.send(msg{"type": "create_room", "key": "k", "id": "first"})
	c.wait("first reply", func(m msg) bool { return m["id"] == "first" })
	c.send(msg{"type": "create_room", "key": "k", "id": "retry"})
	r := c.wait("the retry's reply", func(m msg) bool { return m["id"] == "retry" })
	if r["t"] != "room_created" {
		t.Errorf("reply = %v", r)
	}
}

func TestKeyedErrorsAreRememberedToo(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	if r := c.request(msg{"type": "leave", "key": "k"}); data(r)["code"] != "not_in_room" {
		t.Fatalf("first: %v", r)
	}
	c.request(msg{"type": "create_room"})
	// The retry reports what the original attempt got, not what would happen now.
	if r := c.request(msg{"type": "leave", "key": "k"}); data(r)["code"] != "not_in_room" {
		t.Errorf("retry: %v", r)
	}
}

func TestPingsAndRoomListsAreNeverCached(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	if r := c.request(msg{"type": "list_rooms", "key": "k"}); len(r["d"].([]any)) != 0 {
		t.Fatalf("rooms: %v", r)
	}
	c.request(msg{"type": "create_room"})
	if r := c.request(msg{"type": "list_rooms", "key": "k"}); len(r["d"].([]any)) != 1 {
		t.Errorf("a repeated list must be fresh: %v", r)
	}
}

func TestOverlongKeysAreRefused(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	long := make([]byte, maxKeyLen+1)
	for i := range long {
		long[i] = 'x'
	}
	if r := c.request(msg{"type": "create_room", "key": string(long)}); data(r)["code"] != "bad_message" {
		t.Errorf("long key: %v", r)
	}
}

func TestTheIdempotencyCacheExpiresAndEvicts(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := newIdemCache(10*time.Minute, 3)
	c.put("a", reply{t: "ok"}, t0)
	if _, ok := c.get("a", t0.Add(9*time.Minute)); !ok {
		t.Error("a recent key should be remembered")
	}
	if _, ok := c.get("a", t0.Add(11*time.Minute)); ok {
		t.Error("an old key should have expired")
	}
	for _, k := range []string{"b", "c", "d", "e"} {
		c.put(k, reply{t: "ok"}, t0)
	}
	if _, ok := c.get("b", t0); ok {
		t.Error("the oldest key should have been evicted past the limit")
	}
	for _, k := range []string{"c", "d", "e"} {
		if _, ok := c.get(k, t0); !ok {
			t.Errorf("%s should still be remembered", k)
		}
	}
}

// --- disconnect logging ---

func TestAGracefulCloseIsLoggedAsTheClientLeaving(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, logged(l))
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	c.send(msg{"type": "ping"})
	c.wait("pong", isT("pong"))
	_ = c.ws.Close(websocket.StatusNormalClosure, "bye")

	r := l.wait(t, "the disconnect", disconnectWith("client_closed"))
	if r["player"] != "alice" || r["code"] != int64(1000) && r["code"] != 1000 {
		t.Errorf("record = %v", r)
	}
	for _, k := range []string{"connected_for", "idle_for", "received", "sent"} {
		if _, ok := r[k]; !ok {
			t.Errorf("the record is missing %q: %v", k, r)
		}
	}
	if r["received"] != int64(1) {
		t.Errorf("received = %v (the auth message isn't counted, the ping is)", r["received"])
	}
}

func TestACutConnectionIsLoggedAsTheNetworkDropping(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, logged(l))
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	_ = c.ws.CloseNow() // no close frame: what a dropped mobile connection looks like
	l.wait(t, "a network drop", disconnectWith("network_drop"))
}

func TestReplacedTabsAreLoggedAsSuch(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, logged(l))
	c1 := dial(t, url, "alice")
	c1.wait("hello", isT("hello"))
	c2 := dial(t, url, "alice")
	c2.wait("hello", isT("hello"))
	l.wait(t, "the replaced tab", disconnectWith("replaced"))
}

func TestAnUnresponsiveClientIsLoggedAsAPingTimeout(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, func(c *Config) {
		logged(l)(c)
		c.PingInterval, c.PingTimeout = 20*time.Millisecond, 80*time.Millisecond
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	_ = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"auth","token":"dev:alice"}`))
	// A client that never reads also never answers the close frame, so the
	// close handshake takes its full 5s before the disconnect is logged.
	l.waitFor(t, 9*time.Second, "a ping timeout", disconnectWith("ping_timeout"))
}

func TestFloodingIsLoggedAsRateLimiting(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, func(c *Config) { logged(l)(c); c.RatePerSec = 1; c.RateBurst = 3 })
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	for i := 0; i < 10; i++ {
		_ = c.ws.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`))
	}
	l.wait(t, "rate limiting", disconnectWith("rate_limited"))
}

func TestAServerShutdownIsLoggedAsOne(t *testing.T) {
	l := &capLog{}
	m, url := newTestServer(t, logged(l))
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	m.CloseAll()
	l.wait(t, "the shutdown", disconnectWith("server_shutdown"))
}

func TestRejectedSignInsSayWhy(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, func(c *Config) { logged(l)(c); c.AuthTimeout = 100 * time.Millisecond })

	bad, _ := dialRaw(t, url, nil)
	bad.send(msg{"type": "auth", "token": "nonsense"})
	l.wait(t, "a refused token", disconnectWith("auth_failed"))

	silent, _ := dialRaw(t, url, nil)
	_ = silent
	l.wait(t, "a silent socket", disconnectWith("auth_timeout"))

	wrong, _ := dialRaw(t, url, nil)
	wrong.send(msg{"type": "list_rooms"})
	l.wait(t, "a wrong first message", disconnectWith("auth_bad_message"))
}

func TestAnAuthOutageIsLoggedAsOne(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, func(c *Config) { logged(l)(c); c.Auth = brokenAuth{} })
	c := dial(t, url, "alice")
	_ = c
	l.wait(t, "an outage", disconnectWith("auth_unavailable"))
}

func TestDisconnectReasonsAreSummarisedPeriodically(t *testing.T) {
	l := &capLog{}
	_, url := newTestServer(t, func(c *Config) { logged(l)(c); c.StatsInterval = 60 * time.Millisecond })
	for _, name := range []string{"alice", "bob"} {
		c := dial(t, url, name)
		c.wait("hello", isT("hello"))
		_ = c.ws.Close(websocket.StatusNormalClosure, "bye")
	}
	r := l.wait(t, "a summary", func(r map[string]any) bool {
		return r["msg"] == "disconnect summary" && r["client_closed"] != nil
	})
	if r["client_closed"] != int64(2) && r["client_closed"] != int64(1) {
		t.Errorf("summary = %v", r)
	}
}

// --- missed-message replay ---

func TestAReconnectingPlayerCanAskForWhatTheyMissed(t *testing.T) {
	g := startedGame(t, nil)
	alice, bob := g.players["alice"], g.players["bob"]

	// bob saw some chat, noted its sequence number, then lost his connection
	alice.mustOK(msg{"type": "chat", "text": "first"})
	seen := bob.wait("first line", isT("chat_message"))
	last := seen["s"].(float64)
	if last < 1 {
		t.Fatalf("chat_message should be numbered: %v", seen)
	}
	_ = bob.ws.Close(websocket.StatusNormalClosure, "tunnel")
	alice.wait("bob drops", func(m msg) bool {
		return m["t"] == "connection_changed" && data(m)["player"] == "bob" && data(m)["connected"] == false
	})
	alice.mustOK(msg{"type": "chat", "text": "second"})
	alice.mustOK(msg{"type": "chat", "text": "third"})

	bob2 := dial(t, g.url, "bob")
	snap := bob2.wait("snapshot", isT("snapshot"))
	if snap["d"].(map[string]any)["seq"] == nil {
		t.Fatalf("a snapshot should say where the sequence stood: %v", snap)
	}
	r := bob2.request(msg{"type": "resume", "after": last})
	if r["t"] != "ok" {
		t.Fatalf("resume: %v", r)
	}
	missed := data(bob2.wait("missed", isT("missed")))
	if missed["complete"] != true {
		t.Errorf("missed = %v", missed)
	}
	var texts []string
	for _, raw := range missed["messages"].([]any) {
		m := raw.(map[string]any)
		if m["t"] == "chat_message" {
			texts = append(texts, m["d"].(map[string]any)["text"].(string))
			if m["s"].(float64) <= last {
				t.Errorf("replayed something already seen: %v", m)
			}
		}
	}
	if len(texts) != 2 || texts[0] != "second" || texts[1] != "third" {
		t.Errorf("replayed chat = %v", texts)
	}
}

func TestResumeOutsideARoomIsRefusedAndNegativeIsMalformed(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	if r := c.request(msg{"type": "resume", "after": 0}); data(r)["code"] != "not_in_room" {
		t.Errorf("outside a room: %v", r)
	}
	if r := c.request(msg{"type": "resume", "after": -1}); data(r)["code"] != "bad_message" {
		t.Errorf("negative: %v", r)
	}
}
