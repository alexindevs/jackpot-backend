package e2e

import (
	"context"
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
