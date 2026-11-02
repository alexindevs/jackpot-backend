// Package bot plays the bots of a room. A bot is given nothing but what a
// person in its seat would see: the messages the room sends it, and a snapshot
// of its own view. It acts through the same inputs a person sends, after a
// person's kind of pause, so a game with bots in it plays like a game with
// people in it.
package bot

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/botname"
	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// Config tunes the bots.
type Config struct {
	// Speed scales every pause. 1 is a person's pace; tests use a big number
	// to play a whole game in no time.
	Speed float64
}

// Driver plays every bot in one room. It is used from the room's goroutine
// only and has no clock of its own.
type Driver struct {
	cfg   Config
	rng   *rand.Rand
	bots  map[engine.PlayerID]*brain
	codes map[codeKey]code
	prev  map[engine.TeamID]code   // each team's code last round, so it is not reused
	extra map[engine.PlayerID]bool // people played like bots, for tests
}

// NewDriver makes a driver. The seed decides its choices, so a game played by
// a fake clock can be repeated exactly.
func NewDriver(seed uint64, cfg Config) *Driver {
	if cfg.Speed <= 0 {
		cfg.Speed = 1
	}
	return &Driver{
		cfg:   cfg,
		rng:   rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		bots:  map[engine.PlayerID]*brain{},
		codes: map[codeKey]code{},
		prev:  map[engine.TeamID]code{},
		extra: map[engine.PlayerID]bool{},
	}
}

// Also makes the driver play these people's seats as if they were bots'. It
// is for tests, which need a whole table of players that act on their own.
func (d *Driver) Also(ids ...engine.PlayerID) {
	for _, id := range ids {
		d.extra[id] = true
	}
}

var _ room.BotDriver = (*Driver)(nil)

// think is a pause of between lo and hi seconds, at the configured speed.
func (d *Driver) think(lo, hi float64) time.Duration {
	s := lo + d.rng.Float64()*(hi-lo)
	return time.Duration(s / d.cfg.Speed * float64(time.Second))
}

// respond is how long a bot takes to do something: between 5 and 37 seconds, so
// it plays at a person's pace and never snaps back at once.
func (d *Driver) respond() time.Duration { return d.think(5, 37) }

// respondPrompt is the same for a question the server only waits 30 seconds
// for (what to show when suspected, which card to give up), so a bot answers
// in time like a quick person would.
func (d *Driver) respondPrompt() time.Duration { return d.think(5, 25) }

func (d *Driver) ids() []engine.PlayerID {
	out := make([]engine.PlayerID, 0, len(d.bots))
	for id := range d.bots {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Observe keeps what the room sent to its bots.
func (d *Driver) Observe(now time.Time, outs []room.Out) {
	for _, o := range outs {
		if !botname.IsBotID(o.To) && !d.extra[o.To] {
			continue
		}
		b := d.bots[o.To]
		if b == nil {
			b = newBrain(o.To)
			d.bots[o.To] = b
		}
		b.see(now, o.Msg)
		b.dirty = true
	}
}

// Wake is when a bot next wants to do something.
func (d *Driver) Wake() (time.Time, bool) {
	var at time.Time
	ok := false
	take := func(t time.Time) {
		if !ok || t.Before(at) {
			at, ok = t, true
		}
	}
	for _, b := range d.bots {
		if b.done {
			continue
		}
		if b.dirty {
			take(time.Time{}) // something new to think about
		}
		for _, it := range b.intents {
			take(it.at)
		}
		if !b.nextTick.IsZero() {
			take(b.nextTick)
		}
	}
	return at, ok
}

// Act works out what each bot does now.
func (d *Driver) Act(now time.Time, view func(engine.PlayerID) room.Snapshot) []room.BotAct {
	var acts []room.BotAct
	for _, id := range d.ids() {
		b := d.bots[id]
		if b.done {
			continue
		}
		due := !b.nextTick.IsZero() && !b.nextTick.After(now)
		for _, it := range b.intents {
			if !it.at.After(now) {
				due = true
			}
		}
		if !b.dirty && !due {
			continue
		}
		snap := view(id)
		b.dirty = false
		b.plan(now, snap, d)
		for _, k := range b.dueKinds(now) {
			it := b.intents[k]
			delete(b.intents, k)
			if in, ok := b.build(k, it, now, snap, d); ok {
				acts = append(acts, room.BotAct{Bot: id, In: in})
				b.fired(k, now)
			}
		}
	}
	return acts
}

// Result learns from an act that was refused, so a bot does not try the same
// thing again at once.
func (d *Driver) Result(act room.BotAct, err error) {
	if err == nil {
		return
	}
	b := d.bots[act.Bot]
	if b == nil {
		return
	}
	k := kindOf(act.In)
	b.cool[k] = b.lastNow.Add(d.think(4, 10))
	b.dirty = true
}
