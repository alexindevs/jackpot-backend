package room

import (
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// A game that loses all its players for a while, or whose server restarts, is
// not ended: it is paused. Nothing in a paused game moves (no timers run, no
// one can play) and it waits for its players. When every player still in the
// game is connected again it picks up where it was, and if they don't all come
// back within Config.PausedFor the game is abandoned.

// Paused tells the room a game has been paused and when it will be given up.
type Paused struct {
	Until   time.Time
	Players []engine.PlayerID // who is still in the game and can come back to it
}

// Resumed tells the room a paused game is running again.
type Resumed struct{}

func (Paused) isMessage()  {}
func (Resumed) isMessage() {}

// Paused reports whether the game is waiting for its players, and until when.
func (r *Room) Paused() (time.Time, bool) { return r.pausedUntil, r.paused }

// Players lists the players still in the game, in join order.
func (r *Room) Players() []engine.PlayerID {
	var out []engine.PlayerID
	for _, id := range r.order {
		if m := r.members[id]; m != nil && !m.removed {
			out = append(out, id)
		}
	}
	return out
}

// RosterEntry is one player of a game, as the list of someone's paused games
// shows them.
type RosterEntry struct {
	ID        engine.PlayerID
	Team      engine.TeamID
	Connected bool
}

// Roster lists the players still in the game.
func (r *Room) Roster() []RosterEntry {
	var out []RosterEntry
	for _, id := range r.order {
		if m := r.members[id]; m != nil && !m.removed {
			out = append(out, RosterEntry{ID: id, Team: m.team, Connected: m.connected})
		}
	}
	return out
}

// Round is the round being played, or about to be played, in a game.
func (r *Room) Round() int {
	if r.game == nil {
		return 0
	}
	return r.game.Round()
}

// Pause freezes a running game until the given time. It is for the server
// (shutting down, say) as well as for the room itself.
func (r *Room) Pause(now, until time.Time) []Out {
	r.out = nil
	if r.game != nil && !r.paused && (r.phase == PhaseChat || r.phase == PhasePlaying) {
		r.pause(now, until)
	}
	return r.take()
}

func (r *Room) pause(now, until time.Time) {
	r.paused = true
	r.pausedUntil = until
	r.dl = newDeadlines()
	r.holdFor = ""
	r.dl.set(dkey{kind: dkPause}, until)
	r.broadcast(Paused{Until: until, Players: r.Players()})
}

// maybeResume restarts a paused game once everyone still in it is connected.
func (r *Room) maybeResume(now time.Time) {
	if !r.paused || r.phase == PhaseClosed {
		return
	}
	any := false
	for _, m := range r.members {
		if m.removed {
			continue
		}
		any = true
		if !m.connected {
			return
		}
	}
	if any {
		r.resume(now)
	}
}

// resume starts everything again from fresh timers. A round in progress gets
// a new hold timer for whoever holds the passing card; between rounds the
// private team chat starts over.
func (r *Room) resume(now time.Time) {
	r.paused = false
	r.pausedUntil = time.Time{}
	r.dl = newDeadlines()
	r.holdFor = ""
	r.broadcast(Resumed{})
	switch r.game.Phase() {
	case engine.PhaseOver:
		r.phase = PhaseOver
	case engine.PhaseWaiting:
		r.startChat(now)
	default:
		r.phase = PhasePlaying
		r.syncTimers(now)
	}
}

// Saved is what is kept of a live game so it can be brought back: the
// setup, every engine command applied since, and enough chat to carry on.
type Saved struct {
	ID       string
	Creator  engine.PlayerID
	Setup    engine.Setup
	Commands []engine.Command
	ChatSeq  int
	Chat     []ChatMessage // the most recent public chat, oldest first
	Since    time.Time     // when the game was last active
}

// Restore rebuilds a room from a Saved game. The room comes back paused, with
// nobody connected, waiting for its players for Config.PausedFor after Since.
// It fails if the log doesn't replay or the game was already over.
func Restore(cfg Config, sv Saved, now time.Time) (*Room, error) {
	g, err := engine.Replay(sv.Setup, sv.Commands)
	if err != nil {
		return nil, err
	}
	if g.Phase() == engine.PhaseOver {
		return nil, errors.New("the game was already over")
	}
	until := sv.Since.Add(cfg.PausedFor)
	if !until.After(now) {
		return nil, errors.New("the game was left paused for too long")
	}

	r := &Room{
		id:         sv.ID,
		creator:    sv.Creator,
		cfg:        cfg,
		rng:        rand.New(rand.NewPCG(sv.Setup.SeedA^0x5bd1e995, sv.Setup.SeedB+uint64(len(sv.Commands)))),
		members:    map[engine.PlayerID]*member{},
		spectators: map[engine.PlayerID]bool{},
		invites:    map[inviteKey]bool{},
		dl:         newDeadlines(),
		chatRate:   map[engine.PlayerID]*bucket{},
		sigRate:    map[engine.PlayerID]*bucket{},
		seqs:       map[engine.PlayerID]int64{},
		logs:       map[engine.PlayerID][]Out{},
		game:       g,
		cmdSeq:     len(sv.Commands),
		chatSeq:    sv.ChatSeq,
		chatLog:    slices.Clone(sv.Chat),
	}
	partner := map[engine.PlayerID]engine.PlayerID{}
	for _, a := range sv.Setup.Specs {
		for _, b := range sv.Setup.Specs {
			if a.ID != b.ID && a.Team == b.Team {
				partner[a.ID] = b.ID
			}
		}
	}
	for _, sp := range sv.Setup.Specs {
		r.members[sp.ID] = &member{id: sp.ID, partner: partner[sp.ID], team: sp.Team, removed: !g.Active(sp.ID)}
		r.order = append(r.order, sp.ID)
	}
	if g.Phase() == engine.PhaseWaiting {
		r.phase = PhaseChat
	} else {
		r.phase = PhasePlaying
	}
	r.paused = true
	r.pausedUntil = until
	r.dl.set(dkey{kind: dkPause}, until)
	return r, nil
}
