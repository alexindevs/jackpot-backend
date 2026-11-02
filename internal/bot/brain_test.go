package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

const (
	me      = engine.PlayerID("bot:garba")
	partner = engine.PlayerID("bot:sani")
	rivalA  = engine.PlayerID("bot:uyai")
	rivalB  = engine.PlayerID("bot:emem")
)

// view builds the snapshot a bot would get, in a round that is being played.
func view(hand engine.Hand, holder engine.PlayerID) room.Snapshot {
	h := hand
	gestures := make([]room.Gesture, 0, len(easy)+5)
	for _, id := range easy {
		gestures = append(gestures, room.Gesture{ID: id, Label: strings.ReplaceAll(id, "_", " ")})
	}
	gestures = append(gestures, room.Gesture{ID: "belch", Label: "belch"}, room.Gesture{ID: "hiccup", Label: "hiccup"})
	return room.Snapshot{
		Phase: room.PhasePlaying,
		Players: []room.PlayerInfo{
			{ID: me, Team: 0, Partner: partner, Bot: true},
			{ID: partner, Team: 0, Partner: me, Bot: true},
			{ID: rivalA, Team: 1, Partner: rivalB, Bot: true},
			{ID: rivalB, Team: 1, Partner: rivalA, Bot: true},
		},
		Gestures: gestures,
		Game: &room.GameInfo{
			Round: 3, Phase: engine.PhasePlaying, Holder: holder,
			Seats:    []engine.PlayerID{me, rivalA, partner, rivalB},
			Points:   map[engine.PlayerID]int{me: 3, partner: 3, rivalA: 3, rivalB: 3},
			YourHand: &h,
		},
	}
}

func newTest() (*Driver, *brain, time.Time) {
	d := NewDriver(1, Config{})
	b := newBrain(me)
	d.bots[me] = b
	return d, b, t0
}

func TestAPassKeepsTheCollectedTypeAndGivesAwayTheRarest(t *testing.T) {
	d, _, _ := newTest()
	for i := 0; i < 50; i++ {
		got := choosePass(engine.Hand{3, 1, 1, 0}, 0, d)
		if got != 1 && got != 2 {
			t.Fatalf("passed type %d from [3 1 1 0] collecting 0", got)
		}
		if got := choosePass(engine.Hand{2, 1, 2, 0}, 0, d); got != 1 {
			t.Fatalf("passed type %d: the single card is the one to give away", got)
		}
	}
	if got := choosePass(engine.Hand{5, 0, 0, 0}, 0, d); got != 0 {
		t.Errorf("with only the target type in hand it must pass that: %d", got)
	}
}

func TestARevealShowsTheFewestHeldTypesAndTheNumberAsked(t *testing.T) {
	d, b, _ := newTest()
	h := engine.Hand{3, 1, 1, 0}
	b.reveal = &revealReq{required: 2, options: []engine.CardType{0, 1, 2}}
	for i := 0; i < 50; i++ {
		got := b.chooseReveal(h, d)
		if len(got) != 2 || got[0] == 0 || got[1] == 0 {
			t.Fatalf("revealed %v: must show two types and keep the three of a kind hidden", got)
		}
	}
	b.reveal = &revealReq{required: 3, options: []engine.CardType{0, 1, 2}}
	if got := b.chooseReveal(h, d); len(got) != 3 {
		t.Errorf("asked for 3, showed %v", got)
	}
}

func TestABotNeverSuspectsItsOwnPartnerOrTeam(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	for i := 0; i < 20; i++ { // everyone but the rivals is out of bounds
		b.heard = append(b.heard, heard{from: partner, gesture: "nod", scope: "room", at: now}, heard{from: me, gesture: "nod", scope: "room", at: now})
	}
	target, _ := b.mostSuspicious(now, snap, b.me(snap), d)
	if target == partner || target == me || target == "" {
		t.Fatalf("suspect target = %q", target)
	}
	if _, ok := b.build(kSuspect, &intent{arg: string(partner)}, now, snap, d); ok {
		t.Error("it must refuse to suspect its partner even if told to")
	}
	if _, ok := b.build(kSuspect, &intent{arg: string(me)}, now, snap, d); ok {
		t.Error("it must refuse to suspect itself")
	}
	if _, ok := b.build(kSuspect, &intent{arg: string(rivalA)}, now, snap, d); !ok {
		t.Error("a rival with points to spare is fair game")
	}
}

func TestABotSuspectsWhoKeepsRepeatingAGesture(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	b.heard = []heard{
		{from: rivalA, gesture: "cough", scope: "room", at: now}, {from: rivalA, gesture: "cough", scope: "room", at: now.Add(time.Second)},
		{from: rivalB, gesture: "yawn", scope: "room", at: now}, {from: rivalB, gesture: "nod", scope: "room", at: now},
	}
	target, repeats := b.mostSuspicious(now.Add(2*time.Second), snap, b.me(snap), d)
	if target != rivalA || repeats != 2 {
		t.Errorf("target %q, repeats %d: the one repeating a gesture is the tell", target, repeats)
	}
}

func TestABotWithNoPointsDoesNotSuspect(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	snap.Game.Points[me] = 0
	if _, ok := b.build(kSuspect, &intent{arg: string(rivalA)}, now, snap, d); ok {
		t.Error("with 0 points there is nothing to spend")
	}
}

func TestABotCallsJackpotOnlyOnItsPartnersSayAndNotWhenItWouldBeRefused(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	if _, ok := b.build(kProclaim, &intent{}, now, snap, d); ok {
		t.Error("it must not call Jackpot unprompted")
	}
	b.trusted = &heard{from: partner, gesture: "x", at: now}
	if _, ok := b.build(kProclaim, &intent{}, now, snap, d); !ok {
		t.Fatal("its partner said four, so it should call")
	}
	b.trusted = &heard{from: partner, at: now}
	snap.Game.Holder = partner // the partner holds the passing card: the call would be refused
	if _, ok := b.build(kProclaim, &intent{}, now, snap, d); ok {
		t.Error("it must wait while its partner holds the passing card")
	}
	snap.Game.Holder = rivalA
	b.lockedUntil = now.Add(time.Minute) // locked out of calling
	if _, ok := b.build(kProclaim, &intent{}, now, snap, d); ok {
		t.Error("it must not call while locked out")
	}
}

func TestAnyActionIsRefusedWhilePaused(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{2, 1, 1, 1}, me)
	until := now.Add(time.Hour)
	snap.PausedUntil = &until
	for _, k := range []kind{kPass, kSignal, kProclaim, kSuspect, kReveal, kReady, kPost} {
		if _, ok := b.build(k, &intent{arg: string(rivalA)}, now, snap, d); ok {
			t.Errorf("kind %d acted while the game was paused", k)
		}
	}
}

func TestTheCodeIsSharedByPartnersChangesEveryRoundAndNeverRepeats(t *testing.T) {
	d, _, _ := newTest()
	gs := view(engine.Hand{}, me).Gestures
	a := d.codeFor(0, 1, gs)
	if b := d.codeFor(0, 1, gs); a != b {
		t.Error("both partners must get the same code in a round")
	}
	if a.close == a.four || a.close == "" || a.four == "" {
		t.Errorf("a code needs two different gestures: %+v", a)
	}
	for round := 2; round < 30; round++ {
		next := d.codeFor(0, round, gs)
		if next.close == a.close || next.close == a.four || next.four == a.close || next.four == a.four {
			t.Fatalf("round %d reused a gesture from the round before: %+v then %+v", round, a, next)
		}
		a = next
	}
}

func TestABluffNeverUsesTheTeamsOwnCode(t *testing.T) {
	d, _, _ := newTest()
	gs := view(engine.Hand{}, me).Gestures
	c := d.codeFor(0, 1, gs)
	for i := 0; i < 300; i++ {
		if g := d.bluff(gs, c); g == c.close || g == c.four || g == "" {
			t.Fatalf("bluffed with %q", g)
		}
	}
}

func TestWhatABotSaysToItsPartnerIsPlainWords(t *testing.T) {
	d, _, _ := newTest()
	gs := view(engine.Hand{}, me).Gestures
	c := code{close: "cough", four: "tap_table"}
	for i := 0; i < 40; i++ {
		text := d.postText(c, gs)
		if !strings.Contains(text, "cough") || !strings.Contains(text, "tap table") || !strings.Contains(text, "Jackpot") {
			t.Fatalf("the message must name both gestures and what the bot will do: %q", text)
		}
		if strings.ContainsAny(text, "—–") || len(text) > 400 || strings.Contains(text, "%!") {
			t.Fatalf("bad message: %q", text)
		}
	}
}

// A bot may only know what a person in its seat knows. The brain never gets
// the room, so it must not name it either.
func TestTheBotPackageNeverReachesIntoTheRoomOrTheEngineState(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		for _, bad := range []string{"*room.Room", "room.New(", ".Game()", "engine.New(", "engine.State"} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s uses %s, which would let a bot see more than a player can", f, bad)
			}
		}
	}
}

// A bot takes between 5 and 37 seconds to do anything, and no more than 25 for
// a question the server only waits 30 seconds for.
func TestBotsTakeFiveToThirtySevenSecondsToAct(t *testing.T) {
	check := func(name string, k kind, snap room.Snapshot, lo, hi time.Duration) {
		t.Helper()
		lowest, highest := time.Hour, time.Duration(0)
		for i := 0; i < 400; i++ {
			d := NewDriver(uint64(i+1), Config{})
			b := newBrain(me)
			d.bots[me] = b
			b.plan(t0, snap, d)
			it := b.intents[k]
			if it == nil {
				t.Fatalf("%s: nothing was planned", name)
			}
			wait := it.at.Sub(t0)
			lowest, highest = min(lowest, wait), max(highest, wait)
		}
		if lowest < lo || highest > hi {
			t.Errorf("%s: waits ranged %v to %v, want within %v to %v", name, lowest, highest, lo, hi)
		}
		if highest < hi-8*time.Second || lowest > lo+8*time.Second {
			t.Errorf("%s: waits ranged only %v to %v: the whole range should be used", name, lowest, highest)
		}
	}
	pass := view(engine.Hand{3, 1, 1, 0}, me)
	check("a pass", kPass, pass, 5*time.Second, 37*time.Second)

	reveal := view(engine.Hand{3, 1, 1, 0}, partner)
	reveal.Game.PendingReveals = []engine.PlayerID{me}
	check("a reveal", kReveal, reveal, 5*time.Second, 25*time.Second)

	forced := view(engine.Hand{3, 1, 1, 0}, me)
	forced.Game.ForcedPass = me
	check("a forced pass", kPass, forced, 5*time.Second, 25*time.Second)

	sig := view(engine.Hand{3, 1, 0, 0}, partner) // three of a kind: it signals "close"
	check("a signal", kSignal, sig, 5*time.Second, 37*time.Second)

	chat := view(engine.Hand{}, partner)
	chat.Phase = room.PhaseChat
	chat.Game.Round = 0
	chat.Players[1].Bot = false // a person is its partner, so it must write to them
	chat.Players[1].ID = "person"
	chat.Players[0].Partner = "person"
	check("the team chat code", kPost, chat, 5*time.Second, 37*time.Second)
}
