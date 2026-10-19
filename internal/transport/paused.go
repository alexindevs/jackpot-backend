package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

var errNoSuchGame = errors.New("no such paused game")

// A game that loses its players, or whose server restarted, is paused (see the
// room package). Players are not put back in it automatically: it is listed
// among their paused games (my_games) and they choose to go back (resume_game).
// Until they do it does not count as their current room, so they are free to
// do something else meanwhile.

// RestoreItem is a saved game to bring back, with its database id.
type RestoreItem struct {
	GameID int64
	Saved  room.Saved
}

// Restored says which room a saved game came back as.
type Restored struct {
	GameID int64
	Room   string
}

// Restore brings saved games back as paused rooms. Profiles are the names and
// looks of their players, for showing who is in a game before they sign in.
// Games that can't be rebuilt are returned with the reason.
func (m *Manager) Restore(items []RestoreItem, profiles map[engine.PlayerID]Identity) (ok []Restored, failed map[int64]error) {
	failed = map[int64]error{}
	m.mu.Lock()
	for id, p := range profiles {
		m.known[id] = p
	}
	m.mu.Unlock()

	for _, it := range items {
		r, err := room.Restore(m.cfg.Room, it.Saved, m.cfg.Now())
		if err != nil {
			failed[it.GameID] = err
			continue
		}
		m.mu.Lock()
		if m.rooms[it.Saved.ID] != nil {
			m.mu.Unlock()
			failed[it.GameID] = fmt.Errorf("room %s already exists", it.Saved.ID)
			continue
		}
		m.mu.Unlock()
		m.launch(it.Saved.ID, r, nil)
		m.mu.Lock()
		for _, id := range r.Players() {
			m.addPausedLocked(id, it.Saved.ID)
		}
		m.mu.Unlock()
		ok = append(ok, Restored{GameID: it.GameID, Room: it.Saved.ID})
	}
	return ok, failed
}

// RestoreLobbies brings saved lobbies back as paused rooms. Each is returned
// by room id when it can't be rebuilt, with the reason.
func (m *Manager) RestoreLobbies(items []room.SavedLobby, profiles map[engine.PlayerID]Identity) (ok []string, failed map[string]error) {
	failed = map[string]error{}
	m.mu.Lock()
	for id, p := range profiles {
		m.known[id] = p
	}
	m.mu.Unlock()
	for _, sl := range items {
		r, err := room.RestoreLobby(m.cfg.Room, sl, m.cfg.Now())
		if err != nil {
			failed[sl.ID] = err
			continue
		}
		m.mu.Lock()
		taken := m.rooms[sl.ID] != nil
		m.mu.Unlock()
		if taken {
			failed[sl.ID] = fmt.Errorf("room %s already exists", sl.ID)
			continue
		}
		m.launch(sl.ID, r, nil)
		m.mu.Lock()
		for _, id := range r.Players() {
			m.addPausedLocked(id, sl.ID)
		}
		m.mu.Unlock()
		ok = append(ok, sl.ID)
	}
	return ok, failed
}

func (m *Manager) addPausedLocked(user engine.PlayerID, roomID string) {
	if m.pausedBy[user] == nil {
		m.pausedBy[user] = map[string]bool{}
	}
	m.pausedBy[user][roomID] = true
}

// MyGame is one of a player's paused games.
type MyGame struct {
	Kind    string         `json:"kind"` // "game", or "lobby" for a room that had not started yet
	Room    string         `json:"room"`
	Round   int            `json:"round"`
	Until   time.Time      `json:"until"` // the game is given up at this time
	Players []MyGamePlayer `json:"players"`
}

// MyGamePlayer is someone in a paused game.
type MyGamePlayer struct {
	ID    engine.PlayerID `json:"id"`
	Name  string          `json:"name"`
	Style json.RawMessage `json:"style,omitempty"`
	Team  int             `json:"team"`
	Here  bool            `json:"here"` // connected to the game right now
}

// MyGames lists the paused games a player can go back to.
func (m *Manager) MyGames(user engine.PlayerID) []MyGame {
	m.mu.Lock()
	var ids []string
	for id := range m.pausedBy[user] {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	m.mu.Unlock()

	games := []MyGame{}
	for _, id := range ids {
		m.mu.Lock()
		mr := m.rooms[id]
		m.mu.Unlock()
		if mr == nil {
			m.forgetPaused(user, id)
			continue
		}
		var g MyGame
		var roster []room.RosterEntry
		var paused bool
		err := mr.runner.Inspect(func(r *room.Room) {
			g.Until, paused = r.Paused()
			g.Round = r.Round()
			g.Kind = "game"
			if r.Phase() == room.PhaseLobby {
				g.Kind = "lobby"
			}
			roster = r.Roster()
		})
		inGame := slices.ContainsFunc(roster, func(e room.RosterEntry) bool { return e.ID == user })
		if err != nil || !paused || !inGame {
			m.forgetPaused(user, id)
			continue
		}
		g.Room = id
		m.mu.Lock()
		for _, e := range roster {
			ident, ok := m.identLocked(e.ID)
			if !ok {
				ident = Identity{ID: e.ID, Name: string(e.ID)}
			}
			g.Players = append(g.Players, MyGamePlayer{ID: ident.ID, Name: ident.Name, Style: ident.Style, Team: int(e.Team), Here: e.Connected})
		}
		m.mu.Unlock()
		games = append(games, g)
	}
	return games
}

func (m *Manager) forgetPaused(user engine.PlayerID, roomID string) {
	m.mu.Lock()
	delete(m.pausedBy[user], roomID)
	if len(m.pausedBy[user]) == 0 {
		delete(m.pausedBy, user)
	}
	m.mu.Unlock()
}

// ResumeGame puts a player back into one of their paused games. The game
// carries on once every player still in it has done the same.
func (m *Manager) ResumeGame(user engine.PlayerID, roomID string) error {
	m.mu.Lock()
	if _, in := m.userRoom[user]; in {
		m.mu.Unlock()
		return errAlreadyInRoom
	}
	mr := m.rooms[roomID]
	if !m.pausedBy[user][roomID] || mr == nil {
		m.mu.Unlock()
		return errNoSuchGame
	}
	m.userRoom[user] = roomID
	delete(m.pausedBy[user], roomID)
	if len(m.pausedBy[user]) == 0 {
		delete(m.pausedBy, user)
	}
	m.mu.Unlock()

	if err := mr.runner.Submit(user, room.Connect{}); err != nil {
		m.dropMapping(user)
		if errors.Is(err, room.ErrNotPlayer) || errors.Is(err, room.ErrClosed) {
			return errNoSuchGame
		}
		m.mu.Lock()
		m.addPausedLocked(user, roomID)
		m.mu.Unlock()
		return err
	}
	return nil
}

// pausedNow reports whether a user's current room is paused.
func (m *Manager) pausedNow(mr *managed) bool {
	var paused bool
	if err := mr.runner.Inspect(func(r *room.Room) { _, paused = r.Paused() }); err != nil {
		return false
	}
	return paused
}

// launch starts a room's loop. initial is what the room said while being
// created, if anything.
func (m *Manager) launch(id string, r *room.Room, initial []room.Out) {
	ctx, cancel := context.WithCancel(m.ctx)
	mr := &managed{id: id, cancel: cancel}
	mr.runner = room.NewRunner(r, m.cfg.Now, func(outs []room.Out) { m.deliver(id, outs) })
	m.mu.Lock()
	m.rooms[id] = mr
	m.mu.Unlock()
	if len(initial) > 0 {
		m.deliver(id, initial)
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		mr.runner.Run(ctx)
		cancel()
		m.roomEnded(id)
	}()
}

// onPaused moves everyone out of a room that has just been paused: it stops
// being anyone's current room, and its players can find it in my_games.
func (m *Manager) onPaused(roomID string, to engine.PlayerID, players []engine.PlayerID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userRoom[to] == roomID {
		delete(m.userRoom, to)
	}
	if slices.Contains(players, to) {
		m.addPausedLocked(to, roomID)
	}
	m.gcIdentLocked(to)
}

// identLocked finds what we know about a player: who is signed in, or who was
// loaded from the database for a paused game.
func (m *Manager) identLocked(id engine.PlayerID) (Identity, bool) {
	if i, ok := m.idents[id]; ok {
		return i, true
	}
	i, ok := m.known[id]
	return i, ok
}
