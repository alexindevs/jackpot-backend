package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/live"
)

// TestAGameSurvivesARestartAndCarriesOn plays a game partway, stops the whole
// server, starts a new one on the same database, and picks the game up again.
func TestAGameSurvivesARestartAndCarriesOn(t *testing.T) {
	ctx := context.Background()
	st1 := newStack(t, nil)
	players, roomID := lobby(t, st1)
	startRound(t, players)
	players["bob"].ok(msg{"type": "chat", "text": "before the restart"})

	// A few real passes.
	for i := 0; i < 6; i++ {
		var holder string
		eventually(t, "exactly one holder", func() bool {
			n := 0
			for name, c := range players {
				if total(c.hand()) == 5 {
					holder, n = name, n+1
				}
			}
			return n == 1
		})
		hand := players[holder].hand()
		card := 0
		for ty, c := range hand {
			if c > 0 {
				card = ty
				break
			}
		}
		players[holder].ok(msg{"type": "pass", "card": card})
	}
	st1.stop() // everything recorded so far is on disk now

	// Startup, as the real server does it, on the same database.
	st2 := newStackOn(t, st1.db, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := live.Restore(ctx, st2.db, st2.m, st2.writer, log); err != nil {
		t.Fatal(err)
	}
	if g, _ := st2.db.Game(ctx, 1); g.Status != "playing" {
		t.Fatalf("the game should still be open after the restart: %+v", g)
	}

	back := map[string]*client{}
	for _, n := range names {
		back[n] = dial(t, st2.wsURL, n)
		back[n].wait("hello", func(m msg) bool { return m["t"] == "hello" })
	}
	list := back["carol"].ok(msg{"type": "my_games"})
	games := list["d"].([]any)
	if len(games) != 1 || games[0].(map[string]any)["room"] != roomID {
		t.Fatalf("my_games = %v", list)
	}
	for _, n := range names {
		back[n].ok(msg{"type": "resume_game", "room": roomID})
	}
	for _, n := range names {
		back[n].wait("resumed", func(m msg) bool { return m["t"] == "resumed" })
	}

	// Chat numbering carries on, and the earlier line is in the snapshot.
	snap := back["alice"].wait("a snapshot", func(m msg) bool { return m["t"] == "snapshot" })
	lines := snap["d"].(map[string]any)["chat"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["text"] != "before the restart" {
		t.Errorf("chat in the snapshot = %v", lines)
	}
	back["alice"].ok(msg{"type": "chat", "text": "after the restart"})

	// Carol and dave walk away: alice and bob win by forfeit.
	back["carol"].ok(msg{"type": "leave"})
	for _, n := range []string{"alice", "bob", "dave"} {
		back[n].wait("game over", func(m msg) bool { return m["t"] == "game_event" && m["kind"] == "game_over" })
	}
	st2.stop()

	g, err := st2.db.Game(ctx, 1)
	if err != nil || g.Status != "finished" || g.EndReason == nil || *g.EndReason != "forfeit" {
		t.Fatalf("game = %+v, %v", g, err)
	}
	chat, _ := st2.db.GameChat(ctx, 1, time.Time{}, 0, 100)
	if len(chat) != 2 || chat[0].Text != "before the restart" || chat[1].Text != "after the restart" || chat[1].ID != 2 {
		t.Errorf("chat across the restart = %+v", chat)
	}
	// The command log kept growing across the restart, in one unbroken sequence.
	n, max, err := st2.db.CommandCount(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n < 8 || n != max {
		t.Errorf("game_events: %d rows, max seq %d", n, max)
	}
}

func restartOn(t *testing.T, st1 *stack) *stack {
	t.Helper()
	st1.stop()
	st2 := newStackOn(t, st1.db, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := live.Restore(context.Background(), st2.db, st2.m, st2.writer, log); err != nil {
		t.Fatal(err)
	}
	return st2
}

// TestALobbySurvivesARestartAndGoesOnToStartAGame: three people have gathered
// and two have teamed up when the server restarts.
func TestALobbySurvivesARestartAndGoesOnToStartAGame(t *testing.T) {
	ctx := context.Background()
	st1 := newStack(t, nil)
	alice, bob, carol := dial(t, st1.wsURL, "alice"), dial(t, st1.wsURL, "bob"), dial(t, st1.wsURL, "carol")
	roomID := alice.ok(msg{"type": "create_room"})["d"].(map[string]any)["room"].(string)
	bob.ok(msg{"type": "join", "room": roomID})
	carol.ok(msg{"type": "join", "room": roomID})
	alice.ok(msg{"type": "invite_partner", "to": "bob"})
	bob.ok(msg{"type": "accept_partner", "from": "alice"})
	time.Sleep(100 * time.Millisecond)

	st2 := restartOn(t, st1)
	if rows, _, _ := st2.db.Lobbies(ctx); len(rows) != 1 || len(rows[0].Members) != 3 {
		t.Fatalf("the lobby should still be saved: %+v", rows)
	}

	back := map[string]*client{}
	for _, n := range []string{"alice", "bob", "carol", "dave"} {
		back[n] = dial(t, st2.wsURL, n)
		back[n].wait("hello", func(m msg) bool { return m["t"] == "hello" })
	}
	games := back["bob"].ok(msg{"type": "my_games"})["d"].([]any)
	if len(games) != 1 {
		t.Fatalf("my_games = %v", games)
	}
	if g := games[0].(map[string]any); g["kind"] != "lobby" || g["room"] != roomID || len(g["players"].([]any)) != 3 {
		t.Fatalf("the lobby should be listed as a paused lobby: %v", g)
	}
	// Dave isn't a member and can't walk into a paused lobby.
	if r := back["dave"].try(msg{"type": "join", "room": roomID}); r["t"] != "error" {
		t.Errorf("joining a paused lobby: %v", r)
	}
	for _, n := range []string{"alice", "bob", "carol"} {
		back[n].ok(msg{"type": "resume_game", "room": roomID})
	}
	for _, n := range []string{"alice", "bob", "carol"} {
		back[n].wait("resumed", func(m msg) bool { return m["t"] == "resumed" })
	}
	snap := back["alice"].wait("snapshot", func(m msg) bool { return m["t"] == "snapshot" })
	var paired bool
	for _, p := range snap["d"].(map[string]any)["players"].([]any) {
		if p.(map[string]any)["id"] == "alice" && p.(map[string]any)["partner"] == "bob" {
			paired = true
		}
	}
	if !paired {
		t.Errorf("alice and bob should still be partners: %v", snap["d"])
	}

	// The lobby is open again: dave joins, carol teams up with him, and the
	// creator starts the game.
	back["dave"].ok(msg{"type": "join", "room": roomID})
	back["carol"].ok(msg{"type": "invite_partner", "to": "dave"})
	back["dave"].ok(msg{"type": "accept_partner", "from": "carol"})
	back["alice"].ok(msg{"type": "start_now"})
	for _, n := range []string{"alice", "bob", "carol", "dave"} {
		back[n].wait("chat_started", func(m msg) bool { return m["t"] == "chat_started" })
	}
	st2.drain(t)
	if rows, _, _ := st2.db.Lobbies(ctx); len(rows) != 0 {
		t.Errorf("a lobby that became a game is no longer saved as a lobby: %+v", rows)
	}
}

// TestPrivateTeamChatSurvivesARestart: partners agree a code, the server
// restarts, and the code is still on their screens, but not the other team's.
func TestPrivateTeamChatSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	st1 := newStack(t, nil)
	players, roomID := lobby(t, st1)
	players["alice"].ok(msg{"type": "start_now"})
	for _, n := range names {
		players[n].wait("chat_started", func(m msg) bool { return m["t"] == "chat_started" })
	}
	players["alice"].ok(msg{"type": "team_chat", "text": "a sneeze means circles"})
	players["bob"].ok(msg{"type": "team_chat", "text": "agreed, two blinks is a no"})
	players["carol"].ok(msg{"type": "team_chat", "text": "we use the singing"})
	time.Sleep(100 * time.Millisecond)

	st2 := restartOn(t, st1)
	back := map[string]*client{}
	for _, n := range names {
		back[n] = dial(t, st2.wsURL, n)
		back[n].wait("hello", func(m msg) bool { return m["t"] == "hello" })
		back[n].ok(msg{"type": "resume_game", "room": roomID})
	}
	chatOf := func(name string) []any {
		snap := back[name].wait("snapshot", func(m msg) bool { return m["t"] == "snapshot" })
		lines, _ := snap["d"].(map[string]any)["teamChat"].([]any)
		return lines
	}
	if lines := chatOf("alice"); len(lines) != 2 || lines[0].(map[string]any)["text"] != "a sneeze means circles" || lines[1].(map[string]any)["from"] != "bob" {
		t.Errorf("alice's team chat = %v", lines)
	}
	if lines := chatOf("bob"); len(lines) != 2 {
		t.Errorf("bob's team chat = %v", lines)
	}
	if lines := chatOf("carol"); len(lines) != 1 || lines[0].(map[string]any)["text"] != "we use the singing" {
		t.Errorf("carol must only see her own team's chat: %v", lines)
	}
	// Still stored for settling disputes.
	if saved, err := st2.db.GameTeamChat(ctx, 1); err != nil || len(saved) != 3 {
		t.Errorf("saved team chat = %+v, %v", saved, err)
	}
}

// try is ok without the failure: it returns the reply, error or not.
func (c *client) try(v msg) msg {
	c.t.Helper()
	id := float64(time.Now().UnixNano())
	v["id"] = id
	c.send(v)
	return c.wait("a reply to "+fmt.Sprint(v["type"]), func(m msg) bool { return m["id"] == id })
}
