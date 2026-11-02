package room

import (
	"slices"
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

func TestThreePeopleWithoutPartnersEachGetABotPartner(t *testing.T) {
	h := newHarness(t, 108)
	h.must(pid(1), Join{})
	h.must(pid(2), Join{})
	h.must(pid(0), AddBots{})
	if len(h.r.members) != 6 || botsIn(h) != 3 {
		t.Fatalf("members %d, bots %d, want 6 and 3", len(h.r.members), botsIn(h))
	}
	for _, id := range []engine.PlayerID{pid(0), pid(1), pid(2)} {
		if p := h.partnerOf(id); p == "" || !h.r.members[p].bot {
			t.Errorf("%s should have a bot partner, has %q", id, p)
		}
	}
}

func TestBreakingAPairLetsBothPairUpWithSomeoneElse(t *testing.T) {
	h := newHarness(t, 109)
	h.joinN(4) // p00..p03
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.mustFail(pid(0), InvitePartner{To: pid(2)}, ErrAlreadyPaired) // paired people cannot pair again...

	h.must(pid(0), BreakPartner{}) // ...until the pair is broken
	if h.partnerOf(pid(0)) != "" || h.partnerOf(pid(1)) != "" {
		t.Fatal("both should be free")
	}
	if got := msgs[TeamBroken](h.last, pid(2)); len(got) != 1 || got[0].A != pid(0) || got[0].B != pid(1) {
		t.Errorf("the room should hear team_broken: %v", got)
	}
	h.must(pid(0), InvitePartner{To: pid(2)})
	h.must(pid(2), AcceptPartner{From: pid(0)})
	h.must(pid(1), InvitePartner{To: pid(3)})
	h.must(pid(3), AcceptPartner{From: pid(1)})
	if h.partnerOf(pid(0)) != pid(2) || h.partnerOf(pid(1)) != pid(3) {
		t.Error("they should be able to pair with someone else")
	}
	h.must(pid(0), BreakPartner{})
	h.must(pid(0), InvitePartner{To: pid(2)}) // and with each other again
	h.must(pid(2), AcceptPartner{From: pid(0)})
}

func TestBreakingFromABotPartnerRemovesTheBot(t *testing.T) {
	h := newHarness(t, 110)
	h.must(pid(0), AddBots{})
	bot := h.partnerOf(pid(0))
	h.must(pid(0), BreakPartner{})
	if h.r.members[bot] != nil || h.partnerOf(pid(0)) != "" {
		t.Error("the bot partner should go and the person should be free")
	}
}

func TestBreakPartnerRefusals(t *testing.T) {
	h := newHarness(t, 111)
	h.must(pid(1), Join{})
	h.mustFail(pid(0), BreakPartner{}, ErrNotPaired)
	h.mustFail("fan", BreakPartner{}, ErrNotMember)
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.must(pid(0), AddBots{})
	h.must(pid(0), StartNow{})
	h.mustFail(pid(0), BreakPartner{}, ErrWrongPhase)
}

// The bug that was found in play: a winner left the finished game, and a
// watcher who came later could not be told who had won.
func TestTheResultStillNamesTheWinnersAfterTheyLeave(t *testing.T) {
	h := newHarness(t, 112)
	h.startGame(4)
	h.startRound()
	h.must(pid(0), Leave{}) // a team drops out; the other wins by forfeit
	if h.r.Phase() != PhaseOver {
		t.Fatalf("phase = %v: with one team left the game is won by forfeit", h.r.Phase())
	}
	res := h.r.Game().Result()
	winners := append([]engine.PlayerID(nil), res.Teams[res.Winner]...)
	if len(winners) != 2 {
		t.Fatalf("the winning team = %v", winners)
	}
	for _, id := range winners { // the winners leave after the game is over
		h.must(id, Leave{})
		if h.r.members[id] != nil {
			t.Fatalf("%s should be gone from the room's players", id)
		}
	}
	got := h.r.snapshotFor(h.watch("fan")).Game.Result
	if got == nil || len(got.Teams) != 2 {
		t.Fatalf("result = %+v", got)
	}
	if !slices.Equal(got.Teams[res.Winner], winners) {
		t.Errorf("the winning team must still name the players who left: %v, want %v", got.Teams[res.Winner], winners)
	}
}
