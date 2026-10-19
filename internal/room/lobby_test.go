package room

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func TestCreatorIsInTheRoom(t *testing.T) {
	h := newHarness(t, 1)
	if len(h.r.members) != 1 || h.r.Phase() != PhaseLobby {
		t.Fatal("expected a lobby with the creator in it")
	}
	if len(msgs[Snapshot](h.last, pid(0))) != 1 {
		t.Error("the creator should get a snapshot")
	}
}

func TestCountdownStartsAtFourAndCancelsBelow(t *testing.T) {
	h := newHarness(t, 2)
	h.joinN(3)
	if _, ok := h.r.dl.get(dkey{kind: dkCountdown}); ok {
		t.Fatal("countdown must not start below 4 players")
	}
	h.must(pid(3), Join{})
	cs := msgs[CountdownStarted](h.last, pid(0))
	if len(cs) != 1 || !cs[0].EndsAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("countdown = %+v", cs)
	}
	h.must(pid(3), Leave{})
	if !anyMsg[CountdownCancelled](h.last) {
		t.Error("dropping below 4 should cancel the countdown")
	}
	h.now = h.now.Add(time.Minute)
	h.must(pid(4), Join{})
	cs = msgs[CountdownStarted](h.last, pid(0))
	if len(cs) != 1 || !cs[0].EndsAt.Equal(h.now.Add(2*time.Minute)) {
		t.Errorf("a restarted countdown should run the full 2 minutes: %+v", cs)
	}
}

func TestAFullRoomStartsImmediately(t *testing.T) {
	h := newHarness(t, 3)
	h.joinN(9)
	if h.r.Phase() != PhaseLobby {
		t.Fatal("nine players should still be waiting for the countdown")
	}
	h.must(pid(9), Join{})
	if h.r.Phase() != PhaseChat {
		t.Fatalf("a full room should start at once; phase = %v", h.r.Phase())
	}
	gs := msgs[GameStarting](h.last, pid(0))
	if len(gs) != 1 || len(gs[0].Teams) != 5 {
		t.Fatalf("GameStarting = %+v", gs)
	}
	if h.r.dl.has(dkey{kind: dkCountdown}) {
		t.Error("the countdown should be gone")
	}
	h.mustFail(pid(10), Join{}, ErrNotJoinable)
	h.must(pid(10), Spectate{}) // but they can watch
	h.mustFail(pid(3), Join{}, ErrNotJoinable)
}

// Accepting a partner invite joins you to the room and pairs you. If that
// fills the room, the pair must survive the start.
func TestTheLastSeatTakenByAnAcceptedInviteKeepsItsPair(t *testing.T) {
	h := newHarness(t, 14)
	h.joinN(9)
	h.must(pid(3), InvitePartner{To: "late"})
	h.must("late", AcceptPartner{From: pid(3)})
	if h.r.Phase() != PhaseChat {
		t.Fatalf("phase = %v, want chat", h.r.Phase())
	}
	if h.partnerOf(pid(3)) != "late" || h.partnerOf("late") != pid(3) {
		t.Error("the accepted pair must be kept")
	}
	found := false
	for _, tm := range msgs[GameStarting](h.last, pid(0))[0].Teams {
		if (tm[0] == pid(3) && tm[1] == "late") || (tm[1] == pid(3) && tm[0] == "late") {
			found = true
		}
	}
	if !found {
		t.Error("the pair is missing from the teams")
	}
}

func TestPartnerInviteFlow(t *testing.T) {
	h := newHarness(t, 4)
	h.joinN(4)
	h.must(pid(1), InvitePartner{To: pid(2)})
	if len(msgs[PartnerInvite](h.last, pid(2))) != 1 {
		t.Fatal("the invitee should be told")
	}
	h.mustFail(pid(1), InvitePartner{To: pid(2)}, ErrDuplicateInvite)
	h.mustFail(pid(2), AcceptPartner{From: pid(3)}, ErrNoInvite)
	h.must(pid(2), AcceptPartner{From: pid(1)})
	if !anyMsg[TeamFormed](h.last) || h.partnerOf(pid(1)) != pid(2) || h.partnerOf(pid(2)) != pid(1) {
		t.Fatal("expected a team")
	}
	h.mustFail(pid(1), InvitePartner{To: pid(3)}, ErrAlreadyPaired)
	h.mustFail(pid(3), InvitePartner{To: pid(1)}, ErrAlreadyPaired)
	h.mustFail(pid(3), InvitePartner{To: pid(3)}, ErrBadTarget)
}

func TestDecliningAnInvite(t *testing.T) {
	h := newHarness(t, 5)
	h.joinN(3)
	h.must(pid(1), InvitePartner{To: pid(2)})
	h.must(pid(2), DeclinePartner{From: pid(1)})
	if len(msgs[PartnerDeclined](h.last, pid(1))) != 1 {
		t.Error("the inviter should hear about it")
	}
	h.mustFail(pid(2), AcceptPartner{From: pid(1)}, ErrNoInvite)
	h.must(pid(1), InvitePartner{To: pid(2)}) // and may ask again
}

func TestAcceptingAnOutsidersInviteBringsThemIn(t *testing.T) {
	h := newHarness(t, 6)
	h.must(pid(0), InvitePartner{To: "newcomer"})
	h.must("newcomer", AcceptPartner{From: pid(0)})
	if h.r.members["newcomer"] == nil || h.partnerOf(pid(0)) != "newcomer" {
		t.Fatal("the invitee should have joined and paired")
	}
}

func TestInviteToRoom(t *testing.T) {
	h := newHarness(t, 8)
	h.must(pid(0), InviteToRoom{To: "friend"})
	if len(msgs[RoomInvite](h.last, "friend")) != 1 {
		t.Error("friend should be invited")
	}
	h.mustFail(pid(0), InviteToRoom{To: pid(0)}, ErrBadTarget)
	h.mustFail("friend", InviteToRoom{To: "x"}, ErrNotPlayer)
}

func TestLeavingTheLobbyBreaksTheTeam(t *testing.T) {
	h := newHarness(t, 9)
	h.joinN(4)
	h.must(pid(1), InvitePartner{To: pid(2)})
	h.must(pid(2), AcceptPartner{From: pid(1)})
	h.must(pid(1), Leave{})
	if !anyMsg[TeamBroken](h.last) || h.partnerOf(pid(2)) != "" {
		t.Error("the partner should be free again")
	}
	if h.r.members[pid(1)] != nil {
		t.Error("they should be gone")
	}
}

func TestGameStartPairsTheFreeAtRandomAndKeepsChosenPairs(t *testing.T) {
	h := newHarness(t, 10)
	h.joinN(6)
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.advance(2 * time.Minute)

	if h.r.Phase() != PhaseChat {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	gs := msgs[GameStarting](h.last, pid(0))
	if len(gs) != 1 || len(gs[0].Teams) != 3 || len(gs[0].Demoted) != 0 {
		t.Fatalf("GameStarting = %+v", gs)
	}
	if h.partnerOf(pid(0)) != pid(1) {
		t.Error("a chosen pair must survive the random pairing")
	}
	for _, id := range h.r.order {
		if p := h.partnerOf(id); p == "" || h.partnerOf(p) != id {
			t.Errorf("%s is not properly paired (%q)", id, p)
		}
	}
	if len(h.r.game.Seats()) != 6 {
		t.Error("engine should have 6 players")
	}
}

func TestAnOddPlayerOutBecomesASpectator(t *testing.T) {
	h := newHarness(t, 11)
	h.joinN(5)
	h.advance(2 * time.Minute)
	gs := msgs[GameStarting](h.last, pid(0))[0]
	if len(gs.Demoted) != 1 || len(gs.Teams) != 2 {
		t.Fatalf("GameStarting = %+v", gs)
	}
	out := gs.Demoted[0]
	if !h.r.spectators[out] || h.r.members[out] != nil {
		t.Error("the leftover player should be watching")
	}
	h.mustFail(out, Ready{Ready: true}, ErrNotPlayer)
	h.mustFail(out, Join{}, ErrNotJoinable)
}

func TestInvitesAreDroppedWhenTheGameStarts(t *testing.T) {
	h := newHarness(t, 12)
	h.joinN(4)
	h.must(pid(1), InvitePartner{To: pid(2)})
	h.advance(2 * time.Minute)
	if len(h.r.invites) != 0 {
		t.Error("pending invites should be cleared")
	}
	h.mustFail(pid(2), AcceptPartner{From: pid(1)}, ErrWrongPhase)
}

func TestNoJoiningAfterStartButWatchingIsFine(t *testing.T) {
	h := newHarness(t, 13)
	h.startGame(4)
	h.mustFail("late", Join{}, ErrNotJoinable)
	h.must("late", Spectate{})
	h.mustFail(pid(0), Spectate{}, ErrAlreadyMember)
}

func TestSameSeedSameTeams(t *testing.T) {
	teams := func() [][2]engine.PlayerID {
		h := newHarness(t, 99)
		h.joinN(8)
		h.advance(2 * time.Minute)
		return msgs[GameStarting](h.last, pid(0))[0].Teams
	}
	a, b := teams(), teams()
	if !slices.Equal(a, b) {
		t.Errorf("not deterministic: %v vs %v", a, b)
	}
}

func TestEveryPartnerAssignmentKeepsThePairsDistinct(t *testing.T) {
	for seed := uint64(0); seed < 100; seed++ {
		n := 4 + int(seed%7)
		r, _ := New(DefaultConfig(), "r", pid(0), rand.New(rand.NewPCG(seed, 1)), t0)
		for i := 1; i < n; i++ {
			if _, err := r.Handle(t0, pid(i), Join{}); err != nil {
				t.Fatal(err)
			}
		}
		r.Tick(t0.Add(2 * time.Minute))
		seen := map[engine.PlayerID]bool{}
		for _, id := range r.order {
			if seen[id] {
				t.Fatalf("seed %d: duplicate %s", seed, id)
			}
			seen[id] = true
		}
		if want := n - n%2; len(r.order) != want {
			t.Fatalf("seed %d: %d players for %d joiners", seed, len(r.order), n)
		}
	}
}

func TestTheCreatorCanSkipTheCountdownWhenEveryoneIsPaired(t *testing.T) {
	h := newHarness(t, 15)
	h.joinN(4)
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.must(pid(2), InvitePartner{To: pid(3)})
	h.must(pid(3), AcceptPartner{From: pid(2)})
	if h.r.Phase() != PhaseLobby {
		t.Fatal("the countdown should still be running")
	}
	h.must(pid(0), StartNow{})
	if h.r.Phase() != PhaseChat {
		t.Fatalf("phase = %v, want chat", h.r.Phase())
	}
	if h.r.dl.has(dkey{kind: dkCountdown}) {
		t.Error("the countdown should be gone")
	}
	gs := msgs[GameStarting](h.last, pid(3))
	if len(gs) != 1 || len(gs[0].Teams) != 2 || len(gs[0].Demoted) != 0 {
		t.Errorf("GameStarting = %+v", gs)
	}
	if h.partnerOf(pid(0)) != pid(1) || h.partnerOf(pid(2)) != pid(3) {
		t.Error("chosen pairs must be kept")
	}
}

func TestOnlyTheCreatorCanSkipAndOnlyWhenReady(t *testing.T) {
	h := newHarness(t, 16)
	h.joinN(5)
	h.mustFail(pid(1), StartNow{}, ErrNotCreator)
	h.mustFail("stranger", StartNow{}, ErrNotCreator)
	h.mustFail(pid(0), StartNow{}, ErrUnpairedPlayers) // nobody has picked a partner yet

	for _, p := range [][2]int{{0, 1}, {2, 3}} {
		h.must(pid(p[0]), InvitePartner{To: pid(p[1])})
		h.must(pid(p[1]), AcceptPartner{From: pid(p[0])})
	}
	h.mustFail(pid(0), StartNow{}, ErrUnpairedPlayers) // pid(4) is left over
	h.must(pid(4), Leave{})
	h.must(pid(0), StartNow{})
}

func TestSkippingNeedsAtLeastTwoTeams(t *testing.T) {
	h := newHarness(t, 17)
	h.joinN(2)
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.mustFail(pid(0), StartNow{}, ErrTooFewPlayers)
}

func TestSkippingIsOnlyForTheLobby(t *testing.T) {
	h := newHarness(t, 18)
	h.startGame(4)
	h.mustFail(pid(0), StartNow{}, ErrWrongPhase)
}

func TestIfTheCreatorLeavesNobodyCanSkip(t *testing.T) {
	h := newHarness(t, 19)
	h.joinN(4)
	h.must(pid(0), Leave{})
	for i := 1; i < 4; i++ {
		h.mustFail(pid(i), StartNow{}, ErrNotCreator)
	}
	h.mustFail(pid(0), StartNow{}, ErrNotCreator)
}

func TestSnapshotsNameTheCreator(t *testing.T) {
	h := newHarness(t, 20)
	h.must(pid(1), Join{})
	if s := msgs[Snapshot](h.last, pid(1)); len(s) != 1 || s[0].Creator != pid(0) {
		t.Errorf("snapshot = %+v", s)
	}
}
