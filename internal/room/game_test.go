package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// driveCircles plays real passes until target holds four or more circles.
// With asHolder it stops while target still holds the passing card; otherwise
// it keeps going until target has passed the spare card on.
func (h *harness) driveCircles(target engine.PlayerID, asHolder bool) {
	h.t.Helper()
	const circle = engine.Circle
	for i := 0; i < 300; i++ {
		g := h.r.game
		holder := g.Holder()
		if g.Hand(target)[circle] >= 4 && (holder == target) == asHolder {
			return
		}
		hand := g.Hand(holder)
		card, best := circle, -1
		if holder == target {
			// Keep circles: pass the type we hold the fewest of, if not circle.
			best = 1 << 30
			card = circle
			for ty, c := range hand {
				if engine.CardType(ty) != circle && c > 0 && c < best {
					card, best = engine.CardType(ty), c
				}
			}
		} else if hand[circle] == 0 {
			for ty, c := range hand {
				if c > best {
					card, best = engine.CardType(ty), c
				}
			}
		}
		h.must(holder, Pass{Card: card})
	}
	h.t.Fatalf("never got %s to four circles", target)
}

func TestChatStartsWithADeadline(t *testing.T) {
	h := newHarness(t, 20)
	h.startGame(4)
	cs := msgs[ChatStarted](h.last, pid(0))
	if len(cs) != 1 || cs[0].Round != 1 || !cs[0].EndsAt.Equal(h.now.Add(2*time.Minute)) {
		t.Fatalf("ChatStarted = %+v", cs)
	}
}

func TestEveryoneReadyStartsTheRoundEarly(t *testing.T) {
	h := newHarness(t, 21)
	h.startGame(4)
	for i := 0; i < 3; i++ {
		h.must(pid(i), Ready{Ready: true})
		if h.r.Phase() != PhaseChat {
			t.Fatalf("started after only %d ready", i+1)
		}
	}
	h.must(pid(3), Ready{Ready: true})
	if h.r.Phase() != PhasePlaying {
		t.Fatal("everyone is ready: the round should start")
	}
	if len(gameEvents[engine.RoundStarted](h.last, pid(0))) != 1 {
		t.Error("RoundStarted should be broadcast")
	}
	for i := 0; i < 4; i++ {
		hu := msgs[HandUpdate](h.last, pid(i))
		if len(hu) != 1 || hu[0].Hand.Total() < 4 {
			t.Errorf("player %d hand update: %+v", i, hu)
		}
	}
}

func TestOneUnreadyPlayerMakesEveryoneWaitForTheTimer(t *testing.T) {
	h := newHarness(t, 22)
	h.startGame(4)
	for i := 0; i < 3; i++ {
		h.must(pid(i), Ready{Ready: true})
	}
	h.advance(2*time.Minute - time.Second)
	if h.r.Phase() != PhaseChat {
		t.Fatal("should still be waiting")
	}
	h.advance(time.Second)
	if h.r.Phase() != PhasePlaying {
		t.Fatal("the chat timer should start the round")
	}
}

func TestUnreadyingTakesItBack(t *testing.T) {
	h := newHarness(t, 23)
	h.startGame(4)
	for i := 0; i < 3; i++ {
		h.must(pid(i), Ready{Ready: true})
	}
	h.must(pid(0), Ready{Ready: false})
	h.must(pid(3), Ready{Ready: true})
	if h.r.Phase() != PhaseChat {
		t.Error("one player took their ready back, so we should still be waiting")
	}
}

func TestTeamChatReachesOnlyThePartner(t *testing.T) {
	h := newHarness(t, 24)
	h.startGame(6)
	a := pid(0)
	b := h.partnerOf(a)
	h.must(a, TeamChat{Text: "  signal: scratch your nose  "})
	got := 0
	for _, o := range h.last {
		if m, ok := o.Msg.(TeamChatMessage); ok {
			got++
			if o.To != a && o.To != b {
				t.Errorf("%s was sent a private message", o.To)
			}
			if m.Text != "signal: scratch your nose" {
				t.Errorf("text = %q", m.Text)
			}
		}
	}
	if got != 2 {
		t.Errorf("%d recipients, want sender and partner", got)
	}
	h.mustFail(a, TeamChat{Text: ""}, ErrBadText)
	h.mustFail(a, TeamChat{Text: string(make([]rune, 501))}, ErrBadText)
}

func TestNoPrivateChatDuringARound(t *testing.T) {
	h := newHarness(t, 25)
	h.playing(4)
	h.mustFail(pid(0), TeamChat{Text: "psst"}, ErrWrongPhase)
	h.mustFail(pid(0), Ready{Ready: true}, ErrWrongPhase)
}

func TestHoldTimerPassesForAStallingHolder(t *testing.T) {
	h := newHarness(t, 26)
	h.playing(4)
	first := h.hold()
	at, ok := h.r.dl.get(dkey{dkHold, first})
	if !ok || !at.Equal(h.now.Add(time.Minute)) {
		t.Fatalf("hold deadline = %v (%v)", at, ok)
	}
	h.advance(59 * time.Second)
	if h.hold() != first {
		t.Fatal("passed too early")
	}
	outs := h.advance(time.Second)
	if h.hold() == first {
		t.Fatal("the hold timer should have passed the card")
	}
	if p := gameEvents[engine.Passed](outs, pid(0)); len(p) != 1 || !p[0].Auto {
		t.Errorf("expected one automatic pass, got %+v", p)
	}
	if at, _ := h.r.dl.get(dkey{dkHold, h.hold()}); !at.Equal(h.now.Add(time.Minute)) {
		t.Errorf("the new holder's clock should run a full minute, ends %v", at)
	}
}

func TestPassingRestartsTheClockForTheNextHolder(t *testing.T) {
	h := newHarness(t, 27)
	h.playing(4)
	h.advance(45 * time.Second)
	holder := h.hold()
	h.must(holder, Pass{Card: h.r.game.Hand(holder).Types()[0]})
	if at, _ := h.r.dl.get(dkey{dkHold, h.hold()}); !at.Equal(h.now.Add(time.Minute)) {
		t.Errorf("clock ends %v, want a minute from the pass", at)
	}
	// The old deadline passing changes nothing.
	prev := h.hold()
	h.advance(20 * time.Second)
	if h.hold() != prev {
		t.Error("the previous holder's old deadline must not fire")
	}
}

func TestOnlyTheHolderMayPass(t *testing.T) {
	h := newHarness(t, 28)
	h.playing(4)
	other := pid(0)
	if h.hold() == other {
		other = pid(1)
	}
	h.mustFail(other, Pass{Card: engine.Circle}, engine.ErrNotHolder)
}

func TestSuspectingStartsARevealPromptThatTimesOut(t *testing.T) {
	h := newHarness(t, 29)
	h.playing(4)
	holder := h.hold()
	var target, by engine.PlayerID
	for _, id := range h.r.order {
		if id != holder {
			if target == "" {
				target = id
			} else if by == "" {
				by = id
			}
		}
	}
	h.must(by, Suspect{Target: target})
	if len(gameEvents[engine.RevealRequested](h.last, target)) != 1 {
		t.Fatal("the target should be asked to choose")
	}
	if at, _ := h.r.dl.get(dkey{dkReveal, target}); !at.Equal(h.now.Add(30 * time.Second)) {
		t.Fatalf("prompt deadline = %v", at)
	}
	outs := h.advance(30 * time.Second)
	if f := gameEvents[engine.SuspicionFreed](outs, by); len(f) != 1 || f[0].Target != target {
		t.Errorf("the timeout should reveal for them: %+v", f)
	}
	if h.r.dl.has(dkey{dkReveal, target}) {
		t.Error("the prompt timer should be gone")
	}
}

func TestAnsweringARevealCancelsItsTimer(t *testing.T) {
	h := newHarness(t, 30)
	h.playing(4)
	holder := h.hold()
	target := pid(0)
	if target == holder {
		target = pid(1)
	}
	by := h.partnerOf(holder) // anyone else will do
	if by == target {
		by = holder
	}
	h.must(by, Suspect{Target: target})
	h.must(target, Reveal{Types: []engine.CardType{engine.Circle, engine.Cross}})
	if h.r.dl.has(dkey{dkReveal, target}) {
		t.Error("answered prompt left a timer behind")
	}
	h.advance(time.Minute) // nothing else should misfire (invariants checked)
}

func TestCaughtHolderMustPassThenResets(t *testing.T) {
	h := newHarness(t, 31)
	h.playing(4)
	target := pid(1)
	h.driveCircles(target, true)
	if h.hold() != target {
		t.Fatal("setup: target should hold the card")
	}
	by := h.partnerOf(target) // even a partner may suspect, and the engine allows it
	h.must(by, Suspect{Target: target})
	if len(gameEvents[engine.ForcedPassRequested](h.last, target)) != 1 {
		t.Fatal("the caught holder should be asked to pass")
	}
	if len(gameEvents[engine.SuspicionFailed](h.last, pid(2))) != 1 {
		t.Error("everyone sees the failed hand")
	}
	if at, _ := h.r.dl.get(dkey{dkForced, target}); !at.Equal(h.now.Add(30 * time.Second)) {
		t.Fatalf("forced-pass prompt ends %v", at)
	}
	if h.r.dl.has(dkey{dkHold, target}) {
		t.Error("the hold timer should be replaced by the prompt")
	}
	outs := h.advance(30 * time.Second)
	if len(gameEvents[engine.PlayerReset](outs, pid(0))) != 1 {
		t.Error("after the forced pass the player resets")
	}
	if got := h.r.game.Hand(target); got != (engine.Hand{1, 1, 1, 1}) {
		t.Errorf("reset hand = %v", got)
	}
	if h.hold() == target {
		t.Error("the card should have moved on")
	}
}

func TestCaughtHolderCanChooseTheCardInTime(t *testing.T) {
	h := newHarness(t, 32)
	h.playing(4)
	target := pid(2)
	h.driveCircles(target, true)
	h.must(pid(0), Suspect{Target: target})
	h.advance(10 * time.Second)
	h.must(target, Pass{Card: h.r.game.Hand(target).Types()[len(h.r.game.Hand(target).Types())-1]})
	if h.r.dl.has(dkey{dkForced, target}) {
		t.Error("the prompt should be answered")
	}
	h.advance(time.Minute)
}

func TestWinningARoundReturnsToChat(t *testing.T) {
	h := newHarness(t, 33)
	h.playing(4)
	target := pid(1)
	h.driveCircles(target, false)
	proclaimer := h.partnerOf(target)
	h.must(proclaimer, Proclaim{})
	if len(gameEvents[engine.RoundWon](h.last, pid(0))) != 1 {
		t.Fatal("expected a round win")
	}
	if h.r.Phase() != PhaseChat {
		t.Fatalf("phase = %v, want chat", h.r.Phase())
	}
	cs := msgs[ChatStarted](h.last, pid(0))
	if len(cs) != 1 || cs[0].Round != 2 {
		t.Errorf("next chat = %+v", cs)
	}
	h.startRoundFromChat(t)
	if h.r.game.Round() != 2 {
		t.Errorf("round = %d", h.r.game.Round())
	}
}

func (h *harness) startRoundFromChat(t *testing.T) {
	t.Helper()
	for _, id := range h.r.order {
		if !h.r.members[id].removed {
			h.must(id, Ready{Ready: true})
		}
	}
	if h.r.Phase() != PhasePlaying {
		t.Fatalf("phase = %v", h.r.Phase())
	}
}

func TestFalseProclaimIsRejectedWithoutBreakingTimers(t *testing.T) {
	h := newHarness(t, 34)
	h.playing(4)
	holder := h.hold()
	other := h.partnerOf(holder)
	// The holder's partner can't proclaim while the holder has the card.
	h.mustFail(other, Proclaim{}, engine.ErrPartnerHoldsPassingCard)
}

func TestVotingToEndEndsTheGame(t *testing.T) {
	h := newHarness(t, 35)
	h.playing(4)
	for i := 0; i < 3; i++ {
		h.must(pid(i), VoteEnd{Agree: true})
	}
	if h.r.Phase() != PhasePlaying {
		t.Fatal("not everyone has agreed yet")
	}
	h.must(pid(3), VoteEnd{Agree: true})
	if h.r.Phase() != PhaseOver {
		t.Fatalf("phase = %v, want over", h.r.Phase())
	}
	if h.r.game.Result() == nil || !h.r.game.Result().Draw {
		t.Errorf("0-0 should be a draw: %+v", h.r.game.Result())
	}
	if _, ok := h.r.NextDeadline(); ok && h.r.dl.has(dkey{kind: dkHold}) {
		t.Error("timers should stop when the game ends")
	}
	h.mustFail(pid(0), Pass{}, ErrWrongPhase)
}
