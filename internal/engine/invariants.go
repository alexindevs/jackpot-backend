package engine

import (
	"fmt"
	"slices"
)

// CheckInvariants returns an error describing the first broken invariant, or
// nil. It is for tests and debugging; the engine never needs it to run.
func (s *State) CheckInvariants() error {
	// Cards are conserved: for each type, hands plus pool equal the deck.
	for t := 0; t < NumTypes; t++ {
		sum := s.pool[t]
		for _, p := range s.players {
			sum += p.hand[t]
		}
		if sum != s.cfg.CopiesPerType {
			return fmt.Errorf("type %v: %d cards in play, want %d", CardType(t), sum, s.cfg.CopiesPerType)
		}
		if s.pool[t] < 0 {
			return fmt.Errorf("type %v: negative pool %d", CardType(t), s.pool[t])
		}
	}
	for _, p := range s.players {
		for t, c := range p.hand {
			if c < 0 {
				return fmt.Errorf("player %s has %d of type %v", p.id, c, CardType(t))
			}
		}
		if p.points < 0 || p.points > s.cfg.SuspicionPoints {
			return fmt.Errorf("player %s has %d points", p.id, p.points)
		}
		if !p.active && p.hand.Total() != 0 {
			return fmt.Errorf("removed player %s holds cards", p.id)
		}
	}
	if len(s.seats) != 2*len(s.teams) {
		return fmt.Errorf("%d seats for %d teams", len(s.seats), len(s.teams))
	}
	for _, id := range s.seats {
		if !s.players[id].active {
			return fmt.Errorf("removed player %s still seated", id)
		}
	}
	if s.phase == PhasePlaying {
		if !slices.Contains(s.seats, s.holder) {
			return fmt.Errorf("holder %q is not seated", s.holder)
		}
		for _, id := range s.seats {
			want := 4
			if id == s.holder {
				want = 5
			}
			if got := s.players[id].hand.Total(); got != want {
				return fmt.Errorf("player %s holds %d cards, want %d", id, got, want)
			}
		}
	}
	for id := range s.pendingReveals {
		if !s.players[id].active {
			return fmt.Errorf("pending reveal for removed player %s", id)
		}
	}
	if s.forcedPass != "" && s.forcedPass != s.holder {
		return fmt.Errorf("forced pass for %s but holder is %s", s.forcedPass, s.holder)
	}
	if s.phase == PhaseOver && s.result == nil {
		return fmt.Errorf("game over without a result")
	}
	return nil
}
