package store

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// Writer saves what rooms report, off the game's critical path. Rooms hand it
// records through a bounded queue and carry on; one goroutine writes them in
// order. If the database is slow or down, records wait in the queue.
//
// A game is rebuilt from its log of commands, so a missing command loses every
// move after it. The records that make up that log (a game starting, its
// commands, its rounds, its end) are therefore never dropped on the spot: when
// the queue is full the room waits for space (up to maxWait), and a write that
// fails is retried until the database comes back. Everything else (names,
// chat, lobby snapshots) is dropped and counted rather than slowing a game.
type Writer struct {
	s   *Store
	log *slog.Logger
	in  chan any

	mu    sync.Mutex       // guards games
	games map[string]int64 // room id to game id, while a game is running
	done  chan struct{}

	quit     chan struct{} // closed by Close
	quitOnce sync.Once
	maxWait  time.Duration // how long a room waits for queue space before a game record is dropped

	dropped atomic.Int64
}

// DefaultMaxWait is how long a room waits for queue space for a game record.
const DefaultMaxWait = 30 * time.Second

// NewWriter starts a writer with a queue of the given size.
func NewWriter(s *Store, log *slog.Logger, queue int) *Writer {
	if queue <= 0 {
		queue = 1024
	}
	w := &Writer{
		s: s, log: log, in: make(chan any, queue), games: map[string]int64{},
		done: make(chan struct{}), quit: make(chan struct{}), maxWait: DefaultMaxWait,
	}
	go w.run()
	return w
}

// closing reports whether Close has been called.
func (w *Writer) closing() bool {
	select {
	case <-w.quit:
		return true
	default:
		return false
	}
}

// partOfGameLog is true for the records a game cannot be rebuilt without.
func partOfGameLog(item any) bool {
	switch item.(type) {
	case room.GameStarted, room.CommandApplied, room.RoundWon, room.GameFinished:
		return true
	}
	return false
}

func (w *Writer) enqueue(item any) {
	if w.closing() {
		return
	}
	select {
	case w.in <- item:
		return
	default:
	}
	if !partOfGameLog(item) {
		n := w.dropped.Add(1)
		w.log.Error("database queue full: dropping a record", "dropped", n)
		return
	}
	// The database is behind. Slow this room down rather than leave a hole in
	// the game's log, but not for ever: a database that stays away must not
	// freeze the server.
	w.log.Warn("database queue full: a game is waiting for space")
	t := time.NewTimer(w.maxWait)
	defer t.Stop()
	select {
	case w.in <- item:
	case <-w.quit:
	case <-t.C:
		n := w.dropped.Add(1)
		w.log.Error("database queue stayed full: dropping a game record", "dropped", n, "waited", w.maxWait)
	}
}

// Record is a room.Config.OnRecord callback. It never blocks.
func (w *Writer) Record(r room.Record) { w.enqueue(r) }

// Seen saves a player's name and look. It never blocks.
func (w *Writer) Seen(p Player) { w.enqueue(p) }

// Dropped is how many records were thrown away because the queue was full.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Adopt tells the writer that a room is playing a game that is already saved,
// as after a restart, so what the room reports goes to the right game.
func (w *Writer) Adopt(roomID string, gameID int64) {
	w.mu.Lock()
	w.games[roomID] = gameID
	w.mu.Unlock()
}

// GameID is the database id of the game a room is playing, once it is saved.
func (w *Writer) GameID(roomID string) (int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.games[roomID]
	return id, ok
}

// Close stops accepting records and waits for the queue to drain, up to ctx.
func (w *Writer) Close(ctx context.Context) error {
	w.quitOnce.Do(func() { close(w.quit) })
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) run() {
	defer close(w.done)
	for {
		select {
		case item := <-w.in:
			w.handle(item)
		case <-w.quit:
			for { // write what is already queued, then stop
				select {
				case item := <-w.in:
					w.handle(item)
				default:
					return
				}
			}
		}
	}
}

func (w *Writer) handle(item any) {
	switch v := item.(type) {
	case Player:
		w.try("save player", func(ctx context.Context) error { return w.s.UpsertPlayer(ctx, v) })

	case room.GameStarted:
		teams := make([][2]string, len(v.Teams))
		for i, t := range v.Teams {
			teams[i] = [2]string{string(t[0]), string(t[1])}
		}
		var id int64
		if w.tryHard("start game", func(ctx context.Context) (err error) {
			id, err = w.s.StartGame(ctx, v.Room, v.At, teams)
			return err
		}) {
			w.mu.Lock()
			w.games[v.Room] = id
			w.mu.Unlock()
			w.tryHard("save setup", func(ctx context.Context) error {
				return w.s.SaveSetup(ctx, id, string(v.Creator), v.Setup)
			})
		}

	case room.CommandApplied:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("command for a game that was never saved", "room", v.Room, "seq", v.Seq)
			return
		}
		w.tryHard("save command", func(ctx context.Context) error {
			return w.s.AddEvent(ctx, id, v.Seq, v.At, v.Command)
		})

	case room.TeamChatPosted:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("team chat for a game that was never saved", "room", v.Room, "seq", v.Seq)
			return
		}
		w.try("save team chat", func(ctx context.Context) error {
			return w.s.AddTeamChat(ctx, id, TeamChatLine{Seq: v.Seq, Team: int(v.Team), Round: v.Round, Player: string(v.Player), Text: v.Text, At: v.At})
		})

	case room.TeamJoined:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("late team for a game that was never saved", "room", v.Room)
			return
		}
		w.try("save late team", func(ctx context.Context) error {
			return w.s.AddGamePlayers(ctx, id, int(v.Team), [2]string{string(v.Members[0]), string(v.Members[1])})
		})

	case room.LobbyState:
		if v.Gone {
			w.try("forget lobby", func(ctx context.Context) error { return w.s.DeleteLobby(ctx, v.Room) })
			return
		}
		row := LobbyRow{Room: v.Room, Creator: string(v.Creator), Short: v.Short, UpdatedAt: v.At}
		for _, m := range v.Members {
			row.Members = append(row.Members, string(m))
		}
		for _, p := range v.Pairs {
			row.Pairs = append(row.Pairs, [2]string{string(p[0]), string(p[1])})
		}
		for _, p := range v.Invites {
			row.Invites = append(row.Invites, [2]string{string(p[0]), string(p[1])})
		}
		w.try("save lobby", func(ctx context.Context) error { return w.s.SaveLobby(ctx, row) })

	case room.RoundWon:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("round for a game that was never saved", "room", v.Room, "round", v.Round)
			return
		}
		w.tryHard("save round", func(ctx context.Context) error {
			return w.s.RecordRound(ctx, id, v.Round, int(v.Team), string(v.Proclaimer), string(v.Holder), v.At)
		})

	case room.ChatPosted:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("chat for a game that was never saved", "room", v.Room, "seq", v.Seq)
			return
		}
		w.try("save chat", func(ctx context.Context) error {
			return w.s.AddChat(ctx, id, v.Seq, string(v.Player), v.Text, v.At)
		})

	case room.GameFinished:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("finish for a game that was never saved", "room", v.Room)
			return
		}
		w.tryHard("finish game", func(ctx context.Context) error {
			if v.Abandoned || v.Result == nil {
				return w.s.AbandonGame(ctx, id, v.At)
			}
			o := outcome(*v.Result)
			o.Rounds = v.Round
			return w.s.FinishGame(ctx, id, v.At, o)
		})
		w.mu.Lock()
		delete(w.games, v.Room)
		w.mu.Unlock()
	}
}

func outcome(r engine.Result) Outcome {
	return Outcome{Reason: string(r.Reason), WinnerTeam: int(r.Winner), Draw: r.Draw, CountsForStats: r.CountsForStats}
}

// transient reports whether a failed write is worth retrying: the database was
// unreachable, busy or restarting. An error the server answered with (a
// constraint, a bad value) will fail the same way every time.
func transient(err error) bool {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return true // no answer from the server: a network, timeout or pool error
	}
	switch {
	case strings.HasPrefix(pg.Code, "08"), // connection exception
		strings.HasPrefix(pg.Code, "40"), // transaction rollback: deadlock, serialization
		strings.HasPrefix(pg.Code, "53"), // out of resources, too many connections
		strings.HasPrefix(pg.Code, "57"): // shutting down, crashed
		return true
	}
	return false
}

// tryHard is try for the records a game cannot be rebuilt without. It keeps
// retrying, backing off to a few seconds, for as long as the failure looks
// temporary, so a database outage delays the log instead of putting a hole in
// it. Once the writer is closing it gives up after a few attempts, so a dead
// database cannot hold up shutdown. It reports whether the write went through.
func (w *Writer) tryHard(what string, fn func(ctx context.Context) error) bool {
	wait := 200 * time.Millisecond
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := fn(ctx)
		cancel()
		if err == nil {
			if attempt > 0 {
				w.log.Info("database write went through after retrying", "what", what, "attempts", attempt+1)
			}
			return true
		}
		if !transient(err) || (w.closing() && attempt >= 2) {
			w.log.Error("database write failed", "what", what, "err", err)
			return false
		}
		if attempt == 0 || attempt%10 == 0 {
			w.log.Warn("database write failed, retrying", "what", what, "attempt", attempt+1, "err", err)
		}
		time.Sleep(wait)
		wait = min(wait*3, 5*time.Second)
	}
}

// try runs a write, retrying a couple of times if the database hiccups. It
// reports whether the write went through.
func (w *Writer) try(what string, fn func(ctx context.Context) error) bool {
	var err error
	for attempt, wait := 0, 200*time.Millisecond; attempt < 3; attempt, wait = attempt+1, wait*3 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = fn(ctx)
		cancel()
		if err == nil {
			return true
		}
		if attempt < 2 {
			time.Sleep(wait)
		}
	}
	w.log.Error("database write failed", "what", what, "err", err)
	return false
}
