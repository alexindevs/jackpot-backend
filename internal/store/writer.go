package store

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// Writer saves what rooms report, off the game's critical path. Rooms hand it
// records through a bounded queue and carry on; one goroutine writes them in
// order. If the database is slow or down, records wait in the queue, and once
// it is full new ones are dropped (and counted) rather than slowing a game.
type Writer struct {
	s   *Store
	log *slog.Logger
	in  chan any

	mu     sync.Mutex // guards closed, games
	closed bool
	games  map[string]int64 // room id to game id, while a game is running
	done   chan struct{}

	dropped atomic.Int64
}

// NewWriter starts a writer with a queue of the given size.
func NewWriter(s *Store, log *slog.Logger, queue int) *Writer {
	if queue <= 0 {
		queue = 1024
	}
	w := &Writer{s: s, log: log, in: make(chan any, queue), games: map[string]int64{}, done: make(chan struct{})}
	go w.run()
	return w
}

func (w *Writer) enqueue(item any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.in <- item:
	default:
		n := w.dropped.Add(1)
		w.log.Error("database queue full: dropping a record", "dropped", n)
	}
}

// Record is a room.Config.OnRecord callback. It never blocks.
func (w *Writer) Record(r room.Record) { w.enqueue(r) }

// Seen saves a player's name and look. It never blocks.
func (w *Writer) Seen(p Player) { w.enqueue(p) }

// Dropped is how many records were thrown away because the queue was full.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// GameID is the database id of the game a room is playing, once it is saved.
func (w *Writer) GameID(roomID string) (int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.games[roomID]
	return id, ok
}

// Close stops accepting records and waits for the queue to drain, up to ctx.
func (w *Writer) Close(ctx context.Context) error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.in)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) run() {
	defer close(w.done)
	for item := range w.in {
		w.handle(item)
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
		if w.try("start game", func(ctx context.Context) (err error) {
			id, err = w.s.StartGame(ctx, v.Room, v.At, teams)
			return err
		}) {
			w.mu.Lock()
			w.games[v.Room] = id
			w.mu.Unlock()
		}

	case room.RoundWon:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("round for a game that was never saved", "room", v.Room, "round", v.Round)
			return
		}
		w.try("save round", func(ctx context.Context) error {
			return w.s.RecordRound(ctx, id, v.Round, int(v.Team), string(v.Proclaimer), string(v.Holder), v.At)
		})

	case room.GameFinished:
		id, ok := w.GameID(v.Room)
		if !ok {
			w.log.Error("finish for a game that was never saved", "room", v.Room)
			return
		}
		w.try("finish game", func(ctx context.Context) error {
			if v.Abandoned || v.Result == nil {
				return w.s.AbandonGame(ctx, id, v.At)
			}
			return w.s.FinishGame(ctx, id, v.At, outcome(*v.Result))
		})
		w.mu.Lock()
		delete(w.games, v.Room)
		w.mu.Unlock()
	}
}

func outcome(r engine.Result) Outcome {
	return Outcome{Reason: string(r.Reason), WinnerTeam: int(r.Winner), Draw: r.Draw, CountsForStats: r.CountsForStats}
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
