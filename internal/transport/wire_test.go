package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/wirejson"
)

func TestFieldNamesAreCamelCase(t *testing.T) {
	for in, want := range map[string]string{
		"PointsLeft": "pointsLeft", "ID": "id", "Hand": "hand", "EndsAt": "endsAt",
		"HTTPServer": "httpServer", "A": "a", "YourHand": "yourHand",
	} {
		if got := wirejson.LowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"MemberJoined": "member_joined", "Passed": "passed", "RoundWon": "round_won"} {
		if got := wirejson.Snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}

func decode(t *testing.T, b []byte) msg {
	t.Helper()
	var m msg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return m
}

func TestRoomMessagesEncodeWithTheirNames(t *testing.T) {
	m := decode(t, encodeRoom(room.HandUpdate{Player: "bob", Hand: engine.Hand{1, 2, 0, 1}}))
	if m["t"] != "hand_update" || data(m)["player"] != "bob" {
		t.Fatalf("encoded: %v", m)
	}
	if h := data(m)["hand"].([]any); len(h) != 4 || h[1] != float64(2) {
		t.Errorf("hand = %v", h)
	}

	m = decode(t, encodeRoom(room.GameEvent{Event: engine.Passed{From: "a", To: "b", Card: room.CardHidden, Auto: true}}))
	if m["t"] != "game_event" || m["kind"] != "passed" || data(m)["card"] != float64(255) || data(m)["auto"] != true {
		t.Errorf("encoded: %v", m)
	}

	// Map keys such as player ids and team numbers are left alone.
	m = decode(t, encodeRoom(room.GameEvent{Event: engine.GameOver{Result: engine.Result{
		Winner: 1, Reason: engine.EndWins, Wins: map[engine.TeamID]int{0: 3, 1: 7},
	}}}))
	res := data(m)["result"].(map[string]any)
	if wins := res["wins"].(map[string]any); wins["1"] != float64(7) {
		t.Errorf("wins = %v", wins)
	}

	ends := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m = decode(t, encodeRoom(room.CountdownStarted{EndsAt: ends}))
	if data(m)["endsAt"] != "2026-01-01T12:00:00Z" {
		t.Errorf("time encoded as %v", data(m)["endsAt"])
	}

	m = decode(t, encodeRoom(room.Snapshot{Room: "r1", Phase: room.PhasePlaying, Game: &room.GameInfo{Phase: engine.PhasePlaying, Holder: "a"}}))
	if data(m)["phase"] != "playing" || data(m)["game"].(map[string]any)["phase"] != "playing" {
		t.Errorf("phases should read as words: %v", m)
	}
}

func TestPlayerIDsWithCapitalsSurviveAsMapKeys(t *testing.T) {
	m := decode(t, encodeRoom(room.Snapshot{Game: &room.GameInfo{Points: map[engine.PlayerID]int{"Alice": 3}}}))
	points := data(m)["game"].(map[string]any)["points"].(map[string]any)
	if points["Alice"] != float64(3) {
		t.Errorf("points = %v", points)
	}
}

func TestClientMessagesBecomeRoomInputs(t *testing.T) {
	yes := true
	one, nine := 1, 9
	cases := []struct {
		in   inbound
		want room.Input
		bad  bool
	}{
		{inbound{Type: "start_now"}, room.StartNow{}, false},
		{inbound{Type: "invite_partner", To: "bob"}, room.InvitePartner{To: "bob"}, false},
		{inbound{Type: "accept_partner", From: "bob"}, room.AcceptPartner{From: "bob"}, false},
		{inbound{Type: "ready", Ready: &yes}, room.Ready{Ready: true}, false},
		{inbound{Type: "ready"}, nil, true},
		{inbound{Type: "pass", Card: &one}, room.Pass{Card: engine.Triangle}, false},
		{inbound{Type: "pass", Card: &nine}, nil, true},
		{inbound{Type: "pass"}, nil, true},
		{inbound{Type: "suspect", Target: "bob"}, room.Suspect{Target: "bob"}, false},
		{inbound{Type: "reveal", Types: []int{0, 2}}, room.Reveal{Types: []engine.CardType{engine.Circle, engine.Cross}}, false},
		{inbound{Type: "reveal", Types: []int{0, 7}}, nil, true},
		{inbound{Type: "reveal"}, nil, true},
		{inbound{Type: "reveal", Types: []int{0, 1, 2, 3, 0}}, nil, true},
		{inbound{Type: "vote_end", Agree: &yes}, room.VoteEnd{Agree: true}, false},
		{inbound{Type: "vote_end"}, nil, true},
		{inbound{Type: "proclaim"}, room.Proclaim{}, false},
		{inbound{Type: "launch_missiles"}, nil, true},
	}
	for _, c := range cases {
		got, err := toInput(c.in)
		if (err != nil) != c.bad {
			t.Errorf("%+v: error = %v, want error = %v", c.in, err, c.bad)
			continue
		}
		if !c.bad && !equalInput(got, c.want) {
			t.Errorf("%+v: got %#v, want %#v", c.in, got, c.want)
		}
	}
}

func equalInput(a, b room.Input) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb) && wirejson.TypeName(a) == wirejson.TypeName(b)
}

func TestTheSenderIsNeverTakenFromTheMessage(t *testing.T) {
	// Messages have no "player" or "by" field to spoof; the connection knows
	// who is speaking. Unknown fields are simply ignored.
	var in inbound
	if err := json.Unmarshal([]byte(`{"type":"proclaim","by":"mallory","player":"mallory"}`), &in); err != nil {
		t.Fatal(err)
	}
	got, _ := toInput(in)
	if _, ok := got.(room.Proclaim); !ok {
		t.Errorf("got %#v", got)
	}
}

type fakeCloser struct{ codes chan websocket.StatusCode }

func (f fakeCloser) Close(code websocket.StatusCode, _ string) error {
	f.codes <- code
	return nil
}

func TestASlowClientIsDroppedNotWaitedFor(t *testing.T) {
	fc := fakeCloser{codes: make(chan websocket.StatusCode, 1)}
	c := &conn{closer: fc, send: make(chan []byte, 2), done: make(chan struct{})}
	if !c.enqueue([]byte("1")) || !c.enqueue([]byte("2")) {
		t.Fatal("the queue has room for two")
	}
	start := time.Now()
	if c.enqueue([]byte("3")) {
		t.Error("a full queue must refuse")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("enqueue must never block")
	}
	select {
	case code := <-fc.codes:
		if code != websocket.StatusPolicyViolation {
			t.Errorf("closed with %d", code)
		}
	case <-time.After(time.Second):
		t.Error("the slow client should be disconnected")
	}
	if c.enqueue([]byte("4")) {
		t.Error("a closed connection accepts nothing")
	}
}

func TestTokenBucket(t *testing.T) {
	now := time.Now()
	b := newBucket(10, 3, now)
	for i := 0; i < 3; i++ {
		if !b.allow(now) {
			t.Fatalf("burst message %d refused", i)
		}
	}
	if b.allow(now) {
		t.Error("the bucket should be empty")
	}
	if !b.allow(now.Add(150 * time.Millisecond)) {
		t.Error("it should refill at the sustained rate")
	}
}

func TestStartNGAuthenticator(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/games/jackpot/player" {
			http.NotFound(w, r)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer good":
			_, _ = w.Write([]byte(`{"id":7,"name":"Trini","style":{"hair":"braids","hat":"crown"},"email":"leak@example.com"}`))
		case "Bearer broken":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "no", http.StatusUnauthorized)
		}
	}))
	defer api.Close()
	a := StartNG{BaseURL: api.URL + "/", Game: "jackpot"}

	id, err := a.Authenticate(t.Context(), "good")
	if err != nil || id.ID != "7" || id.Name != "Trini" {
		t.Fatalf("good token: %+v, %v", id, err)
	}
	if !strings.Contains(string(id.Style), "braids") {
		t.Errorf("style = %s", id.Style)
	}
	if _, err := a.Authenticate(t.Context(), "expired"); err != ErrUnauthorized {
		t.Errorf("expired token: %v", err)
	}
	if _, err := a.Authenticate(t.Context(), ""); err != ErrUnauthorized {
		t.Errorf("empty token: %v", err)
	}
	if _, err := a.Authenticate(t.Context(), "broken"); err == nil || err == ErrUnauthorized {
		t.Errorf("a start.ng outage must not look like a bad token: %v", err)
	}
	if _, err := (StartNG{BaseURL: "http://127.0.0.1:1", Game: "jackpot"}).Authenticate(t.Context(), "good"); err == nil || err == ErrUnauthorized {
		t.Errorf("an unreachable start.ng must not look like a bad token: %v", err)
	}
}

func TestDevAuthenticator(t *testing.T) {
	for token, ok := range map[string]bool{"dev:alice": true, "alice": false, "dev:": false, "dev:" + strings.Repeat("x", 41): false} {
		_, err := Dev{}.Authenticate(t.Context(), token)
		if (err == nil) != ok {
			t.Errorf("%q: err = %v", token, err)
		}
	}
}
