package room

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type harness struct {
	t    *testing.T
	r    *Room
	now  time.Time
	last []Out // outputs of the most recent call
}

func pid(i int) engine.PlayerID { return engine.PlayerID(fmt.Sprintf("p%02d", i)) }

func newHarness(t *testing.T, seed uint64) *harness {
	t.Helper()
	return newHarnessCfg(t, seed, DefaultConfig())
}

func newHarnessCfg(t *testing.T, seed uint64, cfg Config) *harness {
	t.Helper()
	r, outs := New(cfg, "room1", pid(0), rand.New(rand.NewPCG(seed, seed+1)), t0)
	return &harness{t: t, r: r, now: t0, last: outs}
}

func (h *harness) do(from engine.PlayerID, in Input) error {
	h.t.Helper()
	outs, err := h.r.Handle(h.now, from, in)
	h.last = outs
	h.check()
	return err
}

func (h *harness) must(from engine.PlayerID, in Input) {
	h.t.Helper()
	if err := h.do(from, in); err != nil {
		h.t.Fatalf("%T from %s: %v", in, from, err)
	}
}

func (h *harness) mustFail(from engine.PlayerID, in Input, want error) {
	h.t.Helper()
	if err := h.do(from, in); err != want {
		h.t.Fatalf("%T from %s: error = %v, want %v", in, from, err, want)
	}
	if len(h.last) != 0 {
		h.t.Fatalf("a rejected %T produced output: %v", in, h.last)
	}
}

// advance moves the clock and runs whatever came due.
func (h *harness) advance(d time.Duration) []Out {
	h.t.Helper()
	h.now = h.now.Add(d)
	h.last = h.r.Tick(h.now)
	h.check()
	return h.last
}

func (h *harness) check() {
	h.t.Helper()
	if err := h.r.checkInvariants(); err != nil {
		h.t.Fatalf("invariant: %v", err)
	}
}

// joinN joins players 1..n-1 (player 0 created the room).
func (h *harness) joinN(n int) {
	h.t.Helper()
	for i := 1; i < n; i++ {
		h.must(pid(i), Join{})
	}
}

// startGame fills the room, lets the countdown run out, and leaves it in the
// first chat. Everyone is auto-paired.
func (h *harness) startGame(players int) {
	h.t.Helper()
	h.joinN(players)
	h.advance(h.r.cfg.LobbyCountdown)
	if h.r.Phase() != PhaseChat {
		h.t.Fatalf("phase = %v, want chat", h.r.Phase())
	}
}

// startRound readies everyone so the first round begins.
func (h *harness) startRound() {
	h.t.Helper()
	for _, id := range h.r.order {
		if !h.r.members[id].removed {
			h.must(id, Ready{Ready: true})
		}
	}
	if h.r.Phase() != PhasePlaying {
		h.t.Fatalf("phase = %v, want playing", h.r.Phase())
	}
}

func (h *harness) playing(players int) {
	h.t.Helper()
	h.startGame(players)
	h.startRound()
}

// msgs finds the messages of one type sent to one recipient.
func msgs[T Message](outs []Out, to engine.PlayerID) []T {
	var res []T
	for _, o := range outs {
		if v, ok := o.Msg.(T); ok && o.To == to {
			res = append(res, v)
		}
	}
	return res
}

func gameEvents[T engine.Event](outs []Out, to engine.PlayerID) []T {
	var res []T
	for _, ge := range msgs[GameEvent](outs, to) {
		if v, ok := ge.Event.(T); ok {
			res = append(res, v)
		}
	}
	return res
}

func anyMsg[T Message](outs []Out) bool {
	for _, o := range outs {
		if _, ok := o.Msg.(T); ok {
			return true
		}
	}
	return false
}

func (h *harness) hold() engine.PlayerID { return h.r.game.Holder() }

func (h *harness) partnerOf(id engine.PlayerID) engine.PlayerID { return h.r.members[id].partner }

// checkInvariants is for tests: it verifies the room's bookkeeping agrees with
// the engine and the timer table.
func (r *Room) checkInvariants() error {
	if r.phase == PhaseClosed {
		return nil
	}
	if len(r.order) != len(r.members) {
		return fmt.Errorf("order has %d ids, members %d", len(r.order), len(r.members))
	}
	for _, id := range r.order {
		if r.members[id] == nil {
			return fmt.Errorf("order lists %s but they are not a member", id)
		}
		if r.spectators[id] {
			return fmt.Errorf("%s is both a member and a spectator", id)
		}
	}
	if len(r.members) > r.cfg.MaxPlayers {
		return fmt.Errorf("%d members, max %d", len(r.members), r.cfg.MaxPlayers)
	}
	if r.paused {
		if len(r.dl.m) != 1 || !r.dl.has(dkey{kind: dkPause}) {
			return fmt.Errorf("a paused game should have only its pause deadline, has %v", r.dl.m)
		}
		if r.phase != PhaseChat && r.phase != PhasePlaying {
			return fmt.Errorf("paused in phase %v", r.phase)
		}
		return nil
	}
	has := func(kind dkind) int { return len(r.dl.players(kind)) }
	switch r.phase {
	case PhaseLobby:
		if r.game != nil {
			return fmt.Errorf("lobby with a game")
		}
		if want := len(r.members) >= r.cfg.MinPlayers; want != r.dl.has(dkey{kind: dkCountdown}) {
			return fmt.Errorf("%d members but countdown running = %v", len(r.members), !want)
		}
		for _, k := range []dkind{dkChat, dkHold, dkReveal, dkForced, dkProclaim} {
			if has(k) != 0 || r.dl.has(dkey{kind: k}) {
				return fmt.Errorf("lobby has a %s timer", k.label())
			}
		}
	case PhaseChat:
		if !r.dl.has(dkey{kind: dkChat}) {
			return fmt.Errorf("chat without a chat deadline")
		}
		if r.game.Phase() != engine.PhaseWaiting {
			return fmt.Errorf("chat but engine is %v", r.game.Phase())
		}
		if has(dkHold)+has(dkReveal)+has(dkForced)+has(dkProclaim) != 0 {
			return fmt.Errorf("round timers during chat")
		}
	case PhasePlaying:
		if r.game.Phase() != engine.PhasePlaying {
			return fmt.Errorf("playing but engine is %v", r.game.Phase())
		}
		if r.dl.has(dkey{kind: dkChat}) {
			return fmt.Errorf("chat deadline during a round")
		}
		forced := r.game.ForcedPassPlayer()
		if forced != "" {
			if !r.dl.has(dkey{dkForced, forced}) || has(dkHold) != 0 {
				return fmt.Errorf("forced pass for %s needs exactly a forced timer", forced)
			}
		} else if !r.dl.has(dkey{dkHold, r.game.Holder()}) || has(dkHold) != 1 || has(dkForced) != 0 {
			return fmt.Errorf("holder %s needs exactly one hold timer", r.game.Holder())
		}
		for _, id := range r.order {
			if r.game.ProclaimRevoked(id) != r.dl.has(dkey{dkProclaim, id}) {
				return fmt.Errorf("%s: locked out = %v but lockout timer = %v", id, r.game.ProclaimRevoked(id), r.dl.has(dkey{dkProclaim, id}))
			}
		}
		pending := r.game.PendingReveals()
		got := r.dl.players(dkReveal)
		if len(pending) != len(got) {
			return fmt.Errorf("pending reveals %v but timers %v", pending, got)
		}
		for i := range pending {
			if pending[i] != got[i] {
				return fmt.Errorf("pending reveals %v but timers %v", pending, got)
			}
		}
	case PhaseOver:
		for _, k := range []dkind{dkChat, dkHold, dkReveal, dkForced, dkProclaim, dkReconnect, dkCountdown} {
			if has(k) != 0 || r.dl.has(dkey{kind: k}) {
				return fmt.Errorf("finished game still has a %s timer", k.label())
			}
		}
	}
	for _, id := range r.dl.players(dkReconnect) {
		m := r.members[id]
		if m == nil || m.connected || m.removed {
			return fmt.Errorf("reconnect timer for %s who is not a disconnected player", id)
		}
	}
	connected := 0
	for _, m := range r.members {
		if m.connected && !m.removed {
			connected++
		}
	}
	if (connected == 0) != r.dl.has(dkey{kind: dkEmpty}) {
		return fmt.Errorf("%d connected but empty timer = %v", connected, r.dl.has(dkey{kind: dkEmpty}))
	}
	if r.game != nil {
		if err := r.game.CheckInvariants(); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
	}
	return nil
}
