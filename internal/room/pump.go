package room

import (
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Pump runs a Room together with its bots' driver, with no clock and no
// goroutines of its own: you tell it what time it is. The Runner uses it with
// real time and tests use it with a fake one, so a whole game with bots can be
// played in a blink.
type Pump struct {
	Room *Room
	Bots BotDriver // nil when the room has no bots
	Sink func([]Out)
}

func (p *Pump) deliver(now time.Time, outs []Out) {
	if p.Bots != nil {
		p.Bots.Observe(now, outs)
	}
	if p.Sink != nil {
		p.Sink(outs)
	}
}

// Handle gives the room an input from a person, and passes on what comes out.
func (p *Pump) Handle(now time.Time, from engine.PlayerID, in Input) error {
	outs, err := p.Room.Handle(now, from, in)
	p.deliver(now, outs)
	return err
}

// Advance does everything that is due at now: the room's deadlines, then what
// the bots want to do, again and again until nothing more is due.
func (p *Pump) Advance(now time.Time) {
	for i := 0; i < 10000 && p.Room.Phase() != PhaseClosed; i++ {
		p.deliver(now, p.Room.Tick(now))
		if p.Bots == nil {
			return
		}
		acts := p.Bots.Act(now, p.Room.ViewFor)
		if len(acts) == 0 {
			return
		}
		for _, a := range acts {
			outs, err := p.Room.Handle(now, a.Bot, a.In)
			p.deliver(now, outs)
			p.Bots.Result(a, err)
		}
	}
}

// Next is when the pump next has something to do.
func (p *Pump) Next() (time.Time, bool) {
	at, ok := p.Room.NextDeadline()
	if p.Bots != nil {
		if bt, bok := p.Bots.Wake(); bok && (!ok || bt.Before(at)) {
			at, ok = bt, true
		}
	}
	return at, ok
}
