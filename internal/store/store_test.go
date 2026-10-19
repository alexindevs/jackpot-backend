package store

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// newTestStore makes a fresh, migrated database on the server named by
// JACKPOT_TEST_DATABASE_URL, and drops it afterwards. Tests are skipped when
// that variable isn't set, so `go test ./...` works without a database.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	admin := os.Getenv("JACKPOT_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set JACKPOT_TEST_DATABASE_URL to run the database tests")
	}
	ctx := context.Background()
	name := fmt.Sprintf("jackpot_test_%d_%d", time.Now().UnixNano(), rand.IntN(1e6))

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	s, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrateIsIdempotentAndSafeToRunTwiceAtOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { errs <- s.Migrate(ctx) }()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 4 {
		t.Errorf("migrations recorded: %d, %v", n, err)
	}
}

func TestPlayersAreUpsertedAndRefreshed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertPlayer(ctx, Player{ID: "7", Name: "Trini", Style: []byte(`{"hair":"braids"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertPlayer(ctx, Player{ID: "7", Name: "Trini Q", Style: nil}); err != nil {
		t.Fatal(err)
	}
	var name string
	var style []byte
	if err := s.pool.QueryRow(ctx, `SELECT name, style FROM players WHERE id = '7'`).Scan(&name, &style); err != nil {
		t.Fatal(err)
	}
	if name != "Trini Q" || style != nil {
		t.Errorf("name %q style %s", name, style)
	}
}

// play records a whole game: teams, rounds, and an outcome.
func play(t *testing.T, s *Store, teams [][2]string, rounds []RoundSummary, o *Outcome) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := s.StartGame(ctx, "room", t0, teams)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rounds {
		if err := s.RecordRound(ctx, id, r.Round, r.Team, r.Proclaimer.ID, r.Holder.ID, t0); err != nil {
			t.Fatal(err)
		}
	}
	if o != nil {
		if err := s.FinishGame(ctx, id, t0.Add(time.Hour), *o); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func round(n, team int, proclaimer, holder string) RoundSummary {
	return RoundSummary{Round: n, Team: team, Proclaimer: PlayerRef{ID: proclaimer}, Holder: PlayerRef{ID: holder}}
}

func TestAGameAndItsRoundsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, p := range []Player{{ID: "a", Name: "Ada"}, {ID: "b", Name: "Bayo"}} {
		if err := s.UpsertPlayer(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	id := play(t, s, [][2]string{{"a", "b"}, {"c", "d"}},
		[]RoundSummary{round(1, 0, "a", "b"), round(2, 1, "d", "c")},
		&Outcome{Reason: "wins", WinnerTeam: 0, CountsForStats: true})

	g, err := s.Game(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "finished" || g.WinnerTeam == nil || *g.WinnerTeam != 0 || g.Draw || !g.CountsForStats || *g.EndReason != "wins" {
		t.Errorf("game = %+v", g)
	}
	if len(g.Players) != 4 || g.Players[0].Player.Name != "Ada" || g.Players[2].Player.Name != "c" {
		t.Errorf("players = %+v", g.Players) // c has no profile, so the id stands in for the name
	}
	if len(g.Rounds) != 2 || g.Rounds[0].Proclaimer.Name != "Ada" || g.Rounds[0].Holder.ID != "b" || g.Rounds[1].Team != 1 {
		t.Errorf("rounds = %+v", g.Rounds)
	}
	if _, err := s.Game(ctx, id+999); err != ErrNotFound {
		t.Errorf("missing game: %v", err)
	}
}

func TestSavingARoundTwiceDoesNotDoubleCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	for i := 0; i < 3; i++ {
		if err := s.RecordRound(ctx, id, 1, 0, "a", "b", t0); err != nil {
			t.Fatal(err)
		}
	}
	top, _ := s.TopPlayers(ctx, RoleProclaimer, 10)
	if len(top) != 1 || top[0].Wins != 1 {
		t.Errorf("top = %+v", top)
	}
}

func TestMVPBoardsRankByRoleWithSharedRanksForTies(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertPlayer(ctx, Player{ID: "a", Name: "Ada"})
	// a proclaims 3, d proclaims 2, b and c proclaim 1 each.
	play(t, s, [][2]string{{"a", "b"}, {"c", "d"}}, []RoundSummary{
		round(1, 0, "a", "b"), round(2, 0, "a", "b"), round(3, 0, "a", "b"),
		round(4, 1, "d", "c"), round(5, 1, "d", "c"), round(6, 0, "b", "a"), round(7, 1, "c", "d"),
	}, nil)

	proc, err := s.TopPlayers(ctx, RoleProclaimer, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id   string
		wins int
		rank int
	}{{"a", 3, 1}, {"d", 2, 2}, {"b", 1, 3}, {"c", 1, 3}}
	if len(proc) != len(want) {
		t.Fatalf("proclaimers = %+v", proc)
	}
	for i, w := range want {
		if proc[i].Player.ID != w.id || proc[i].Wins != w.wins || proc[i].Rank != w.rank {
			t.Errorf("row %d = %+v, want %+v", i, proc[i], w)
		}
	}
	if proc[0].Player.Name != "Ada" || proc[3].Player.Name != "c" {
		t.Errorf("names: %q, %q", proc[0].Player.Name, proc[3].Player.Name)
	}

	hold, _ := s.TopPlayers(ctx, RoleHolder, 2)
	if len(hold) != 2 || hold[0].Player.ID != "b" || hold[0].Wins != 3 {
		t.Errorf("holders = %+v", hold)
	}
	if _, err := s.TopPlayers(ctx, Role("x; DROP TABLE rounds"), 1); err == nil {
		t.Error("an unknown role must be refused")
	}
}

func TestDuoBoardCountsOnlyRealWins(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	win := func(team int) *Outcome { return &Outcome{Reason: "wins", WinnerTeam: team, CountsForStats: true} }
	ab, cd := [][2]string{{"a", "b"}, {"c", "d"}}, [][2]string{{"a", "b"}, {"c", "e"}}

	play(t, s, ab[:0:0], nil, nil) // an empty game changes nothing
	play(t, s, ab, nil, win(0))    // a+b win
	play(t, s, ab, nil, win(0))    // a+b win again
	play(t, s, ab, nil, win(1))    // c+d win
	play(t, s, cd, nil, win(1))    // c+e win
	// Forfeits, abandoned games and games still on don't count at all.
	play(t, s, ab, nil, &Outcome{Reason: "forfeit", WinnerTeam: 0, CountsForStats: false})
	gid := play(t, s, ab, nil, nil)
	if err := s.AbandonGame(ctx, gid, t0); err != nil {
		t.Fatal(err)
	}
	play(t, s, ab, nil, nil)
	// A draw is a game played together, but nobody won it.
	play(t, s, ab, nil, &Outcome{Reason: "agreement", Draw: true, CountsForStats: true})

	duos, err := s.TopDuos(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		a, b        string
		wins, games int
		rank        int
	}{
		{"a", "b", 2, 5, 1}, // 4 decided games (2 won) plus the draw
		{"c", "e", 1, 1, 2}, // fewer games for the same wins ranks higher
		{"c", "d", 1, 4, 3}, // three decided games with a+b plus the draw, one won
	}
	if len(duos) != len(want) {
		t.Fatalf("duos = %+v", duos)
	}
	for i, w := range want {
		d := duos[i]
		if d.Players[0].ID != w.a || d.Players[1].ID != w.b || d.Wins != w.wins || d.Games != w.games || d.Rank != w.rank {
			t.Errorf("duo %d = %+v, want %+v", i, d, w)
		}
	}
}

func TestDrawsAreStoredWithoutAWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := play(t, s, [][2]string{{"a", "b"}, {"c", "d"}}, nil, &Outcome{Reason: "agreement", WinnerTeam: 5, Draw: true, CountsForStats: true})
	g, _ := s.Game(ctx, id)
	if !g.Draw || g.WinnerTeam != nil {
		t.Errorf("draw game = %+v", g)
	}
}

func TestAbandoningAFinishedGameChangesNothing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	open := play(t, s, [][2]string{{"a", "b"}, {"c", "d"}}, nil, nil)
	done := play(t, s, [][2]string{{"a", "b"}, {"c", "d"}}, nil, &Outcome{Reason: "wins", CountsForStats: true})

	if err := s.AbandonGame(ctx, open, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Game(ctx, open); g.Status != "abandoned" || g.EndedAt == nil {
		t.Errorf("open game = %+v", g)
	}
	if g, _ := s.Game(ctx, done); g.Status != "finished" {
		t.Errorf("finished game was touched: %+v", g)
	}
	// A game that is already finished can't be abandoned afterwards.
	if err := s.AbandonGame(ctx, done, t0); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Game(ctx, done); g.Status != "finished" {
		t.Error("AbandonGame overwrote a finished game")
	}
}

func TestChatIsKeptUntilADayAfterTheGameEnds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertPlayer(ctx, Player{ID: "a", Name: "Ada"})
	live, _ := s.StartGame(ctx, "live", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	recent, _ := s.StartGame(ctx, "recent", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	old, _ := s.StartGame(ctx, "old", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	for _, id := range []int64{live, recent, old} {
		// Even old messages stay while the game is live or recently over.
		if err := s.AddChat(ctx, id, 1, "a", "first", t0.Add(-48*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := s.AddChat(ctx, id, 2, "a", "second", t0); err != nil {
			t.Fatal(err)
		}
	}
	end := Outcome{Reason: "wins", CountsForStats: true}
	_ = s.FinishGame(ctx, recent, t0.Add(-23*time.Hour), end)
	_ = s.AbandonGame(ctx, old, t0.Add(-25*time.Hour))

	n, err := s.SweepExpired(ctx, t0.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_ = n
	count := func(id int64) int {
		got, err := s.GameChat(ctx, id, time.Time{}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if count(live) != 2 || count(recent) != 2 {
		t.Errorf("chat of a live or recently finished game was swept: live %d, recent %d", count(live), count(recent))
	}
	if count(old) != 0 {
		t.Errorf("chat of a game over for more than a day should be gone: %d", count(old))
	}
	if g, _ := s.Game(ctx, old); g == nil || g.Status != "abandoned" {
		t.Errorf("the game itself must stay: %+v", g)
	}
	if ok, _ := s.WasPlayer(ctx, old, "a"); !ok {
		t.Error("who played must stay too")
	}
}

func TestChatCanBeReadIncrementally(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	for seq := 1; seq <= 10; seq++ {
		if err := s.AddChat(ctx, id, seq, "a", fmt.Sprintf("m%d", seq), t0); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GameChat(ctx, id, t0.Add(-time.Hour), 4, 3)
	if len(got) != 3 || got[0].ID != 5 || got[2].ID != 7 {
		t.Errorf("after 4, limit 3: %+v", got)
	}
	got, _ = s.GameChat(ctx, id, t0.Add(-time.Hour), 10, 100)
	if len(got) != 0 {
		t.Errorf("after the last message there is nothing: %+v", got)
	}
	got, _ = s.GameChat(ctx, id, t0.Add(-time.Hour), 0, 100)
	for i, m := range got {
		if m.ID != i+1 {
			t.Fatalf("messages out of order: %+v", got)
		}
	}
}

func TestSavingAChatLineTwiceDoesNotDuplicateIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	for i := 0; i < 3; i++ {
		if err := s.AddChat(ctx, id, 1, "a", "once", t0); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GameChat(ctx, id, t0.Add(-time.Hour), 0, 100)
	if len(got) != 1 {
		t.Errorf("%d lines", len(got))
	}
}

func TestARoomsLatestGame(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.RoomGame(ctx, "nope"); err != ErrNotFound {
		t.Errorf("unknown room: %v", err)
	}
	first, _ := s.StartGame(ctx, "k3m9xq", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	second, _ := s.StartGame(ctx, "k3m9xq", t0.Add(time.Hour), [][2]string{{"a", "b"}, {"c", "d"}})
	other, _ := s.StartGame(ctx, "other", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	got, err := s.RoomGame(ctx, "k3m9xq")
	if err != nil || got != second || got == first || got == other {
		t.Errorf("RoomGame = %d, %v (first %d, second %d, other %d)", got, err, first, second, other)
	}
}

func TestDeletingAGameDeletesItsDetails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := play(t, s, [][2]string{{"a", "b"}, {"c", "d"}}, []RoundSummary{round(1, 0, "a", "b")}, nil)
	_ = s.AddChat(ctx, id, 1, "a", "hi", t0)
	if _, err := s.pool.Exec(ctx, `DELETE FROM games WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM rounds) + (SELECT count(*) FROM game_players) + (SELECT count(*) FROM chat_messages)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d orphan rows", n)
	}
}
