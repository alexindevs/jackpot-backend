package room

import (
	"cmp"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// A game that loses all its players for a while, or whose server restarts, is
// not ended: it is paused. Nothing in a paused game moves (no timers run, no
// one can play) and it waits for its players. When every player still in the
// game is connected again it picks up where it was, and if they don't all come
// back within Config.PausedFor the game is abandoned.

// Paused tells the room a game has been paused and when it will be given up.
type Paused struct {
	Until   time.Time
	Players []engine.PlayerID // who is still in the game and can come back to it
}

// Resumed tells the room a paused game is running again.
type Resumed struct{}

func (Paused) isMessage()  {}
func (Resumed) isMessage() {}

// Paused reports whether the game is waiting for its players, and until when.
func (r *Room) Paused() (time.Time, bool) { return r.pausedUntil, r.paused }

// Players lists the players still in the game, in join order.
func (r *Room) Players() []engine.PlayerID {
	var out []engine.PlayerID
	for _, id := range r.order {
		if m := r.members[id]; m != nil && !m.removed {
			out = append(out, id)
		}
	}
	return out
}

// RosterEntry is one player of a game, as the list of someone's paused games
// shows them.
type RosterEntry struct {
	ID        engine.PlayerID
	Team      engine.TeamID
	Connected bool
}

// Roster lists the players still in the game.
func (r *Room) Roster() []RosterEntry {
	var out []RosterEntry
	pairs := map[engine.PlayerID]engine.TeamID{} // lobby teams are numbered in join order
	if r.game == nil {
		for _, id := range r.order {
			if p := r.members[id].partner; p != "" {
				if _, seen := pairs[p]; !seen {
					pairs[id] = engine.TeamID(len(pairs) / 2)
					pairs[p] = pairs[id]
				}
			}
		}
	}
	for _, id := range r.order {
		if m := r.members[id]; m != nil && !m.removed {
			team := m.team
			if r.game == nil {
				team = -1 // no partner yet
				if t, ok := pairs[id]; ok {
					team = t
				}
			}
			out = append(out, RosterEntry{ID: id, Team: team, Connected: m.connected})
		}
	}
	return out
}

// Round is the round being played, or about to be played, in a game.
func (r *Room) Round() int {
	if r.game == nil {
		return 0
	}
	return r.game.Round()
}

// Pause freezes a running game until the given time. It is for the server
// (shutting down, say) as well as for the room itself.
func (r *Room) Pause(now, until time.Time) []Out {
	r.out = nil
	if r.game != nil && !r.paused && (r.phase == PhaseChat || r.phase == PhasePlaying) {
		r.pause(now, until)
	}
	return r.take()
}

func (r *Room) pause(now, until time.Time) {
	r.paused = true
	r.pausedUntil = until
	r.dl = newDeadlines()
	r.holdFor = ""
	r.dl.set(dkey{kind: dkPause}, until)
	r.broadcast(Paused{Until: until, Players: r.Players()})
}

// maybeResume restarts a paused game once everyone still in it is connected.
func (r *Room) maybeResume(now time.Time) {
	if !r.paused || r.phase == PhaseClosed {
		return
	}
	any := false
	for _, m := range r.members {
		if m.removed {
			continue
		}
		any = true
		if !m.connected {
			return
		}
	}
	if any {
		r.resume(now)
	}
}

// resume starts everything again from fresh timers. A round in progress gets
// a new hold timer for whoever holds the passing card; between rounds the
// private team chat starts over.
func (r *Room) resume(now time.Time) {
	r.paused = false
	r.pausedUntil = time.Time{}
	r.dl = newDeadlines()
	r.holdFor = ""
	r.broadcast(Resumed{})
	if r.game == nil { // a lobby: the countdown starts again if there are enough people
		return
	}
	switch r.game.Phase() {
	case engine.PhaseOver:
		r.phase = PhaseOver
	case engine.PhaseWaiting:
		r.startChat(now)
	default:
		r.phase = PhasePlaying
		r.syncTimers(now)
	}
}

// Saved is what is kept of a live game so it can be brought back: the
// setup, every engine command applied since, and enough chat to carry on.
type Saved struct {
	ID       string
	Creator  engine.PlayerID
	Setup    engine.Setup
	Commands []engine.Command
	ChatSeq  int
	Chat     []ChatMessage // the most recent public chat, oldest first
	TeamChat []TeamChatEntry
	Since    time.Time // when the game was last active
}

// TeamChatEntry is a saved line of private team chat.
type TeamChatEntry struct {
	Seq   int
	Round int
	Team  engine.TeamID
	From  engine.PlayerID
	Text  string
	At    time.Time
}

// Restore rebuilds a room from a Saved game. The room comes back paused, with
// nobody connected, waiting for its players for Config.PausedFor after Since.
// It fails if the log doesn't replay or the game was already over.
func Restore(cfg Config, sv Saved, now time.Time) (*Room, error) {
	g, err := engine.Replay(sv.Setup, sv.Commands)
	if err != nil {
		return nil, err
	}
	if g.Phase() == engine.PhaseOver {
		return nil, errors.New("the game was already over")
	}
	until := sv.Since.Add(cfg.PausedFor)
	if !until.After(now) {
		return nil, errors.New("the game was left paused for too long")
	}

	r := &Room{
		id:         sv.ID,
		creator:    sv.Creator,
		cfg:        cfg,
		rng:        rand.New(rand.NewPCG(sv.Setup.SeedA^0x5bd1e995, sv.Setup.SeedB+uint64(len(sv.Commands)))),
		members:    map[engine.PlayerID]*member{},
		spectators: map[engine.PlayerID]bool{},
		invites:    map[inviteKey]bool{},
		dl:         newDeadlines(),
		chatRate:   map[engine.PlayerID]*bucket{},
		sigRate:    map[engine.PlayerID]*bucket{},
		seqs:       map[engine.PlayerID]int64{},
		logs:       map[engine.PlayerID][]Out{},
		game:       g,
		cmdSeq:     len(sv.Commands),
		chatSeq:    sv.ChatSeq,
		chatLog:    slices.Clone(sv.Chat),
		teamLog:    map[engine.TeamID][]TeamChatMessage{},
	}
	for _, e := range sv.TeamChat {
		r.teamLog[e.Team] = append(r.teamLog[e.Team], TeamChatMessage{ID: e.Seq, Round: e.Round, From: e.From, Text: e.Text, At: e.At})
		r.teamSeq = max(r.teamSeq, e.Seq)
	}
	for t, l := range r.teamLog {
		if over := len(l) - maxTeamLog; over > 0 {
			r.teamLog[t] = slices.Clone(l[over:])
		}
	}
	partner := map[engine.PlayerID]engine.PlayerID{}
	for _, a := range sv.Setup.Specs {
		for _, b := range sv.Setup.Specs {
			if a.ID != b.ID && a.Team == b.Team {
				partner[a.ID] = b.ID
			}
		}
	}
	for _, sp := range sv.Setup.Specs {
		r.members[sp.ID] = &member{id: sp.ID, partner: partner[sp.ID], team: sp.Team, removed: !g.Active(sp.ID)}
		r.order = append(r.order, sp.ID)
	}
	if g.Phase() == engine.PhaseWaiting {
		r.phase = PhaseChat
	} else {
		r.phase = PhasePlaying
	}
	r.paused = true
	r.pausedUntil = until
	r.dl.set(dkey{kind: dkPause}, until)
	return r, nil
}

// reportLobby tells OnRecord about the lobby when it has changed, so it can be
// saved. When the room stops being a lobby it says so once, so the saved copy
// can be removed.
func (r *Room) reportLobby(now time.Time) {
	if r.cfg.OnRecord == nil {
		return
	}
	if r.phase != PhaseLobby {
		if r.savedLobby != nil {
			r.savedLobby = nil
			r.record(LobbyState{Room: r.id, At: now, Creator: r.creator, Gone: true})
		}
		return
	}
	st := LobbyState{Room: r.id, At: now, Creator: r.creator, Members: slices.Clone(r.order)}
	for _, id := range r.order {
		if p := r.members[id].partner; p != "" && id < p {
			st.Pairs = append(st.Pairs, [2]engine.PlayerID{id, p})
		}
	}
	for k := range r.invites {
		st.Invites = append(st.Invites, [2]engine.PlayerID{k.from, k.to})
	}
	slices.SortFunc(st.Invites, func(a, b [2]engine.PlayerID) int {
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		return cmp.Compare(a[1], b[1])
	})
	if r.savedLobby != nil && sameLobby(*r.savedLobby, st) {
		return
	}
	r.savedLobby = &st
	r.record(st)
}

func sameLobby(a, b LobbyState) bool {
	a.At, b.At = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

// SavedLobby is a saved lobby, to bring back with RestoreLobby.
type SavedLobby struct {
	ID      string
	Creator engine.PlayerID
	Members []engine.PlayerID
	Pairs   [][2]engine.PlayerID
	Invites [][2]engine.PlayerID
	Since   time.Time // when the lobby last changed
}

// RestoreLobby rebuilds a lobby after a restart. It comes back paused with
// nobody connected: its members find it in their paused games and go back, and
// the countdown starts again once they all have. It is closed if nobody does
// within Config.LobbyPausedFor of the last change.
func RestoreLobby(cfg Config, sl SavedLobby, now time.Time) (*Room, error) {
	if len(sl.Members) == 0 {
		return nil, errors.New("the lobby had no members")
	}
	until := sl.Since.Add(cfg.LobbyPausedFor)
	if !until.After(now) {
		return nil, errors.New("the lobby waited too long for its members")
	}
	r := &Room{
		id:         sl.ID,
		creator:    sl.Creator,
		cfg:        cfg,
		rng:        rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(len(sl.Members)))),
		members:    map[engine.PlayerID]*member{},
		spectators: map[engine.PlayerID]bool{},
		invites:    map[inviteKey]bool{},
		dl:         newDeadlines(),
		chatRate:   map[engine.PlayerID]*bucket{},
		sigRate:    map[engine.PlayerID]*bucket{},
		seqs:       map[engine.PlayerID]int64{},
		logs:       map[engine.PlayerID][]Out{},
		teamLog:    map[engine.TeamID][]TeamChatMessage{},
		phase:      PhaseLobby,
	}
	for _, id := range sl.Members {
		if r.members[id] != nil {
			return nil, errors.New("a member is listed twice")
		}
		r.members[id] = &member{id: id}
		r.order = append(r.order, id)
	}
	for _, p := range sl.Pairs {
		a, b := r.members[p[0]], r.members[p[1]]
		if a == nil || b == nil || a.partner != "" || b.partner != "" {
			return nil, errors.New("the lobby's pairs don't match its members")
		}
		a.partner, b.partner = p[1], p[0]
	}
	for _, iv := range sl.Invites {
		if r.members[iv[0]] != nil && r.members[iv[1]] != nil {
			r.invites[inviteKey{iv[0], iv[1]}] = true
		}
	}
	r.paused = true
	r.pausedUntil = until
	r.dl.set(dkey{kind: dkPause}, until)
	st := LobbyState{Room: sl.ID, Creator: sl.Creator, Members: slices.Clone(r.order), Pairs: sl.Pairs, Invites: sl.Invites}
	r.savedLobby = &st
	return r, nil
}
