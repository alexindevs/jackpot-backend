package room

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// TestRandomRooms throws arbitrary (mostly illegal) inputs and time jumps at
// rooms. After every step the room's bookkeeping must agree with the engine
// and the timer table, and a rejected input must send nothing.
func TestRandomRooms(t *testing.T) {
	rooms := 1500
	if testing.Short() {
		rooms = 150
	}
	reached := map[Phase]int{}
	var rounds, removals int
	for seed := uint64(0); seed < uint64(rooms); seed++ {
		r, _ := New(DefaultConfig(), "fuzz", pid(0), rand.New(rand.NewPCG(seed, 7)), t0)
		d := rand.New(rand.NewPCG(seed, 11))
		now := t0
		ids := []engine.PlayerID{"ghost"}
		for i := 0; i < 12; i++ {
			ids = append(ids, pid(i))
		}
		pick := func() engine.PlayerID { return ids[d.IntN(len(ids))] }

		overSteps := 0
		for step := 0; step < 700 && r.Phase() != PhaseClosed && overSteps < 25; step++ {
			if r.Phase() == PhaseOver {
				overSteps++ // a little post-game activity, then move on to the next room
			}
			var outs []Out
			if d.IntN(100) < 22 {
				now = now.Add([]time.Duration{0, time.Second, 10 * time.Second, 31 * time.Second, 61 * time.Second, 91 * time.Second, 121 * time.Second, 10 * time.Minute}[d.IntN(8)])
				outs = r.Tick(now)
			} else {
				in := randomInput(d, r, pick)
				var err error
				outs, err = r.Handle(now, pick2(d, r, pick), in)
				if err != nil && len(outs) != 0 {
					t.Fatalf("seed %d step %d: rejected %T still sent %d messages", seed, step, in, len(outs))
				}
			}
			if err := r.checkInvariants(); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
			for _, o := range outs {
				if ge, ok := o.Msg.(GameEvent); ok {
					switch ge.Event.(type) {
					case engine.RoundStarted:
						rounds++
					case engine.TeamRemoved:
						removals++
					}
				}
			}
			reached[r.Phase()]++
		}
	}
	t.Logf("%d rooms: phase-steps %v, %d rounds started, %d team removals", rooms, reached, rounds, removals)
	if reached[PhasePlaying] == 0 || reached[PhaseOver] == 0 || reached[PhaseClosed] == 0 || removals == 0 {
		t.Error("the fuzzer never reached some phases")
	}
}

// pick2 prefers the player who can actually act, so rounds make progress.
func pick2(d *rand.Rand, r *Room, pick func() engine.PlayerID) engine.PlayerID {
	if r.game != nil && r.phase == PhasePlaying && d.IntN(3) == 0 {
		if h := r.game.Holder(); h != "" {
			return h
		}
	}
	return pick()
}

func randomInput(d *rand.Rand, r *Room, pick func() engine.PlayerID) Input {
	if r.phase == PhaseLobby && d.IntN(3) == 0 {
		return Join{}
	}
	switch d.IntN(27) {
	case 25, 26:
		return Chat{Text: "hello\nthere"}
	case 24:
		return StartNow{}
	case 0:
		return Join{}
	case 1:
		return Spectate{}
	case 2:
		if d.IntN(4) == 0 {
			return Leave{}
		}
		return Connect{}
	case 3, 4:
		return Disconnect{}
	case 5:
		return Connect{}
	case 6:
		return InviteToRoom{To: pick()}
	case 7, 8:
		return InvitePartner{To: pick()}
	case 9, 10:
		return AcceptPartner{From: pick()}
	case 11:
		return DeclinePartner{From: pick()}
	case 12, 13:
		return Ready{Ready: d.IntN(4) != 0}
	case 14:
		return TeamChat{Text: "hi"}
	case 15, 16, 17:
		return Pass{Card: engine.CardType(d.IntN(engine.NumTypes + 1))}
	case 18, 19:
		return Suspect{Target: pick()}
	case 20:
		var types []engine.CardType
		for _, ty := range d.Perm(engine.NumTypes)[:1+d.IntN(3)] {
			types = append(types, engine.CardType(ty))
		}
		return Reveal{Types: types}
	case 21, 22:
		return Proclaim{}
	}
	return VoteEnd{Agree: d.IntN(3) != 0}
}
