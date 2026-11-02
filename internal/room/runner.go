package room

import (
	"context"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

type request struct {
	from    engine.PlayerID
	in      Input
	inspect func(*Room) // runs inside the loop instead of handling an input
	reply   chan error
}

// Runner drives a Room from one goroutine: inputs arrive on a channel (so
// they are handled strictly in arrival order), and a timer wakes it for the
// room's next deadline. Everything the room produces goes to sink, which must
// not block for long.
type Runner struct {
	room *Room
	now  func() time.Time
	sink func([]Out)
	bots BotDriver
	in   chan request
	done chan struct{}
}

func NewRunner(room *Room, now func() time.Time, sink func([]Out)) *Runner {
	return &Runner{room: room, now: now, sink: sink, in: make(chan request), done: make(chan struct{})}
}

// SetBots gives the runner the driver that plays the room's bots. It must be
// called before Run.
func (rn *Runner) SetBots(d BotDriver) { rn.bots = d }

// Submit hands an input to the room and waits for its verdict. After the
// runner has stopped it returns ErrClosed.
func (rn *Runner) Submit(from engine.PlayerID, in Input) error {
	req := request{from: from, in: in, reply: make(chan error, 1)}
	select {
	case rn.in <- req:
		return <-req.reply
	case <-rn.done:
		return ErrClosed
	}
}

// Inspect runs fn inside the room's loop, so it can read the room without
// racing. fn must not block or keep the *Room.
func (rn *Runner) Inspect(fn func(*Room)) error {
	req := request{inspect: fn, reply: make(chan error, 1)}
	select {
	case rn.in <- req:
		return <-req.reply
	case <-rn.done:
		return ErrClosed
	}
}

// Run blocks until ctx is cancelled or the room closes.
func (rn *Runner) Run(ctx context.Context) {
	defer close(rn.done)
	pump := &Pump{Room: rn.room, Bots: rn.bots, Sink: rn.sink}
	for {
		var wake <-chan time.Time
		var timer *time.Timer
		if at, ok := pump.Next(); ok {
			d := max(at.Sub(rn.now()), 0)
			timer = time.NewTimer(d)
			wake = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case req := <-rn.in:
			if req.inspect != nil {
				req.inspect(rn.room)
				req.reply <- nil
				break
			}
			req.reply <- pump.Handle(rn.now(), req.from, req.in)
		case <-wake:
			pump.Advance(rn.now())
		}
		if timer != nil {
			timer.Stop()
		}
		if rn.room.Phase() == PhaseClosed {
			return
		}
	}
}
