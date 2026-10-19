package room

import (
	"strings"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func signalHarness(t *testing.T, seed uint64, teamChance float64) *harness {
	cfg := DefaultConfig()
	cfg.TeamSignalChance = teamChance
	cfg.SignalBurst, cfg.SignalRatePerSec = 1000000, 1e6
	h := newHarnessCfg(t, seed, cfg)
	h.playing(4)
	return h
}

func (h *harness) signal(from engine.PlayerID, g string) []Out {
	h.t.Helper()
	outs, err := h.r.Handle(h.now, from, SendSignal{Gesture: g})
	if err != nil {
		h.t.Fatalf("signal: %v", err)
	}
	return outs
}

func TestATeamSignalReachesOnlyThePartner(t *testing.T) {
	h := signalHarness(t, 1, 1)
	from := h.r.order[0]
	partner := h.partnerOf(from)
	outs := h.signal(from, "blink_once")
	for _, id := range h.r.order {
		got := msgs[Signal](outs, id)
		switch id {
		case from:
			if len(got) != 1 || got[0].Scope != "" {
				t.Errorf("the sender gets one copy with no scope: %+v", got)
			}
		case partner:
			if len(got) != 1 || got[0].Scope != "team" || got[0].From != from || got[0].Gesture != "blink_once" {
				t.Errorf("partner got %+v", got)
			}
		default:
			if len(got) != 0 {
				t.Errorf("%s should not see a team signal: %+v", id, got)
			}
		}
	}
}

func TestARoomSignalReachesEveryoneIncludingSpectators(t *testing.T) {
	h := signalHarness(t, 2, 0)
	if _, err := h.r.Handle(h.now, "watcher", Spectate{}); err != nil {
		t.Fatal(err)
	}
	from := h.r.order[0]
	outs := h.signal(from, "cough")
	for _, id := range append(append([]engine.PlayerID{}, h.r.order...), "watcher") {
		got := msgs[Signal](outs, id)
		want := "room"
		if id == from {
			want = ""
		}
		if len(got) != 1 || got[0].Scope != want {
			t.Errorf("%s got %+v, want scope %q", id, got, want)
		}
	}
}

func TestSignalsRollTheirVisibilityAboutAsConfigured(t *testing.T) {
	h := signalHarness(t, 3, 0.2)
	from := h.r.order[0]
	team := 0
	const n = 2000
	for i := 0; i < n; i++ {
		outs := h.signal(from, "nod")
		if s := msgs[Signal](outs, h.partnerOf(from)); len(s) == 1 && s[0].Scope == "team" {
			team++
		}
	}
	if share := float64(team) / n; share < 0.16 || share > 0.24 {
		t.Errorf("team-only share = %.3f, want about 0.2", share)
	}
}

func TestSignalsAreOnlyForARunningRound(t *testing.T) {
	h := newHarness(t, 4)
	h.startGame(4) // the pre-round chat, not a round
	h.mustFail(h.r.order[0], SendSignal{Gesture: "blink_once"}, ErrWrongPhase)
}

func TestUnknownGesturesAreRefused(t *testing.T) {
	h := signalHarness(t, 5, 0.2)
	h.mustFail(h.r.order[0], SendSignal{Gesture: "backflip"}, ErrBadGesture)
}

func TestOnlyPlayersStillInTheGameCanSignal(t *testing.T) {
	h := signalHarness(t, 6, 0.2)
	h.mustFail("watcher", SendSignal{Gesture: "blink_once"}, ErrNotPlayer)
}

func TestSignalsAreRateLimited(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SignalBurst, cfg.SignalRatePerSec = 3, 1
	h := newHarnessCfg(t, 7, cfg)
	h.playing(4)
	from := h.r.order[0]
	for i := 0; i < 3; i++ {
		h.signal(from, "blink_once")
	}
	h.mustFail(from, SendSignal{Gesture: "blink_once"}, ErrSlowDown)
	h.advance(2 * time.Second)
	h.signal(from, "blink_once")
}

func TestSnapshotsListTheGestures(t *testing.T) {
	h := signalHarness(t, 8, 0.2)
	s := h.r.snapshotFor(h.r.order[0])
	if len(s.Gestures) != len(DefaultConfig().Gestures) || s.Gestures[0].ID != "blink_once" {
		t.Errorf("gestures = %v", s.Gestures)
	}
}

func TestSignalsAreKeptForReplayAfterAReconnect(t *testing.T) {
	h := signalHarness(t, 9, 0)
	from, other := h.r.order[0], h.r.order[1]
	before := h.r.seqs[other]
	h.signal(from, "sigh")
	outs, _ := h.r.Handle(h.now, other, Resume{After: before})
	m := msgs[Missed](outs, other)[0]
	if len(m.Messages) != 1 {
		t.Fatalf("missed = %+v", m)
	}
	if _, ok := m.Messages[0].Msg.(Signal); !ok {
		t.Errorf("replayed %T", m.Messages[0].Msg)
	}
}

func TestTheVocabularyIsAboutTwoHundredDistinctGestures(t *testing.T) {
	g := DefaultGestures()
	if len(g) < 190 || len(g) > 210 {
		t.Errorf("%d gestures, want about 200", len(g))
	}
	ids, labels := map[string]bool{}, map[string]bool{}
	for _, x := range g {
		if x.ID == "" || x.Label == "" || x.Seen == "" || x.Group == "" {
			t.Errorf("incomplete gesture %+v", x)
		}
		if ids[x.ID] {
			t.Errorf("duplicate id %q", x.ID)
		}
		if labels[x.Label] {
			t.Errorf("duplicate label %q", x.Label)
		}
		ids[x.ID], labels[x.Label] = true, true
		if strings.ToLower(x.ID) != x.ID || strings.ContainsAny(x.ID, " -") {
			t.Errorf("id %q should be lower snake_case", x.ID)
		}
	}
}
