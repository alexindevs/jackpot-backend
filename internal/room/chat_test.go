package room

import (
	"strings"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func chatsTo(outs []Out, to engine.PlayerID) []ChatMessage { return msgs[ChatMessage](outs, to) }

func TestEveryoneInTheRoomHearAPublicChatLine(t *testing.T) {
	h := newHarness(t, 70)
	h.playing(6)
	h.must("fan", Spectate{})
	leaver := pid(1)
	h.must(leaver, Leave{}) // removed players keep watching
	speaker := pid(0)
	if speaker == leaver || speaker == h.partnerOf(leaver) {
		speaker = pid(2)
	}
	if speaker == leaver || speaker == h.partnerOf(leaver) {
		speaker = pid(3)
	}
	h.must(speaker, Chat{Text: "I said it first"})

	for _, id := range h.r.audience() {
		got := chatsTo(h.last, id)
		if len(got) != 1 || got[0].From != speaker || got[0].Text != "I said it first" || got[0].ID != 1 {
			t.Errorf("%s got %+v", id, got)
		}
	}
	if got := chatsTo(h.last, "fan"); len(got) != 1 {
		t.Error("spectators read the chat")
	}
	if got := chatsTo(h.last, leaver); len(got) != 1 {
		t.Error("removed players read the chat")
	}
}

func TestOnlyPlayersStillInTheGameMayPost(t *testing.T) {
	h := newHarness(t, 71)
	h.playing(6)
	h.must("fan", Spectate{})
	h.mustFail("fan", Chat{Text: "hi"}, ErrNotPlayer)
	h.mustFail("stranger", Chat{Text: "hi"}, ErrNotPlayer)

	leaver := pid(2)
	h.must(leaver, Leave{})
	h.mustFail(leaver, Chat{Text: "hi"}, ErrNotPlayer)
	h.mustFail(h.partnerOf(leaver), Chat{Text: "hi"}, ErrNotPlayer)
}

func TestPublicChatOpensWithTheGameAndStaysOpenAfterIt(t *testing.T) {
	h := newHarness(t, 72)
	h.joinN(4)
	h.mustFail(pid(0), Chat{Text: "hi"}, ErrWrongPhase) // lobby: there is no game to attach it to

	h.advance(2 * time.Minute) // the game starts, and the private chat phase begins
	h.must(pid(0), Chat{Text: "between rounds too"})
	h.startRound()
	h.must(pid(1), Chat{Text: "during a round"})

	h.must(pid(2), VoteEnd{Agree: true})
	h.must(pid(3), VoteEnd{Agree: true})
	h.must(pid(0), VoteEnd{Agree: true})
	h.must(pid(1), VoteEnd{Agree: true})
	if h.r.Phase() != PhaseOver {
		t.Fatalf("phase = %v", h.r.Phase())
	}
	h.must(pid(0), Chat{Text: "gg, but that last call was disputed"}) // disputes are argued after the game
	if h.r.chatSeq != 3 {
		t.Errorf("chatSeq = %d, want 3", h.r.chatSeq)
	}
}

func TestChatLinesAreNumberedAndOneTidyLine(t *testing.T) {
	h := newHarness(t, 73)
	h.playing(4)
	h.must(pid(0), Chat{Text: "  two\nlines\tand\rtabs  "})
	h.must(pid(1), Chat{Text: "second"})
	log := h.r.chatLog
	if len(log) != 2 || log[0].ID != 1 || log[1].ID != 2 {
		t.Fatalf("log = %+v", log)
	}
	if log[0].Text != "two lines and tabs" {
		t.Errorf("text = %q", log[0].Text)
	}
	h.mustFail(pid(0), Chat{Text: ""}, ErrBadText)
	h.mustFail(pid(0), Chat{Text: " \n\t "}, ErrBadText)
	h.mustFail(pid(0), Chat{Text: strings.Repeat("é", 501)}, ErrBadText)
	h.must(pid(0), Chat{Text: strings.Repeat("é", 500)}) // 500 characters, though more than 500 bytes
	if h.r.chatSeq != 3 {
		t.Errorf("rejected messages must not use up numbers: chatSeq = %d", h.r.chatSeq)
	}
}

func TestPostingTooFastIsRefusedAndRecovers(t *testing.T) {
	cfg := DefaultConfig() // 1 a second, burst of 5
	h := newHarnessCfg(t, 74, cfg)
	h.playing(4)
	for i := 0; i < 5; i++ {
		h.must(pid(0), Chat{Text: "spam"})
	}
	h.mustFail(pid(0), Chat{Text: "spam"}, ErrSlowDown)
	// Someone else is not affected.
	h.must(pid(1), Chat{Text: "hello"})

	h.now = h.now.Add(1100 * time.Millisecond)
	h.must(pid(0), Chat{Text: "back"})
	h.mustFail(pid(0), Chat{Text: "again"}, ErrSlowDown)
	if h.r.chatSeq != 7 {
		t.Errorf("chatSeq = %d, want 7 (refused lines take no number)", h.r.chatSeq)
	}
}

func TestASnapshotCarriesTheRecentChat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ChatRatePerSec, cfg.ChatBurst = 1000, 1000
	h := newHarnessCfg(t, 75, cfg)
	h.playing(4)
	for i := 1; i <= 120; i++ {
		h.must(pid(i%4), Chat{Text: "line " + string(rune('a'+i%26))})
	}
	h.must(pid(0), Disconnect{})
	h.must(pid(0), Connect{})
	snap := msgs[Snapshot](h.last, pid(0))[0]
	if len(snap.Chat) != snapshotChat {
		t.Fatalf("snapshot has %d lines, want %d", len(snap.Chat), snapshotChat)
	}
	if snap.Chat[0].ID != 71 || snap.Chat[len(snap.Chat)-1].ID != 120 {
		t.Errorf("snapshot covers %d to %d, want 71 to 120", snap.Chat[0].ID, snap.Chat[len(snap.Chat)-1].ID)
	}
	if len(h.r.chatLog) != maxChatLog {
		t.Errorf("only %d lines should be kept in memory, have %d", maxChatLog, len(h.r.chatLog))
	}
}

func TestChatLinesAreRecordedForStorage(t *testing.T) {
	h, recs := recorded(t, DefaultConfig())
	h.playing(4)
	h.must(pid(2), Chat{Text: "a\nb"})
	h.must(pid(3), Chat{Text: "c"})
	var chats []ChatPosted
	for _, r := range *recs {
		if c, ok := r.(ChatPosted); ok {
			chats = append(chats, c)
		}
	}
	if len(chats) != 2 || chats[0].Room != "room1" || chats[0].Seq != 1 || chats[0].Player != pid(2) || chats[0].Text != "a b" || chats[1].Seq != 2 {
		t.Errorf("chat records = %+v", chats)
	}
	// A refused line is not recorded.
	_ = h.do(pid(2), Chat{Text: ""})
	n := 0
	for _, r := range *recs {
		if _, ok := r.(ChatPosted); ok {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d chat records, want 2", n)
	}
}

func TestSpectatorsJoiningLaterGetTheChatInTheirSnapshot(t *testing.T) {
	h := newHarness(t, 76)
	h.playing(4)
	h.must(pid(0), Chat{Text: "before you arrived"})
	h.must("late", Spectate{})
	snap := msgs[Snapshot](h.last, "late")[0]
	if len(snap.Chat) != 1 || snap.Chat[0].Text != "before you arrived" {
		t.Errorf("snapshot chat = %+v", snap.Chat)
	}
}
