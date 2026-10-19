package engine

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func testSpecs(teams int) []PlayerSpec {
	var out []PlayerSpec
	for t := 0; t < teams; t++ {
		out = append(out,
			PlayerSpec{ID: PlayerID(fmt.Sprintf("t%da", t)), Team: TeamID(t)},
			PlayerSpec{ID: PlayerID(fmt.Sprintf("t%db", t)), Team: TeamID(t)})
	}
	return out
}

func testRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

func newGame(t *testing.T, teams int, seed uint64) *State {
	t.Helper()
	s, err := New(DefaultConfig(), testSpecs(teams), testRNG(seed))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func started(t *testing.T, teams int, seed uint64) *State {
	t.Helper()
	s := newGame(t, teams, seed)
	mustApply(t, s, StartRound{})
	return s
}

func mustApply(t *testing.T, s *State, c Command) []Event {
	t.Helper()
	evs, err := s.Apply(c)
	if err != nil {
		t.Fatalf("%T: %v", c, err)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("after %T: %v", c, err)
	}
	return evs
}

func mustFail(t *testing.T, s *State, c Command, want error) {
	t.Helper()
	before := snapshot(s)
	_, err := s.Apply(c)
	if err != want {
		t.Fatalf("%T: error = %v, want %v", c, err, want)
	}
	if after := snapshot(s); after != before {
		t.Fatalf("%T failed but changed state:\nbefore %s\nafter  %s", c, before, after)
	}
}

// setHand gives a player exactly h, taking cards from and returning cards to
// the pool, so card conservation still holds.
func setHand(t *testing.T, s *State, id PlayerID, h Hand) {
	t.Helper()
	if !trySetHand(s, id, h) {
		t.Fatalf("pool cannot supply %v", h)
	}
}

func trySetHand(s *State, id PlayerID, h Hand) bool {
	p := s.players[id]
	pool := s.pool
	for i := range h {
		pool[i] += p.hand[i]
		if pool[i] < h[i] {
			return false
		}
	}
	for i := range h {
		pool[i] -= h[i]
	}
	s.pool, p.hand = pool, h
	return true
}

// roles names the players of a started two-team game.
type roles struct{ holder, holderPartner, o1, o2 PlayerID }

func rolesOf(s *State) roles {
	h := s.Holder()
	hp := s.partnerOf(h)
	var others []PlayerID
	for _, id := range s.seats {
		if id != h && id != hp {
			others = append(others, id)
		}
	}
	return roles{holder: h, holderPartner: hp, o1: others[0], o2: others[1]}
}

func find[T Event](evs []Event) (T, bool) {
	for _, e := range evs {
		if v, ok := e.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// snapshot renders everything that Apply may change, for before/after checks.
func snapshot(s *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "phase=%d round=%d holder=%q forced=%q reshuffle=%v pool=%v seats=%v teams=%v wins=%v ",
		s.phase, s.round, s.holder, s.forcedPass, s.reshuffle, s.pool, s.seats, s.teams, sortedWins(s))
	ids := make([]PlayerID, 0, len(s.players))
	for id := range s.players {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		p := s.players[id]
		fmt.Fprintf(&b, "[%s %v %v pts=%d cp=%v]", id, p.active, p.hand, p.points, p.canProclaim)
	}
	var pr []string
	for id, r := range s.pendingReveals {
		pr = append(pr, fmt.Sprintf("%s:%d:%v", id, r.required, r.options))
	}
	slices.Sort(pr)
	var vs []string
	for id := range s.votes {
		vs = append(vs, string(id))
	}
	slices.Sort(vs)
	fmt.Fprintf(&b, " pending=%v votes=%v over=%v", pr, vs, s.result != nil)
	return b.String()
}

func sortedWins(s *State) []string {
	var out []string
	for t, w := range s.wins {
		out = append(out, fmt.Sprintf("%d=%d", t, w))
	}
	slices.Sort(out)
	return out
}
