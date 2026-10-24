package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// lockedOutPlayer spends a player's 3 suspicion points and then makes a false
// proclamation, which locks them out of proclaiming.
func lockedOutPlayer(t *testing.T, h *harness) engine.PlayerID {
	t.Helper()
	holder := h.hold()
	var p engine.PlayerID
	for _, id := range h.r.order {
		if h.partnerOf(id) != holder { // their partner can't be holding the passing card
			p = id
			break
		}
	}
	for _, id := range h.r.order {
		if id != p {
			h.must(p, Suspect{Target: id})
		}
	}
	h.must(p, Proclaim{}) // false, at 0 points
	if !h.r.game.ProclaimRevoked(p) {
		t.Fatalf("%s should be locked out", p)
	}
	return p
}

func TestAFalseProclaimAtZeroPointsLocksThePlayerOutForThreeMinutes(t *testing.T) {
	h := newHarness(t, 71)
	h.playing(4)
	p := lockedOutPlayer(t, h)

	at, ok := h.r.dl.get(dkey{dkProclaim, p})
	if !ok || !at.Equal(h.now.Add(3*time.Minute)) {
		t.Fatalf("lockout timer = %v %v, want 3 minutes from now", at, ok)
	}
	h.mustFail(p, Proclaim{}, engine.ErrProclaimRevoked)

	h.advance(3*time.Minute - time.Second)
	if !h.r.game.ProclaimRevoked(p) {
		t.Fatal("still locked out just before the 3 minutes are up")
	}
	outs := h.advance(time.Second)
	if h.r.game.ProclaimRevoked(p) {
		t.Fatal("the lockout should be over after 3 minutes")
	}
	if got := gameEvents[engine.ProclaimRestored](outs, p); len(got) != 1 {
		t.Errorf("the player should be told: %v", got)
	}
	if h.r.dl.has(dkey{dkProclaim, p}) {
		t.Error("the lockout timer should be gone")
	}
}
