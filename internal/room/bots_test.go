package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/botname"
	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func botsIn(h *harness) (n int) {
	for _, m := range h.r.members {
		if m.bot {
			n++
		}
	}
	return
}

func TestOnePersonAddsBotsAndEveryoneHasAPartner(t *testing.T) {
	h := newHarness(t, 100)
	h.must(pid(0), AddBots{})
	if len(h.r.members) != 4 || botsIn(h) != 3 {
		t.Fatalf("members %d, bots %d, want 4 and 3", len(h.r.members), botsIn(h))
	}
	for id, m := range h.r.members {
		if m.partner == "" || h.r.members[m.partner].partner != id {
			t.Errorf("%s has no matching partner", id)
		}
		if m.bot && (!m.connected || !botname.IsBotID(id)) {
			t.Errorf("%s: a bot is always connected and has a bot id", id)
		}
	}
	if !h.r.members[h.partnerOf(pid(0))].bot {
		t.Error("the person's partner should be a bot")
	}
	if !h.r.Info().Joinable == false && h.r.Info().Bots != 3 {
		t.Errorf("info = %+v", h.r.Info())
	}
	for _, p := range h.r.snapshotFor(pid(0)).Players {
		if p.Bot != botname.IsBotID(p.ID) {
			t.Errorf("snapshot marks %s bot=%v", p.ID, p.Bot)
		}
	}
}

func TestTwoPeopleGetABotTeamToPlayAgainst(t *testing.T) {
	h := newHarness(t, 101)
	h.must(pid(1), Join{})
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.must(pid(0), AddBots{})
	if len(h.r.members) != 4 || botsIn(h) != 2 {
		t.Errorf("members %d, bots %d: two people paired need one team of bots", len(h.r.members), botsIn(h))
	}
}

func TestAddBotsRefusals(t *testing.T) {
	h := newHarness(t, 102)
	h.must(pid(1), Join{})
	h.mustFail(pid(1), AddBots{}, ErrNotCreator)
	h.mustFail(pid(0), AddBots{Teams: -1}, ErrBadTarget)
	h.must(pid(0), AddBots{})
	h.mustFail(pid(0), AddBots{}, ErrNothingToAdd) // nobody left without a partner
	h.mustFail(pid(0), AddBots{Teams: 5}, ErrRoomFull)
	h.mustFail(pid(0), RemoveBot{Player: pid(1)}, ErrNotABot)
	h.mustFail(pid(1), RemoveBot{Player: h.partnerOf(pid(0))}, ErrNotCreator)
	h.startGame(0)
	h.mustFail(pid(0), AddBots{Teams: 1}, ErrWrongPhase)
}

func TestARoomFilledWithBotsStartsAtOnce(t *testing.T) {
	h := newHarness(t, 103)
	h.must(pid(0), AddBots{Teams: 4}) // a bot partner and four more teams
	if h.r.Phase() != PhaseChat {
		t.Fatalf("a full room starts at once; phase = %v", h.r.Phase())
	}
	if botsIn(h) != 9 {
		t.Errorf("bots = %d, want 9", botsIn(h))
	}
}

func TestRemovingBots(t *testing.T) {
	h := newHarness(t, 104)
	h.must(pid(0), AddBots{Teams: 2})
	partner := h.partnerOf(pid(0))
	h.must(pid(0), RemoveBot{Player: partner})
	if h.r.members[partner] != nil || h.r.members[pid(0)].partner != "" {
		t.Error("removing a person's bot partner leaves them without a partner")
	}
	var botTeamMember engine.PlayerID
	for id, m := range h.r.members {
		if m.bot {
			botTeamMember = id
		}
	}
	other := h.partnerOf(botTeamMember)
	h.must(pid(0), RemoveBot{Player: botTeamMember})
	if h.r.members[botTeamMember] != nil || h.r.members[other] != nil {
		t.Error("a bot team goes together")
	}
}

func TestThePersonLeavingTakesTheirBotPartnerAndAnEmptyLobbyCloses(t *testing.T) {
	h := newHarness(t, 105)
	h.must(pid(0), AddBots{})
	h.must(pid(0), Leave{})
	if h.r.Phase() != PhaseClosed {
		t.Errorf("a lobby of bots with nobody in it must close; phase = %v", h.r.Phase())
	}
}

func TestAGameWithBotsDoesNotCountForStats(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.must(pid(0), AddBots{})
	h.must(pid(0), StartNow{})
	for _, r := range *recs {
		if g, ok := r.(GameStarted); ok {
			if !g.Setup.Config.NoStats {
				t.Error("a game with bots must be set up with NoStats")
			}
			return
		}
	}
	t.Error("no GameStarted record")
}

func TestAGameWithBotsEndsWhenTheLastPersonLeaves(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.must(pid(0), AddBots{Teams: 2}) // three teams, so two bot teams would be left playing each other
	h.must(pid(0), StartNow{})
	h.must(pid(0), Leave{}) // drops their team; only bots are left
	if h.r.Phase() != PhaseClosed {
		t.Fatalf("phase = %v: with no person left the game must end", h.r.Phase())
	}
	abandoned := false
	for _, r := range *recs {
		if f, ok := r.(GameFinished); ok && f.Abandoned {
			abandoned = true
		}
	}
	if !abandoned {
		t.Error("the game should be recorded as abandoned")
	}
}

func TestAGameWithBotsWaitsOnlyBrieflyForAPersonWhoDisconnects(t *testing.T) {
	h := newHarness(t, 106)
	h.must(pid(0), AddBots{})
	h.must(pid(0), StartNow{})
	h.must(pid(0), Disconnect{})
	h.advance(time.Minute) // nobody connected for a minute: paused
	if _, paused := h.r.Paused(); !paused {
		t.Fatal("the game should pause when its only person is gone")
	}
	h.advance(9 * time.Minute)
	if h.r.Phase() == PhaseClosed {
		t.Fatal("it should still be waiting before 10 minutes are up")
	}
	h.advance(2 * time.Minute)
	if h.r.Phase() != PhaseClosed {
		t.Errorf("after the wait the game ends; phase = %v", h.r.Phase())
	}
}

func TestARestoredGameKeepsItsBotsConnected(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.must(pid(0), AddBots{})
	h.must(pid(0), StartNow{})
	h.must(pid(0), Ready{Ready: true})
	r, err := Restore(DefaultConfig(), savedFrom(t, h, recs), h.now)
	if err != nil {
		t.Fatal(err)
	}
	for id, m := range r.members {
		if m.bot != botname.IsBotID(id) || m.bot != m.connected {
			t.Errorf("%s restored with bot=%v connected=%v", id, m.bot, m.connected)
		}
	}
	// The person coming back is enough to resume it: bots are never "away".
	if _, err := r.Handle(h.now, pid(0), Connect{}); err != nil {
		t.Fatal(err)
	}
	if _, paused := r.Paused(); paused {
		t.Error("the game should resume once its person is back")
	}
}

func TestWithOneBotTeamLeftTheGameEndsByForfeitAndDoesNotCount(t *testing.T) {
	h := newHarness(t, 107)
	h.must(pid(0), AddBots{}) // two teams
	h.must(pid(0), StartNow{})
	h.must(pid(0), Leave{})
	res := h.r.Game().Result()
	if h.r.Phase() != PhaseOver || res == nil || res.Reason != engine.EndForfeit || res.CountsForStats {
		t.Errorf("phase %v, result %+v", h.r.Phase(), res)
	}
}
