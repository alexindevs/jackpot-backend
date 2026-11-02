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

	// What makes a bot suspicious. A turn is one pass of the card.
	teamOf     map[engine.PlayerID]engine.TeamID
	myTeam     engine.TeamID
	turns      int                           // passes so far this round
	lastSignal map[engine.PlayerID]int       // the turn each player last signalled
	recent     map[engine.PlayerID][]sigSeen // what each player signalled lately
	tells      map[engine.PlayerID]map[string]bool
	tellHit    map[engine.PlayerID]time.Time // used a gesture they used before a win
	sudden     map[engine.PlayerID]time.Time // started signalling after a long quiet
	newHolder  engine.PlayerID               // someone just got the passing card

	// Collecting: a bot that is not getting anywhere changes what it collects.
	banned    map[engine.CardType]bool // types given up on this round
	missed    int                      // its own turns in a row without being given the type it collects
	suspected map[engine.PlayerID]time.Time
}

// sigSeen is a signal the bot saw, and on which turn.
type sigSeen struct {
	gesture string
	turn    int
}

func newBrain(id engine.PlayerID) *brain {
	return &brain{
		id: id, intents: map[kind]*intent{}, cool: map[kind]time.Time{},
		teamOf: map[engine.PlayerID]engine.TeamID{}, lastSignal: map[engine.PlayerID]int{},
		recent: map[engine.PlayerID][]sigSeen{}, tells: map[engine.PlayerID]map[string]bool{},
		tellHit: map[engine.PlayerID]time.Time{}, sudden: map[engine.PlayerID]time.Time{},
		banned: map[engine.CardType]bool{}, suspected: map[engine.PlayerID]time.Time{},
	}
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
			b.noticeSignal(now, m)
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
			b.turns = 0
			b.lastSignal = map[engine.PlayerID]int{}
		case engine.SuspicionRaised:
			// One accusation is enough: the others hold off on that player.
			b.suspected[e.Target] = now
			if it := b.intents[kSuspect]; it != nil && it.arg == string(e.Target) {
				delete(b.intents, kSuspect)
			}
		case engine.Passed:
			b.turns++
			if e.To == b.id {
				// A turn of its own. The card it was given shows whether the
				// type it is collecting is coming its way.
				if b.hasTarget && e.Card == b.target {
					b.missed = 0
				} else {
					b.missed++
				}
			} else {
				b.newHolder = e.To
			}
		case engine.RoundWon:
			b.learnTells(e.Team)
		case engine.FalseProclaim:
			if e.By == b.id {
				b.trusted = nil
			}
		}
	}
}

// noticeSignal looks at one signal for the two things that make a bot suspicious:
// a gesture the player used before a win, and a player who has been quiet for
// five turns suddenly signalling.
func (b *brain) noticeSignal(now time.Time, s room.Signal) {
	r := append(b.recent[s.From], sigSeen{gesture: s.Gesture, turn: b.turns})
	if len(r) > 8 {
		r = r[len(r)-8:]
	}
	b.recent[s.From] = r
	if b.tells[s.From][s.Gesture] {
		b.tellHit[s.From] = now
	}
	last, signalled := b.lastSignal[s.From]
	if (signalled && b.turns-last >= 5) || (!signalled && b.turns >= 5) {
		b.sudden[s.From] = now
	}
	b.lastSignal[s.From] = b.turns
}

// learnTells remembers what each player on the winning team signalled in the
// last few turns before the win. If one of them signals the same thing again,
// that is worth a second look.
func (b *brain) learnTells(team engine.TeamID) {
	if team == b.myTeam {
		return
	}
	for id, t := range b.teamOf {
		if t != team || id == b.id {
			continue
		}
		for _, r := range b.recent[id] {
			if r.turn >= b.turns-3 {
				if b.tells[id] == nil {
					b.tells[id] = map[string]bool{}
				}
				b.tells[id][r.gesture] = true
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
	b.turns = 0
	b.lastSignal = map[engine.PlayerID]int{}
	b.recent = map[engine.PlayerID][]sigSeen{}
	b.tellHit = map[engine.PlayerID]time.Time{}
	b.sudden = map[engine.PlayerID]time.Time{}
	b.newHolder = ""
	b.banned = map[engine.CardType]bool{}
	b.missed = 0
	b.suspected = map[engine.PlayerID]time.Time{}
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
	if me != nil {
		b.myTeam = me.Team
		for _, p := range snap.Players {
			b.teamOf[p.ID] = p.Team
		}
	}
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
		// Let the signal sink in: never proclaim straight after it.
		at := now.Add(d.respond())
		if gap := b.trusted.at.Add(d.think(5, 15)); gap.After(at) {
			at = gap
		}
		b.schedule(kProclaim, at, "")
	}

	b.considerSuspicion(now, snap, me, d)

	// From time to time: maybe bluff.
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
	if now.Sub(b.lastSignalAt) > d.think(10, 20) && d.rng.Float64() < 0.3 {
		b.schedule(kSignal, now.Add(d.respond()), "bluff")
	}
}

// What makes a bot suspect someone, and how likely it is to act on it. Each
// reason is looked at once, when it happens, and then used up.
const (
	chanceHolder = 0.10 // someone has just been given the passing card: the chance for the whole table
	chanceSudden = 0.40 // someone quiet for 5 turns started signalling
	chanceRepeat = 0.50 // someone sent the same gesture twice lately...
	chanceMany   = 0.80 // ...or three times or more
	chanceTell   = 0.70 // someone used a gesture they used before winning a round

	// chanceIgnore is how often a bot lets a reason go by without acting on it,
	// whatever the reason, so it does not suspect every time.
	chanceIgnore = 0.40
)

// holderChance is a bot's own share of the 10% chance that a pass of the card
// makes someone at the table suspicious. Every bot on another team than the
// holder rolls for it, so each takes its share and the table as a whole acts
// on about one pass in ten, however many bots there are.
func holderChance(snap room.Snapshot, holder engine.PlayerID) float64 {
	var team engine.TeamID
	for _, p := range snap.Players {
		if p.ID == holder {
			team = p.Team
		}
	}
	n := 0
	for _, p := range snap.Players {
		if p.Bot && !p.Removed && p.Team != team {
			n++
		}
	}
	return chanceHolder / float64(max(n, 1))
}

// suspicionTrigger finds the opponent the bot has most reason to suspect right
// now and the chance it acts on it. It looks only at opponents: never the bot's
// own partner or team, and never someone who is already answering a suspicion.
func (b *brain) suspicionTrigger(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) (engine.PlayerID, float64) {
	opp := map[engine.PlayerID]bool{}
	for _, p := range snap.Players {
		if at, was := b.suspected[p.ID]; was && now.Sub(at) < time.Minute {
			continue // someone has just suspected them
		}
		if !p.Removed && p.Team != me.Team && p.ID != b.id && !slices.Contains(snap.Game.PendingReveals, p.ID) && snap.Game.ForcedPass != p.ID {
			opp[p.ID] = true
		}
	}
	type pair struct {
		who engine.PlayerID
		g   string
	}
	seen := map[pair]int{}
	repeats := map[engine.PlayerID]int{}
	for _, h := range b.heard {
		if opp[h.from] && h.scope == "room" && now.Sub(h.at) < 60*time.Second {
			k := pair{h.from, h.gesture}
			seen[k]++
			repeats[h.from] = max(repeats[h.from], seen[k])
		}
	}
	recent := func(m map[engine.PlayerID]time.Time, id engine.PlayerID) bool {
		at, ok := m[id]
		return ok && now.Sub(at) < 2*time.Minute
	}
	var best []engine.PlayerID
	top := 0.0
	for _, id := range snap.Game.Seats {
		if !opp[id] {
			continue
		}
		p := 0.0
		switch {
		case repeats[id] >= 3:
			p = chanceMany
		case repeats[id] == 2:
			p = chanceRepeat
		}
		if recent(b.tellHit, id) {
			p = max(p, chanceTell)
		}
		if recent(b.sudden, id) {
			p = max(p, chanceSudden)
		}
		if b.newHolder == id {
			p = max(p, holderChance(snap, id))
		}
		switch {
		case p > top:
			top, best = p, []engine.PlayerID{id}
		case p == top && p > 0:
			best = append(best, id)
		}
	}
	if len(best) == 0 {
		return "", 0
	}
	return best[d.rng.IntN(len(best))], top
}

// considerSuspicion rolls the dice on whatever has made the bot suspicious
// since it last looked, then forgets those reasons.
func (b *brain) considerSuspicion(now time.Time, snap room.Snapshot, me *room.PlayerInfo, d *Driver) {
	g := snap.Game
	if _, planned := b.intents[kSuspect]; planned {
		return
	}
	target, p := b.suspicionTrigger(now, snap, me, d)
	b.newHolder = ""
	b.tellHit = map[engine.PlayerID]time.Time{}
	b.sudden = map[engine.PlayerID]time.Time{}
	if target == "" || g.Points[b.id] <= 0 || now.Sub(b.playStart) < d.think(25, 25) || now.Sub(b.lastSuspect) < d.think(25, 40) {
		return
	}
	// Keep a point back unless there is real cause.
	if b.suspicions >= 2 && p < chanceTell {
		return
	}
	if d.rng.Float64() < chanceIgnore {
		return
	}
	if d.rng.Float64() < p {
		b.schedule(kSuspect, now.Add(d.respond()+d.think(0, 4)), string(target)) // a little extra randomness after the tell
	}
}

// updateTarget picks which card type to collect: the one held most, keeping the
// current choice while it is still the best. Partners may collect the same
// type. A bot that has had three turns of its own without being given a card
// of the type it is collecting gives that type up for the rest of the round and
// collects something else; choosePass then lets the old stack go first.
func (b *brain) updateTarget(h engine.Hand, d *Driver) {
	if b.hasTarget && b.missed >= 3 && h[b.target] < 4 {
		b.banned[b.target] = true
		b.hasTarget = false
		b.missed = 0
		if len(b.banned) >= engine.NumTypes-1 {
			b.banned = map[engine.CardType]bool{}
		}
	}
	most := -1
	for t, c := range h {
		if !b.banned[engine.CardType(t)] {
			most = max(most, c)
		}
	}
	if b.hasTarget && !b.banned[b.target] && h[b.target] == most {
		return
	}
	var top []engine.CardType
	for t, c := range h {
		if !b.banned[engine.CardType(t)] && c == most {
			top = append(top, engine.CardType(t))
		}
	}
	if len(top) == 0 { // everything is ruled out: start again
		b.banned = map[engine.CardType]bool{}
		for t := range h {
			top = append(top, engine.CardType(t))
		}
	}
	next := top[d.rng.IntN(len(top))]
	if b.hasTarget && next != b.target {
		b.missed = 0 // a new plan starts from scratch
	}
	b.target, b.hasTarget = next, true
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
		return room.Pass{Card: choosePass(hand, b.target, b.banned, d)}, true
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

// choosePass gives away the card it wants least: a type it has given up on
// first, then not the type it is collecting, and of the others the one it holds
// fewest of.
func choosePass(h engine.Hand, target engine.CardType, banned map[engine.CardType]bool, d *Driver) engine.CardType {
	var old []engine.CardType
	for t, c := range h {
		if c > 0 && banned[engine.CardType(t)] && engine.CardType(t) != target {
			old = append(old, engine.CardType(t))
		}
	}
	if len(old) > 0 {
		return old[d.rng.IntN(len(old))]
	}
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
