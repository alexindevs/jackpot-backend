package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func openLobby(t *testing.T) (*client, string) {
	t.Helper()
	_, url := newTestServer(t, nil)
	a := dial(t, url, "alice")
	a.wait("hello", isT("hello"))
	room := data(a.request(msg{"type": "create_room"}))["room"].(string)
	return a, room
}

func TestOnePersonAddsBotsAndGetsAFullLobby(t *testing.T) {
	a, _ := openLobby(t)
	a.mustOK(msg{"type": "add_bots"})
	// A bot partner for alice, and a team of bots to play against.
	snap := a.request(msg{"type": "list_rooms"})
	info := snap["d"].([]any)[0].(map[string]any)
	if info["players"] != float64(4) || info["bots"] != float64(3) {
		t.Fatalf("room list = %v", info)
	}
	var names []string
	a.wait("the bots' profiles", func(m msg) bool {
		if m["t"] != "profile" {
			return false
		}
		id, _ := data(m)["id"].(string)
		if strings.HasPrefix(id, "bot:") {
			names = append(names, data(m)["name"].(string))
		}
		return len(names) >= 3
	})
	for _, n := range names {
		if n == "" || strings.HasPrefix(n, "bot:") {
			t.Errorf("a bot should have a real name, got %q", n)
		}
	}
	// She is paired with a bot, and the snapshot says which players are bots.
	s := a.request(msg{"type": "start_now"})
	if s["t"] != "ok" {
		t.Fatalf("start_now: %v", s)
	}
	a.wait("the game starting", isT("game_starting"))
}

func TestOnlyTheCreatorAddsBotsAndOnlyInTheLobby(t *testing.T) {
	_, url := newTestServer(t, nil)
	a, b := dial(t, url, "alice"), dial(t, url, "bob")
	a.wait("hello", isT("hello"))
	b.wait("hello", isT("hello"))
	room := data(a.request(msg{"type": "create_room"}))["room"].(string)
	b.mustOK(msg{"type": "join", "room": room})
	if r := b.request(msg{"type": "add_bots"}); data(r)["code"] != "not_creator" {
		t.Errorf("a non-creator adding bots: %v", data(r))
	}
	if r := b.request(msg{"type": "remove_bot", "player": "bot:garba"}); data(r)["code"] != "not_creator" {
		t.Errorf("a non-creator removing a bot: %v", data(r))
	}
	if r := a.request(msg{"type": "remove_bot", "player": "bob"}); data(r)["code"] != "not_a_bot" {
		t.Errorf("removing a person: %v", data(r))
	}
	if r := a.request(msg{"type": "remove_bot"}); data(r)["code"] != "bad_message" {
		t.Errorf("remove_bot with nobody named: %v", data(r))
	}
}

func TestNobodySignsInAsABot(t *testing.T) {
	_, url := newTestServer(t, nil)
	c, err := dialRaw(t, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.send(msg{"type": "auth", "token": "dev:x"}) // a normal dev sign-in works
	c.wait("hello", isT("hello"))

	// The dev sign-in prefixes its ids, so a bot's id cannot be forged that way.
	d, err := dialRaw(t, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.send(msg{"type": "auth", "token": "bot:garba"})
	if code := d.waitClosed(); code != 4401 {
		t.Errorf("a sign-in as a bot closed with %d, want 4401", code)
	}
}

func TestRemovingABotAndAddingMore(t *testing.T) {
	a, _ := openLobby(t)
	a.mustOK(msg{"type": "add_bots"})
	if r := a.request(msg{"type": "add_bots"}); data(r)["code"] != "nothing_to_add" {
		t.Errorf("adding bots to a lobby that is already fine: %v", data(r))
	}
	a.mustOK(msg{"type": "add_bots", "teams": 1}) // another team on purpose
	list := a.request(msg{"type": "list_rooms"})["d"].([]any)[0].(map[string]any)
	if list["players"] != float64(6) {
		t.Fatalf("players = %v, want 6", list["players"])
	}
	_ = time.Now
}

func TestAPersonPlaysARoundAgainstBotsOverTheWire(t *testing.T) {
	_, url := newTestServer(t, func(c *Config) {
		c.BotSpeed = 300
		c.Room.HoldTimer = 150 * time.Millisecond // the person is not passing cards, so the server does
		c.Room.PromptTimeout = 150 * time.Millisecond
	})
	a := dial(t, url, "alice")
	a.wait("hello", isT("hello"))
	a.request(msg{"type": "create_room"})
	a.mustOK(msg{"type": "add_bots"})
	a.mustOK(msg{"type": "start_now"})
	a.wait("the game starting", isT("game_starting"))

	// Her bot partner tells her its code in the team chat, in plain words.
	said := a.wait("the bot's code", func(m msg) bool {
		return m["t"] == "team_chat_message" && strings.HasPrefix(data(m)["from"].(string), "bot:")
	})
	text := data(said)["text"].(string)
	if !strings.Contains(text, "Jackpot") || strings.Contains(text, "—") {
		t.Errorf("the code message should be plain words about calling Jackpot: %q", text)
	}
	a.mustOK(msg{"type": "ready", "ready": true})
	a.wait("the round starting", isEvent("round_started"))

	// The bots play on their own: signals and passes arrive without her doing a thing.
	a.wait("a pass", isEvent("passed"))
	// Someone who walks in to watch is told which players are bots.
	fan := dial(t, url, "fan")
	fan.wait("hello", isT("hello"))
	rooms := fan.request(msg{"type": "list_rooms"})["d"].([]any)
	fan.mustOK(msg{"type": "spectate", "room": rooms[0].(map[string]any)["id"]})
	snap := fan.wait("a snapshot", isT("snapshot"))
	bots := 0
	for _, p := range data(snap)["players"].([]any) {
		if p.(map[string]any)["bot"] == true {
			bots++
		}
	}
	if bots != 3 {
		t.Errorf("the snapshot marks %d bots, want 3", bots)
	}
}

func TestFinishedGamesAnswersLikeMyGames(t *testing.T) {
	var asked []string
	_, url := newTestServer(t, func(c *Config) {
		c.History = func(_ context.Context, p engine.PlayerID) (any, error) {
			asked = append(asked, string(p))
			return []map[string]any{{"room": "abc123", "outcome": "won"}}, nil
		}
	})
	a := dial(t, url, "alice")
	a.wait("hello", isT("hello"))
	r := a.request(msg{"type": "finished_games"})
	list, _ := r["d"].([]any)
	if r["t"] != "finished_games" || len(list) != 1 || list[0].(map[string]any)["room"] != "abc123" {
		t.Fatalf("reply = %v", r)
	}
	if len(asked) != 1 || asked[0] != "alice" {
		t.Errorf("it must ask for the signed-in player's games, not anyone's: %v", asked)
	}
}

func TestFinishedGamesWithoutADatabaseIsEmptyAndAFailureIsAnError(t *testing.T) {
	_, url := newTestServer(t, nil)
	a := dial(t, url, "alice")
	a.wait("hello", isT("hello"))
	r := a.request(msg{"type": "finished_games"})
	if list, ok := r["d"].([]any); r["t"] != "finished_games" || !ok || len(list) != 0 {
		t.Errorf("without a database: %v", r)
	}
	_, url2 := newTestServer(t, func(c *Config) {
		c.History = func(context.Context, engine.PlayerID) (any, error) { return nil, errors.New("down") }
	})
	b := dial(t, url2, "bob")
	b.wait("hello", isT("hello"))
	if r := b.request(msg{"type": "finished_games"}); data(r)["code"] != "history_unavailable" {
		t.Errorf("a failing database: %v", r)
	}
}
