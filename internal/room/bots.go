package room

import (
	"errors"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/botname"
	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Bots are players the server drives. To the room a bot is a member like any
// other, with an id that starts with botname.Prefix; what makes it play is a
// BotDriver, which sees only what that player is allowed to see. The room
// itself treats bots in four ways differently from people:
//
//   - they are never disconnected, so they never start a reconnect window and
//     never keep a room alive on their own;
//   - a lobby needs a person in it to exist, and a game ends when no person is
//     left in it;
//   - a game with a bot in it never counts toward leaderboards;
//   - only the room's creator, always a person, can add or remove them.

// AddBots fills the lobby with bots. Each person without a partner gets a bot
// partner, and bot teams are added until there are two teams, or Teams of them
// if that is more than needed. Only the room's creator can send it, and only
// in the lobby.
type AddBots struct{ Teams int }

// RemoveBot takes a bot out of the lobby. A bot partnered with another bot
// goes together with it; a person who loses a bot partner is left to find
// another.
type RemoveBot struct{ Player engine.PlayerID }

func (AddBots) isInput()   {}
func (RemoveBot) isInput() {}

var (
	ErrNothingToAdd = errors.New("there is nobody to add bots for")
	ErrNotABot      = errors.New("that player is not a bot")
)

// BotDriver plays the bots of one room. It lives on the room's goroutine, next
// to the Runner, and is never given the room itself: it is told what the room
// sent to its bots, and asks for a snapshot of a bot's own view when it needs
// one, so it cannot see anything a person in that seat could not.
type BotDriver interface {
	// Observe is told everything the room has just sent, to anyone, and when.
	Observe(now time.Time, outs []Out)
	// Wake is when the driver next wants to act, if it does.
	Wake() (time.Time, bool)
	// Act returns what the bots do now. view gives a bot's own snapshot.
	Act(now time.Time, view func(engine.PlayerID) Snapshot) []BotAct
	// Result tells the driver what became of an act it asked for.
	Result(act BotAct, err error)
}

// BotAct is one thing a bot does.
type BotAct struct {
	Bot engine.PlayerID
	In  Input
}

// ViewFor is a player's own snapshot of the room: what they may see, no more.
func (r *Room) ViewFor(id engine.PlayerID) Snapshot { return r.snapshotFor(id) }

func isBot(m *member) bool { return m != nil && m.bot }

// hasBots reports whether any bot is, or was, in the game.
func (r *Room) hasBots() bool {
	for _, m := range r.members {
		if m.bot {
			return true
		}
	}
	return false
}

// hasPerson reports whether a person who has not been removed is still in the
// room.
func (r *Room) hasPerson() bool {
	for _, m := range r.members {
		if !m.bot && !m.removed {
			return true
		}
	}
	return false
}

func (r *Room) bots() int {
	n := 0
	for _, m := range r.members {
		if m.bot && !m.removed {
			n++
		}
	}
	return n
}

// newBot puts one bot in the lobby and returns its id.
func (r *Room) newBot() (engine.PlayerID, bool) {
	taken := map[engine.PlayerID]bool{}
	for id := range r.members {
		taken[id] = true
	}
	id, ok := botname.Pick(r.rng, taken)
	if !ok {
		return "", false
	}
	r.members[id] = &member{id: id, connected: true, bot: true}
	r.order = append(r.order, id)
	r.broadcast(MemberJoined{Player: id})
	return id, true
}

func (r *Room) addBots(now time.Time, from engine.PlayerID, in AddBots) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	if from != r.creator || r.members[from] == nil || r.members[from].bot {
		return ErrNotCreator
	}
	if in.Teams < 0 {
		return ErrBadTarget
	}
	teams := 0
	var lonely []engine.PlayerID
	for _, id := range r.order {
		m := r.members[id]
		if m.partner == "" {
			if !m.bot {
				lonely = append(lonely, id)
			}
		} else {
			teams++
		}
	}
	teams /= 2
	// Every person without a partner gets a bot, and then bot teams are added
	// until there are two teams in all, or the number asked for.
	seats := r.cfg.MaxPlayers - len(r.members)
	need := len(lonely)
	botTeams := in.Teams
	if botTeams == 0 && teams+len(lonely) < 2 {
		botTeams = 2 - teams - len(lonely)
	}
	if need+2*botTeams == 0 {
		return ErrNothingToAdd
	}
	if need+2*botTeams > seats {
		return ErrRoomFull
	}
	for _, id := range lonely {
		b, ok := r.newBot()
		if !ok {
			return ErrRoomFull
		}
		r.pair(id, b)
	}
	for i := 0; i < botTeams; i++ {
		a, ok := r.newBot()
		if !ok {
			return ErrRoomFull
		}
		b, ok := r.newBot()
		if !ok {
			return ErrRoomFull
		}
		r.pair(a, b)
	}
	return nil
}

func (r *Room) removeBot(from, id engine.PlayerID) error {
	if r.phase != PhaseLobby {
		return ErrWrongPhase
	}
	if from != r.creator || r.members[from] == nil || r.members[from].bot {
		return ErrNotCreator
	}
	b := r.members[id]
	if !isBot(b) {
		return ErrNotABot
	}
	r.dropBot(id)
	return nil
}

// dropBot takes a bot out of the lobby, its bot partner with it.
func (r *Room) dropBot(id engine.PlayerID) {
	b := r.members[id]
	if b == nil {
		return
	}
	if p := b.partner; p != "" {
		r.broadcast(TeamBroken{A: id, B: p})
		if pm := r.members[p]; pm != nil {
			pm.partner = ""
			if pm.bot {
				r.broadcast(MemberLeft{Player: p, Reason: "removed"})
				r.deleteMember(p)
			}
		}
	}
	r.broadcast(MemberLeft{Player: id, Reason: "removed"})
	r.deleteMember(id)
}
