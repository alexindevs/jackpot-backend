package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func liveSetup() engine.Setup {
	return engine.Setup{
		Config: engine.DefaultConfig(),
		Specs: []engine.PlayerSpec{
			{ID: "a", Team: 0}, {ID: "b", Team: 0}, {ID: "c", Team: 1}, {ID: "d", Team: 1},
		},
		// Larger than a double can hold exactly: they must come back unchanged.
		SeedA: 18446744073709551557, SeedB: 9007199254740993,
	}
}

func TestALiveGameComesBackFromTheDatabaseExactly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "room1", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	su := liveSetup()
	if err := s.SaveSetup(ctx, id, "a", su); err != nil {
		t.Fatal(err)
	}
	cmds := []engine.Command{
		engine.StartRound{}, engine.Pass{Player: "a", Card: 2}, engine.Suspect{By: "b", Target: "c"},
		engine.Reveal{Player: "c", Types: []engine.CardType{0, 1}}, engine.RemoveTeam{Team: 1}, engine.VoteEnd{Player: "a", Agree: true},
	}
	for i, c := range cmds {
		if err := s.AddEvent(ctx, id, i+1, t0.Add(time.Duration(i)*time.Second), c); err != nil {
			t.Fatal(err)
		}
	}
	// A retry of the same command is harmless.
	if err := s.AddEvent(ctx, id, 2, t0.Add(time.Hour), cmds[1]); err != nil {
		t.Fatal(err)
	}
	_ = s.AddChat(ctx, id, 1, "a", "hello", t0)
	_ = s.AddChat(ctx, id, 2, "b", "hi", t0)

	live, bad, err := s.LiveGames(ctx, 50)
	if err != nil || len(bad) != 0 || len(live) != 1 {
		t.Fatalf("live = %d, bad = %v, err = %v", len(live), bad, err)
	}
	g := live[0]
	if g.GameID != id || g.Room != "room1" || g.Creator != "a" || g.Gap {
		t.Errorf("game = %+v", g)
	}
	if !reflect.DeepEqual(g.Setup, su) {
		t.Errorf("setup = %+v, want %+v", g.Setup, su)
	}
	if !reflect.DeepEqual(g.Commands, cmds) {
		t.Errorf("commands = %#v", g.Commands)
	}
	if !g.LastActive.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("last active = %v (the retry must not move it)", g.LastActive)
	}
	if g.ChatSeq != 2 || len(g.Chat) != 2 || g.Chat[1].Text != "hi" {
		t.Errorf("chat = %d %+v", g.ChatSeq, g.Chat)
	}
}

func TestALogWithAHoleComesBackUpToTheHole(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	_ = s.SaveSetup(ctx, id, "a", liveSetup())
	_ = s.AddEvent(ctx, id, 1, t0, engine.StartRound{})
	_ = s.AddEvent(ctx, id, 2, t0, engine.Pass{Player: "a", Card: 1})
	_ = s.AddEvent(ctx, id, 4, t0, engine.Pass{Player: "b", Card: 1}) // 3 never arrived
	live, _, _ := s.LiveGames(ctx, 50)
	if len(live) != 1 || !live[0].Gap || len(live[0].Commands) != 2 {
		t.Errorf("live = %+v", live)
	}
}

func TestOnlyUnfinishedGamesWithASetupAreLive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teams := [][2]string{{"a", "b"}, {"c", "d"}}
	live, _ := s.StartGame(ctx, "live", t0, teams)
	done, _ := s.StartGame(ctx, "done", t0, teams)
	legacy, _ := s.StartGame(ctx, "legacy", t0, teams)
	_ = s.SaveSetup(ctx, live, "a", liveSetup())
	_ = s.SaveSetup(ctx, done, "a", liveSetup())
	_ = s.FinishGame(ctx, done, t0, Outcome{Reason: "wins", CountsForStats: true})

	got, _, _ := s.LiveGames(ctx, 50)
	if len(got) != 1 || got[0].GameID != live {
		t.Errorf("live games = %+v", got)
	}
	n, err := s.AbandonUnrestorable(ctx, t0)
	if err != nil || n != 1 {
		t.Fatalf("abandoned %d, %v", n, err)
	}
	if g, _ := s.Game(ctx, legacy); g.Status != "abandoned" {
		t.Errorf("legacy = %+v", g)
	}
	if g, _ := s.Game(ctx, live); g.Status != "playing" {
		t.Errorf("a game with a setup must be left for restoring: %+v", g)
	}
}

func TestUnreadableGamesAreReportedNotLost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	if _, err := s.pool.Exec(ctx, `UPDATE games SET setup = '{"seedA":"x"}' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	live, bad, err := s.LiveGames(ctx, 50)
	if err != nil || len(live) != 0 || bad[id] == nil {
		t.Errorf("live %d bad %v err %v", len(live), bad, err)
	}
	id2, _ := s.StartGame(ctx, "r2", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	_ = s.SaveSetup(ctx, id2, "a", liveSetup())
	if _, err := s.pool.Exec(ctx, `INSERT INTO game_events (game_id, seq, at, command) VALUES ($1, 1, now(), '{"k":"nope"}')`, id2); err != nil {
		t.Fatal(err)
	}
	_, bad, _ = s.LiveGames(ctx, 50)
	if bad[id2] == nil {
		t.Errorf("a command that cannot be read should be reported: %v", bad)
	}
}

func TestExpiryClearsDisputeDataButKeepsTheSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	teams := [][2]string{{"a", "b"}, {"c", "d"}}
	old, _ := s.StartGame(ctx, "old", t0, teams)
	recent, _ := s.StartGame(ctx, "recent", t0, teams)
	live, _ := s.StartGame(ctx, "live", t0, teams)
	for _, id := range []int64{old, recent, live} {
		_ = s.SaveSetup(ctx, id, "a", liveSetup())
		_ = s.AddEvent(ctx, id, 1, t0, engine.StartRound{})
	}
	_ = s.RecordRound(ctx, old, 1, 0, "a", "b", t0)
	_ = s.FinishGame(ctx, old, t0.Add(-25*time.Hour), Outcome{Reason: "wins", WinnerTeam: 0, CountsForStats: true})
	_ = s.FinishGame(ctx, recent, t0.Add(-time.Hour), Outcome{Reason: "wins", WinnerTeam: 0, CountsForStats: true})

	n, err := s.SweepExpired(ctx, t0.Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("cleared %d, %v", n, err)
	}
	events := func(id int64) (n int) {
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM game_events WHERE game_id = $1`, id).Scan(&n)
		return
	}
	hasSetup := func(id int64) (b bool) {
		_ = s.pool.QueryRow(ctx, `SELECT setup IS NOT NULL FROM games WHERE id = $1`, id).Scan(&b)
		return
	}
	if events(old) != 0 || hasSetup(old) {
		t.Error("an expired game keeps no events or setup")
	}
	if events(recent) != 1 || !hasSetup(recent) || events(live) != 1 || !hasSetup(live) {
		t.Error("recent and live games keep theirs")
	}
	g, err := s.Game(ctx, old)
	if err != nil || g.Status != "finished" || len(g.Rounds) != 1 {
		t.Errorf("the summary of an expired game stays: %+v, %v", g, err)
	}
	duos, _ := s.TopDuos(ctx, 10)
	if len(duos) == 0 {
		t.Error("leaderboards are built from the summaries, which stay")
	}
}

func TestLobbiesAreSavedReplacedAndForgotten(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	l := LobbyRow{Room: "abc", Creator: "a", Members: []string{"a", "b", "c"}, Pairs: [][2]string{{"a", "b"}}, Invites: [][2]string{{"c", "a"}}, UpdatedAt: t0}
	if err := s.SaveLobby(ctx, l); err != nil {
		t.Fatal(err)
	}
	// A later state replaces it; an older one arriving late does not.
	l2 := l
	l2.Members, l2.Pairs, l2.Invites, l2.UpdatedAt = []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"c", "d"}}, nil, t0.Add(time.Minute)
	_ = s.SaveLobby(ctx, l2)
	_ = s.SaveLobby(ctx, l)
	got, bad, err := s.Lobbies(ctx)
	if err != nil || len(bad) != 0 || len(got) != 1 {
		t.Fatalf("lobbies = %+v, bad %v, err %v", got, bad, err)
	}
	if !reflect.DeepEqual(got[0].Members, l2.Members) || !reflect.DeepEqual(got[0].Pairs, l2.Pairs) || !got[0].UpdatedAt.Equal(l2.UpdatedAt) || got[0].Creator != "a" {
		t.Errorf("lobby = %+v", got[0])
	}
	_ = s.SaveLobby(ctx, LobbyRow{Room: "xyz", Creator: "z", Members: []string{"z"}, UpdatedAt: t0})
	if err := s.DeleteLobby(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Lobbies(ctx)
	if len(got) != 1 || got[0].Room != "xyz" {
		t.Errorf("after deleting: %+v", got)
	}
	if err := s.DeleteLobby(ctx, "never-existed"); err != nil {
		t.Errorf("deleting nothing is fine: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE lobbies SET state = '"nonsense"' WHERE room = 'xyz'`); err != nil {
		t.Fatal(err)
	}
	got, bad, _ = s.Lobbies(ctx)
	if len(got) != 0 || bad["xyz"] == nil {
		t.Errorf("an unreadable lobby is reported: %+v %v", got, bad)
	}
}

func TestTeamChatIsSavedInOrderAndComesBackWithALiveGame(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id, _ := s.StartGame(ctx, "r", t0, [][2]string{{"a", "b"}, {"c", "d"}})
	_ = s.SaveSetup(ctx, id, "a", liveSetup())
	lines := []TeamChatLine{
		{Seq: 1, Team: 0, Round: 1, Player: "a", Text: "sneeze means circles", At: t0},
		{Seq: 2, Team: 1, Round: 1, Player: "c", Text: "we have our own", At: t0.Add(time.Second)},
	}
	for _, l := range lines {
		if err := s.AddTeamChat(ctx, id, l); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddTeamChat(ctx, id, lines[0]); err != nil {
		t.Fatal(err) // a retry is harmless
	}
	same := func(a, b []TeamChatLine) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !a[i].At.Equal(b[i].At) {
				return false
			}
			a[i].At, b[i].At = time.Time{}, time.Time{}
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	got, err := s.GameTeamChat(ctx, id)
	if err != nil || !same(got, append([]TeamChatLine(nil), lines...)) {
		t.Fatalf("team chat = %+v, %v", got, err)
	}
	live, _, _ := s.LiveGames(ctx, 50)
	if len(live) != 1 || !same(live[0].TeamChat, append([]TeamChatLine(nil), lines...)) {
		t.Errorf("live game team chat = %+v", live)
	}

	// It expires with the rest of the game's dispute data, not before.
	_ = s.FinishGame(ctx, id, t0.Add(-25*time.Hour), Outcome{Reason: "wins", CountsForStats: true})
	if _, err := s.SweepExpired(ctx, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GameTeamChat(ctx, id); len(got) != 0 {
		t.Errorf("team chat of an expired game should be gone: %+v", got)
	}
}
