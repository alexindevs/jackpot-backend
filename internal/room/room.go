// Package room runs one live game: the lobby, the between-round chat, the
// timers, and everything that happens over time around the pure engine.
//
// A Room does no I/O and never sleeps. The caller feeds it inputs with a
// timestamp (Handle), tells it when time has passed (Tick), and asks when it
// next needs attention (NextDeadline). Each call returns the messages to send.
// A Runner (runner.go) drives a Room with real time from a single goroutine.
package room

import (
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Phase is where the room is in its life.
type Phase int

const (
	PhaseLobby   Phase = iota // players joining and picking partners
	PhaseChat                 // private team chat before a round
	PhasePlaying              // a round is on
	PhaseOver                 // the game has ended
	PhaseClosed               // the room is gone
)

func (p Phase) String() string {
	return [...]string{"lobby", "chat", "playing", "over", "closed"}[p]
}

var (
	ErrClosed          = errors.New("room is closed")
	ErrNotJoinable     = errors.New("the game has already started")
	ErrRoomFull        = errors.New("room is full")
	ErrAlreadyMember   = errors.New("already in this room")
	ErrBadGesture      = errors.New("not a known gesture")
	ErrNotMember       = errors.New("not in this room")
	ErrNotPlayer       = errors.New("not a player in this game")
	ErrAlreadyPaired   = errors.New("already has a partner")
	ErrNoInvite        = errors.New("no such invitation")
	ErrDuplicateInvite = errors.New("already invited")
	ErrBadTarget       = errors.New("invalid target")
	ErrWrongPhase      = errors.New("not allowed right now")
	ErrBadText         = errors.New("message must be 1 to 500 characters")
	ErrSlowDown        = errors.New("you are sending messages too fast")
	ErrNotCreator      = errors.New("only the room's creator can do that")
	ErrTooFewPlayers   = errors.New("not enough players to start")
	ErrUnpairedPlayers = errors.New("some players don't have a partner yet")
)

// Config holds the room's tunable numbers. Defaults match docs/rules.md.
type Config struct {
	Engine          engine.Config
	MinPlayers      int           // to start the lobby countdown
	MaxPlayers      int           // the room stops accepting joiners here
	LobbyCountdown  time.Duration // from reaching MinPlayers to the game starting
	PreRoundChat    time.Duration // private team chat before each round
	HoldTimer       time.Duration // the holder must pass within this
	PromptTimeout   time.Duration // reveal and forced-pass prompts
	ReconnectWindow time.Duration // a disconnected player's time to come back
	EmptyRoomGrace  time.Duration // nobody connected for this long closes the room

	// SpectatorsSeeCards is the temporary perk: spectators see every hand.
	SpectatorsSeeCards bool

	// ChatRatePerSec and ChatBurst limit how fast one player can post to the
	// public chat. Public chats get spammy.
	ChatRatePerSec float64
	ChatBurst      int

	// Gestures is the fixed vocabulary for signals. TeamSignalChance is the
	// chance that one signal is seen only by the sender's partner; otherwise
	// the whole room sees it. SignalRatePerSec and SignalBurst limit how fast
	// one player can signal.
	Gestures         []Gesture
	TeamSignalChance float64
	SignalRatePerSec float64
	SignalBurst      int

	// OnRecord, if set, is told about games starting, rounds being won and
	// games ending. It runs on the room's goroutine and must not block.
	OnRecord func(Record)
}

func DefaultConfig() Config {
	return Config{
		Engine:             engine.DefaultConfig(),
		MinPlayers:         4,
		MaxPlayers:         10,
		LobbyCountdown:     2 * time.Minute,
		PreRoundChat:       2 * time.Minute,
		HoldTimer:          60 * time.Second,
		PromptTimeout:      30 * time.Second,
		ReconnectWindow:    90 * time.Second,
		EmptyRoomGrace:     time.Minute,
		SpectatorsSeeCards: true,
		ChatRatePerSec:     1,
		ChatBurst:          5,
		Gestures:           DefaultGestures(),
		TeamSignalChance:   0.2,
		SignalRatePerSec:   1,
		SignalBurst:        3,
	}
}

type member struct {
	id        engine.PlayerID
	connected bool
	partner   engine.PlayerID // "" while unpaired
	ready     bool
	team      engine.TeamID
	removed   bool // kicked from the game; now only watching
}

type inviteKey struct{ from, to engine.PlayerID }

// Room is one game room. It is not safe for concurrent use.
type Room struct {
	id      string
	creator engine.PlayerID
	cfg     Config
	rng     *rand.Rand

	phase      Phase
	members    map[engine.PlayerID]*member
	order      []engine.PlayerID // join order, for deterministic output
	spectators map[engine.PlayerID]bool
	invites    map[inviteKey]bool

	game    *engine.State
	dl      *deadlines
	holdFor engine.PlayerID // whose hold timer is running

	chatSeq  int
	chatLog  []ChatMessage // the most recent lines, for snapshots
	chatRate map[engine.PlayerID]*bucket
	sigRate  map[engine.PlayerID]*bucket

	out []Out

	seqs map[engine.PlayerID]int64 // last sequence number given to each recipient
	logs map[engine.PlayerID][]Out // what each recipient was last sent, for replay
}

// New creates a room with its creator already in it.
func New(cfg Config, id string, creator engine.PlayerID, rng *rand.Rand, now time.Time) (*Room, []Out) {
	r := &Room{
		id:         id,
		creator:    creator,
		cfg:        cfg,
		rng:        rng,
		members:    map[engine.PlayerID]*member{},
		spectators: map[engine.PlayerID]bool{},
		invites:    map[inviteKey]bool{},
		dl:         newDeadlines(),
		chatRate:   map[engine.PlayerID]*bucket{},
		sigRate:    map[engine.PlayerID]*bucket{},
		seqs:       map[engine.PlayerID]int64{},
		logs:       map[engine.PlayerID][]Out{},
	}
	_ = r.join(now, creator)
	r.syncCountdown(now)
	r.updateEmpty(now)
	return r, r.take()
}

// MarshalText makes phases readable in JSON.
func (p Phase) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// Info is what a room list shows.
type Info struct {
	ID         string
	Creator    engine.PlayerID
	Phase      Phase
	Players    int
	Teams      int // teams formed so far (pairs)
	Spectators int
	MaxPlayers int
	Joinable   bool
}

func (r *Room) Info() Info {
	pairs := 0
	for _, m := range r.members {
		if m.partner != "" {
			pairs++
		}
	}
	return Info{
		ID: r.id, Creator: r.creator, Phase: r.phase,
		Players: len(r.members), Teams: pairs / 2, Spectators: len(r.spectators),
		MaxPlayers: r.cfg.MaxPlayers,
		Joinable:   r.phase == PhaseLobby && len(r.members) < r.cfg.MaxPlayers,
	}
}

func (r *Room) ID() string          { return r.id }
func (r *Room) Phase() Phase        { return r.phase }
func (r *Room) Game() *engine.State { return r.game }
func (r *Room) take() []Out         { o := r.out; r.out = nil; return o }

// emit queues a message for one recipient. Messages that say something
// happened get the recipient's next sequence number and are kept, so a client
// that reconnects can be told what it missed. Messages that only describe the
// current state (snapshots, hands, timers) are not numbered: a snapshot
// replaces them.
func (r *Room) emit(to engine.PlayerID, m Message) {
	o := Out{To: to, Msg: m}
	if logged(m) {
		r.seqs[to]++
		o.Seq = r.seqs[to]
		l := append(r.logs[to], o)
		if len(l) > replayLimit {
			l = slices.Clone(l[len(l)-replayLimit:])
		}
		r.logs[to] = l
	}
	r.out = append(r.out, o)
}

// replayLimit is how many messages are kept per recipient for replay.
const replayLimit = 500

func logged(m Message) bool {
	switch m.(type) {
	case Snapshot, HandUpdate, TimerStarted, Missed:
		return false
	}
	return true
}

// missed answers a Resume: everything sent to id after sequence number after.
func (r *Room) missed(id engine.PlayerID, after int64) {
	l := r.logs[id]
	cur := r.seqs[id]
	m := Missed{From: after + 1, To: cur, Complete: true}
	if after >= cur {
		m.From = cur + 1
	}
	for _, o := range l {
		if o.Seq > after {
			m.Messages = append(m.Messages, o)
		}
	}
	// The log is a window: if it no longer reaches back to what the client
	// last saw, say so and let the snapshot stand in for the gap.
	if after < cur && (len(l) == 0 || l[0].Seq > after+1) {
		m.Complete = false
	}
	r.emit(id, m)
}

// NextDeadline is when the room next needs a Tick.
func (r *Room) NextDeadline() (time.Time, bool) {
	_, at, ok := r.dl.next()
	return at, ok
}

// Handle applies one input from a user. On error nothing was changed or sent.
func (r *Room) Handle(now time.Time, from engine.PlayerID, in Input) ([]Out, error) {
	r.out = nil
	err := r.dispatch(now, from, in)
	if err != nil {
		r.out = nil
		return nil, err
	}
	r.syncCountdown(now)
	r.updateEmpty(now)
	return r.take(), nil
}

// Tick runs everything that has come due by now, in order.
func (r *Room) Tick(now time.Time) []Out {
	r.out = nil
	for {
		k, at, ok := r.dl.next()
		if !ok || at.After(now) {
			break
		}
		r.dl.clear(k)
		r.fire(k, at)
	}
	r.syncCountdown(now)
	r.updateEmpty(now)
	return r.take()
}

func (r *Room) dispatch(now time.Time, from engine.PlayerID, in Input) error {
	if r.phase == PhaseClosed {
		return ErrClosed
	}
	switch v := in.(type) {
	case Join:
		return r.join(now, from)
	case Spectate:
		return r.spectate(from)
	case Leave:
		return r.leave(now, from)
	case Connect:
		return r.connect(now, from)
	case Disconnect:
		return r.disconnect(now, from)
	case StartNow:
		return r.startNow(now, from)
	case InviteToRoom:
		return r.inviteToRoom(from, v.To)
	case InvitePartner:
		return r.invitePartner(from, v.To)
	case AcceptPartner:
		return r.acceptPartner(now, from, v.From)
	case DeclinePartner:
		return r.declinePartner(from, v.From)
	case Ready:
		return r.setReady(now, from, v.Ready)
	case TeamChat:
		return r.teamChat(from, v.Text)
	case Chat:
		return r.chat(now, from, v.Text)
	case SendSignal:
		return r.signal(now, from, v.Gesture)
	case Resume:
		if r.members[from] == nil && !r.spectators[from] {
			return ErrNotMember
		}
		r.missed(from, v.After)
		return nil
	case Pass:
		return r.play(now, from, engine.Pass{Player: from, Card: v.Card})
	case Suspect:
		return r.play(now, from, engine.Suspect{By: from, Target: v.Target})
	case Reveal:
		return r.play(now, from, engine.Reveal{Player: from, Types: v.Types})
	case Proclaim:
		return r.play(now, from, engine.Proclaim{By: from})
	case VoteEnd:
		return r.play(now, from, engine.VoteEnd{Player: from, Agree: v.Agree})
	}
	return ErrWrongPhase
}

// --- audiences ---

// audience is everyone who should hear room-wide news, in a stable order:
// players (including removed ones, who keep watching), then spectators.
func (r *Room) audience() []engine.PlayerID {
	out := slices.Clone(r.order)
	var specs []engine.PlayerID
	for id := range r.spectators {
		specs = append(specs, id)
	}
	slices.Sort(specs)
	return append(out, specs...)
}

func (r *Room) broadcast(m Message) {
	for _, id := range r.audience() {
		r.emit(id, m)
	}
}

// seesCards is the spectator perk.
func (r *Room) seesCards(id engine.PlayerID) bool {
	return r.cfg.SpectatorsSeeCards && r.spectators[id]
}

func (r *Room) activeMember(id engine.PlayerID) (*member, error) {
	m := r.members[id]
	if m == nil || m.removed {
		return nil, ErrNotPlayer
	}
	return m, nil
}

// --- lobby ---

func (r *Room) join(now time.Time, id engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrNotJoinable
	}
	if r.members[id] != nil {
		return ErrAlreadyMember
	}
	if len(r.members) >= r.cfg.MaxPlayers {
		return ErrRoomFull
	}
	delete(r.spectators, id)
	r.members[id] = &member{id: id, connected: true}
	r.order = append(r.order, id)
	r.broadcast(MemberJoined{Player: id})
	r.emit(id, r.snapshotFor(id))
	return nil
}

func (r *Room) spectate(id engine.PlayerID) error {
	if r.members[id] != nil {
		return ErrAlreadyMember
	}
	r.spectators[id] = true
	r.emit(id, r.snapshotFor(id))
	return nil
}

func (r *Room) leave(now time.Time, id engine.PlayerID) error {
	if r.spectators[id] {
		delete(r.spectators, id)
		return nil
	}
	m := r.members[id]
	if m == nil {
		return ErrNotPlayer
	}
	switch {
	case m.removed || r.phase == PhaseOver:
		r.deleteMember(id)
	case r.phase == PhaseLobby:
		r.leaveLobby(now, id, "left")
	default:
		// Deliberate drop-out is instant.
		return r.dropTeam(now, m.team)
	}
	return nil
}

func (r *Room) deleteMember(id engine.PlayerID) {
	delete(r.members, id)
	r.order = slices.DeleteFunc(r.order, func(o engine.PlayerID) bool { return o == id })
	r.dl.clear(dkey{dkReconnect, id})
}

func (r *Room) leaveLobby(now time.Time, id engine.PlayerID, reason string) {
	m := r.members[id]
	if p := m.partner; p != "" {
		r.members[p].partner = ""
		r.broadcast(TeamBroken{A: id, B: p})
	}
	for k := range r.invites {
		if k.from == id || k.to == id {
			delete(r.invites, k)
		}
	}
	r.broadcast(MemberLeft{Player: id, Reason: reason})
	r.deleteMember(id)
}

// syncCountdown runs after anything that can change who is in the lobby. It
// starts the countdown when enough players are in, cancels it if they drop
// below the minimum, and starts the game at once when the room is full (five
// teams' worth of players).
func (r *Room) syncCountdown(now time.Time) {
	if r.phase != PhaseLobby {
		return
	}
	if len(r.members) >= r.cfg.MaxPlayers {
		r.startGame(now)
		return
	}
	k := dkey{kind: dkCountdown}
	enough := len(r.members) >= r.cfg.MinPlayers
	switch {
	case enough && !r.dl.has(k):
		at := now.Add(r.cfg.LobbyCountdown)
		r.dl.set(k, at)
		r.broadcast(CountdownStarted{EndsAt: at})
	case !enough && r.dl.has(k):
		r.dl.clear(k)
		r.broadcast(CountdownCancelled{})
	}
}

// startNow lets the creator skip the countdown when everything is ready: at
// least two teams, and no player still without a partner.
func (r *Room) startNow(now time.Time, from engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	if from != r.creator || r.members[from] == nil {
		return ErrNotCreator
	}
	if len(r.members) < r.cfg.MinPlayers {
		return ErrTooFewPlayers
	}
	for _, m := range r.members {
		if m.partner == "" {
			return ErrUnpairedPlayers
		}
	}
	r.startGame(now)
	return nil
}

func (r *Room) inviteToRoom(from, to engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	if _, err := r.activeMember(from); err != nil {
		return err
	}
	if from == to || r.members[to] != nil {
		return ErrBadTarget
	}
	r.emit(to, RoomInvite{From: from, Room: r.id})
	return nil
}

func (r *Room) invitePartner(from, to engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	m, err := r.activeMember(from)
	if err != nil {
		return err
	}
	if from == to {
		return ErrBadTarget
	}
	if m.partner != "" {
		return ErrAlreadyPaired
	}
	if t := r.members[to]; t != nil {
		if t.partner != "" {
			return ErrAlreadyPaired
		}
	} else if len(r.members) >= r.cfg.MaxPlayers {
		return ErrRoomFull // they would need a seat to accept
	}
	k := inviteKey{from, to}
	if r.invites[k] {
		return ErrDuplicateInvite
	}
	r.invites[k] = true
	r.emit(to, PartnerInvite{From: from, To: to, Room: r.id})
	return nil
}

func (r *Room) acceptPartner(now time.Time, me, from engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	k := inviteKey{from, me}
	if !r.invites[k] {
		return ErrNoInvite
	}
	inviter := r.members[from]
	if inviter == nil {
		return ErrNoInvite
	}
	if inviter.partner != "" {
		return ErrAlreadyPaired
	}
	if mm := r.members[me]; mm != nil && mm.partner != "" {
		return ErrAlreadyPaired
	}
	if r.members[me] == nil { // accepting a partner invite also joins the room
		if err := r.join(now, me); err != nil {
			return err
		}
	}
	r.pair(from, me)
	return nil
}

func (r *Room) pair(a, b engine.PlayerID) {
	r.members[a].partner = b
	r.members[b].partner = a
	for k := range r.invites {
		if k.from == a || k.to == a || k.from == b || k.to == b {
			delete(r.invites, k)
		}
	}
	r.broadcast(TeamFormed{A: a, B: b})
}

func (r *Room) declinePartner(me, from engine.PlayerID) error {
	k := inviteKey{from, me}
	if !r.invites[k] {
		return ErrNoInvite
	}
	delete(r.invites, k)
	r.emit(from, PartnerDeclined{By: me})
	return nil
}

// startGame runs when the lobby countdown ends. Anyone still without a
// partner is paired at random with someone else who is free. If one player is
// left over (an odd number of free players), they become a spectator.
func (r *Room) startGame(now time.Time) {
	r.dl.clear(dkey{kind: dkCountdown})
	r.invites = map[inviteKey]bool{}

	var free []engine.PlayerID
	for _, id := range r.order {
		if r.members[id].partner == "" {
			free = append(free, id)
		}
	}
	r.rng.Shuffle(len(free), func(i, j int) { free[i], free[j] = free[j], free[i] })
	var demoted []engine.PlayerID
	if len(free)%2 == 1 {
		demoted = append(demoted, free[len(free)-1])
		free = free[:len(free)-1]
	}
	for i := 0; i+1 < len(free); i += 2 {
		r.members[free[i]].partner = free[i+1]
		r.members[free[i+1]].partner = free[i]
	}
	for _, id := range demoted {
		r.deleteMember(id)
		r.spectators[id] = true
	}

	var pairs [][2]engine.PlayerID
	for _, id := range r.order {
		if p := r.members[id].partner; id < p {
			pairs = append(pairs, [2]engine.PlayerID{id, p})
		}
	}
	r.rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	var specs []engine.PlayerSpec
	for t, pr := range pairs {
		for _, id := range pr {
			r.members[id].team = engine.TeamID(t)
			specs = append(specs, engine.PlayerSpec{ID: id, Team: engine.TeamID(t)})
		}
	}
	g, err := engine.New(r.cfg.Engine, specs, rand.New(rand.NewPCG(r.rng.Uint64(), r.rng.Uint64())))
	if err != nil {
		r.close(now, "could not start the game")
		return
	}
	r.game = g
	r.record(GameStarted{Room: r.id, At: now, Teams: slices.Clone(pairs)})
	r.broadcast(GameStarting{
		Teams:           pairs,
		Demoted:         demoted,
		Seats:           g.Seats(),
		SuspicionPoints: r.cfg.Engine.SuspicionPoints,
		WinsToFinish:    r.cfg.Engine.WinsToFinish,
	})
	r.startChat(now)
}

func (r *Room) close(at time.Time, reason string) {
	if r.game != nil && r.phase != PhaseOver && r.phase != PhaseClosed {
		r.record(GameFinished{Room: r.id, At: at, Abandoned: true})
	}
	r.phase = PhaseClosed
	r.dl = newDeadlines()
	r.holdFor = ""
	r.broadcast(RoomClosed{Reason: reason})
}

// updateEmpty closes the room if nobody has been connected for the grace
// period.
func (r *Room) updateEmpty(now time.Time) {
	k := dkey{kind: dkEmpty}
	if r.phase == PhaseClosed {
		return
	}
	connected := 0
	for _, m := range r.members {
		if m.connected && !m.removed {
			connected++
		}
	}
	switch {
	case connected == 0 && !r.dl.has(k):
		r.dl.set(k, now.Add(r.cfg.EmptyRoomGrace))
	case connected > 0 && r.dl.has(k):
		r.dl.clear(k)
	}
}

// fire handles one deadline that has come due. at is when it was due, so
// follow-on deadlines chain from there even if Tick runs late.
func (r *Room) fire(k dkey, at time.Time) {
	switch k.kind {
	case dkCountdown:
		r.startGame(at)
	case dkChat:
		r.startRound(at)
	case dkHold, dkForced:
		_ = r.apply(at, engine.AutoPass{Player: k.player})
	case dkReveal:
		_ = r.apply(at, engine.AutoReveal{Player: k.player})
	case dkReconnect:
		r.reconnectExpired(at, k.player)
	case dkEmpty:
		r.close(at, "empty")
	}
}
