package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func TestDisconnectGivesNinetySecondsThenRemovesTheTeam(t *testing.T) {
	h := newHarness(t, 40)
	h.playing(6)
	gone := pid(2)
	h.must(gone, Disconnect{})
	cc := msgs[ConnectionChanged](h.last, pid(0))
	if len(cc) != 1 || cc[0].Connected || cc[0].ReconnectBy == nil || !cc[0].ReconnectBy.Equal(h.now.Add(90*time.Second)) {
		t.Fatalf("ConnectionChanged = %+v", cc)
	}
	h.advance(89 * time.Second)
	if h.r.members[gone].removed {
		t.Fatal("removed too early")
	}
	outs := h.advance(time.Second)
	partner := h.partnerOf(gone)
	if !h.r.members[gone].removed || !h.r.members[partner].removed {
		t.Fatal("the whole team should be removed")
	}
	if len(gameEvents[engine.TeamRemoved](outs, pid(0))) != 1 {
		t.Error("TeamRemoved should be broadcast")
	}
	if h.r.Phase() != PhasePlaying {
		t.Errorf("two teams remain, so the game goes on; phase = %v", h.r.Phase())
	}
	h.mustFail(partner, Pass{Card: engine.Circle}, ErrNotPlayer)
}

func TestReconnectingInTimeKeepsYourPlace(t *testing.T) {
	h := newHarness(t, 41)
	h.playing(6)
	gone := pid(2)
	h.must(gone, Disconnect{})
	h.advance(60 * time.Second)
	h.must(gone, Connect{})
	if len(msgs[Snapshot](h.last, gone)) != 1 {
		t.Error("a returning player needs a snapshot")
	}
	snap := msgs[Snapshot](h.last, gone)[0]
	if snap.Game == nil || snap.Game.YourHand == nil || snap.Game.YourHand.Total() < 4 {
		t.Errorf("the snapshot should include their hand: %+v", snap.Game)
	}
	if len(msgs[ConnectionChanged](h.last, pid(0))) != 1 {
		t.Error("everyone should hear they're back")
	}
	h.advance(5 * time.Minute)
	if h.r.members[gone].removed {
		t.Error("they came back; they must not be removed")
	}
}

func TestBothPartnersGoneRemovesTheTeamOnce(t *testing.T) {
	h := newHarness(t, 42)
	h.playing(6)
	a := pid(1)
	b := h.partnerOf(a)
	h.must(a, Disconnect{})
	h.advance(10 * time.Second)
	h.must(b, Disconnect{})
	outs := h.advance(80 * time.Second) // a's window ends
	if len(gameEvents[engine.TeamRemoved](outs, pid(0))) != 1 {
		t.Fatal("expected the team to be removed")
	}
	h.advance(time.Minute) // b's old timer must not misfire
}

func TestLobbyDisconnectRemovesAfterTheWindow(t *testing.T) {
	h := newHarness(t, 43)
	h.joinN(4)
	h.must(pid(3), Disconnect{})
	h.advance(89 * time.Second)
	if h.r.members[pid(3)] == nil {
		t.Fatal("removed early")
	}
	outs := h.advance(time.Second)
	if ml := msgs[MemberLeft](outs, pid(0)); len(ml) != 1 || ml[0].Reason != "disconnected" {
		t.Errorf("MemberLeft = %+v", ml)
	}
	if _, ok := h.r.dl.get(dkey{kind: dkCountdown}); ok {
		t.Error("three players left: the countdown should be cancelled")
	}
}

func TestEveryoneGoneFromAGamePausesItAfterAMinute(t *testing.T) {
	h := newHarness(t, 44)
	h.playing(4)
	for i := 0; i < 4; i++ {
		h.must(pid(i), Disconnect{})
	}
	h.advance(59 * time.Second)
	if _, paused := h.r.Paused(); paused {
		t.Fatal("paused early")
	}
	outs := h.advance(time.Second)
	until, paused := h.r.Paused()
	if !paused || h.r.Phase() != PhasePlaying || !anyMsg[Paused](outs) {
		t.Fatalf("phase = %v, paused = %v", h.r.Phase(), paused)
	}
	if want := h.now.Add(h.r.cfg.PausedFor); !until.Equal(want) {
		t.Errorf("paused until %v, want %v", until, want)
	}
	// A paused game waits: nothing runs, even days later... until the limit.
	h.advance(23 * time.Hour)
	if h.r.Phase() == PhaseClosed {
		t.Fatal("a paused game should wait for its players")
	}
	outs = h.advance(time.Hour + time.Second)
	if h.r.Phase() != PhaseClosed || !anyMsg[RoomClosed](outs) {
		t.Fatalf("after the wait the game is given up: phase = %v", h.r.Phase())
	}
	if _, err := h.r.Handle(h.now, pid(0), Connect{}); err != ErrClosed {
		t.Errorf("a closed room says %v", err)
	}
}

func TestAnEmptyLobbyStillClosesAfterAMinute(t *testing.T) {
	h := newHarness(t, 45)
	h.must(pid(0), Disconnect{})
	h.advance(time.Minute)
	if h.r.Phase() != PhaseClosed {
		t.Fatalf("phase = %v", h.r.Phase())
	}
}

func TestSomeoneReturningCancelsTheEmptyTimer(t *testing.T) {
	h := newHarness(t, 45)
	h.playing(4)
	for i := 0; i < 4; i++ {
		h.must(pid(i), Disconnect{})
	}
	h.advance(50 * time.Second)
	h.must(pid(2), Connect{})
	h.advance(30 * time.Second)
	if h.r.Phase() == PhaseClosed {
		t.Error("someone is back; the room must stay open")
	}
}

func TestDeliberateDropOutIsInstant(t *testing.T) {
	h := newHarness(t, 46)
	h.playing(6)
	leaver := pid(3)
	partner := h.partnerOf(leaver)
	h.must(leaver, Leave{})
	if !h.r.members[leaver].removed || !h.r.members[partner].removed {
		t.Fatal("both partners should be removed immediately")
	}
	if len(gameEvents[engine.TeamRemoved](h.last, pid(0))) != 1 {
		t.Error("TeamRemoved should be broadcast")
	}
	// They still hear what happens next, but can't act.
	h.mustFail(partner, Suspect{Target: pid(0)}, ErrNotPlayer)
}

func TestDroppingOutLeavesOneTeamWhichWinsByForfeit(t *testing.T) {
	h := newHarness(t, 47)
	h.playing(4)
	h.must(pid(0), Leave{})
	if h.r.Phase() != PhaseOver {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	res := h.r.game.Result()
	if res == nil || res.Reason != engine.EndForfeit || res.CountsForStats {
		t.Errorf("result = %+v", res)
	}
	if !anyMsg[GameEvent](h.last) {
		t.Error("GameOver should be broadcast")
	}
}

func TestTheHolderLeavingPassesTheCardOn(t *testing.T) {
	h := newHarness(t, 48)
	h.playing(6)
	holder := h.hold()
	h.must(holder, Leave{})
	if nh := h.hold(); nh == holder || h.r.members[nh].removed {
		t.Errorf("the card should go to someone staying, got %s", nh)
	}
}

func TestLeavingDuringChatCanTriggerTheRoundStart(t *testing.T) {
	h := newHarness(t, 49)
	h.startGame(6)
	leaver := pid(0)
	for _, id := range h.r.order {
		if id != leaver && id != h.partnerOf(leaver) {
			h.must(id, Ready{Ready: true})
		}
	}
	if h.r.Phase() != PhaseChat {
		t.Fatal("the leaving team hasn't clicked ready")
	}
	h.must(leaver, Leave{})
	if h.r.Phase() != PhasePlaying {
		t.Errorf("the remaining players were all ready; phase = %v", h.r.Phase())
	}
}

func TestSpectatorsSeeEverythingTheyShouldNot(t *testing.T) {
	h := newHarness(t, 50)
	h.playing(4)
	h.must("fan", Spectate{})
	snap := msgs[Snapshot](h.last, "fan")[0]
	if snap.Game == nil || len(snap.Game.Hands) != 4 || snap.Game.YourHand != nil {
		t.Fatalf("a spectator should see every hand and own none: %+v", snap.Game)
	}
	h.mustFail("fan", Pass{Card: engine.Circle}, ErrNotPlayer)
	h.mustFail("fan", Proclaim{}, ErrNotPlayer)
}

func TestTheSpectatorPerkCanBeSwitchedOff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SpectatorsSeeCards = false
	h := newHarnessCfg(t, 51, cfg)
	h.playing(4)
	h.must("fan", Spectate{})
	if snap := msgs[Snapshot](h.last, "fan")[0]; snap.Game.Hands != nil {
		t.Error("no card perk: hands must stay hidden")
	}
}

func TestRemovedPlayersWatchButDontGetThePerk(t *testing.T) {
	h := newHarness(t, 52)
	h.playing(6)
	leaver := pid(1)
	h.must(leaver, Leave{})
	h.must(leaver, Connect{})
	snap := msgs[Snapshot](h.last, leaver)[0]
	if snap.Game.Hands != nil || snap.Game.YourHand != nil {
		t.Error("a removed player must not see anyone's cards")
	}
}

func TestPassedCardsAreHiddenFromEveryoneButTheTwoPlayers(t *testing.T) {
	h := newHarness(t, 53)
	h.playing(6)
	h.must("fan", Spectate{})
	from := h.hold()
	card := h.r.game.Hand(from).Types()[0]
	h.must(from, Pass{Card: card})
	to := h.hold()
	for _, id := range h.r.order {
		p := gameEvents[engine.Passed](h.last, id)
		if len(p) != 1 {
			t.Fatalf("%s got %d Passed events", id, len(p))
		}
		wantSeen := id == from || id == to
		if seen := p[0].Card != CardHidden; seen != wantSeen {
			t.Errorf("%s sees the card = %v, want %v", id, seen, wantSeen)
		}
	}
	if p := gameEvents[engine.Passed](h.last, "fan"); len(p) != 1 || p[0].Card == CardHidden {
		t.Error("a spectator with the perk sees the card")
	}
}

func TestHandUpdatesGoOnlyToWhoseHandChanged(t *testing.T) {
	h := newHarness(t, 54)
	h.playing(6)
	h.must("fan", Spectate{})
	from := h.hold()
	h.must(from, Pass{Card: h.r.game.Hand(from).Types()[0]})
	to := h.hold()
	for _, id := range h.r.order {
		got := len(msgs[HandUpdate](h.last, id))
		want := 0
		if id == from || id == to {
			want = 1
		}
		if got != want {
			t.Errorf("%s got %d hand updates, want %d", id, got, want)
		}
	}
	if got := msgs[HandUpdate](h.last, "fan"); len(got) != 2 {
		t.Errorf("the perk spectator sees both changes, got %d", len(got))
	}
	for _, hu := range msgs[HandUpdate](h.last, pid(0)) {
		if hu.Player != pid(0) {
			t.Error("a player must only receive their own hand")
		}
	}
}

func TestRevealOptionsAreOnlyForTheTarget(t *testing.T) {
	h := newHarness(t, 55)
	h.playing(4)
	holder := h.hold()
	target := pid(0)
	if target == holder {
		target = pid(1)
	}
	by := holder
	h.must(by, Suspect{Target: target})
	for _, id := range h.r.order {
		n := len(gameEvents[engine.RevealRequested](h.last, id))
		if (id == target) != (n == 1) {
			t.Errorf("%s got %d reveal prompts", id, n)
		}
	}
}
