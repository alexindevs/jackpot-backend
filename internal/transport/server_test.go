package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alexindevs/jackpot-backend/internal/room"
)

func TestSignInRequired(t *testing.T) {
	_, url := newTestServer(t, nil)

	c, _ := dialRaw(t, url, nil)
	c.send(msg{"type": "auth", "token": "nonsense"})
	if code := c.waitClosed(); code != 4401 {
		t.Errorf("bad token: close code %d, want 4401", code)
	}

	c, _ = dialRaw(t, url, nil)
	c.send(msg{"type": "list_rooms"}) // anything but auth first
	if code := c.waitClosed(); code != 4401 {
		t.Errorf("no auth: close code %d, want 4401", code)
	}

	c, _ = dialRaw(t, url, nil)
	c.send(msg{"type": "auth"}) // no token
	if code := c.waitClosed(); code != 4401 {
		t.Errorf("empty token: close code %d, want 4401", code)
	}
}

func TestSilentSocketsAreDropped(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) { c.AuthTimeout = 100 * time.Millisecond })
	c, _ := dialRaw(t, url, nil)
	// The library drops a socket whose read timed out without a close frame,
	// so there is no code to check: it is enough that it is closed.
	c.waitClosed()
}

type brokenAuth struct{}

func (brokenAuth) Authenticate(context.Context, string) (Identity, error) {
	return Identity{}, errors.New("start.ng is down")
}

func TestAnAuthOutageSaysTryAgainRatherThanSignIn(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) { c.Auth = brokenAuth{} })
	c := dial(t, url, "alice")
	if code := c.waitClosed(); code != websocket.StatusTryAgainLater {
		t.Errorf("close code %d, want %d", code, websocket.StatusTryAgainLater)
	}
}

func TestOnlyAllowedOriginsMayConnect(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) { c.OriginPatterns = []string{"jackpot.start.ng"} })
	if _, err := dialRaw(t, url, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		t.Error("a page on another site must not be able to open a socket")
	}
	c, err := dialRaw(t, url, http.Header{"Origin": {"https://jackpot.start.ng"}})
	if err != nil {
		t.Fatalf("jackpot.start.ng should be allowed: %v", err)
	}
	c.send(msg{"type": "auth", "token": "dev:alice"})
	c.wait("hello", isT("hello"))
}

func TestHelloNamesYouFromTheAuthenticator(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	hello := c.wait("hello", isT("hello"))
	you := data(hello)["you"].(map[string]any)
	if you["id"] != "alice" || you["name"] != "alice" {
		t.Errorf("you = %v", you)
	}
	if _, ok := data(hello)["serverTime"].(string); !ok {
		t.Error("hello should carry the server's time")
	}
}

func TestRoomLifecycleOverTheWire(t *testing.T) {
	_, url := newTestServer(t, nil)
	a, b := dial(t, url, "alice"), dial(t, url, "bob")
	a.wait("hello", isT("hello"))
	b.wait("hello", isT("hello"))

	// Errors carry stable codes.
	if r := a.request(msg{"type": "leave"}); data(r)["code"] != "not_in_room" {
		t.Errorf("leave without a room: %v", data(r))
	}
	if r := a.request(msg{"type": "join", "room": "nope"}); data(r)["code"] != "no_such_room" {
		t.Errorf("join unknown: %v", data(r))
	}
	if r := a.request(msg{"type": "pass", "card": 9}); data(r)["code"] != "bad_message" {
		t.Errorf("bad card: %v", data(r))
	}
	if r := a.request(msg{"type": "pass"}); data(r)["code"] != "bad_message" {
		t.Errorf("missing card: %v", data(r))
	}
	if r := a.request(msg{"type": "proclaim"}); data(r)["code"] != "not_in_room" {
		t.Errorf("proclaim without a room: %v", data(r))
	}

	created := a.request(msg{"type": "create_room"})
	id := data(created)["room"].(string)
	if r := a.request(msg{"type": "create_room"}); data(r)["code"] != "already_in_room" {
		t.Errorf("second room: %v", data(r))
	}
	if r := a.request(msg{"type": "join", "room": id}); data(r)["code"] != "already_in_room" {
		t.Errorf("join while in a room: %v", data(r))
	}

	b.mustOK(msg{"type": "join", "room": id})
	b.wait("snapshot", isT("snapshot"))
	a.wait("member_joined for bob", func(m msg) bool {
		return m["t"] == "member_joined" && data(m)["player"] == "bob"
	})
	// Alice hears about bob, with his name for the character card.
	a.wait("bob's profile", func(m msg) bool { return m["t"] == "profile" && data(m)["id"] == "bob" })

	b.mustOK(msg{"type": "leave"})
	a.wait("member_left", isT("member_left"))
	b.mustOK(msg{"type": "create_room"}) // free to start their own
}

func TestListRoomsShowsOpenRooms(t *testing.T) {
	_, url := newTestServer(t, nil)
	a, b := dial(t, url, "alice"), dial(t, url, "bob")
	a.wait("hello", isT("hello"))
	b.wait("hello", isT("hello"))
	a.request(msg{"type": "create_room"})

	r := b.request(msg{"type": "list_rooms"})
	list, _ := r["d"].([]any)
	if len(list) != 1 {
		t.Fatalf("rooms = %v", r["d"])
	}
	info := list[0].(map[string]any)
	if info["players"] != float64(1) || info["joinable"] != true || info["phase"] != "lobby" || info["creator"] != "alice" {
		t.Errorf("room info = %v", info)
	}
}

func TestAFullGameOverWebSockets(t *testing.T) {
	g := startedGame(t, nil)

	// Everyone is dealt a hand; exactly one player holds the passing card.
	var holder string
	for n, c := range g.players {
		switch h := c.hand(); sum(h) {
		case 5:
			holder = n
		case 4:
		default:
			t.Fatalf("%s holds %v", n, h)
		}
	}
	if holder == "" {
		t.Fatal("nobody holds the passing card")
	}

	// The holder passes a card.
	hh := g.players[holder].hand()
	card := 0
	for i, v := range hh {
		if v > 0 {
			card = i
			break
		}
	}
	g.players[holder].mustOK(msg{"type": "pass", "card": card})

	// Everyone sees a pass, but only the two players involved see the card.
	var from, to string
	for _, c := range g.players {
		d := data(c.wait("passed", isEvent("passed")))
		from, to = d["from"].(string), d["to"].(string)
	}
	if from != holder {
		t.Fatalf("the pass came from %s, want %s", from, holder)
	}
	for n, c := range g.players {
		d := data(c.wait("passed", isEvent("passed")))
		involved := n == from || n == to
		if seen := d["card"] != float64(room.CardHidden); seen != involved {
			t.Errorf("%s sees the card = %v, want %v (card=%v)", n, seen, involved, d["card"])
		}
	}
	// The receiver's hand changed and they were told; others were not.
	for n, c := range g.players {
		updates := c.count(isT("hand_update"))
		if (n == to || n == from) && updates < 2 {
			t.Errorf("%s should have a new hand update", n)
		}
		if n != to && n != from && updates != 1 {
			t.Errorf("%s got %d hand updates, want only the deal", n, updates)
		}
	}
}

func TestReconnectingRestoresYourSeat(t *testing.T) {
	g := startedGame(t, nil)
	alice, bob := g.players["alice"], g.players["bob"]

	_ = bob.ws.Close(websocket.StatusNormalClosure, "refresh")
	alice.wait("bob drops", func(m msg) bool {
		return m["t"] == "connection_changed" && data(m)["player"] == "bob" && data(m)["connected"] == false
	})

	bob2 := dial(t, g.url, "bob")
	bob2.wait("hello", isT("hello"))
	snap := bob2.wait("snapshot", isT("snapshot"))
	game, _ := data(snap)["game"].(map[string]any)
	if game == nil || game["yourHand"] == nil {
		t.Fatalf("a returning player needs their hand in the snapshot: %v", data(snap))
	}
	if got := len(game["yourHand"].([]any)); got != 4 {
		t.Errorf("hand has %d slots", got)
	}
	alice.wait("bob returns", func(m msg) bool {
		return m["t"] == "connection_changed" && data(m)["player"] == "bob" && data(m)["connected"] == true
	})
}

func TestASecondTabReplacesTheFirstWithoutDroppingYouFromTheGame(t *testing.T) {
	g := startedGame(t, nil)
	alice := g.players["alice"]
	bob1 := g.players["bob"]

	bob2 := dial(t, g.url, "bob")
	bob2.wait("snapshot", isT("snapshot"))
	if code := bob1.waitClosed(); code != 4409 {
		t.Errorf("the old tab closed with %d, want 4409", code)
	}
	time.Sleep(100 * time.Millisecond)
	if alice.count(func(m msg) bool {
		return m["t"] == "connection_changed" && data(m)["player"] == "bob" && data(m)["connected"] == false
	}) != 0 {
		t.Error("swapping tabs must not look like a disconnect")
	}
	bob2.mustOK(msg{"type": "vote_end", "agree": true}) // and the new tab works
}

func TestRateLimiting(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) { c.RatePerSec = 1; c.RateBurst = 3 })
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	for i := 0; i < 10; i++ {
		// The server hangs up part-way through, so later writes may fail.
		_ = c.ws.Write(context.Background(), websocket.MessageText, []byte(`{"type":"ping"}`))
	}
	if code := c.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Errorf("close code %d, want %d", code, websocket.StatusPolicyViolation)
	}
}

func TestMalformedJSONIsAnError(t *testing.T) {
	_, url := newTestServer(t, nil)
	c := dial(t, url, "alice")
	c.wait("hello", isT("hello"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, []byte("{not json"))
	c.wait("an error", func(m msg) bool { return m["t"] == "error" && data(m)["code"] == "bad_message" })
	c.send(msg{"type": "ping"})
	c.wait("pong", isT("pong")) // and the connection survives
}

func TestAnEmptyRoomClosesAndFreesItsPlayers(t *testing.T) {
	m, url := newTestServer(t, func(c *Config) { c.Room.EmptyRoomGrace = 100 * time.Millisecond })
	a := dial(t, url, "alice")
	a.wait("hello", isT("hello"))
	a.request(msg{"type": "create_room"})
	_ = a.ws.Close(websocket.StatusNormalClosure, "bye")

	eventually(t, "the room to close", func() bool { return len(m.ListRooms()) == 0 })

	a2 := dial(t, url, "alice")
	a2.wait("hello", isT("hello"))
	if r := a2.request(msg{"type": "create_room"}); r["t"] != "room_created" {
		t.Errorf("alice should be free to make a new room: %v", r)
	}
}

func TestSpectatorsWatchButCannotPlay(t *testing.T) {
	g := startedGame(t, nil)
	fan := dial(t, g.url, "fan")
	fan.wait("hello", isT("hello"))
	fan.mustOK(msg{"type": "spectate", "room": g.room})
	snap := fan.wait("snapshot", isT("snapshot"))
	game := data(snap)["game"].(map[string]any)
	if game["yourHand"] != nil {
		t.Error("a spectator has no hand")
	}
	if hands, _ := game["hands"].(map[string]any); len(hands) != 4 {
		t.Errorf("the spectator perk shows every hand, got %v", game["hands"])
	}
	if r := fan.request(msg{"type": "proclaim"}); data(r)["code"] != "not_player" {
		t.Errorf("a spectator proclaiming: %v", data(r))
	}
}

func TestRevealOptionsAreOnlyForTheTarget(t *testing.T) {
	g := startedGame(t, nil)
	// Alice suspects bob; only bob is asked what to reveal.
	holder := ""
	for n, c := range g.players {
		if sum(c.hand()) == 5 {
			holder = n
		}
	}
	target := "bob"
	if holder == "bob" {
		target = "carol"
	}
	g.players["alice"].mustOK(msg{"type": "suspect", "target": target})
	g.players[target].wait("reveal prompt", isEvent("reveal_requested"))
	time.Sleep(50 * time.Millisecond)
	for n, c := range g.players {
		got := c.count(isEvent("reveal_requested"))
		if (n == target) != (got == 1) {
			t.Errorf("%s got %d reveal prompts", n, got)
		}
	}
}
