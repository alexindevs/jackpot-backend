package engine

import (
	"math/rand/v2"
	"testing"
)

// TestRandomGames plays thousands of games with arbitrary (often illegal)
// commands. After every command the invariants must hold, and a command that
// returns an error must leave the state exactly as it was.
func TestRandomGames(t *testing.T) {
	games := 600
	if testing.Short() {
		games = 100
	}
	var rounds, wins, resets, gameOvers int
	for seed := uint64(0); seed < uint64(games); seed++ {
		teams := 2 + int(seed%4)
		cfg := DefaultConfig()
		if seed%3 == 0 {
			cfg.WinsToFinish = 2
		}
		s, err := New(cfg, testSpecs(teams), testRNG(seed))
		if err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewPCG(seed, 99))
		for step := 0; step < 500 && s.Phase() != PhaseOver; step++ {
			maybeForceFourOfAKind(r, s)
			cmd := randomCommand(r, s, teams)
			before := snapshot(s)
			evs, err := s.Apply(cmd)
			if err != nil && snapshot(s) != before {
				t.Fatalf("seed %d step %d: %T failed (%v) but changed state", seed, step, cmd, err)
			}
			if err := s.CheckInvariants(); err != nil {
				t.Fatalf("seed %d step %d after %T: %v", seed, step, cmd, err)
			}
			for _, e := range evs {
				switch e.(type) {
				case RoundStarted:
					rounds++
				case RoundWon:
					wins++
				case PlayerReset:
					resets++
				case GameOver:
					gameOvers++
				}
			}
		}
	}
	t.Logf("%d games: %d rounds, %d round wins, %d resets, %d games over", games, rounds, wins, resets, gameOvers)
	if wins == 0 || resets == 0 || gameOvers == 0 {
		t.Error("the fuzzer never reached some of the interesting paths")
	}
}

func maybeForceFourOfAKind(r *rand.Rand, s *State) {
	if s.Phase() != PhasePlaying || r.IntN(15) != 0 {
		return
	}
	id := s.Seats()[r.IntN(len(s.Seats()))]
	if id == s.Holder() {
		return
	}
	var h Hand
	h[r.IntN(NumTypes)] = 4
	trySetHand(s, id, h)
}

func randomCommand(r *rand.Rand, s *State, teams int) Command {
	pick := func() PlayerID {
		if r.IntN(25) == 0 {
			return "ghost"
		}
		return s.Seatless(r)
	}
	if s.Phase() == PhaseWaiting && r.IntN(10) < 8 {
		return StartRound{}
	}
	switch n := r.IntN(100); {
	case n < 35:
		p := s.Holder()
		if p == "" || r.IntN(4) == 0 {
			p = pick()
		}
		return Pass{Player: p, Card: CardType(r.IntN(NumTypes + 1))}
	case n < 40:
		return AutoPass{Player: pick()}
	case n < 60:
		return Suspect{By: pick(), Target: pick()}
	case n < 66:
		var types []CardType
		for _, t := range r.Perm(NumTypes)[:1+r.IntN(3)] {
			types = append(types, CardType(t))
		}
		return Reveal{Player: pick(), Types: types}
	case n < 72:
		return AutoReveal{Player: pick()}
	case n < 85:
		return Proclaim{By: pick()}
	case n < 87:
		return RemoveTeam{Team: TeamID(r.IntN(teams + 1))}
	case n < 92:
		return VoteEnd{Player: pick(), Agree: r.IntN(3) != 0}
	}
	return StartRound{}
}

// Seatless picks any player ever in the game, removed or not.
func (s *State) Seatless(r *rand.Rand) PlayerID {
	ids := make([]PlayerID, 0, len(s.players))
	for id := range s.players {
		ids = append(ids, id)
	}
	// Map order is random, but sort for reproducibility.
	sortIDs(ids)
	return ids[r.IntN(len(ids))]
}

func sortIDs(ids []PlayerID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
