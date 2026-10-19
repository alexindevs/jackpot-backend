package room

import (
	"slices"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// snapshotChat is how many recent chat lines a snapshot carries, so a player
// who reconnects sees what they missed without asking.
const snapshotChat = 50

// PlayerInfo is what everyone may know about a player.
type PlayerInfo struct {
	ID        engine.PlayerID
	Connected bool
	Partner   engine.PlayerID
	Ready     bool
	Removed   bool
	Team      engine.TeamID
}

// TimerInfo is a running countdown.
type TimerInfo struct {
	Kind   string
	Player engine.PlayerID
	EndsAt time.Time
}

// GameInfo is the state of the game itself, as one viewer may see it.
type GameInfo struct {
	Round          int
	Phase          engine.Phase
	Holder         engine.PlayerID
	Seats          []engine.PlayerID
	Wins           map[engine.TeamID]int
	Points         map[engine.PlayerID]int
	YourHand       *engine.Hand
	Hands          map[engine.PlayerID]engine.Hand // spectator perk only
	PendingReveals []engine.PlayerID
	ForcedPass     engine.PlayerID
}

// Snapshot is a whole-room state for one viewer. It is sent on join and on
// reconnect, so a client never has to replay history.
type Snapshot struct {
	Room        string
	Creator     engine.PlayerID // the only one who can skip the lobby countdown
	Phase       Phase
	Players     []PlayerInfo
	Spectators  int
	Timers      []TimerInfo
	Game        *GameInfo
	Chat        []ChatMessage // the most recent public chat, oldest first
	Seq         int64         // the recipient's sequence number as of this snapshot
	Gestures    []Gesture     // the signal vocabulary
	PausedUntil *time.Time    // set while the game is paused, waiting for its players
}

func (r *Room) snapshotFor(viewer engine.PlayerID) Snapshot {
	s := Snapshot{Room: r.id, Creator: r.creator, Phase: r.phase, Spectators: len(r.spectators), Seq: r.seqs[viewer], Gestures: slices.Clone(r.cfg.Gestures)}
	if r.paused {
		until := r.pausedUntil
		s.PausedUntil = &until
	}
	for _, id := range r.order {
		m := r.members[id]
		s.Players = append(s.Players, PlayerInfo{
			ID: id, Connected: m.connected, Partner: m.partner,
			Ready: m.ready, Removed: m.removed, Team: m.team,
		})
	}
	for k, e := range r.dl.m {
		if k.kind == dkEmpty {
			continue
		}
		s.Timers = append(s.Timers, TimerInfo{Kind: k.kind.label(), Player: k.player, EndsAt: e.at})
	}
	sortTimers(s.Timers)
	s.Chat = slices.Clone(r.chatLog[max(0, len(r.chatLog)-snapshotChat):])

	if g := r.game; g != nil {
		gi := &GameInfo{
			Round:          g.Round(),
			Phase:          g.Phase(),
			Holder:         g.Holder(),
			Seats:          g.Seats(),
			Wins:           map[engine.TeamID]int{},
			Points:         map[engine.PlayerID]int{},
			PendingReveals: g.PendingReveals(),
			ForcedPass:     g.ForcedPassPlayer(),
		}
		for _, t := range g.Teams() {
			gi.Wins[t] = g.Wins(t)
		}
		for _, id := range gi.Seats {
			gi.Points[id] = g.Points(id)
		}
		if m := r.members[viewer]; m != nil && !m.removed {
			h := g.Hand(viewer)
			gi.YourHand = &h
		}
		if r.seesCards(viewer) {
			gi.Hands = map[engine.PlayerID]engine.Hand{}
			for _, id := range gi.Seats {
				gi.Hands[id] = g.Hand(id)
			}
		}
		s.Game = gi
	}
	return s
}

func sortTimers(ts []TimerInfo) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && lessTimer(ts[j], ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func lessTimer(a, b TimerInfo) bool {
	if !a.EndsAt.Equal(b.EndsAt) {
		return a.EndsAt.Before(b.EndsAt)
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Player < b.Player
}
