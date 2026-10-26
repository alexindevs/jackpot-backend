package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

func closeWriter(t *testing.T, w *Writer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("closing the writer: %v", err)
	}
}

func TestWriterSavesAWholeGameInOrder(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, quietLog(), 16)
	ctx := context.Background()

	w.Seen(Player{ID: "a", Name: "Ada"})
	w.Record(room.GameStarted{Room: "r1", At: t0, Teams: [][2]engine.PlayerID{{"a", "b"}, {"c", "d"}}})
	w.Record(room.RoundWon{Room: "r1", At: t0, Round: 1, Team: 0, Proclaimer: "a", Holder: "b"})
	w.Record(room.RoundWon{Room: "r1", At: t0, Round: 2, Team: 1, Proclaimer: "d", Holder: "c"})
	w.Record(room.ChatPosted{Room: "r1", At: t0, Seq: 1, Player: "a", Text: "I said it first"})
	w.Record(room.ChatPosted{Room: "r1", At: t0, Seq: 2, Player: "d", Text: "no you didn't"})

	// While the game runs, its database id is known.
	eventually(t, "the game to be saved", func() bool { _, ok := w.GameID("r1"); return ok })

	res := engine.Result{Winner: 1, Reason: engine.EndWins, CountsForStats: true}
	w.Record(room.GameFinished{Room: "r1", At: t0.Add(time.Hour), Result: &res})
	closeWriter(t, w)

	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM games WHERE room = 'r1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	g, err := s.Game(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != "finished" || *g.WinnerTeam != 1 || !g.CountsForStats || len(g.Players) != 4 || len(g.Rounds) != 2 {
		t.Errorf("game = %+v", g)
	}
	if g.Players[0].Player.Name != "Ada" {
		t.Errorf("player names should come from Seen: %+v", g.Players[0])
	}
	chat, _ := s.GameChat(ctx, id, t0.Add(-time.Hour), 0, 100)
	if len(chat) != 2 || chat[0].Text != "I said it first" || chat[1].ID != 2 || chat[0].Player.Name != "Ada" {
		t.Errorf("chat = %+v", chat)
	}
	if _, ok := w.GameID("r1"); ok {
		t.Error("a finished game should be forgotten")
	}
}

func TestWriterRecordsAbandonedAndDrawnGames(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, quietLog(), 16)
	ctx := context.Background()
	teams := [][2]engine.PlayerID{{"a", "b"}, {"c", "d"}}

	w.Record(room.GameStarted{Room: "gone", At: t0, Teams: teams})
	w.Record(room.GameFinished{Room: "gone", At: t0, Abandoned: true})
	w.Record(room.GameStarted{Room: "tie", At: t0, Teams: teams})
	w.Record(room.GameFinished{Room: "tie", At: t0, Result: &engine.Result{Draw: true, Reason: engine.EndAgreement, CountsForStats: true}})
	closeWriter(t, w)

	status := func(room string) (string, bool) {
		var st string
		var draw bool
		if err := s.pool.QueryRow(ctx, `SELECT status, draw FROM games WHERE room = $1`, room).Scan(&st, &draw); err != nil {
			t.Fatal(err)
		}
		return st, draw
	}
	if st, _ := status("gone"); st != "abandoned" {
		t.Errorf("status = %s", st)
	}
	if st, draw := status("tie"); st != "finished" || !draw {
		t.Errorf("status = %s, draw = %v", st, draw)
	}
}

func TestWriterIgnoresRecordsForGamesItNeverSaw(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, quietLog(), 16)
	w.Record(room.RoundWon{Room: "ghost", At: t0, Round: 1, Proclaimer: "a", Holder: "b"})
	w.Record(room.GameFinished{Room: "ghost", At: t0, Abandoned: true})
	w.Record(room.ChatPosted{Room: "ghost", At: t0, Seq: 1, Player: "a", Text: "hello?"})
	closeWriter(t, w)
	var n int
	_ = s.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM games) + (SELECT count(*) FROM rounds) + (SELECT count(*) FROM chat_messages)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d rows were written for an unknown game", n)
	}
}

func TestAFullQueueDropsRecordsInsteadOfBlocking(t *testing.T) {
	// A writer whose worker never runs, so nothing drains the queue.
	w := &Writer{log: quietLog(), in: make(chan any, 2), games: map[string]int64{}, done: make(chan struct{})}
	start := time.Now()
	for i := 0; i < 5; i++ {
		w.Seen(Player{ID: "a", Name: "Ada"})
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("enqueueing must never block")
	}
	if got := w.Dropped(); got != 3 {
		t.Errorf("dropped = %d, want 3", got)
	}
}

func TestRecordsAfterCloseAreIgnoredNotAPanic(t *testing.T) {
	s := newTestStore(t)
	w := NewWriter(s, quietLog(), 4)
	closeWriter(t, w)
	w.Seen(Player{ID: "a", Name: "Ada"})
	w.Record(room.GameStarted{Room: "late"})
	closeWriter(t, w) // closing twice is fine too
}

func TestADeadDatabaseNeverCrashesOrBlocksTheWriter(t *testing.T) {
	s := newTestStore(t)
	s.pool.Close() // every write will fail
	w := NewWriter(s, quietLog(), 4)
	w.Seen(Player{ID: "a", Name: "Ada"})
	w.Record(room.GameStarted{Room: "r", At: t0, Teams: [][2]engine.PlayerID{{"a", "b"}, {"c", "d"}}})
	w.Record(room.RoundWon{Room: "r", At: t0, Round: 1, Proclaimer: "a", Holder: "b"})
	closeWriter(t, w)
	if _, ok := w.GameID("r"); ok {
		t.Error("a game that couldn't be saved has no id")
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newStalledWriter(queue int, maxWait time.Duration) *Writer {
	return &Writer{
		log: quietLog(), in: make(chan any, queue), games: map[string]int64{},
		done: make(chan struct{}), quit: make(chan struct{}), maxWait: maxWait,
	}
}

func TestAGameRecordWaitsForQueueSpaceInsteadOfBeingDropped(t *testing.T) {
	w := newStalledWriter(1, 5*time.Second)
	w.Seen(Player{ID: "a", Name: "Ada"}) // fills the queue

	go func() {
		time.Sleep(150 * time.Millisecond)
		<-w.in // the database catches up
	}()
	start := time.Now()
	w.Record(room.CommandApplied{Room: "r", Seq: 1, Command: engine.StartRound{}})
	if took := time.Since(start); took < 100*time.Millisecond {
		t.Errorf("the room should have waited for space, took %v", took)
	}
	if w.Dropped() != 0 {
		t.Errorf("dropped = %d: a command must not be dropped while there is hope of space", w.Dropped())
	}
	select {
	case item := <-w.in:
		if _, ok := item.(room.CommandApplied); !ok {
			t.Errorf("queued %T", item)
		}
	default:
		t.Error("the command never reached the queue")
	}
}

func TestAGameRecordIsDroppedOnlyAfterWaitingTheFullTime(t *testing.T) {
	w := newStalledWriter(1, 80*time.Millisecond)
	w.Seen(Player{ID: "a", Name: "Ada"})
	start := time.Now()
	w.Record(room.CommandApplied{Room: "r", Seq: 1, Command: engine.StartRound{}})
	if took := time.Since(start); took < 80*time.Millisecond {
		t.Errorf("gave up after %v, before the wait was over", took)
	}
	if w.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", w.Dropped())
	}
}

func TestOtherRecordsAreStillDroppedAtOnceWhenTheQueueIsFull(t *testing.T) {
	w := newStalledWriter(1, 5*time.Second)
	w.Seen(Player{ID: "a", Name: "Ada"})
	start := time.Now()
	w.Record(room.ChatPosted{Room: "r", Seq: 1, Player: "a", Text: "hi"})
	if time.Since(start) > 200*time.Millisecond || w.Dropped() != 1 {
		t.Errorf("chat should be dropped at once: %v, dropped %d", time.Since(start), w.Dropped())
	}
}

func TestTryHardRetriesATemporaryFailureUntilItWorks(t *testing.T) {
	w := newStalledWriter(1, time.Second)
	calls := 0
	ok := w.tryHard("test", func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	if !ok || calls != 3 {
		t.Errorf("ok = %v after %d calls, want success on the 3rd", ok, calls)
	}
}

func TestTryHardDoesNotRetryAnErrorTheDatabaseAnswered(t *testing.T) {
	w := newStalledWriter(1, time.Second)
	calls := 0
	ok := w.tryHard("test", func(context.Context) error {
		calls++
		return &pgconn.PgError{Code: "23505"} // unique violation: it will fail every time
	})
	if ok || calls != 1 {
		t.Errorf("ok = %v after %d calls, want one failed attempt", ok, calls)
	}
}

func TestTryHardGivesUpQuicklyOnceTheWriterIsClosing(t *testing.T) {
	w := newStalledWriter(1, time.Second)
	close(w.quit)
	calls := 0
	start := time.Now()
	ok := w.tryHard("test", func(context.Context) error { calls++; return errors.New("connection refused") })
	if ok || calls != 3 || time.Since(start) > 5*time.Second {
		t.Errorf("ok = %v, %d calls in %v: a dead database must not hold up shutdown", ok, calls, time.Since(start))
	}
}

func TestWhichFailuresAreWorthRetrying(t *testing.T) {
	for code, want := range map[string]bool{
		"08006": true, "40P01": true, "53300": true, "57P01": true, // connection, deadlock, too many connections, shutdown
		"23505": false, "22P02": false, "42P01": false, // unique violation, bad value, missing table
	} {
		if got := transient(&pgconn.PgError{Code: code}); got != want {
			t.Errorf("code %s: transient = %v, want %v", code, got, want)
		}
	}
	if !transient(errors.New("dial tcp: connection refused")) || !transient(context.DeadlineExceeded) {
		t.Error("an error with no answer from the server is temporary")
	}
}
