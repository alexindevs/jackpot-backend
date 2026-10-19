package engine

import (
	"math/rand/v2"
	"reflect"
	"testing"
)

// TestReplayRebuildsTheSameGame plays random games from a Setup, keeping the
// commands that were applied (stored and read back, as the database would),
// then replays them and expects an identical game, random generator included.
// This is what live-game saving rests on.
func TestReplayRebuildsTheSameGame(t *testing.T) {
	games := 400
	if testing.Short() {
		games = 60
	}
	var wins, resets, overs, applied, failed int
	for seed := uint64(0); seed < uint64(games); seed++ {
		teams := 2 + int(seed%4)
		cfg := DefaultConfig()
		if seed%3 == 0 {
			cfg.WinsToFinish = 2
		}
		su := Setup{Config: cfg, Specs: testSpecs(teams), SeedA: seed, SeedB: seed*7 + 1}
		s, err := NewFromSetup(su)
		if err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewPCG(seed, 5))
		var stored [][]byte
		for step := 0; step < 600 && s.Phase() != PhaseOver; step++ {
			cmd := randomCommand(r, s, teams)
			if r.IntN(3) == 0 {
				cmd = helpfulCommand(r, s, cmd)
			}
			evs, err := s.Apply(cmd)
			if err != nil {
				failed++
				continue
			}
			applied++
			b, err := MarshalCommand(cmd)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			stored = append(stored, b)
			for _, e := range evs {
				switch e.(type) {
				case RoundWon:
					wins++
				case PlayerReset:
					resets++
				case GameOver:
					overs++
				}
			}
		}

		cmds := make([]Command, len(stored))
		for i, b := range stored {
			if cmds[i], err = UnmarshalCommand(b); err != nil {
				t.Fatalf("seed %d: command %d: %v", seed, i, err)
			}
		}
		got, err := Replay(su, cmds)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !reflect.DeepEqual(s, got) {
			t.Fatalf("seed %d: the replayed game differs from the original (%d commands)", seed, len(cmds))
		}
		// And it carries on identically: the next random draw agrees.
		if s.rng.Uint64() != got.rng.Uint64() {
			t.Fatalf("seed %d: random generators have diverged", seed)
		}
	}
	t.Logf("%d games, %d commands applied, %d refused, %d round wins, %d resets, %d games over", games, applied, failed, wins, resets, overs)
	if wins == 0 || resets == 0 {
		t.Error("the replay test never reached round wins and resets")
	}
}

// helpfulCommand nudges random play toward interesting states: the holder
// passes the card that builds the next player's best hand, and proclaims come
// from partners of players who hold four of a kind.
func helpfulCommand(r *rand.Rand, s *State, fallback Command) Command {
	if s.Phase() != PhasePlaying {
		return fallback
	}
	if r.IntN(2) == 0 {
		for _, id := range s.Seats() {
			h := s.Hand(id)
			for _, n := range h {
				if n >= 4 {
					return Proclaim{By: s.partnerOf(id)}
				}
			}
		}
	}
	h := s.Holder()
	if h == "" {
		return fallback
	}
	// Pass a card of the type the next seat holds most of.
	next := s.nextAfter(h, 0, false)
	best := 0
	for ty, n := range s.Hand(next) {
		if n > s.Hand(next)[best] {
			best = ty
		}
	}
	if s.Hand(h)[best] > 0 {
		return Pass{Player: h, Card: CardType(best)}
	}
	return fallback
}

func TestCommandsRoundTripThroughStorage(t *testing.T) {
	for _, c := range []Command{
		StartRound{}, Pass{Player: "a", Card: 0}, Pass{Player: "a", Card: 3}, AutoPass{Player: "a"},
		Suspect{By: "a", Target: "b"}, Reveal{Player: "a", Types: []CardType{0, 2}}, AutoReveal{Player: "a"},
		Proclaim{By: "a"}, RemoveTeam{Team: 0}, RemoveTeam{Team: 3}, VoteEnd{Player: "a", Agree: false}, VoteEnd{Player: "a", Agree: true},
	} {
		b, err := MarshalCommand(c)
		if err != nil {
			t.Fatalf("%#v: %v", c, err)
		}
		got, err := UnmarshalCommand(b)
		if err != nil || !reflect.DeepEqual(got, c) {
			t.Errorf("%#v -> %s -> %#v (%v)", c, b, got, err)
		}
	}
	for _, bad := range []string{`{}`, `{"k":"nope"}`, `{"k":"pass","p":"a"}`, `{"k":"remove_team"}`, `{"k":"vote_end","p":"a"}`, `not json`} {
		if c, err := UnmarshalCommand([]byte(bad)); err == nil {
			t.Errorf("%s should not parse, got %#v", bad, c)
		}
	}
}

func TestReplayRefusesALogThatDoesNotFit(t *testing.T) {
	su := Setup{Config: DefaultConfig(), Specs: testSpecs(2), SeedA: 1, SeedB: 2}
	if _, err := Replay(su, []Command{Pass{Player: "t0a", Card: 0}}); err == nil {
		t.Error("passing before a round starts cannot be replayed")
	}
	if _, err := Replay(Setup{}, nil); err == nil {
		t.Error("an empty setup is invalid")
	}
}

func TestStateReportsItsSetup(t *testing.T) {
	su := Setup{Config: DefaultConfig(), Specs: testSpecs(2), SeedA: 1, SeedB: 2}
	s, _ := NewFromSetup(su)
	got, ok := s.Setup()
	if !ok || !reflect.DeepEqual(got, su) {
		t.Errorf("setup = %+v, %v", got, ok)
	}
	if _, ok := newGame(t, 2, 1).Setup(); ok {
		t.Error("a game built without a setup has none to report")
	}
	if !s.Active("t0a") || s.Active("nobody") {
		t.Error("Active")
	}
}
