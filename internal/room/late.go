package room

import (
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Late joins. A game that has started cannot be joined on the spot, but
// anyone watching it, or anyone whose team was removed from it, can ask to
// play. They wait in line, still watching, and between rounds the first two in
// line are paired into a new team that starts with no wins and is seated at
// the next round. A player who is already in the game cannot join again.

// WaitingChanged says who is waiting to be paired into a team, in order.
type WaitingChanged struct{ Players []engine.PlayerID }

func (WaitingChanged) isMessage() {}

func (r *Room) joinLate(now time.Time, id engine.PlayerID) error {
	if r.phase != PhaseChat && r.phase != PhasePlaying {
		return ErrNotJoinable
	}
	if m := r.members[id]; m != nil && !m.removed {
		return ErrAlreadyMember
	}
	if slices.Contains(r.waiting, id) {
		return nil
	}
	if len(r.game.Teams()) >= r.cfg.MaxPlayers/2 {
		return ErrRoomFull
	}
	if r.members[id] == nil && !r.spectators[id] {
		r.spectators[id] = true // they watch while they wait
		r.emit(id, r.snapshotFor(id))
	}
	r.waiting = append(r.waiting, id)
	r.broadcast(WaitingChanged{Players: slices.Clone(r.waiting)})
	r.formLateTeams(now)
	return nil
}

// unwait takes someone out of the line, if they were in it.
func (r *Room) unwait(id engine.PlayerID) {
	if i := slices.Index(r.waiting, id); i >= 0 {
		r.waiting = slices.Delete(r.waiting, i, i+1)
		r.broadcast(WaitingChanged{Players: slices.Clone(r.waiting)})
	}
}

// formLateTeams pairs the first two in line, again and again, while there is
// room for another team. It only runs between rounds, while the pre-round
// chat is open, so nobody is dealt in halfway through a round.
func (r *Room) formLateTeams(now time.Time) {
	if r.phase != PhaseChat || r.paused || r.game == nil {
		return
	}
	for len(r.waiting) >= 2 && len(r.game.Teams()) < r.cfg.MaxPlayers/2 {
		a, b := r.waiting[0], r.waiting[1]
		r.waiting = r.waiting[2:]
		if err := r.apply(now, engine.AddTeam{A: a, B: b}); err != nil {
			continue // they stay watchers; the next two in line get their turn
		}
		r.broadcast(WaitingChanged{Players: slices.Clone(r.waiting)})
		r.emit(a, r.snapshotFor(a))
		r.emit(b, r.snapshotFor(b))
	}
}

// teamAdded makes the two players of a late team members of the room.
func (r *Room) teamAdded(now time.Time, e engine.TeamAdded) {
	for i, id := range e.Members {
		m := r.members[id]
		if m == nil {
			m = &member{id: id}
			r.members[id] = m
			r.order = append(r.order, id)
		}
		delete(r.spectators, id)
		*m = member{id: id, connected: true, partner: e.Members[1-i], team: e.Team}
	}
	r.record(TeamJoined{Room: r.id, At: now, Team: e.Team, Members: e.Members})
}
