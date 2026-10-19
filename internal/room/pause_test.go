package room

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// savedFrom builds what the store would hand back for a recorded game.
func savedFrom(t *testing.T, h *harness, recs *[]Record) Saved {
	t.Helper()
	var sv Saved
	for _, r := range *recs {
		switch v := r.(type) {
		case GameStarted:
			sv.ID, sv.Creator, sv.Setup = v.Room, v.Creator, v.Setup
		case CommandApplied:
			sv.Commands = append(sv.Commands, v.Command)
		case ChatPosted:
			sv.ChatSeq = v.Seq
			sv.Chat = append(sv.Chat, ChatMessage{ID: v.Seq, From: v.Player, Text: v.Text, At: v.At})
		}
	}
	sv.Since = h.now
	return sv
}

func restoredRoom(t *testing.T, seed uint64, setup func(h *harness)) (*harness, *Room) {
	t.Helper()
	h, recs := recordedAll(t, DefaultConfig())
	h.playing(4)
	if setup != nil {
		setup(h)
	}
	r, err := Restore(DefaultConfig(), savedFrom(t, h, recs), h.now)
	if err != nil {
		t.Fatal(err)
	}
	return h, r
}

func TestARestoredGameIsTheSameGamePaused(t *testing.T) {
	h, r := restoredRoom(t, 1, func(h *harness) {
		for i := 0; i < 5; i++ {
			h.must(h.hold(), Pass{Card: 0})
		}
		h.must(h.r.order[0], Chat{Text: "before the restart"})
	})
	if !reflect.DeepEqual(h.r.game, r.game) {
		t.Error("the restored engine differs from the original")
	}
	if _, paused := r.Paused(); !paused || r.Phase() != PhasePlaying {
		t.Errorf("phase %v paused %v", r.Phase(), paused)
	}
	if err := r.checkInvariants(); err != nil {
		t.Error(err)
	}
	if !sameSet(r.order, h.r.order) || r.creator != h.r.creator {
		t.Errorf("order %v creator %v", r.order, r.creator)
	}
	for id, m := range h.r.members {
		got := r.members[id]
		if got == nil || got.partner != m.partner || got.team != m.team || got.connected {
			t.Errorf("%s restored as %+v, was %+v", id, got, m)
		}
	}
	s := r.snapshotFor(r.order[0])
	if s.PausedUntil == nil || len(s.Chat) != 1 || s.Chat[0].Text != "before the restart" {
		t.Errorf("snapshot paused=%v chat=%v", s.PausedUntil, s.Chat)
	}
	if r.chatSeq != 1 {
		t.Errorf("chat numbering should carry on from 1, chatSeq = %d", r.chatSeq)
	}
}

func TestNothingMovesWhileAGameIsPaused(t *testing.T) {
	_, r := restoredRoom(t, 2, nil)
	now := t0.Add(time.Hour)
	p := r.order[0]
	for _, in := range []Input{Pass{Card: 0}, Suspect{Target: r.order[1]}, Proclaim{}, VoteEnd{Agree: true}, Ready{Ready: true}, TeamChat{Text: "x"}, SendSignal{Gesture: "nod"}, StartNow{}} {
		if _, err := r.Handle(now, p, in); err != ErrPaused {
			t.Errorf("%T while paused: %v", in, err)
		}
	}
	if outs := r.Tick(now.Add(10 * time.Hour)); len(outs) != 0 || r.Phase() == PhaseClosed {
		t.Errorf("ticking a paused game did %v, phase %v", outs, r.Phase())
	}
}

func TestPlayersWhoReturnToAPausedGameCanStillChatAndLook(t *testing.T) {
	_, r := restoredRoom(t, 3, nil)
	p := r.order[0]
	if _, err := r.Handle(t0, p, Connect{}); err != nil {
		t.Fatal(err)
	}
	outs, err := r.Handle(t0, p, Chat{Text: "hello, are we all here"})
	if err != nil {
		t.Fatal(err)
	}
	if c := msgs[ChatMessage](outs, p); len(c) != 1 || c[0].ID != 1 {
		t.Errorf("chat while paused: %v", c)
	}
	if _, err := r.Handle(t0, "watcher", Spectate{}); err != nil {
		t.Errorf("spectating a paused game: %v", err)
	}
}

func TestAPausedGameResumesWhenEveryoneIsBack(t *testing.T) {
	h, r := restoredRoom(t, 4, func(h *harness) { h.must(h.hold(), Pass{Card: 0}) })
	now := t0.Add(time.Hour)
	for i, id := range r.order {
		outs, err := r.Handle(now, id, Connect{})
		if err != nil {
			t.Fatal(err)
		}
		last := i == len(r.order)-1
		if got := anyMsg[Resumed](outs); got != last {
			t.Fatalf("after %d of %d connected, resumed = %v", i+1, len(r.order), got)
		}
	}
	if _, paused := r.Paused(); paused {
		t.Fatal("still paused")
	}
	if err := r.checkInvariants(); err != nil {
		t.Fatal(err)
	}
	// The round carries on: the holder has a fresh hold timer.
	holder := r.game.Holder()
	at, ok := r.dl.get(dkey{dkHold, holder})
	if !ok || !at.Equal(now.Add(r.cfg.HoldTimer)) {
		t.Errorf("hold timer = %v, %v", at, ok)
	}
	if holder != h.hold() {
		t.Errorf("holder %s, was %s", holder, h.hold())
	}
	// And the game can be played.
	if _, err := r.Handle(now, holder, Pass{Card: oddOne(r, holder)}); err != nil {
		t.Errorf("playing after resume: %v", err)
	}
}

func oddOne(r *Room, id engine.PlayerID) engine.CardType {
	hand := r.game.Hand(id)
	for ty, n := range hand {
		if n > 0 {
			return engine.CardType(ty)
		}
	}
	return 0
}

func TestAGamePausedBetweenRoundsResumesIntoTheTeamChat(t *testing.T) {
	cfg := DefaultConfig()
	h, recs := recordedAll(t, cfg)
	h.playing(4)
	target := pid(1)
	h.driveCircles(target, false)
	h.must(h.partnerOf(target), Proclaim{}) // round won: back to the chat
	if h.r.Phase() != PhaseChat {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	r, err := Restore(cfg, savedFrom(t, h, recs), h.now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase() != PhaseChat {
		t.Fatalf("restored phase = %v", r.Phase())
	}
	now := h.now.Add(time.Hour)
	var outs []Out
	for _, id := range r.order {
		outs, _ = r.Handle(now, id, Connect{})
	}
	cs := msgs[ChatStarted](outs, r.order[0])
	if len(cs) != 1 || cs[0].Round != 2 || !cs[0].EndsAt.Equal(now.Add(cfg.PreRoundChat)) {
		t.Errorf("chat after resume: %+v", cs)
	}
	if err := r.checkInvariants(); err != nil {
		t.Error(err)
	}
}

func TestAPausedGameWaitsForItsDeadlineThenIsAbandoned(t *testing.T) {
	_, r := restoredRoom(t, 5, nil)
	until, _ := r.Paused()
	if outs := r.Tick(until.Add(-time.Second)); len(outs) != 0 || r.Phase() == PhaseClosed {
		t.Fatalf("closed early: %v", outs)
	}
	outs := r.Tick(until)
	if r.Phase() != PhaseClosed || !anyMsg[RoomClosed](outs) {
		t.Errorf("phase = %v", r.Phase())
	}
}

func TestSomeoneLeavingAPausedGameCanLetTheRestResume(t *testing.T) {
	_, r := restoredRoom(t, 6, nil)
	now := t0.Add(time.Minute)
	a, b := r.order[0], r.order[1]
	leaver := a
	for _, id := range r.order {
		if id != a && id != r.members[a].partner {
			leaver = id
		}
	}
	_ = b
	// Everyone but the leaver's team is back; the leaver's team never will be.
	for _, id := range r.order {
		if r.members[id].team != r.members[leaver].team {
			r.Handle(now, id, Connect{})
		}
	}
	if _, paused := r.Paused(); !paused {
		t.Fatal("should still be waiting for the missing team")
	}
	// Their deliberate drop-out (a leave from a returning player) ends the wait: with
	// one team left, the game is won by forfeit.
	r.Handle(now, leaver, Connect{})
	outs, err := r.Handle(now, leaver, Leave{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Phase() != PhaseOver {
		t.Errorf("phase = %v, outs %v", r.Phase(), len(outs))
	}
	if _, paused := r.Paused(); paused {
		t.Error("a finished game is not paused")
	}
}

func TestRestoreRefusesWhatItCannotRebuild(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.playing(4)
	good := savedFrom(t, h, recs)

	old := good
	old.Since = h.now.Add(-25 * time.Hour)
	if _, err := Restore(DefaultConfig(), old, h.now); err == nil {
		t.Error("a game left paused longer than the limit is not restored")
	}
	bad := good
	bad.Commands = append([]engine.Command{engine.Pass{Player: "p00", Card: 0}}, good.Commands...)
	if _, err := Restore(DefaultConfig(), bad, h.now); err == nil {
		t.Error("a log that does not replay is refused")
	}
	h2, recs2 := recordedAll(t, DefaultConfig())
	h2.playing(4)
	cfg := DefaultConfig()
	_ = cfg
	over := savedFrom(t, h2, recs2)
	over.Commands = append(over.Commands, engine.RemoveTeam{Team: 0})
	if _, err := Restore(DefaultConfig(), over, h2.now); err == nil {
		t.Error("a game that was already over is not restored")
	}
}

func TestAServerCanPauseARunningGame(t *testing.T) {
	h := newHarness(t, 7)
	h.playing(4)
	until := h.now.Add(time.Hour)
	outs := h.r.Pause(h.now, until)
	if !anyMsg[Paused](outs) {
		t.Fatal("no Paused message")
	}
	if u, paused := h.r.Paused(); !paused || !u.Equal(until) {
		t.Errorf("paused until %v, %v", u, paused)
	}
	if err := h.r.checkInvariants(); err != nil {
		t.Error(err)
	}
	// Pausing a lobby does nothing.
	h2 := newHarness(t, 8)
	if outs := h2.r.Pause(h2.now, until); len(outs) != 0 {
		t.Error("lobbies are not paused")
	}
}

func sameSet(a, b []engine.PlayerID) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
