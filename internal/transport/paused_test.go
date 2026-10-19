package transport

import (
	"sync"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// recorder collects what a room reports, standing in for the database.
type recorder struct {
	mu   sync.Mutex
	recs []room.Record
}

func (r *recorder) add(rec room.Record) {
	r.mu.Lock()
	r.recs = append(r.recs, rec)
	r.mu.Unlock()
}

func (r *recorder) saved(at time.Time) room.Saved {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sv room.Saved
	for _, rec := range r.recs {
		switch v := rec.(type) {
		case room.GameStarted:
			sv.ID, sv.Creator, sv.Setup = v.Room, v.Creator, v.Setup
		case room.CommandApplied:
			sv.Commands = append(sv.Commands, v.Command)
		case room.ChatPosted:
			sv.ChatSeq = v.Seq
			sv.Chat = append(sv.Chat, room.ChatMessage{ID: v.Seq, From: v.Player, Text: v.Text, At: v.At})
		}
	}
	sv.Since = at
	return sv
}

// afterARestart plays a game for a moment, then "restarts the server": a new
// manager is started with the game restored from what the first one recorded.
func afterARestart(t *testing.T, modify func(*Config)) (url string, roomID string, m *Manager) {
	t.Helper()
	rec := &recorder{}
	g := startedGame(t, func(c *Config) { c.Room.OnRecord = rec.add })
	g.players["alice"].mustOK(msg{"type": "chat", "text": "before the restart"})
	holder := ""
	for n, c := range g.players {
		if len(c.hand()) > 0 && sum(c.hand()) == 5 {
			holder = n
		}
	}
	g.players[holder].mustOK(msg{"type": "pass", "card": firstCard(g.players[holder].hand())})
	time.Sleep(100 * time.Millisecond) // let the room report it

	saved := rec.saved(time.Now())
	if len(saved.Commands) < 2 {
		t.Fatalf("recorded %d commands", len(saved.Commands))
	}
	m, url = newTestServer(t, modify)
	profiles := map[engine.PlayerID]Identity{}
	for _, n := range names {
		profiles[engine.PlayerID(n)] = Identity{ID: engine.PlayerID(n), Name: "Name of " + n}
	}
	ok, failed := m.Restore([]RestoreItem{{GameID: 7, Saved: saved}}, profiles)
	if len(failed) != 0 || len(ok) != 1 || ok[0].GameID != 7 || ok[0].Room != g.room {
		t.Fatalf("restored %v, failed %v", ok, failed)
	}
	return url, g.room, m
}

func firstCard(hand []int) int {
	for ty, n := range hand {
		if n > 0 {
			return ty
		}
	}
	return 0
}

func TestAPausedGameIsListedForItsPlayersAndNotInTheRoomList(t *testing.T) {
	url, roomID, _ := afterARestart(t, nil)
	alice := dial(t, url, "alice")
	alice.wait("hello", isT("hello"))
	if _, ok := alice.find(isT("snapshot")); ok {
		t.Error("a restored game should not pull its players back in by itself")
	}

	r := alice.request(msg{"type": "my_games"})
	games, _ := r["d"].([]any)
	if r["t"] != "my_games" || len(games) != 1 {
		t.Fatalf("my_games = %v", r)
	}
	gm := games[0].(map[string]any)
	if gm["room"] != roomID || gm["until"] == nil || gm["round"] == nil {
		t.Errorf("game = %v", gm)
	}
	players := gm["players"].([]any)
	if len(players) != 4 {
		t.Fatalf("players = %v", players)
	}
	for _, raw := range players {
		p := raw.(map[string]any)
		if p["here"] != false || p["team"] == nil || p["id"] == nil || p["name"] == nil {
			t.Errorf("player = %v", p)
		}
		// Players who haven't signed in yet are named from the saved profiles;
		// alice, who has, is named by her live sign-in.
		want := "Name of " + p["id"].(string)
		if p["id"] == "alice" {
			want = "alice"
		}
		if p["name"] != want {
			t.Errorf("%v is named %q, want %q", p["id"], p["name"], want)
		}
	}

	if rooms := alice.request(msg{"type": "list_rooms"})["d"].([]any); len(rooms) != 0 {
		t.Errorf("a paused game is not a room to spectate: %v", rooms)
	}
	// Someone who wasn't in the game sees nothing.
	eve := dial(t, url, "eve")
	eve.wait("hello", isT("hello"))
	if r := eve.request(msg{"type": "my_games"}); len(r["d"].([]any)) != 0 {
		t.Errorf("eve's games = %v", r)
	}
	if r := eve.request(msg{"type": "resume_game", "room": roomID}); data(r)["code"] != "no_such_game" {
		t.Errorf("eve resuming: %v", r)
	}
}

func TestPlayersGoBackToAPausedGameAndItResumesWhenAllAreBack(t *testing.T) {
	url, roomID, _ := afterARestart(t, nil)
	clients := map[string]*client{}
	for _, n := range names {
		clients[n] = dial(t, url, n)
		clients[n].wait("hello", isT("hello"))
	}

	clients["alice"].mustOK(msg{"type": "resume_game", "room": roomID})
	snap := data(clients["alice"].wait("snapshot", isT("snapshot")))
	if snap["pausedUntil"] == nil {
		t.Errorf("the snapshot should say the game is paused: %v", snap)
	}
	game := snap["game"].(map[string]any)
	if len(game["yourHand"].([]any)) == 0 {
		t.Errorf("a returning player gets their hand back: %v", game)
	}
	chat := snap["chat"].([]any)
	if len(chat) != 1 || chat[0].(map[string]any)["text"] != "before the restart" {
		t.Errorf("chat survives the restart: %v", chat)
	}
	// Nothing can be played yet, and they are now in the room.
	if r := clients["alice"].request(msg{"type": "pass", "card": 0}); data(r)["code"] != "paused" {
		t.Errorf("playing while paused: %v", r)
	}
	if r := clients["alice"].request(msg{"type": "resume_game", "room": roomID}); data(r)["code"] != "already_in_room" {
		t.Errorf("resuming twice: %v", r)
	}
	if r := clients["alice"].request(msg{"type": "my_games"}); len(r["d"].([]any)) != 0 {
		t.Errorf("a game you are in is not in the list: %v", r)
	}

	for _, n := range []string{"bob", "carol"} {
		clients[n].mustOK(msg{"type": "resume_game", "room": roomID})
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok := clients["alice"].find(isT("resumed")); ok {
		t.Fatal("resumed before everyone was back")
	}
	clients["dave"].mustOK(msg{"type": "resume_game", "room": roomID})
	for _, n := range names {
		clients[n].wait(n+" told the game resumed", isT("resumed"))
	}

	// The game is playable again: whoever holds the passing card can pass. A
	// restored game hands out hands in the snapshot, not as hand updates.
	holder, card := "", 0
	for n, c := range clients {
		snap := data(c.wait("snapshot", isT("snapshot")))
		hand := snap["game"].(map[string]any)["yourHand"].([]any)
		total := 0
		for ty, v := range hand {
			total += int(v.(float64))
			if v.(float64) > 0 {
				card = ty
			}
		}
		if total == 5 {
			holder = n
			for ty, v := range hand {
				if v.(float64) > 0 {
					card = ty
					break
				}
			}
		}
	}
	if holder == "" {
		t.Fatal("nobody holds the passing card")
	}
	clients[holder].mustOK(msg{"type": "pass", "card": card})
}

func TestLeavingAPausedGameBeforeItResumesFindsItAgainInTheList(t *testing.T) {
	url, roomID, _ := afterARestart(t, nil)
	alice := dial(t, url, "alice")
	alice.wait("hello", isT("hello"))
	alice.mustOK(msg{"type": "resume_game", "room": roomID})
	alice.wait("snapshot", isT("snapshot"))
	_ = alice.ws.Close(1000, "tunnel")

	alice2 := dial(t, url, "alice")
	alice2.wait("hello", isT("hello"))
	eventually(t, "the game back in her list", func() bool {
		r := alice2.request(msg{"type": "my_games"})
		return len(r["d"].([]any)) == 1
	})
	// And she can create a room meanwhile: a paused game is not her current room.
	if r := alice2.request(msg{"type": "create_room"}); r["t"] != "room_created" {
		t.Errorf("create_room: %v", r)
	}
}

func TestResumingSomethingThatIsNotPausedIsRefused(t *testing.T) {
	_, url := newTestServer(t, nil)
	alice := dial(t, url, "alice")
	alice.wait("hello", isT("hello"))
	if r := alice.request(msg{"type": "resume_game", "room": "nowhere"}); data(r)["code"] != "no_such_game" {
		t.Errorf("resume: %v", r)
	}
	if r := alice.request(msg{"type": "my_games"}); r["t"] != "my_games" || len(r["d"].([]any)) != 0 {
		t.Errorf("my_games with none = %v", r)
	}
}

func TestAGameWhoseEveryoneLeftPausesAndShowsUpInTheirList(t *testing.T) {
	g := startedGame(t, func(c *Config) { c.Room.EmptyRoomGrace = 80 * time.Millisecond })
	for _, n := range names {
		_ = g.players[n].ws.Close(1000, "gone")
	}
	time.Sleep(400 * time.Millisecond) // nobody is there for longer than the grace period

	alice := dial(t, g.url, "alice")
	alice.wait("hello", isT("hello"))
	if _, ok := alice.find(isT("snapshot")); ok {
		t.Error("the game was paused, so signing in does not put her back in it")
	}
	r := alice.request(msg{"type": "my_games"})
	if games := r["d"].([]any); len(games) != 1 || games[0].(map[string]any)["room"] != g.room {
		t.Errorf("my_games = %v", r)
	}
}
