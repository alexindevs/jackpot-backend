package bot

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

const human = engine.PlayerID("dev:human")

type simResult struct {
	over          bool
	rounds        int
	falseCalls    int
	suspicions    int
	caught        int
	signals       int
	steps         int
	elapsed       time.Duration
	countsForStat bool
	winner        engine.TeamID
	reason        engine.EndReason
}

// playGame plays a whole game with a fake clock. One seat is a "person" who is
// played by the same brain as the bots (the driver's Also), so every seat acts.
func playGame(t *testing.T, seed uint64, botTeams int, short bool, afk bool) simResult {
	return playGameWith(t, seed, botTeams, short, afk, nil)
}

// playGameWith can also run a hook every few steps, to disturb the game.
func playGameWith(t *testing.T, seed uint64, botTeams int, short bool, afk bool, disturb func(step int, now time.Time, pump *room.Pump) time.Time) simResult {
	t.Helper()
	cfg := room.DefaultConfig()
	if short {
		cfg.Engine.WinsToFinish = 4
	}
	var opts []room.Option
	r, outs := room.New(cfg, fmt.Sprintf("sim%d", seed), human, rand.New(rand.NewPCG(seed, seed+1)), t0, opts...)
	drv := NewDriver(seed, Config{Speed: 1})
	if !afk {
		drv.Also(human)
	}
	var res simResult
	pump := &room.Pump{Room: r, Bots: drv, Sink: func(outs []room.Out) {
		for _, o := range outs {
			if o.To != human {
				continue
			}
			switch m := o.Msg.(type) {
			case room.GameEvent:
				switch m.Event.(type) {
				case engine.RoundWon:
					res.rounds++
				case engine.FalseProclaim:
					res.falseCalls++
				case engine.SuspicionRaised:
					res.suspicions++
				case engine.SuspicionFailed:
					res.caught++
				}
			case room.Signal:
				res.signals++
			}
		}
	}}
	now := t0
	drv.Observe(now, outs)
	if err := pump.Handle(now, human, room.AddBots{Teams: botTeams}); err != nil {
		t.Fatalf("seed %d: add bots: %v", seed, err)
	}
	if r.Phase() == room.PhaseLobby { // a full room starts by itself
		if err := pump.Handle(now, human, room.StartNow{}); err != nil {
			t.Fatalf("seed %d: start: %v", seed, err)
		}
	}
	for res.steps = 0; res.steps < 200000; res.steps++ {
		at, ok := pump.Next()
		if !ok {
			t.Fatalf("seed %d: nothing is scheduled any more, in phase %v after %v", seed, r.Phase(), now.Sub(t0))
		}
		if at.After(now) {
			now = at
		}
		pump.Advance(now)
		if disturb != nil {
			now = disturb(res.steps, now, pump)
		}
		if g := r.Game(); g != nil {
			if err := g.CheckInvariants(); err != nil {
				t.Fatalf("seed %d: step %d: %v", seed, res.steps, err)
			}
		}
		if r.Phase() == room.PhaseOver || r.Phase() == room.PhaseClosed {
			break
		}
		if now.Sub(t0) > 48*time.Hour {
			t.Fatalf("seed %d: still going after two days of game time (phase %v, round %d)", seed, r.Phase(), r.Game().Round())
		}
	}
	res.elapsed = now.Sub(t0)
	if g := r.Game(); g != nil && g.Result() != nil {
		res.over = true
		res.countsForStat = g.Result().CountsForStats
		res.winner, res.reason = g.Result().Winner, g.Result().Reason
	}
	return res
}

func TestBotsPlayWholeGamesToTheEnd(t *testing.T) {
	games := 30
	if testing.Short() {
		games = 6
	}
	var rounds, falseCalls, suspicions, caught, signals int
	var elapsed time.Duration
	for seed := uint64(1); seed <= uint64(games); seed++ {
		botTeams := 1 + int(seed%4) // 2 to 5 teams in all with the person's
		res := playGame(t, seed, botTeams, true, false)
		if !res.over {
			t.Fatalf("seed %d: the game did not finish: %+v", seed, res)
		}
		if res.countsForStat {
			t.Errorf("seed %d: a game with bots must not count toward leaderboards", seed)
		}
		rounds += res.rounds
		falseCalls += res.falseCalls
		suspicions += res.suspicions
		caught += res.caught
		signals += res.signals
		elapsed += res.elapsed
	}
	t.Logf("%d games: %.1f rounds each, %.1f false calls, %.1f suspicions (%.1f caught someone), %.0f signals seen by the person, %v of game time each",
		games, float64(rounds)/float64(games), float64(falseCalls)/float64(games), float64(suspicions)/float64(games),
		float64(caught)/float64(games), float64(signals)/float64(games), (elapsed / time.Duration(games)).Round(time.Second))
}

// A person who never does anything still has a seat: the server passes for
// them, and the bots carry on around them.
func TestBotsFinishAGameWithAPersonWhoNeverActs(t *testing.T) {
	for seed := uint64(1); seed <= 6; seed++ {
		res := playGame(t, seed, 1+int(seed%3), true, true)
		if !res.over {
			t.Fatalf("seed %d: the game did not finish: %+v", seed, res)
		}
	}
}

// The person drops off in the middle of the game and comes back after the game
// has paused; it must carry on to the end.
func TestBotsCarryOnAfterThePersonReturnsFromAPause(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		done := false
		res := playGameWith(t, seed, 2, true, false, func(step int, now time.Time, pump *room.Pump) time.Time {
			if done || step < 40 || pump.Room.Phase() != room.PhasePlaying {
				return now
			}
			done = true
			_ = pump.Handle(now, human, room.Disconnect{})
			now = now.Add(2 * time.Minute) // long enough for the game to pause
			pump.Advance(now)
			if _, paused := pump.Room.Paused(); !paused {
				t.Fatalf("seed %d: the game should have paused", seed)
			}
			now = now.Add(time.Minute)
			if err := pump.Handle(now, human, room.Connect{}); err != nil {
				t.Fatalf("seed %d: reconnect: %v", seed, err)
			}
			return now
		})
		if !res.over || !done {
			t.Fatalf("seed %d: paused and resumed (%v) but the game did not finish: %+v", seed, done, res)
		}
	}
}
