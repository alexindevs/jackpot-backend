package room

import (
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

type dkind int

const (
	dkCountdown dkind = iota + 1 // lobby countdown to game start
	dkChat                       // pre-round team chat
	dkHold                       // holder must pass
	dkReveal                     // a suspected player must choose what to show
	dkForced                     // a caught holder must choose a card to pass
	dkReconnect                  // a disconnected player's window
	dkEmpty                      // nobody is connected
	dkPause                      // a paused game's time is up
)

func (k dkind) label() string {
	switch k {
	case dkHold:
		return "hold"
	case dkReveal:
		return "reveal"
	case dkForced:
		return "forced"
	case dkReconnect:
		return "reconnect"
	case dkCountdown:
		return "countdown"
	case dkChat:
		return "chat"
	case dkPause:
		return "pause"
	}
	return "empty"
}

type dkey struct {
	kind   dkind
	player engine.PlayerID // empty for room-wide deadlines
}

type dentry struct {
	at  time.Time
	seq uint64 // insertion order, to break ties deterministically
}

// deadlines is the room's timer table. The room never sleeps: it records when
// things are due, and whoever drives it calls Tick.
type deadlines struct {
	m   map[dkey]dentry
	seq uint64
}

func newDeadlines() *deadlines { return &deadlines{m: map[dkey]dentry{}} }

func (d *deadlines) set(k dkey, at time.Time) {
	d.seq++
	d.m[k] = dentry{at: at, seq: d.seq}
}

func (d *deadlines) clear(k dkey) { delete(d.m, k) }

func (d *deadlines) clearKind(kind dkind) {
	for k := range d.m {
		if k.kind == kind {
			delete(d.m, k)
		}
	}
}

func (d *deadlines) has(k dkey) bool { _, ok := d.m[k]; return ok }

func (d *deadlines) get(k dkey) (time.Time, bool) {
	e, ok := d.m[k]
	return e.at, ok
}

// players lists who has a deadline of the given kind, in ID order.
func (d *deadlines) players(kind dkind) []engine.PlayerID {
	var out []engine.PlayerID
	for k := range d.m {
		if k.kind == kind {
			out = append(out, k.player)
		}
	}
	slices.Sort(out)
	return out
}

// next is the earliest deadline; ties go to whichever was set first.
func (d *deadlines) next() (dkey, time.Time, bool) {
	var best dkey
	var be dentry
	found := false
	for k, e := range d.m {
		if !found || e.at.Before(be.at) || (e.at.Equal(be.at) && e.seq < be.seq) {
			best, be, found = k, e, true
		}
	}
	return best, be.at, found
}
