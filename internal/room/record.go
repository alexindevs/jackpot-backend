package room

import (
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Record is something worth keeping once it has happened: a game starting, a
// round being won, a game ending. The room calls Config.OnRecord once per
// occurrence, from its own goroutine, so the callback must not block (hand the
// record to a queue).
type Record interface{ isRecord() }

// GameStarted says who is playing. Teams[i] is team i.
type GameStarted struct {
	Room  string
	At    time.Time
	Teams [][2]engine.PlayerID
}

// RoundWon is one finished round.
type RoundWon struct {
	Room       string
	At         time.Time
	Round      int
	Team       engine.TeamID
	Proclaimer engine.PlayerID
	Holder     engine.PlayerID // who held the four of a kind
}

// GameFinished ends a game. Result is nil when the game was abandoned, which
// is when the room closed before anyone won (everyone left, say).
type GameFinished struct {
	Room      string
	At        time.Time
	Result    *engine.Result
	Abandoned bool
}

func (GameStarted) isRecord()  {}
func (RoundWon) isRecord()     {}
func (GameFinished) isRecord() {}

func (r *Room) record(rec Record) {
	if r.cfg.OnRecord != nil {
		r.cfg.OnRecord(rec)
	}
}
