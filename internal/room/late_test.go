package room

import (
	"slices"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// watch makes someone a watcher of the running game.
func (h *harness) watch(id engine.PlayerID) engine.PlayerID {
	h.t.Helper()
	h.must(id, Spectate{})
	return id
}

// winARound plays the first round to its end, which opens the next pre-round chat.
func (h *harness) winARound() {
	h.t.Helper()
	target := pid(1)
	h.driveCircles(target, false)
	h.must(h.partnerOf(target), Proclaim{})
	if h.r.Phase() != PhaseChat {
		h.t.Fatalf("phase = %v, want chat", h.r.Phase())
	}
}

// fullGame is five teams, in a round: the most a room can hold.
func fullGame(t *testing.T, seed uint64) *harness {
	t.Helper()
	h := newHarness(t, seed)
	h.joinN(10) // a full room starts at once
	h.startRoundFromChat(t)
	return h
}

func TestTwoWatchersBecomeATeamBetweenRounds(t *testing.T) {
	h := newHarness(t, 80)
	h.playing(4)
	h.winARound()

	a := h.watch("late1")
	h.must(a, Join{})
	if !slices.Equal(h.r.waiting, []engine.PlayerID{a}) || len(h.r.game.Teams()) != 2 {
		t.Fatalf("one person alone waits: waiting %v, teams %v", h.r.waiting, h.r.game.Teams())
	}
	if !anyMsg[WaitingChanged](h.last) {
		t.Error("everyone should hear who is waiting")
	}

	b := h.watch("late2")
	h.must(b, Join{})
	if len(h.r.waiting) != 0 || len(h.r.game.Teams()) != 3 {
		t.Fatalf("two waiting become a team: waiting %v, teams %v", h.r.waiting, h.r.game.Teams())
	}
	if got := gameEvents[engine.TeamAdded](h.last, pid(0)); len(got) != 1 || got[0].Members != [2]engine.PlayerID{a, b} {
		t.Errorf("team_added = %v", got)
	}
	for _, id := range []engine.PlayerID{a, b} {
		m := h.r.members[id]
		if m == nil || m.removed || h.r.spectators[id] || h.partnerOf(id) == "" {
			t.Errorf("%s should be a player now: %+v", id, m)
		}
	}
	if h.partnerOf(a) != b || h.r.game.Wins(h.r.members[a].team) != 0 {
		t.Error("they are partners and start with no wins")
	}

	h.startRoundFromChat(t)
	if len(h.r.game.Seats()) != 6 {
		t.Errorf("seats = %v", h.r.game.Seats())
	}
	for _, id := range []engine.PlayerID{a, b} {
		if h.r.game.Hand(id).Total() < 4 {
			t.Errorf("%s was not dealt in", id)
		}
	}
}

func TestWatchersWhoJoinDuringARoundArePairedWhenItEnds(t *testing.T) {
	h := newHarness(t, 81)
	h.playing(4)
	a, b := h.watch("late1"), h.watch("late2")
	h.must(a, Join{})
	h.must(b, Join{})
	if len(h.r.waiting) != 2 || len(h.r.game.Teams()) != 2 {
		t.Fatalf("nobody is dealt in halfway through a round: waiting %v, teams %v", h.r.waiting, h.r.game.Teams())
	}
	h.winARound()
	if len(h.r.waiting) != 0 || len(h.r.game.Teams()) != 3 {
		t.Errorf("when the round ends they become a team: waiting %v, teams %v", h.r.waiting, h.r.game.Teams())
	}
}

func TestSomeoneNewCanJoinARunningGameInOneStep(t *testing.T) {
	h := newHarness(t, 82)
	h.playing(4)
	a := engine.PlayerID("late1")
	h.must(a, Join{}) // not watching yet: joining a running game queues them and lets them watch
	if !h.r.spectators[a] || !slices.Equal(h.r.waiting, []engine.PlayerID{a}) {
		t.Errorf("spectators %v, waiting %v", h.r.spectators, h.r.waiting)
	}
	if !anyMsg[Snapshot](h.last) {
		t.Error("they should get a snapshot to watch with")
	}
}

func TestJoinRefusalsInARunningGame(t *testing.T) {
	h := newHarness(t, 83)
	h.playing(4)
	h.mustFail(pid(0), Join{}, ErrAlreadyMember) // a player cannot join again

	h.r.Pause(h.now, h.now.Add(time.Hour))
	h.mustFail("late1", Join{}, ErrPaused)

	full := fullGame(t, 84)
	full.mustFail("late1", Join{}, ErrRoomFull) // five teams is the most
}

func TestAGameThatIsOverCannotBeJoined(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Engine.WinsToFinish = 1
	h := newHarnessCfg(t, 85, cfg)
	h.playing(4)
	h.driveCircles(pid(1), false)
	h.must(h.partnerOf(pid(1)), Proclaim{})
	if h.r.Phase() != PhaseOver {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	h.mustFail("late1", Join{}, ErrNotJoinable)
}

func TestAPlayerWhoseTeamLeftCanComeBackAsANewTeam(t *testing.T) {
	h := newHarness(t, 86)
	h.startGame(6) // in the first pre-round chat
	gone := pid(1)
	partner := h.partnerOf(gone)
	oldTeam := h.r.members[gone].team
	h.must(gone, Leave{}) // a deliberate drop-out removes the whole team at once
	if !h.r.members[gone].removed || !h.r.members[partner].removed || len(h.r.game.Teams()) != 2 {
		t.Fatalf("the team should be out: %+v", h.r.members[gone])
	}

	h.must(gone, Join{})
	h.must(partner, Join{})
	for _, id := range []engine.PlayerID{gone, partner} {
		m := h.r.members[id]
		if m.removed || m.team == oldTeam || h.r.game.Wins(m.team) != 0 {
			t.Errorf("%s should be on a brand new team with no wins: %+v", id, m)
		}
	}
	if len(h.r.game.Teams()) != 3 || len(h.r.waiting) != 0 {
		t.Errorf("teams %v, waiting %v", h.r.game.Teams(), h.r.waiting)
	}
}

func TestLeavingOrDisconnectingTakesAWatcherOutOfTheLine(t *testing.T) {
	h := newHarness(t, 87)
	h.playing(4)
	a, b := h.watch("late1"), h.watch("late2")
	h.must(a, Join{})
	h.must(b, Join{})
	h.must(a, Leave{})
	if !slices.Equal(h.r.waiting, []engine.PlayerID{b}) {
		t.Errorf("after a leaves, waiting = %v", h.r.waiting)
	}
	h.must(b, Disconnect{})
	if len(h.r.waiting) != 0 {
		t.Errorf("after b disconnects, waiting = %v", h.r.waiting)
	}
}

func TestALateTeamIsRecordedAndSurvivesARestart(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.playing(4)
	h.winARound()
	a, b := h.watch("late1"), h.watch("late2")
	h.must(a, Join{})
	h.must(b, Join{})

	var joined []TeamJoined
	for _, r := range *recs {
		if v, ok := r.(TeamJoined); ok {
			joined = append(joined, v)
		}
	}
	if len(joined) != 1 || joined[0].Members != [2]engine.PlayerID{a, b} {
		t.Fatalf("TeamJoined records = %v", joined)
	}

	r, err := Restore(DefaultConfig(), savedFrom(t, h, recs), h.now)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []engine.PlayerID{a, b} {
		got, was := r.members[id], h.r.members[id]
		if got == nil || got.removed || got.team != was.team || got.partner != was.partner {
			t.Errorf("%s restored as %+v, was %+v", id, got, was)
		}
	}
	if len(r.game.Teams()) != 3 || !sameSet(r.order, h.r.order) {
		t.Errorf("teams %v, order %v (was %v)", r.game.Teams(), r.order, h.r.order)
	}
	if err := r.checkInvariants(); err != nil {
		t.Error(err)
	}
}

func TestTheRoomListSaysWhenAJoinWouldBeAccepted(t *testing.T) {
	h := newHarness(t, 88)
	h.playing(4)
	if !h.r.Info().Joinable {
		t.Error("a running game with room for another team takes join requests")
	}
	full := fullGame(t, 89)
	if full.r.Info().Joinable {
		t.Error("a game of five teams cannot take more")
	}
	h.must(h.watch("late1"), Join{})
	if h.r.Info().Waiting != 1 || len(h.r.snapshotFor(pid(0)).Waiting) != 1 {
		t.Error("who is waiting should show in the room list and the snapshot")
	}
}
