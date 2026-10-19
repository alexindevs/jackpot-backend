package engine

import (
	"slices"
	"testing"
)

func TestNewValidation(t *testing.T) {
	good := testSpecs(2)
	cases := map[string]struct {
		cfg   Config
		specs []PlayerSpec
	}{
		"too few players":    {DefaultConfig(), good[:2]},
		"odd players":        {DefaultConfig(), good[:3]},
		"too many players":   {DefaultConfig(), testSpecs(6)},
		"no spare copies":    {Config{SpareCopies: 0, SuspicionPoints: 3, WinsToFinish: 7}, good},
		"duplicate id":       {DefaultConfig(), []PlayerSpec{{"x", 0}, {"x", 0}, {"y", 1}, {"z", 1}}},
		"unbalanced teams":   {DefaultConfig(), []PlayerSpec{{"a", 0}, {"b", 0}, {"c", 0}, {"d", 1}}},
		"too many win steps": {Config{SpareCopies: 7, SuspicionPoints: 3, WinsToFinish: 8}, good},
	}
	for name, c := range cases {
		if _, err := New(c.cfg, c.specs, testRNG(1)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	for teams := 2; teams <= 5; teams++ {
		if _, err := New(DefaultConfig(), testSpecs(teams), testRNG(1)); err != nil {
			t.Errorf("%d teams: %v", teams, err)
		}
	}
}

func TestStartRoundDeals(t *testing.T) {
	for teams := 2; teams <= 5; teams++ {
		s := started(t, teams, uint64(teams))
		holders := 0
		for _, id := range s.Seats() {
			h := s.Hand(id)
			if id == s.Holder() {
				holders++
				if h.Total() != 5 {
					t.Errorf("holder has %d cards", h.Total())
				}
			} else if h.Total() != 4 || h.Distinct() != 4 {
				t.Errorf("non-holder hand %v, want one of each", h)
			}
			if s.Points(id) != 3 {
				t.Errorf("points = %d, want 3", s.Points(id))
			}
		}
		if holders != 1 {
			t.Errorf("%d holders", holders)
		}
	}
}

func TestStartRoundOnlyBetweenRounds(t *testing.T) {
	s := started(t, 2, 1)
	mustFail(t, s, StartRound{}, ErrWrongPhase)
}

func TestPassMovesCardToNextSeat(t *testing.T) {
	s := started(t, 3, 2)
	from := s.Holder()
	i := slices.Index(s.Seats(), from)
	to := s.Seats()[(i+1)%6]
	card := s.Hand(from).Types()[0]
	before := s.Hand(to)
	evs := mustApply(t, s, Pass{Player: from, Card: card})
	if s.Holder() != to {
		t.Fatalf("holder = %s, want %s", s.Holder(), to)
	}
	if got := s.Hand(to)[card]; got != before[card]+1 {
		t.Errorf("receiver has %d of %v, want %d", got, card, before[card]+1)
	}
	if p, ok := find[Passed](evs); !ok || p.From != from || p.To != to || p.Card != card || p.Auto {
		t.Errorf("bad Passed event %+v", p)
	}
}

func TestPassErrors(t *testing.T) {
	s := started(t, 2, 3)
	r := rolesOf(s)
	mustFail(t, s, Pass{Player: r.o1, Card: Circle}, ErrNotHolder)
	mustFail(t, s, Pass{Player: "ghost", Card: Circle}, ErrUnknownPlayer)
	mustFail(t, s, Pass{Player: r.holder, Card: 9}, ErrBadCard)
	setHand(t, s, r.holder, Hand{4, 1, 0, 0})
	mustFail(t, s, Pass{Player: r.holder, Card: Cross}, ErrNoSuchCard)
}

func TestAutoPassSendsOddOneOut(t *testing.T) {
	s := started(t, 2, 4)
	r := rolesOf(s)
	setHand(t, s, r.holder, Hand{4, 0, 1, 0})
	evs := mustApply(t, s, AutoPass{Player: r.holder})
	p, _ := find[Passed](evs)
	if p.Card != Cross || !p.Auto {
		t.Errorf("auto pass sent %v (auto=%v), want the odd one out (cross)", p.Card, p.Auto)
	}
	mustFail(t, s, AutoPass{Player: r.holder}, ErrNotHolder)
}

func TestSuspicionOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		holder   bool
		hand     Hand
		freed    bool
		required int
	}{
		{"non-holder four of a kind", false, Hand{4, 0, 0, 0}, false, 2},
		{"non-holder 3+1", false, Hand{3, 1, 0, 0}, true, 2},
		{"non-holder 2+2", false, Hand{2, 2, 0, 0}, true, 2},
		{"holder 4+1", true, Hand{4, 1, 0, 0}, false, 3},
		{"holder five of a kind", true, Hand{5, 0, 0, 0}, false, 3},
		{"holder 3+2 only has two types", true, Hand{3, 2, 0, 0}, false, 3},
		{"holder 3+1+1", true, Hand{3, 1, 1, 0}, true, 3},
		{"holder 2+1+1+1", true, Hand{2, 1, 1, 1}, true, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := started(t, 2, 5)
			r := rolesOf(s)
			target, by := r.holderPartner, r.o1
			if c.holder {
				target = r.holder
			}
			setHand(t, s, target, c.hand)
			evs := mustApply(t, s, Suspect{By: by, Target: target})

			if s.Points(by) != 2 {
				t.Errorf("suspector has %d points, want 2", s.Points(by))
			}
			raised, _ := find[SuspicionRaised](evs)
			if raised.Required != c.required {
				t.Errorf("required = %d, want %d", raised.Required, c.required)
			}
			if c.freed {
				if _, ok := find[RevealRequested](evs); !ok {
					t.Fatal("expected RevealRequested")
				}
				if s.Hand(target) != c.hand {
					t.Error("freed player's hand changed")
				}
				return
			}
			failed, ok := find[SuspicionFailed](evs)
			if !ok || failed.Hand != c.hand {
				t.Fatalf("expected SuspicionFailed with the hand, got %+v", failed)
			}
			if c.holder {
				if _, ok := find[ForcedPassRequested](evs); !ok {
					t.Error("a caught holder must be asked to pass first")
				}
				if s.Hand(target) != c.hand {
					t.Error("holder must keep their cards until they pass")
				}
			} else if _, ok := find[PlayerReset](evs); !ok || s.Hand(target).Total() != 4 || s.Hand(target).Distinct() != 4 {
				t.Errorf("expected a fresh hand, got %v", s.Hand(target))
			}
		})
	}
}

// If the holder passed just before the suspicion, they hold 4 cards and show
// 2; if they hadn't yet, they hold 5 and must show 3.
func TestSuspicionUsesHandAtItsPlaceInTheLog(t *testing.T) {
	hand := Hand{3, 2, 0, 0}

	s := started(t, 2, 6)
	r := rolesOf(s)
	setHand(t, s, r.holder, hand)
	mustApply(t, s, Suspect{By: r.o1, Target: r.holder})
	if s.forcedPass != r.holder {
		t.Fatal("suspected before passing: holder should be caught")
	}

	s = started(t, 2, 6)
	r = rolesOf(s)
	setHand(t, s, r.holder, hand)
	mustApply(t, s, Pass{Player: r.holder, Card: Triangle})
	evs := mustApply(t, s, Suspect{By: r.o1, Target: r.holder})
	if rr, ok := find[RevealRequested](evs); !ok || rr.Required != 2 {
		t.Errorf("suspected after passing: want a 2-type reveal, got %+v", evs)
	}
}

func TestCaughtHolderPassesThenResets(t *testing.T) {
	s := started(t, 2, 7)
	r := rolesOf(s)
	setHand(t, s, r.holder, Hand{4, 1, 0, 0})
	mustApply(t, s, Suspect{By: r.o1, Target: r.holder})
	mustFail(t, s, Suspect{By: r.o2, Target: r.holder}, ErrBusy)

	next := s.nextAfter(r.holder, 0, false)
	nextBefore := s.Hand(next)
	evs := mustApply(t, s, Pass{Player: r.holder, Card: Triangle})
	if s.Holder() != next || s.Hand(next)[Triangle] != nextBefore[Triangle]+1 {
		t.Error("the card should have been passed on first")
	}
	if _, ok := find[PlayerReset](evs); !ok {
		t.Error("expected a reset after the pass")
	}
	if h := s.Hand(r.holder); h.Total() != 4 || h.Distinct() != 4 {
		t.Errorf("reset hand = %v", h)
	}
	if s.forcedPass != "" {
		t.Error("forced pass should be cleared")
	}
}

func TestCaughtHolderTimeoutPassesOddOneOut(t *testing.T) {
	s := started(t, 2, 8)
	r := rolesOf(s)
	setHand(t, s, r.holder, Hand{0, 4, 0, 1})
	mustApply(t, s, Suspect{By: r.o1, Target: r.holder})
	evs := mustApply(t, s, AutoPass{Player: r.holder})
	if p, _ := find[Passed](evs); p.Card != Square {
		t.Errorf("passed %v, want the odd one out (square)", p.Card)
	}
	if _, ok := find[PlayerReset](evs); !ok {
		t.Error("expected a reset")
	}
}

func TestRevealValidation(t *testing.T) {
	s := started(t, 2, 9)
	r := rolesOf(s)
	setHand(t, s, r.holderPartner, Hand{2, 1, 1, 0})
	mustFail(t, s, Reveal{Player: r.holderPartner, Types: []CardType{Circle, Cross}}, ErrNoPending)
	mustApply(t, s, Suspect{By: r.o1, Target: r.holderPartner})
	mustFail(t, s, Suspect{By: r.o2, Target: r.holderPartner}, ErrBusy)
	mustFail(t, s, Reveal{Player: r.holderPartner, Types: []CardType{Circle}}, ErrBadReveal)
	mustFail(t, s, Reveal{Player: r.holderPartner, Types: []CardType{Circle, Circle}}, ErrBadReveal)
	mustFail(t, s, Reveal{Player: r.holderPartner, Types: []CardType{Circle, Square}}, ErrBadReveal)
	evs := mustApply(t, s, Reveal{Player: r.holderPartner, Types: []CardType{Cross, Circle}})
	if f, _ := find[SuspicionFreed](evs); !slices.Equal(f.Revealed, []CardType{Circle, Cross}) {
		t.Errorf("revealed %v", f.Revealed)
	}
	// Answered: they can be suspected again.
	mustApply(t, s, Suspect{By: r.o2, Target: r.holderPartner})
}

func TestAutoRevealPicksValidTypes(t *testing.T) {
	for seed := uint64(0); seed < 50; seed++ {
		s := started(t, 2, seed)
		r := rolesOf(s)
		setHand(t, s, r.holder, Hand{2, 1, 1, 1})
		mustApply(t, s, Suspect{By: r.o1, Target: r.holder})
		evs := mustApply(t, s, AutoReveal{Player: r.holder})
		f, _ := find[SuspicionFreed](evs)
		if len(f.Revealed) != 3 || !slices.IsSorted(f.Revealed) {
			t.Fatalf("revealed %v", f.Revealed)
		}
	}
}

func TestSuspicionPoints(t *testing.T) {
	s := started(t, 3, 10)
	by := s.Seats()[0]
	var targets []PlayerID
	for _, id := range s.Seats() {
		if id != by {
			targets = append(targets, id)
		}
	}
	mustFail(t, s, Suspect{By: by, Target: by}, ErrSelfSuspect)
	mustFail(t, s, Suspect{By: by, Target: "ghost"}, ErrUnknownPlayer)
	for i := 0; i < 3; i++ {
		evs := mustApply(t, s, Suspect{By: by, Target: targets[i]})
		if len(evs) == 0 {
			t.Fatal("no events")
		}
		// Answer the reveal so the target isn't busy.
		if _, ok := s.pendingReveals[targets[i]]; ok {
			mustApply(t, s, AutoReveal{Player: targets[i]})
		}
	}
	mustFail(t, s, Suspect{By: by, Target: targets[3]}, ErrNoPoints)
}

func TestSuspectingYourPartnerIsAllowed(t *testing.T) {
	s := started(t, 2, 11)
	r := rolesOf(s)
	mustApply(t, s, Suspect{By: r.o1, Target: r.o2})
}

func TestProclaimWinsRound(t *testing.T) {
	s := started(t, 2, 12)
	r := rolesOf(s)
	setHand(t, s, r.o2, Hand{4, 0, 0, 0})
	evs := mustApply(t, s, Proclaim{By: r.o1})
	won, ok := find[RoundWon](evs)
	if !ok || won.Proclaimer != r.o1 || won.Holder != r.o2 || won.Wins != 1 || won.Letter != "J" {
		t.Fatalf("bad RoundWon %+v", won)
	}
	if s.Phase() != PhaseWaiting {
		t.Error("expected to be waiting for the next round")
	}
	mustFail(t, s, Proclaim{By: r.o1}, ErrWrongPhase)

	// Next round redeals and refreshes points.
	s.players[r.o1].points = 0
	mustApply(t, s, StartRound{})
	if s.Round() != 2 || s.Points(r.o1) != 3 {
		t.Errorf("round %d, points %d", s.Round(), s.Points(r.o1))
	}
}

func TestProclaimRejectedWhilePartnerHoldsPassingCard(t *testing.T) {
	s := started(t, 2, 13)
	r := rolesOf(s)
	setHand(t, s, r.holder, Hand{4, 1, 0, 0})
	mustFail(t, s, Proclaim{By: r.holderPartner}, ErrPartnerHoldsPassingCard)
	if s.Points(r.holderPartner) != 3 {
		t.Error("a rejected proclamation must cost nothing")
	}
	// Once the holder passes, the same proclamation is accepted.
	mustApply(t, s, Pass{Player: r.holder, Card: Triangle})
	mustApply(t, s, Proclaim{By: r.holderPartner})
	if s.Wins(s.players[r.holder].team) != 1 {
		t.Error("expected a round win")
	}
}

func TestFalseProclaimCostsAPointThenRevokes(t *testing.T) {
	s := started(t, 2, 14)
	r := rolesOf(s)
	evs := mustApply(t, s, Proclaim{By: r.o1})
	if f, _ := find[FalseProclaim](evs); f.PointsLeft != 2 || f.Revoked {
		t.Errorf("first false proclaim: %+v", f)
	}
	s.players[r.o1].points = 0
	evs = mustApply(t, s, Proclaim{By: r.o1})
	if f, _ := find[FalseProclaim](evs); f.PointsLeft != 0 || !f.Revoked {
		t.Errorf("false proclaim at 0 points: %+v", f)
	}
	mustFail(t, s, Proclaim{By: r.o1}, ErrProclaimRevoked)
	// The partner can still proclaim.
	setHand(t, s, r.o1, Hand{0, 4, 0, 0})
	mustApply(t, s, Proclaim{By: r.o2})
}

// Whichever event the queue puts first wins.
func TestSuspicionAndProclaimRace(t *testing.T) {
	setup := func() (*State, roles) {
		s := started(t, 2, 15)
		r := rolesOf(s)
		setHand(t, s, r.o2, Hand{4, 0, 0, 0})
		return s, r
	}

	s, r := setup()
	mustApply(t, s, Suspect{By: r.holder, Target: r.o2})
	evs := mustApply(t, s, Proclaim{By: r.o1})
	if _, ok := find[FalseProclaim](evs); !ok {
		t.Error("suspicion first: the late proclamation should be false")
	}

	s, r = setup()
	mustApply(t, s, Proclaim{By: r.o1})
	mustFail(t, s, Suspect{By: r.holder, Target: r.o2}, ErrWrongPhase)
}

func TestGameEndsAtWinsToFinish(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WinsToFinish = 2
	s, err := New(cfg, testSpecs(2), testRNG(16))
	if err != nil {
		t.Fatal(err)
	}
	var winner TeamID
	for round := 1; round <= 2; round++ {
		mustApply(t, s, StartRound{})
		r := rolesOf(s)
		winner = s.players[r.o1].team
		setHand(t, s, r.o2, Hand{0, 0, 4, 0})
		evs := mustApply(t, s, Proclaim{By: r.o1})
		won, _ := find[RoundWon](evs)
		if want := string(Letters[round-1]); won.Letter != want {
			t.Errorf("letter %q, want %q", won.Letter, want)
		}
		_, over := find[GameOver](evs)
		if over != (round == 2) {
			t.Errorf("round %d: game over = %v", round, over)
		}
	}
	res := s.Result()
	if res.Reason != EndWins || res.Draw || !res.CountsForStats || res.Winner != winner || res.Wins[winner] != 2 {
		t.Errorf("result %+v", res)
	}
	mustFail(t, s, StartRound{}, ErrGameOver)
}

func TestRemoveTeamMidRound(t *testing.T) {
	s := started(t, 3, 17)
	holderTeam := s.players[s.Holder()].team
	evs := mustApply(t, s, RemoveTeam{Team: holderTeam})
	if p, ok := find[Passed](evs); !ok || !p.Auto || s.players[p.To].team == holderTeam {
		t.Errorf("the passing card should go to someone staying: %+v", p)
	}
	if s.players[s.Holder()].team == holderTeam {
		t.Error("holder is still on the removed team")
	}
	if len(s.Seats()) != 4 || len(s.Teams()) != 2 {
		t.Errorf("seats %d, teams %d", len(s.Seats()), len(s.Teams()))
	}
	// Their cards went back to the pool: no type is missing from play.
	mustFail(t, s, RemoveTeam{Team: holderTeam}, ErrUnknownTeam)
}

func TestRemoveTeamLeavingOneTeamForfeits(t *testing.T) {
	s := started(t, 2, 18)
	evs := mustApply(t, s, RemoveTeam{Team: 0})
	over, ok := find[GameOver](evs)
	if !ok || over.Result.Reason != EndForfeit || over.Result.Winner != 1 || over.Result.CountsForStats {
		t.Errorf("result %+v", over.Result)
	}
	mustFail(t, s, Pass{Player: "t1a", Card: Circle}, ErrGameOver)
}

func TestRemoveTeamBetweenRounds(t *testing.T) {
	s := newGame(t, 3, 19)
	mustApply(t, s, RemoveTeam{Team: 1})
	mustApply(t, s, StartRound{})
	if len(s.Seats()) != 4 {
		t.Errorf("%d seats", len(s.Seats()))
	}
}

func TestReshuffleWhenRemovalSeatsPartnersTogether(t *testing.T) {
	s := newGame(t, 3, 20)
	s.seats = []PlayerID{"t0a", "t1a", "t0b", "t2a", "t1b", "t2b"}
	mustApply(t, s, RemoveTeam{Team: 1}) // leaves t0a t0b t2a t2b
	if !s.reshuffle {
		t.Fatal("partners are now adjacent: expected a reshuffle to be scheduled")
	}
	evs := mustApply(t, s, StartRound{})
	if _, ok := find[SeatsReshuffled](evs); !ok || partnersAdjacent(s.Seats(), s.teamOf) {
		t.Errorf("seats not reshuffled correctly: %v", s.Seats())
	}

	// No reshuffle if the ring is still fine.
	s = newGame(t, 3, 21)
	s.seats = []PlayerID{"t0a", "t1a", "t2a", "t0b", "t1b", "t2b"}
	mustApply(t, s, RemoveTeam{Team: 1}) // t0a t2a t0b t2b
	if s.reshuffle {
		t.Error("no partners adjacent: seating should stay")
	}
}

func TestSeatingStaysTheSameAcrossRounds(t *testing.T) {
	s := started(t, 4, 22)
	seats := s.Seats()
	r := rolesOf4(s)
	setHand(t, s, r.partner, Hand{4, 0, 0, 0})
	mustApply(t, s, Proclaim{By: r.proclaimer})
	mustApply(t, s, StartRound{})
	if !slices.Equal(seats, s.Seats()) {
		t.Errorf("seats changed between rounds: %v -> %v", seats, s.Seats())
	}
}

// rolesOf4 finds a non-holder and their partner in any game size.
type pair struct{ proclaimer, partner PlayerID }

func rolesOf4(s *State) pair {
	for _, id := range s.Seats() {
		partner := s.partnerOf(id)
		if id != s.Holder() && partner != s.Holder() {
			return pair{proclaimer: id, partner: partner}
		}
	}
	panic("no pair without the holder")
}

func TestVoteEnd(t *testing.T) {
	s := newGame(t, 3, 23)
	s.wins = map[TeamID]int{0: 2, 1: 1}
	for _, id := range []PlayerID{"t0a", "t0b"} {
		evs := mustApply(t, s, VoteEnd{Player: id, Agree: true})
		if _, over := find[GameOver](evs); over {
			t.Fatal("one team of three is not a majority")
		}
	}
	mustApply(t, s, VoteEnd{Player: "t1a", Agree: true})
	if s.Phase() == PhaseOver {
		t.Fatal("a team needs both partners")
	}
	mustApply(t, s, VoteEnd{Player: "t1a", Agree: false})
	mustApply(t, s, VoteEnd{Player: "t1a", Agree: true})
	evs := mustApply(t, s, VoteEnd{Player: "t1b", Agree: true})
	over, ok := find[GameOver](evs)
	if !ok || over.Result.Reason != EndAgreement || over.Result.Draw || over.Result.Winner != 0 {
		t.Errorf("result %+v", over.Result)
	}
}

func TestVoteEndTieIsADraw(t *testing.T) {
	s := newGame(t, 2, 24)
	s.wins = map[TeamID]int{0: 3, 1: 3}
	for _, id := range []PlayerID{"t0a", "t0b", "t1a"} {
		mustApply(t, s, VoteEnd{Player: id, Agree: true})
	}
	evs := mustApply(t, s, VoteEnd{Player: "t1b", Agree: true})
	if over, _ := find[GameOver](evs); !over.Result.Draw {
		t.Errorf("expected a draw, got %+v", over.Result)
	}
}

func TestVoteEndCanPassWhenARemovalShrinksTheTeams(t *testing.T) {
	s := newGame(t, 3, 25)
	for _, id := range []PlayerID{"t0a", "t0b"} {
		mustApply(t, s, VoteEnd{Player: id, Agree: true})
	}
	// Two teams left, one of which agrees: 1*2 <= 2, still not a majority.
	evs := mustApply(t, s, RemoveTeam{Team: 2})
	if _, over := find[GameOver](evs); over {
		t.Error("one of two teams is not a majority")
	}
}

func TestProclaimRightIsRestoredNextRound(t *testing.T) {
	s := started(t, 2, 26)
	r := rolesOf(s)
	s.players[r.o1].points = 0
	mustApply(t, s, Proclaim{By: r.o1}) // false, at 0 points: revoked
	mustFail(t, s, Proclaim{By: r.o1}, ErrProclaimRevoked)

	setHand(t, s, r.o1, Hand{0, 4, 0, 0})
	mustApply(t, s, Proclaim{By: r.o2}) // the partner wins the round
	mustApply(t, s, StartRound{})
	if !s.players[r.o1].canProclaim {
		t.Error("the right to proclaim should come back with the next round")
	}
}

func TestNewRejectsEmptyPlayerID(t *testing.T) {
	specs := testSpecs(2)
	specs[0].ID = ""
	if _, err := New(DefaultConfig(), specs, testRNG(1)); err == nil {
		t.Error("expected an error for an empty player id")
	}
}

// When the leaving team's partner sits right after the holder, the card must
// skip past them to someone who is staying.
func TestRemoveTeamPassesPastTheLeavingPartner(t *testing.T) {
	s := newGame(t, 3, 27)
	s.seats = []PlayerID{"t0a", "t0b", "t1a", "t1b", "t2a", "t2b"}
	mustApply(t, s, StartRound{})
	if h := s.Holder(); h != "t0a" {
		s.transfer(h, "t0a", s.Hand(h).Types()[0], false) // move the passing card to t0a
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	evs := mustApply(t, s, RemoveTeam{Team: 0})
	if p, _ := find[Passed](evs); p.To != "t1a" {
		t.Errorf("passing card went to %s, want t1a", p.To)
	}
}

func TestRemoveTeamClearsPromptsOwedByTheLeavers(t *testing.T) {
	// A caught holder who still owes a forced pass.
	s := started(t, 3, 28)
	h := s.Holder()
	setHand(t, s, h, Hand{4, 1, 0, 0})
	var by PlayerID
	for _, id := range s.Seats() {
		if s.players[id].team != s.players[h].team {
			by = id
			break
		}
	}
	mustApply(t, s, Suspect{By: by, Target: h})
	mustApply(t, s, RemoveTeam{Team: s.players[h].team})
	if s.forcedPass != "" {
		t.Error("forced pass should have been cleared")
	}

	// A player with a reveal still pending.
	s = started(t, 3, 29)
	target := s.partnerOf(s.Holder())
	mustApply(t, s, Suspect{By: s.Holder(), Target: target})
	mustApply(t, s, RemoveTeam{Team: s.players[target].team})
	if len(s.pendingReveals) != 0 {
		t.Error("pending reveal should have been cleared")
	}
}

func TestDeckSizeFollowsThePlayerCount(t *testing.T) {
	for teams := 2; teams <= 5; teams++ {
		s := newGame(t, teams, 30)
		if want := 2*teams + 7; s.copies != want {
			t.Errorf("%d teams: %d cards per type, want %d", teams, s.copies, want)
		}
	}
}

// A reset always deals exactly one of each type, even if the pool has to be
// topped up.
func TestResetAlwaysDealsOneOfEachType(t *testing.T) {
	s := started(t, 2, 31)
	r := rolesOf(s)
	setHand(t, s, r.holderPartner, Hand{4, 0, 0, 0})

	// Drain every triangle from the pool into other players' hands by swapping,
	// so card totals per player don't change.
	for _, id := range s.Seats() {
		if id == r.holderPartner {
			continue
		}
		p := s.players[id]
		for ty := range p.hand {
			for CardType(ty) != Triangle && p.hand[ty] > 0 && s.pool[Triangle] > 0 {
				p.hand[ty]--
				s.pool[ty]++
				p.hand[Triangle]++
				s.pool[Triangle]--
			}
		}
	}
	if s.pool[Triangle] != 0 {
		t.Fatalf("could not drain the pool: %d triangles left", s.pool[Triangle])
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}

	evs := mustApply(t, s, Suspect{By: r.o1, Target: r.holderPartner})
	if h := s.Hand(r.holderPartner); h != (Hand{1, 1, 1, 1}) {
		t.Errorf("reset hand = %v, want one of each", h)
	}
	if pr, _ := find[PlayerReset](evs); !slices.Equal(pr.Minted, []CardType{Triangle}) {
		t.Errorf("minted = %v, want [triangle]", pr.Minted)
	}
}

func TestResetNormallyMintsNothing(t *testing.T) {
	s := started(t, 5, 32)
	r := rolesOf4(s)
	setHand(t, s, r.partner, Hand{4, 0, 0, 0})
	var by PlayerID
	for _, id := range s.Seats() {
		if id != r.partner {
			by = id
			break
		}
	}
	evs := mustApply(t, s, Suspect{By: by, Target: r.partner})
	if pr, _ := find[PlayerReset](evs); len(pr.Minted) != 0 {
		t.Errorf("minted %v with a full-size deck", pr.Minted)
	}
}

func TestAccessorsForTheRoomLayer(t *testing.T) {
	s := started(t, 2, 33)
	r := rolesOf(s)
	if !s.CanProclaim(r.o1) || s.CanProclaim("ghost") {
		t.Error("CanProclaim")
	}
	if s.ForcedPassPlayer() != "" || len(s.PendingReveals()) != 0 {
		t.Error("nothing should be pending yet")
	}
	mustApply(t, s, Suspect{By: r.o1, Target: r.o2})
	mustApply(t, s, Suspect{By: r.o1, Target: r.holderPartner})
	got := s.PendingReveals()
	if len(got) != 2 || got[0] > got[1] {
		t.Errorf("PendingReveals = %v, want two, sorted", got)
	}
	s.players[r.o1].canProclaim = false
	if s.CanProclaim(r.o1) {
		t.Error("revoked right should show")
	}
}
