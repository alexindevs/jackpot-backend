package room

import (
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func lobbyStates(recs *[]Record) []LobbyState {
	var out []LobbyState
	for _, r := range *recs {
		if l, ok := r.(LobbyState); ok {
			out = append(out, l)
		}
	}
	return out
}

func TestALobbyIsReportedWheneverItChanges(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	if got := lobbyStates(recs); len(got) != 1 || got[0].Gone || len(got[0].Members) != 1 || got[0].Creator != pid(0) {
		t.Fatalf("a new room should report itself: %+v", got)
	}
	h.must(pid(1), Join{})
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	got := lobbyStates(recs)
	last := got[len(got)-1]
	if len(got) != 4 || len(last.Members) != 2 || len(last.Pairs) != 1 || last.Pairs[0] != [2]engine.PlayerID{pid(0), pid(1)} || len(last.Invites) != 0 {
		t.Fatalf("lobby states = %+v", got)
	}
	// A room invitation is not part of the saved lobby: nothing changes.
	h.must(pid(0), InviteToRoom{To: "x"})
	if len(lobbyStates(recs)) != 4 {
		t.Error("a change that doesn't touch the lobby should not be reported")
	}
}

func TestALobbyIsReportedGoneWhenTheGameStartsOrTheRoomCloses(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.startGame(4)
	got := lobbyStates(recs)
	if last := got[len(got)-1]; !last.Gone {
		t.Fatalf("starting the game should report the lobby gone: %+v", last)
	}
	n := len(got)
	h.must(pid(0), Chat{Text: "hi"})
	if len(lobbyStates(recs)) != n {
		t.Error("nothing more should be reported once the game has started")
	}

	h2, recs2 := recordedAll(t, DefaultConfig())
	h2.must(pid(0), Disconnect{})
	h2.advance(DefaultConfig().LobbyEmptyGrace)
	got = lobbyStates(recs2)
	if last := got[len(got)-1]; !last.Gone || h2.r.Phase() != PhaseClosed {
		t.Errorf("a closed lobby is gone: %+v", last)
	}
}

func savedLobbyOf(l LobbyState) SavedLobby {
	return SavedLobby{ID: l.Room, Creator: l.Creator, Members: l.Members, Pairs: l.Pairs, Invites: l.Invites, Since: l.At}
}

func TestARestoredLobbyIsTheSameLobbyPausedUntilItsMembersReturn(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.joinN(4)
	h.must(pid(0), InvitePartner{To: pid(1)})
	h.must(pid(1), AcceptPartner{From: pid(0)})
	h.must(pid(2), InvitePartner{To: pid(3)})
	got := lobbyStates(recs)
	saved := savedLobbyOf(got[len(got)-1])

	now := h.now.Add(10 * time.Minute)
	r, err := RestoreLobby(DefaultConfig(), saved, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, paused := r.Paused(); !paused || r.Phase() != PhaseLobby {
		t.Fatalf("phase %v paused %v", r.Phase(), paused)
	}
	if !sameSet(r.order, h.r.order) || r.creator != pid(0) {
		t.Errorf("members %v creator %v", r.order, r.creator)
	}
	if r.members[pid(0)].partner != pid(1) || r.members[pid(1)].partner != pid(0) || r.members[pid(2)].partner != "" {
		t.Error("pairs not restored")
	}
	if !r.invites[inviteKey{pid(2), pid(3)}] {
		t.Error("the open invitation should still be open")
	}
	if r.dl.has(dkey{kind: dkCountdown}) {
		t.Error("the countdown must not run while members are away")
	}

	// New people can't join and nobody can skip the countdown yet.
	if _, err := r.Handle(now, "late", Join{}); err != ErrPaused {
		t.Errorf("join while paused: %v", err)
	}
	if _, err := r.Handle(now, pid(0), StartNow{}); err != ErrPaused {
		t.Errorf("start_now while paused: %v", err)
	}
	// But the people in it can carry on pairing up.
	for _, id := range []engine.PlayerID{pid(0), pid(1), pid(2)} {
		if _, err := r.Handle(now, id, Connect{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Handle(now, pid(3), Connect{}); err != nil {
		t.Fatal(err)
	}
	outs, err := r.Handle(now, pid(3), AcceptPartner{From: pid(2)})
	if err != nil {
		t.Fatalf("accepting a partner invite in a restored lobby: %v", err)
	}
	_ = outs
	if _, paused := r.Paused(); paused {
		t.Fatal("everyone is back, so the lobby is running again")
	}
	if at, ok := r.dl.get(dkey{kind: dkCountdown}); !ok || !at.Equal(now.Add(r.cfg.LobbyCountdown)) {
		t.Errorf("the countdown should start fresh: %v %v", at, ok)
	}
	if err := r.checkInvariants(); err != nil {
		t.Error(err)
	}
}

func TestARestoredLobbyIsClosedIfNobodyComesBack(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.joinN(2)
	got := lobbyStates(recs)
	saved := savedLobbyOf(got[len(got)-1])
	now := h.now.Add(time.Minute)
	r, err := RestoreLobby(DefaultConfig(), saved, now)
	if err != nil {
		t.Fatal(err)
	}
	until, _ := r.Paused()
	if want := saved.Since.Add(DefaultConfig().LobbyPausedFor); !until.Equal(want) {
		t.Errorf("until %v, want %v", until, want)
	}
	if outs := r.Tick(until); r.Phase() != PhaseClosed || !anyMsg[RoomClosed](outs) {
		t.Errorf("phase = %v", r.Phase())
	}
}

func TestRestoreLobbyRefusesWhatItCannotRebuild(t *testing.T) {
	now := t0.Add(time.Minute)
	good := SavedLobby{ID: "r", Creator: "a", Members: []engine.PlayerID{"a", "b"}, Since: t0}
	if _, err := RestoreLobby(DefaultConfig(), good, now); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*SavedLobby){
		"no members":      func(s *SavedLobby) { s.Members = nil },
		"too old":         func(s *SavedLobby) { s.Since = t0.Add(-2 * time.Hour) },
		"duplicate":       func(s *SavedLobby) { s.Members = []engine.PlayerID{"a", "a"} },
		"pair of a ghost": func(s *SavedLobby) { s.Pairs = [][2]engine.PlayerID{{"a", "zed"}} },
		"twice paired": func(s *SavedLobby) {
			s.Members = []engine.PlayerID{"a", "b", "c"}
			s.Pairs = [][2]engine.PlayerID{{"a", "b"}, {"a", "c"}}
		},
	}
	for name, mod := range cases {
		s := good
		mod(&s)
		if _, err := RestoreLobby(DefaultConfig(), s, now); err == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

// --- private team chat ---

func TestTeamChatIsNumberedRecordedAndShownAgainInTheSnapshot(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.startGame(4)
	a := h.r.order[0]
	partner := h.partnerOf(a)
	h.must(a, TeamChat{Text: "one for the code"})
	h.must(partner, TeamChat{Text: "two"})

	var posted []TeamChatPosted
	for _, r := range *recs {
		if p, ok := r.(TeamChatPosted); ok {
			posted = append(posted, p)
		}
	}
	if len(posted) != 2 || posted[0].Seq != 1 || posted[1].Seq != 2 || posted[0].Player != a || posted[0].Round != 1 || posted[0].Team != h.r.members[a].team {
		t.Fatalf("recorded %+v", posted)
	}
	s := h.r.snapshotFor(a)
	if len(s.TeamChat) != 2 || s.TeamChat[0].Text != "one for the code" || s.TeamChat[1].From != partner {
		t.Errorf("snapshot team chat = %+v", s.TeamChat)
	}
	// Other teams, and spectators, never see it.
	for _, id := range h.r.order {
		if h.r.members[id].team != h.r.members[a].team {
			if got := h.r.snapshotFor(id).TeamChat; len(got) != 0 {
				t.Errorf("%s sees another team's chat: %+v", id, got)
			}
		}
	}
	h.r.Handle(h.now, "watcher", Spectate{})
	if got := h.r.snapshotFor("watcher").TeamChat; len(got) != 0 {
		t.Errorf("a spectator sees team chat: %+v", got)
	}
}

func TestTeamChatSurvivesARestore(t *testing.T) {
	h, recs := recordedAll(t, DefaultConfig())
	h.startGame(4)
	a := h.r.order[0]
	h.must(a, TeamChat{Text: "before the restart"})
	sv := savedFrom(t, h, recs)
	for _, r := range *recs {
		if p, ok := r.(TeamChatPosted); ok {
			sv.TeamChat = append(sv.TeamChat, TeamChatEntry{Seq: p.Seq, Round: p.Round, Team: p.Team, From: p.Player, Text: p.Text, At: p.At})
		}
	}
	r, err := Restore(DefaultConfig(), sv, h.now)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.snapshotFor(a).TeamChat; len(got) != 1 || got[0].Text != "before the restart" || got[0].ID != 1 {
		t.Fatalf("restored team chat = %+v", got)
	}
	// And numbering carries on.
	for _, id := range r.order {
		r.Handle(h.now, id, Connect{})
	}
	if _, err := r.Handle(h.now, a, TeamChat{Text: "after"}); err != nil {
		t.Fatal(err)
	}
	if got := r.snapshotFor(a).TeamChat; len(got) != 2 || got[1].ID != 2 {
		t.Errorf("after: %+v", got)
	}
}
