package api

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alexindevs/jackpot-backend/internal/store"
)

// A real database behind the real handlers, to check the whole path returns
// what a page expects. Skipped unless JACKPOT_TEST_DATABASE_URL is set.
func TestLeaderboardsEndToEndOnARealDatabase(t *testing.T) {
	admin := os.Getenv("JACKPOT_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set JACKPOT_TEST_DATABASE_URL to run the database tests")
	}
	ctx := context.Background()
	name := fmt.Sprintf("jackpot_api_test_%d_%d", time.Now().UnixNano(), rand.IntN(1e6))
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + name
	s, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	_ = s.UpsertPlayer(ctx, store.Player{ID: "alice", Name: "Alice", Style: []byte(`{"hair":"afro"}`)})
	_ = s.UpsertPlayer(ctx, store.Player{ID: "bob", Name: "Bob"})
	id, err := s.StartGame(ctx, "room", now, [][2]string{{"alice", "bob"}, {"carol", "dave"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.RecordRound(ctx, id, 1, 0, "alice", "bob", now)
	_ = s.RecordRound(ctx, id, 2, 0, "alice", "bob", now)
	_ = s.FinishGame(ctx, id, now, store.Outcome{Reason: "wins", WinnerTeam: 0, CountsForStats: true})
	_ = s.AddChat(ctx, id, 1, "bob", "good game", now.Add(-time.Hour))
	_ = s.AddChat(ctx, id, 2, "bob", "old news", now.Add(-30*time.Hour))

	srv := newAPI(t, func(c *Config) { c.Store = s })

	_, body := get(t, srv, "/api/leaderboards/mvp?role=proclaimer")
	top := body.([]any)[0].(map[string]any)
	if p := top["player"].(map[string]any); top["wins"] != float64(2) || p["name"] != "Alice" || p["style"].(map[string]any)["hair"] != "afro" {
		t.Errorf("mvp = %v", top)
	}
	_, body = get(t, srv, "/api/leaderboards/duos")
	duo := body.([]any)[0].(map[string]any)
	if duo["wins"] != float64(1) || duo["games"] != float64(1) || duo["players"].([]any)[1].(map[string]any)["name"] != "Bob" {
		t.Errorf("duo = %v", duo)
	}
	_, body = get(t, srv, fmt.Sprintf("/api/games/%d", id))
	if g := body.(map[string]any); len(g["rounds"].([]any)) != 2 || len(g["players"].([]any)) != 4 {
		t.Errorf("game = %v", g)
	}
	resp, body := get(t, srv, fmt.Sprintf("/api/games/%d/chat", id), "Authorization", "Bearer dev:alice")
	msgs := body.([]any)
	if resp.StatusCode != 200 || len(msgs) != 2 || msgs[0].(map[string]any)["text"] != "good game" {
		t.Errorf("chat of a game that just ended stays readable: %d %v", resp.StatusCode, body)
	}

	// A day after the game, the dispute data goes; the game itself stays.
	if _, err := s.SweepExpired(ctx, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, fmt.Sprintf("/api/games/%d/chat", id), "Authorization", "Bearer dev:alice")
	if msgs := body.([]any); len(msgs) != 0 {
		t.Errorf("expired chat = %v", body)
	}
	_, body = get(t, srv, "/api/leaderboards/duos")
	if len(body.([]any)) != 1 {
		t.Errorf("leaderboards must survive expiry: %v", body)
	}
}
