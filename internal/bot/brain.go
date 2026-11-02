package bot

import (
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/botname"
	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

type kind int

const (
	kPost kind = iota + 1 // tell a human partner the code for this round
	kReady
	kPass
	kReveal
	kSignal
	kProclaim
	kSuspect
	lastKind = kSuspect
)

func kindOf(in room.Input) kind {
	switch in.(type) {
	case room.TeamChat:
		return kPost
	case room.Ready:
		return kReady
	case room.Pass:
		return kPass
	case room.Reveal:
		return kReveal
	case room.SendSignal:
		return kSignal
	case room.Proclaim:
		return kProclaim
	case room.Suspect:
		return kSuspect
	}
	return 0
}

type intent struct {
	at  time.Time
	arg string // for a signal: "state" (the code) or "bluff"; for a suspicion: the target
}

type heard struct {
	from    engine.PlayerID
	gesture string
	scope   string
	at      time.Time
}

type revealReq struct {
	required int
	options  []engine.CardType
}

// brain is one bot. It keeps what it has seen and what it means to do next.
type brain struct {
	id      engine.PlayerID
	dirty   bool
	done    bool
	intents map[kind]*intent
	cool    map[kind]time.Time
	lastNow time.Time

	heard       []heard
	reveal      *revealReq
	lockedUntil time.Time
	playStart   time.Time
	postedRound int

	target    engine.CardType
	hasTarget bool

	sentState    int // 0 nothing, 3 close, 4 four: the last thing signalled about my own hand
	fourSent     int
	lastFourAt   time.Time
	lastSignalAt time.Time
	trusted      *heard // my partner's "I have four", not acted on yet
	lastProclaim time.Time
	suspicions   int
	lastSuspect  time.Time
	nextTick     time.Time
}

func newBrain(id engine.PlayerID) *brain {
	return &brain{id: id, intents: map[kind]*intent{}, cool: map[kind]time.Time{}}
}

// see takes in one message the room sent this bot.
func (b *brain) see(now time.Time, msg room.Message) {
	b.lastNow = now
	switch m := msg.(type) {
	case room.RoomClosed:
		b.done = true
	case room.ChatStarted:
		b.newRound()
	case room.Signal:
		if m.From != b.id {
			b.heard = append(b.heard, heard{from: m.From, gesture: m.Gesture, scope: m.Scope, at: now})
			if len(b.heard) > 80 {
				b.heard = slices.Clone(b.heard[len(b.heard)-60:])
			}
		}
	case room.TimerStarted:
		if m.Kind == "proclaim" && m.Player == b.id {
			b.lockedUntil = m.EndsAt
		}
	case room.GameEvent:
		switch e := m.Event.(type) {
		case engine.RevealRequested:
			if e.Target == b.id {
				b.reveal = &revealReq{required: e.Required, options: slices.Clone(e.Options)}
			}
		case engine.RoundStarted:
			b.playStart = now
			b.nextTick = time.Time{}
		case engine.FalseProclaim:
			if e.By == b.id {
				b.trusted = nil
			}
		}
	}
}

// newRound forgets the last round. Nothing in it is worth carrying over.
func (b *brain) newRound() {
	b.intents = map[kind]*intent{}
	b.heard = nil
	b.reveal = nil
	b.lockedUntil = time.Time{}
	b.sentState, b.fourSent = 0, 0
	b.trusted = nil
	b.suspicions = 0
	b.hasTarget = false
	b.nextTick = time.Time{}
}

func (b *brain) me(snap room.Snapshot) *room.PlayerInfo {
	for i := range snap.Players {
		if snap.Players[i].ID == b.id {
			return &snap.Players[i]
		}
	}
	return nil
}

func (b *brain) schedule(k kind, at time.Time, arg string) {
	if _, ok := b.intents[k]; ok {
		return
	}
	if until, cooling := b.cool[k]; cooling && at.Before(until) {
		return
	}
	b.intents[k] = &intent{at: at, arg: arg}
}

func (b *brain) dueKinds(now time.Time) []kind {
	var out []kind
	for k := kPost; k <= lastKind; k++ {
		if it, ok := b.intents[k]; ok && !it.at.After(now) {
			out = append(out, k)
		}
	}
	return out
}

func (b *brain) fired(k kind, now time.Time) {
	if k == kSignal {
		b.lastSignalAt = now
	}
}

// plan decides what this bot wants to do, given what it can see now.
func (b *brain) plan(now time.Time, snap room.Snapshot, d *Driver) {
	b.lastNow = now
	me := b.me(snap)
	if snap.PausedUntil != nil || me == nil || me.Removed || snap.Game == nil {
		b.intents = map[kind]*intent{}
		return
	}
	switch snap.Phase {
	case room.PhaseChat:
		b.planChat(now, snap, me, d)
	case room.PhasePlaying:
		b.planPlay(now, snap, me, d)
	default:
		b.intents = map[kind]*intent{}
		b.nextTick = time.Time{}
	}
}

func (b *brain) planChat(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) {
	upcoming := snap.Game.Round + 1
	if b.postedRound != upcoming {
		if botname.IsBotID(me.Partner) {
			b.postedRound = upcoming // a bot partner needs no message
		} else {
			b.schedule(kPost, now.Add(d.respond()), "")
		}
	}
	// Ready comes after the code has been posted, so a person can read it first.
	if !me.Ready && b.postedRound == upcoming {
		b.schedule(kReady, now.Add(d.respond()), "")
	}
}

func (b *brain) planPlay(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) {
	g := snap.Game
	if g.YourHand == nil {
		return
	}
	hand := *g.YourHand
	b.updateTarget(hand, d)
	total := hand.Total()

	pending := slices.Contains(g.PendingReveals, b.id)
	if !pending {
		b.reveal = nil
	}
	switch {
	case pending:
		b.schedule(kReveal, now.Add(d.respondPrompt()), "")
	case total == 5 && g.ForcedPass == b.id:
		b.schedule(kPass, now.Add(d.respondPrompt()), "") // a caught holder has 30 seconds
	case total == 5 && g.Holder == b.id:
		b.schedule(kPass, now.Add(d.respond()), "")
	}

	// Say how my own hand stands, in the team's code.
	want := 0
	switch cnt := hand[b.target]; {
	case cnt == 4 && total == 4:
		want = 4
	case cnt == 3:
		want = 3
	}
	switch {
	case want != b.sentState && want != 0:
		b.schedule(kSignal, now.Add(d.respond()), "state")
	case want == 0:
		b.sentState, b.fourSent = 0, 0
	case want == 4 && b.fourSent < 3 && now.Sub(b.lastFourAt) > d.think(15, 25):
		b.schedule(kSignal, now.Add(d.respond()), "state")
	}

	// Read my partner.
	cd := d.codeFor(me.Team, g.Round, snap.Gestures)
	if b.trusted == nil {
		for i := len(b.heard) - 1; i >= 0; i-- {
			h := b.heard[i]
			if h.from == me.Partner && h.gesture == cd.four && h.at.After(b.lastProclaim) && now.Sub(h.at) < 45*time.Second {
				hh := h
				b.trusted = &hh
				break
			}
		}
	}
	if b.trusted != nil && now.Sub(b.trusted.at) >= 45*time.Second {
		b.trusted = nil
	}
	if b.trusted != nil && g.Holder != me.Partner && !now.Before(b.lockedUntil) {
		b.schedule(kProclaim, now.Add(d.respond()), "")
	}

	// From time to time: maybe bluff, maybe suspect someone.
	if b.nextTick.IsZero() {
		b.nextTick = now.Add(d.think(10, 20))
		if b.playStart.IsZero() {
			b.playStart = now
		}
	} else if !now.Before(b.nextTick) {
		b.tick(now, snap, me, d)
		b.nextTick = now.Add(d.think(8, 18))
	}
}

func (b *brain) tick(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) {
	g := snap.Game
	if now.Sub(b.lastSignalAt) > d.think(10, 20) && d.rng.Float64() < 0.3 {
		b.schedule(kSignal, now.Add(d.respond()), "bluff")
	}
	if g.Points[b.id] <= 0 || now.Sub(b.playStart) < d.think(20, 30) || now.Sub(b.lastSuspect) < d.think(25, 40) {
		return
	}
	target, repeats := b.mostSuspicious(now, snap, me, d)
	if target == "" {
		return
	}
	// A suspicion only catches someone holding four of a kind, so spend one on
	// a real tell: an opponent who keeps repeating the same gesture, which is
	// what a team's "I have four" looks like when it is sent again. Otherwise
	// only now and then, and keep a point back.
	var p float64
	switch {
	case repeats >= 3:
		p = 0.8
	case repeats == 2:
		p = 0.5
	case b.suspicions < 1:
		p = 0.03
	}
	if b.suspicions >= 2 && repeats < 3 {
		p = 0
	}
	if p > 0 && d.rng.Float64() < p {
		b.schedule(kSuspect, now.Add(d.respond()), string(target))
	}
}

// mostSuspicious is the opponent who has repeated one gesture most often in the
// last minute, among the signals the whole table could see, and how many times.
func (b *brain) mostSuspicious(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) (engine.PlayerID, int) {
	opp := map[engine.PlayerID]bool{}
	for _, p := range snap.Players {
		if !p.Removed && p.Team != me.Team && p.ID != b.id && !slices.Contains(snap.Game.PendingReveals, p.ID) {
			opp[p.ID] = true
		}
	}
	type pair struct {
		who engine.PlayerID
		g   string
	}
	seen := map[pair]int{}
	score := map[engine.PlayerID]int{}
	for _, h := range b.heard {
		if opp[h.from] && h.scope == "room" && now.Sub(h.at) < 60*time.Second {
			k := pair{h.from, h.gesture}
			seen[k]++
			score[h.from] = max(score[h.from], seen[k])
		}
	}
	var best []engine.PlayerID
	top := -1
	for _, id := range snap.Game.Seats {
		if !opp[id] {
			continue
		}
		switch s := score[id]; {
		case s > top:
			top, best = s, []engine.PlayerID{id}
		case s == top:
			best = append(best, id)
		}
	}
	if len(best) == 0 {
		return "", 0
	}
	return best[d.rng.IntN(len(best))], top
}

// updateTarget picks which card type to collect: the one held most, keeping the
// current choice while it is still among the most held.
func (b *brain) updateTarget(h engine.Hand, d *Driver) {
	most := 0
	for _, c := range h {
		most = max(most, c)
	}
	if b.hasTarget && h[b.target] == most {
		return
	}
	var top []engine.CardType
	for t, c := range h {
		if c == most {
			top = append(top, engine.CardType(t))
		}
	}
	b.target, b.hasTarget = top[d.rng.IntN(len(top))], true
}

// build turns an intent that has come due into an input, if it still makes
// sense now.
func (b *brain) build(k kind, it *intent, now time.Time, snap room.Snapshot, d *Driver) (room.Input, bool) {
	me := b.me(snap)
	g := snap.Game
	if me == nil || me.Removed || snap.PausedUntil != nil || g == nil {
		return nil, false
	}
	switch k {
	case kPost:
		upcoming := g.Round + 1
		if snap.Phase != room.PhaseChat || b.postedRound == upcoming {
			return nil, false
		}
		b.postedRound = upcoming
		return room.TeamChat{Text: d.postText(d.codeFor(me.Team, upcoming, snap.Gestures), snap.Gestures)}, true
	case kReady:
		if snap.Phase != room.PhaseChat || me.Ready {
			return nil, false
		}
		return room.Ready{Ready: true}, true
	}
	if snap.Phase != room.PhasePlaying || g.YourHand == nil {
		return nil, false
	}
	hand := *g.YourHand
	switch k {
	case kPass:
		if hand.Total() != 5 || slices.Contains(g.PendingReveals, b.id) || (g.Holder != b.id && g.ForcedPass != b.id) {
			return nil, false
		}
		return room.Pass{Card: choosePass(hand, b.target, d)}, true
	case kReveal:
		if !slices.Contains(g.PendingReveals, b.id) {
			return nil, false
		}
		return room.Reveal{Types: b.chooseReveal(hand, d)}, true
	case kSignal:
		cd := d.codeFor(me.Team, g.Round, snap.Gestures)
		if it.arg == "bluff" {
			id := d.bluff(snap.Gestures, cd)
			if id == "" {
				return nil, false
			}
			return room.SendSignal{Gesture: id}, true
		}
		switch cnt := hand[b.target]; {
		case cnt == 4 && hand.Total() == 4:
			b.sentState, b.lastFourAt = 4, now
			b.fourSent++
			return room.SendSignal{Gesture: cd.four}, true
		case cnt == 3:
			b.sentState = 3
			return room.SendSignal{Gesture: cd.close}, true
		}
		return nil, false
	case kProclaim:
		if b.trusted == nil || g.Holder == me.Partner || now.Before(b.lockedUntil) {
			return nil, false
		}
		b.trusted = nil
		b.lastProclaim = now
		return room.Proclaim{}, true
	case kSuspect:
		target := engine.PlayerID(it.arg)
		ok := false
		for _, p := range snap.Players {
			if p.ID == target && !p.Removed && p.Team != me.Team {
				ok = true
			}
		}
		if !ok || g.Points[b.id] <= 0 || slices.Contains(g.PendingReveals, target) || g.ForcedPass == target {
			return nil, false
		}
		b.suspicions++
		b.lastSuspect = now
		return room.Suspect{Target: target}, true
	}
	return nil, false
}

// choosePass gives away the card it wants least: not the type it is
// collecting, and of the others the one it holds fewest of.
func choosePass(h engine.Hand, target engine.CardType, d *Driver) engine.CardType {
	best := -1
	var cands []engine.CardType
	for t, c := range h {
		if c == 0 || engine.CardType(t) == target {
			continue
		}
		switch {
		case best == -1 || c < best:
			best, cands = c, []engine.CardType{engine.CardType(t)}
		case c == best:
			cands = append(cands, engine.CardType(t))
		}
	}
	if len(cands) == 0 {
		return target
	}
	return cands[d.rng.IntN(len(cands))]
}

// chooseReveal shows the types it holds fewest of, to give away as little as
// possible about what it is collecting.
func (b *brain) chooseReveal(h engine.Hand, d *Driver) []engine.CardType {
	opts := h.Types()
	need := 2
	if h.Total() >= 5 {
		need = 3
	}
	if b.reveal != nil {
		opts, need = slices.Clone(b.reveal.options), b.reveal.required
	}
	d.rng.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	slices.SortStableFunc(opts, func(a, c engine.CardType) int { return h[a] - h[c] })
	if need > len(opts) {
		need = len(opts)
	}
	return opts[:need]
}
