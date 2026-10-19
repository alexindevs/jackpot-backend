package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// recorded collects the records worth a summary: the game starting, rounds
// won, the game ending. The per-command log is left out; see recordedAll.
func recorded(t *testing.T, cfg Config) (*harness, *[]Record) {
	t.Helper()
	var recs []Record
	cfg.OnRecord = func(r Record) {
		if _, ok := r.(CommandApplied); !ok {
			recs = append(recs, r)
		}
	}
	return newHarnessCfg(t, 60, cfg), &recs
}

func recordedAll(t *testing.T, cfg Config) (*harness, *[]Record) {
	t.Helper()
	var recs []Record
	cfg.OnRecord = func(r Record) { recs = append(recs, r) }
	return newHarnessCfg(t, 60, cfg), &recs
}

func TestAGameIsRecordedFromStartToFinish(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Engine.WinsToFinish = 1
	h, recs := recorded(t, cfg)
	h.playing(4)

	if len(*recs) != 1 {
		t.Fatalf("after the start: %v", *recs)
	}
	gs, ok := (*recs)[0].(GameStarted)
	if !ok || gs.Room != "room1" || len(gs.Teams) != 2 || gs.Creator != pid(0) || len(gs.Setup.Specs) != 4 {
		t.Fatalf("GameStarted = %+v", (*recs)[0])
	}
	// Team i in the record is team i in the engine.
	for ti, team := range gs.Teams {
		for _, id := range team {
			if h.r.members[id].team != engine.TeamID(ti) {
				t.Errorf("%s is in record team %d but engine team %d", id, ti, h.r.members[id].team)
			}
		}
	}

	target := pid(1)
	h.driveCircles(target, false)
	proclaimer := h.partnerOf(target)
	h.must(proclaimer, Proclaim{})

	if len(*recs) != 3 {
		t.Fatalf("records = %v", *recs)
	}
	won, ok := (*recs)[1].(RoundWon)
	if !ok || won.Round != 1 || won.Proclaimer != proclaimer || won.Holder != target || won.Team != h.r.members[target].team {
		t.Errorf("RoundWon = %+v", (*recs)[1])
	}
	fin, ok := (*recs)[2].(GameFinished)
	if !ok || fin.Abandoned || fin.Result == nil || fin.Result.Reason != engine.EndWins || fin.Result.Winner != won.Team {
		t.Errorf("GameFinished = %+v", (*recs)[2])
	}
}

func TestEachRoundWinIsRecordedWithItsNumber(t *testing.T) {
	h, recs := recorded(t, DefaultConfig())
	h.playing(4)
	for round := 1; round <= 2; round++ {
		target := pid(1)
		h.driveCircles(target, false)
		h.must(h.partnerOf(target), Proclaim{})
		if round == 1 {
			h.startRoundFromChat(t)
		}
	}
	var rounds []int
	for _, r := range *recs {
		if w, ok := r.(RoundWon); ok {
			rounds = append(rounds, w.Round)
		}
	}
	if len(rounds) != 2 || rounds[0] != 1 || rounds[1] != 2 {
		t.Errorf("round numbers = %v", rounds)
	}
}

func TestAForfeitIsRecordedAsFinishedNotAbandoned(t *testing.T) {
	h, recs := recorded(t, DefaultConfig())
	h.playing(4)
	h.must(pid(0), Leave{})
	last := (*recs)[len(*recs)-1]
	fin, ok := last.(GameFinished)
	if !ok || fin.Abandoned || fin.Result == nil || fin.Result.Reason != engine.EndForfeit || fin.Result.CountsForStats {
		t.Errorf("last record = %+v", last)
	}
}

func TestAGameThatEveryoneWalksAwayFromIsAbandoned(t *testing.T) {
	h, recs := recorded(t, DefaultConfig())
	h.playing(4)
	for i := 0; i < 4; i++ {
		h.must(pid(i), Disconnect{})
	}
	h.advance(time.Minute)
	if _, paused := h.r.Paused(); !paused {
		t.Fatal("everyone gone pauses the game; it is not abandoned yet")
	}
	h.advance(h.r.cfg.PausedFor)
	if h.r.Phase() != PhaseClosed {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	fin, ok := (*recs)[len(*recs)-1].(GameFinished)
	if !ok || !fin.Abandoned || fin.Result != nil {
		t.Errorf("last record = %+v", (*recs)[len(*recs)-1])
	}
}

func TestLobbiesAndFinishedGamesProduceNoAbandonedRecord(t *testing.T) {
	// A room that never became a game records nothing.
	h, recs := recorded(t, DefaultConfig())
	h.must(pid(0), Disconnect{})
	h.advance(time.Minute)
	if h.r.Phase() != PhaseClosed || len(*recs) != 0 {
		t.Errorf("phase %v, records %v", h.r.Phase(), *recs)
	}

	// A finished game that is later closed is not "abandoned" as well.
	cfg := DefaultConfig()
	cfg.Engine.WinsToFinish = 1
	h, recs = recorded(t, cfg)
	h.playing(4)
	h.driveCircles(pid(1), false)
	h.must(h.partnerOf(pid(1)), Proclaim{})
	n := len(*recs)
	for i := 0; i < 4; i++ {
		h.must(pid(i), Disconnect{})
	}
	h.advance(time.Minute)
	if len(*recs) != n {
		t.Errorf("closing a finished game recorded more: %v", (*recs)[n:])
	}
}

func TestEveryAppliedCommandIsRecordedInOrder(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.playing(4)
	holder := h.hold()
	h.must(holder, Pass{Card: engine.CardType(0)})
	var cmds []CommandApplied
	for _, r := range *recs {
		if c, ok := r.(CommandApplied); ok {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) < 2 {
		t.Fatalf("commands recorded: %v", cmds)
	}
	for i, c := range cmds {
		if c.Seq != i+1 {
			t.Errorf("command %d has seq %d", i, c.Seq)
		}
	}
	if _, ok := cmds[0].Command.(engine.StartRound); !ok {
		t.Errorf("the first command is %T", cmds[0].Command)
	}
	if p, ok := cmds[1].Command.(engine.Pass); !ok || p.Player != holder {
		t.Errorf("the second command is %#v", cmds[1].Command)
	}
}
