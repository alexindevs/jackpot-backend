package room

import (
	"math/rand/v2"
	"testing"
	"time"
)

// newShortHarness is a recorded harness whose creator asked for a short game.
func newShortHarness(t *testing.T) (*harness, *[]Record) {
	t.Helper()
	var recs []Record
	cfg := DefaultConfig()
	cfg.OnRecord = func(r Record) { recs = append(recs, r) }
	r, outs := New(cfg, "room1", pid(0), rand.New(rand.NewPCG(60, 61)), t0, Short())
	return &harness{t: t, r: r, now: t0, last: outs}, &recs
}

func TestAShortGameEndsAtFourWins(t *testing.T) {
	h, recs := newShortHarness(t)
	h.startGame(4)

	gs := msgs[GameStarting](h.last, pid(0))
	if len(gs) != 1 || gs[0].WinsToFinish != 4 {
		t.Fatalf("game_starting = %+v, want 4 wins to finish", gs)
	}
	var started GameStarted
	for _, rec := range *recs {
		if v, ok := rec.(GameStarted); ok {
			started = v
		}
	}
	if started.Setup.Config.WinsToFinish != 4 {
		t.Errorf("the saved setup says %d wins, so a restart would replay the wrong game", started.Setup.Config.WinsToFinish)
	}
}

func TestALongGameIsTheDefault(t *testing.T) {
	h := newHarness(t, 22)
	if h.r.Info().Short || h.r.snapshotFor(pid(0)).Short {
		t.Fatal("a room is long unless the creator asks for short")
	}
	h.startGame(4)
	if gs := msgs[GameStarting](h.last, pid(0)); len(gs) != 1 || gs[0].WinsToFinish != 7 {
		t.Fatalf("game_starting = %+v, want 7 wins to finish", gs)
	}
}

func TestTheLengthIsShownInTheRoomListAndTheSnapshot(t *testing.T) {
	h, _ := newShortHarness(t)
	if !h.r.Info().Short {
		t.Error("the room list should say the game is short")
	}
	if !h.r.snapshotFor(pid(0)).Short {
		t.Error("the snapshot should say the game is short, so a joiner can see it in the lobby")
	}
}

func TestAShortLobbyComesBackShortAfterARestart(t *testing.T) {
	h, recs := newShortHarness(t)
	h.joinN(4)
	got := lobbyStates(recs)
	last := got[len(got)-1]
	if !last.Short {
		t.Fatal("the saved lobby should remember the game is short")
	}

	r, err := RestoreLobby(DefaultConfig(), savedLobbyOf(last), h.now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Info().Short {
		t.Error("a restored short lobby should still be short")
	}
}
