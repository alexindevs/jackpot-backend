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
		got := choosePass(engine.Hand{3, 1, 1, 0}, 0, nil, d)
		if got != 1 && got != 2 {
			t.Fatalf("passed type %d from [3 1 1 0] collecting 0", got)
		}
		if got := choosePass(engine.Hand{2, 1, 2, 0}, 0, nil, d); got != 1 {
			t.Fatalf("passed type %d: the single card is the one to give away", got)
		}
	}
	if got := choosePass(engine.Hand{5, 0, 0, 0}, 0, nil, d); got != 0 {
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

// teamsKnown tells a brain who is on which team, as its planning would.
func teamsKnown(b *brain, snap room.Snapshot) {
	b.myTeam = 0
	for _, p := range snap.Players {
		b.teamOf[p.ID] = p.Team
	}
}

func passTo(b *brain, now time.Time, to engine.PlayerID) {
	b.see(now, room.GameEvent{Event: engine.Passed{From: partner, To: to}})
}

func signal(b *brain, now time.Time, from engine.PlayerID, gesture string) {
	b.see(now, room.Signal{From: from, Gesture: gesture, Scope: "room"})
}

func TestABotNeverSuspectsItsOwnPartnerOrTeam(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	teamsKnown(b, snap)
	for i := 0; i < 20; i++ { // every reason there is, aimed at the bot's own side
		signal(b, now, partner, "nod")
		signal(b, now, partner, "nod")
	}
	passTo(b, now, partner)
	b.tellHit[partner], b.sudden[partner] = now, now
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != "" || p != 0 {
		t.Fatalf("suspect target = %q (%.2f): nothing here points at an opponent", target, p)
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

func TestABotSuspectsWhoRepeatsAGesture(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	teamsKnown(b, snap)
	signal(b, now, rivalA, "cough")
	signal(b, now, rivalB, "yawn")
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != "" {
		t.Fatalf("one gesture each is nothing: %q %.2f", target, p)
	}
	signal(b, now.Add(time.Second), rivalA, "cough")
	if target, p := b.suspicionTrigger(now.Add(2*time.Second), snap, b.me(snap), d); target != rivalA || p != chanceRepeat {
		t.Errorf("twice: %q %.2f, want %q %.2f", target, p, rivalA, chanceRepeat)
	}
	signal(b, now.Add(2*time.Second), rivalA, "cough")
	if target, p := b.suspicionTrigger(now.Add(3*time.Second), snap, b.me(snap), d); target != rivalA || p != chanceMany {
		t.Errorf("three times: %q %.2f, want %q %.2f", target, p, rivalA, chanceMany)
	}
	if _, p := b.suspicionTrigger(now.Add(3*time.Minute), snap, b.me(snap), d); p != 0 {
		t.Errorf("old signals stop counting after a minute: %.2f", p)
	}
}

func TestABotSuspectsTheGestureAPlayerUsedBeforeWinning(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	teamsKnown(b, snap)
	for i := 0; i < 6; i++ {
		passTo(b, now, me)
	}
	signal(b, now, rivalA, "cough") // rivalA signals, then their team wins the round
	signal(b, now, rivalB, "yawn")
	b.see(now, room.GameEvent{Event: engine.RoundWon{Team: 1}})
	b.see(now, room.ChatStarted{Round: 2}) // the next round; what it learned stays

	signal(b, now, rivalA, "nod") // a different gesture is nothing
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != "" {
		t.Fatalf("a new gesture: %q %.2f", target, p)
	}
	signal(b, now, rivalA, "cough") // the winning gesture again
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != rivalA || p != chanceTell {
		t.Errorf("the winning gesture again: %q %.2f, want %q %.2f", target, p, rivalA, chanceTell)
	}

	// A win by the bot's own team teaches it nothing about its partner.
	b2 := newBrain(me)
	teamsKnown(b2, snap)
	signal(b2, now, partner, "cough")
	b2.see(now, room.GameEvent{Event: engine.RoundWon{Team: 0}})
	if len(b2.tells[partner]) != 0 {
		t.Error("it must not learn tells from its own team")
	}
}

func TestABotSuspectsSomeoneWhoStartsSignallingAfterFiveQuietTurns(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	teamsKnown(b, snap)
	for i := 0; i < 4; i++ {
		passTo(b, now, me)
	}
	signal(b, now, rivalB, "yawn") // only 4 turns in: not yet unusual
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != "" {
		t.Fatalf("after 4 turns: %q %.2f", target, p)
	}
	b.sudden = map[engine.PlayerID]time.Time{}
	for i := 0; i < 5; i++ {
		passTo(b, now, me)
	}
	signal(b, now, rivalB, "sneeze") // 5 turns since their last signal
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != rivalB || p != chanceSudden {
		t.Errorf("after 5 quiet turns: %q %.2f, want %q %.2f", target, p, rivalB, chanceSudden)
	}

	fresh := newBrain(me) // someone who has never signalled this round, 5 turns in
	teamsKnown(fresh, snap)
	for i := 0; i < 5; i++ {
		passTo(fresh, now, me)
	}
	signal(fresh, now, rivalA, "cough")
	if target, p := fresh.suspicionTrigger(now, snap, fresh.me(snap), d); target != rivalA || p != chanceSudden {
		t.Errorf("a first signal after 5 turns: %q %.2f", target, p)
	}
}

func TestABotSometimesSuspectsWhoseHandJustGotThePassingCard(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	teamsKnown(b, snap)
	passTo(b, now, rivalA)
	// Two bots are on the other team from rivalA, so each has half of the 10%.
	if target, p := b.suspicionTrigger(now, snap, b.me(snap), d); target != rivalA || p != 0.05 {
		t.Errorf("a pass to an opponent: %q %.3f, want %q 0.05", target, p, rivalA)
	}
	for _, own := range []engine.PlayerID{partner, me} {
		other := newBrain(me)
		teamsKnown(other, snap)
		passTo(other, now, own)
		if target, _ := other.suspicionTrigger(now, snap, other.me(snap), d); target != "" {
			t.Errorf("a pass to %s must not make the bot suspect anyone: %q", own, target)
		}
	}
}

// About one pass in ten makes someone at the table suspicious, which is what
// was asked for, however many bots there are; and a reason is used up once it
// has been rolled for.
func TestThePassingCardMakesTheTableSuspiciousOnTheRightShareOfPasses(t *testing.T) {
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	acted := 0
	const trials = 6000
	for i := 0; i < trials; i++ {
		any := false
		for k, id := range []engine.PlayerID{me, partner} { // the bots on the other team from rivalA
			d := NewDriver(uint64(i*2+k+1), Config{})
			b := newBrain(id)
			d.bots[id] = b
			teamsKnown(b, snap)
			b.playStart = t0
			passTo(b, t0, rivalA)
			b.considerSuspicion(t0.Add(time.Hour), snap, b.me(snap), d)
			if b.intents[kSuspect] != nil {
				any = true
			}
			if b.newHolder != "" {
				t.Fatal("a reason must be used up once it has been rolled for")
			}
		}
		if any {
			acted++
		}
	}
	want := chanceHolder * (1 - chanceIgnore) // the 10%, less the times a bot lets it go by
	if rate := float64(acted) / trials; rate < want-0.02 || rate > want+0.02 {
		t.Errorf("someone suspected on %.1f%% of passes to an opponent, want about %.0f%%", rate*100, want*100)
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

func TestABotWaitsAtLeastFiveSecondsAfterThePartnersSignalBeforeCalling(t *testing.T) {
	snap := view(engine.Hand{1, 1, 1, 1}, rivalA)
	for seed := uint64(1); seed <= 200; seed++ {
		d := NewDriver(seed, Config{})
		b := newBrain(me)
		teamsKnown(b, snap)
		b.trusted = &heard{from: partner, at: t0}
		b.dirty = true
		b.plan(t0, snap, d)
		it := b.intents[kProclaim]
		if it == nil {
			t.Fatalf("seed %d: it should plan to call", seed)
		}
		if gap := it.at.Sub(t0); gap < 5*time.Second {
			t.Fatalf("seed %d: called %v after the signal, want at least 5s", seed, gap)
		}
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

// A bot takes between 5 and 15 seconds to do anything, and the same
// for a question the server only waits 30 seconds for.
func TestBotsTakeFiveToFifteenSecondsToAct(t *testing.T) {
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
	check("a pass", kPass, pass, 5*time.Second, 15*time.Second)

	reveal := view(engine.Hand{3, 1, 1, 0}, partner)
	reveal.Game.PendingReveals = []engine.PlayerID{me}
	check("a reveal", kReveal, reveal, 5*time.Second, 15*time.Second)

	forced := view(engine.Hand{3, 1, 1, 0}, me)
	forced.Game.ForcedPass = me
	check("a forced pass", kPass, forced, 5*time.Second, 15*time.Second)

	sig := view(engine.Hand{3, 1, 0, 0}, partner) // three of a kind: it signals "close"
	check("a signal", kSignal, sig, 5*time.Second, 15*time.Second)

	chat := view(engine.Hand{}, partner)
	chat.Phase = room.PhaseChat
	chat.Game.Round = 0
	chat.Players[1].Bot = false // a person is its partner, so it must write to them
	chat.Players[1].ID = "person"
	chat.Players[0].Partner = "person"
	check("the team chat code", kPost, chat, 5*time.Second, 15*time.Second)
}

func givenCard(b *brain, now time.Time, card engine.CardType) {
	b.see(now, room.GameEvent{Event: engine.Passed{From: rivalB, To: me, Card: card}})
}

func TestABotGivesUpAHoardAfterThreeOfItsTurnsWithoutBeingGivenThatType(t *testing.T) {
	d, b, now := newTest()
	hand := engine.Hand{3, 1, 0, 0}
	b.updateTarget(hand, d)
	if b.target != 0 {
		t.Fatalf("it should collect the type it holds most: %d", b.target)
	}
	givenCard(b, now, 1) // turn 1: not what it collects
	givenCard(b, now, 3) // turn 2
	b.updateTarget(hand, d)
	if b.target != 0 {
		t.Fatal("two turns is not long enough to give up")
	}
	givenCard(b, now, 0) // it is given what it collects: the count starts again
	givenCard(b, now, 1)
	givenCard(b, now, 2)
	b.updateTarget(hand, d)
	if b.target != 0 {
		t.Fatal("two turns since it was last given its type is still not enough")
	}
	givenCard(b, now, 3) // the third turn in a row
	b.updateTarget(hand, d)
	if b.target == 0 || !b.banned[0] {
		t.Fatalf("after three turns it must collect something else, still %d", b.target)
	}
	// It lets the old stack go first, and never goes back to it this round.
	for i := 0; i < 30; i++ {
		if got := choosePass(hand, b.target, b.banned, d); got != 0 {
			t.Fatalf("it should pass the type it gave up on first, passed %d", got)
		}
		b.updateTarget(hand, d)
		if b.target == 0 {
			t.Fatal("it went back to a type it gave up on")
		}
	}
	b.newRound()
	if len(b.banned) != 0 || b.missed != 0 {
		t.Error("a new round starts fresh")
	}
}

func TestPartnersAreAllowedToCollectTheSameType(t *testing.T) {
	d := NewDriver(1, Config{})
	a, b := newBrain(me), newBrain(partner)
	hand := engine.Hand{3, 1, 0, 0} // one clear favourite for both
	a.updateTarget(hand, d)
	b.updateTarget(hand, d)
	if a.target != 0 || b.target != 0 {
		t.Errorf("both partners should collect the type they hold most: %d and %d", a.target, b.target)
	}
}

// Two bots with the same reason must not accuse the same player at the same
// moment: one accusation is enough.
func TestTwoBotsNeverSuspectTheSamePlayerAtTheSameMoment(t *testing.T) {
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	for seed := uint64(1); seed <= 100; seed++ {
		d := NewDriver(seed, Config{})
		for _, id := range []engine.PlayerID{me, partner} { // both are on the team opposing rivalA
			b := newBrain(id)
			teamsKnown(b, snap)
			b.dirty = true
			b.intents[kSuspect] = &intent{at: t0, arg: string(rivalA)}
			d.bots[id] = b
		}
		acts := d.Act(t0, func(engine.PlayerID) room.Snapshot { return snap })
		suspects := 0
		for _, a := range acts {
			if s, ok := a.In.(room.Suspect); ok && s.Target == rivalA {
				suspects++
			}
		}
		if suspects > 1 {
			t.Fatalf("seed %d: %d bots suspected the same player at once", seed, suspects)
		}
	}
}

// Whatever the reason, a bot lets it go by two times in five.
func TestABotIgnoresAReasonToSuspectFortyPercentOfTheTime(t *testing.T) {
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	acted := 0
	const trials = 6000
	for i := 0; i < trials; i++ {
		d := NewDriver(uint64(i+1), Config{})
		b := newBrain(me)
		d.bots[me] = b
		teamsKnown(b, snap)
		b.playStart = t0
		late := t0.Add(time.Hour)
		b.tellHit[rivalA] = late // a tell, worth a 70% chance before the 40% is taken off
		b.considerSuspicion(late, snap, b.me(snap), d)
		if b.intents[kSuspect] != nil {
			acted++
		}
	}
	want := chanceTell * (1 - chanceIgnore)
	if rate := float64(acted) / trials; rate < want-0.03 || rate > want+0.03 {
		t.Errorf("acted on %.1f%% of tells, want about %.0f%%", rate*100, want*100)
	}
}

func TestOneSuspicionIsEnoughOthersHoldOff(t *testing.T) {
	d, b, now := newTest()
	snap := view(engine.Hand{1, 1, 1, 1}, partner)
	teamsKnown(b, snap)
	signal(b, now, rivalA, "cough")
	signal(b, now, rivalA, "cough")
	if target, _ := b.suspicionTrigger(now, snap, b.me(snap), d); target != rivalA {
		t.Fatalf("before anyone has acted: %q", target)
	}
	b.intents[kSuspect] = &intent{at: now.Add(time.Minute), arg: string(rivalA)}
	b.see(now, room.GameEvent{Event: engine.SuspicionRaised{By: partner, Target: rivalA}}) // someone else got there first
	if b.intents[kSuspect] != nil {
		t.Error("its own plan to suspect the same player must be dropped")
	}
	if target, p := b.suspicionTrigger(now.Add(10*time.Second), snap, b.me(snap), d); target != "" || p != 0 {
		t.Errorf("no second suspicion of the same player: %q %.2f", target, p)
	}
	if target, _ := b.suspicionTrigger(now.Add(2*time.Minute), snap, b.me(snap), d); target != "" {
		// the repeated gestures are old by now, so there is simply nothing to act on
		t.Errorf("after two minutes: %q", target)
	}
}
