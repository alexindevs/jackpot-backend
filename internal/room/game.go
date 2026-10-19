package room

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

const maxChatRunes = 500

// startChat opens the private team chat before a round.
func (r *Room) startChat(now time.Time) {
	r.phase = PhaseChat
	for _, m := range r.members {
		m.ready = false
	}
	at := now.Add(r.cfg.PreRoundChat)
	r.dl.set(dkey{kind: dkChat}, at)
	r.broadcast(ChatStarted{Round: r.game.Round() + 1, EndsAt: at})
}

func (r *Room) setReady(now time.Time, id engine.PlayerID, ready bool) error {
	if r.phase != PhaseChat {
		return ErrWrongPhase
	}
	m, err := r.activeMember(id)
	if err != nil {
		return err
	}
	m.ready = ready
	r.broadcast(ReadyChanged{Player: id, Ready: ready})
	r.checkAllReady(now)
	return nil
}

// checkAllReady starts the round early once every remaining player has
// clicked ready. Anyone who hasn't, connected or not, makes everyone wait.
func (r *Room) checkAllReady(now time.Time) {
	if r.phase != PhaseChat {
		return
	}
	any := false
	for _, m := range r.members {
		if m.removed {
			continue
		}
		any = true
		if !m.ready {
			return
		}
	}
	if any {
		r.startRound(now)
	}
}

func (r *Room) teamChat(from engine.PlayerID, text string) error {
	if r.phase != PhaseChat {
		return ErrWrongPhase
	}
	m, err := r.activeMember(from)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n == 0 || n > maxChatRunes {
		return ErrBadText
	}
	msg := TeamChatMessage{From: from, Text: text}
	r.emit(from, msg)
	r.emit(m.partner, msg)
	return nil
}

func (r *Room) startRound(now time.Time) {
	r.dl.clear(dkey{kind: dkChat})
	if err := r.apply(now, engine.StartRound{}); err != nil {
		r.close("could not start the round")
		return
	}
	r.phase = PhasePlaying
}

// play forwards a player's action to the engine.
func (r *Room) play(now time.Time, from engine.PlayerID, cmd engine.Command) error {
	if _, err := r.activeMember(from); err != nil {
		return err
	}
	if r.game == nil || r.phase == PhaseOver {
		return ErrWrongPhase
	}
	return r.apply(now, cmd)
}

func (r *Room) hands() map[engine.PlayerID]engine.Hand {
	m := make(map[engine.PlayerID]engine.Hand, len(r.members))
	for id := range r.members {
		m[id] = r.game.Hand(id)
	}
	return m
}

// apply runs one engine command and turns its events into messages and timers.
func (r *Room) apply(now time.Time, cmd engine.Command) error {
	before := r.hands()
	evs, err := r.game.Apply(cmd)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		r.deliver(ev)
		if rm, ok := ev.(engine.TeamRemoved); ok {
			for _, id := range rm.Members {
				if m := r.members[id]; m != nil {
					m.removed = true
					m.ready = false
				}
				r.dl.clear(dkey{dkReconnect, id})
			}
		}
	}
	r.sendHandUpdates(before)

	switch r.game.Phase() {
	case engine.PhaseOver:
		r.phase = PhaseOver
		r.dl = newDeadlines()
		r.holdFor = ""
	case engine.PhaseWaiting:
		r.dl.clearKind(dkHold)
		r.dl.clearKind(dkReveal)
		r.dl.clearKind(dkForced)
		r.holdFor = ""
		switch r.phase {
		case PhasePlaying: // a round was just won
			r.startChat(now)
		case PhaseChat: // a team left; the rest may all be ready
			r.checkAllReady(now)
		}
	case engine.PhasePlaying:
		r.syncTimers(now)
	}
	return nil
}

// syncTimers derives the round's timers from the engine's state, so they
// can't drift out of step with it.
func (r *Room) syncTimers(now time.Time) {
	g := r.game
	holder, forced := g.Holder(), g.ForcedPassPlayer()

	if forced != "" {
		r.dl.clearKind(dkHold)
		r.holdFor = ""
		if !r.dl.has(dkey{dkForced, forced}) {
			r.startTimer(dkForced, forced, now.Add(r.cfg.PromptTimeout))
		}
	} else {
		r.dl.clearKind(dkForced)
		if r.holdFor != holder {
			r.dl.clearKind(dkHold)
			r.holdFor = holder
			r.startTimer(dkHold, holder, now.Add(r.cfg.HoldTimer))
		}
	}

	pending := g.PendingReveals()
	for _, id := range r.dl.players(dkReveal) {
		if !slices.Contains(pending, id) {
			r.dl.clear(dkey{dkReveal, id})
		}
	}
	for _, id := range pending {
		if !r.dl.has(dkey{dkReveal, id}) {
			r.startTimer(dkReveal, id, now.Add(r.cfg.PromptTimeout))
		}
	}
}

func (r *Room) startTimer(kind dkind, id engine.PlayerID, at time.Time) {
	r.dl.set(dkey{kind, id}, at)
	r.broadcast(TimerStarted{Kind: kind.label(), Player: id, EndsAt: at})
}

// --- leaving and reconnecting ---

func (r *Room) dropTeam(now time.Time, team engine.TeamID) error {
	err := r.apply(now, engine.RemoveTeam{Team: team})
	if err == engine.ErrUnknownTeam {
		return nil
	}
	return err
}

func (r *Room) connect(now time.Time, id engine.PlayerID) error {
	m := r.members[id]
	if m == nil {
		return ErrNotPlayer
	}
	if !m.connected {
		m.connected = true
		r.dl.clear(dkey{dkReconnect, id})
		r.broadcast(ConnectionChanged{Player: id, Connected: true})
	}
	r.emit(id, r.snapshotFor(id))
	return nil
}

func (r *Room) disconnect(now time.Time, id engine.PlayerID) error {
	if r.spectators[id] {
		delete(r.spectators, id)
		return nil
	}
	m := r.members[id]
	if m == nil {
		return ErrNotPlayer
	}
	if m.removed {
		r.deleteMember(id)
		return nil
	}
	if !m.connected {
		return nil
	}
	m.connected = false
	if r.phase >= PhaseOver {
		r.broadcast(ConnectionChanged{Player: id})
		return nil
	}
	by := now.Add(r.cfg.ReconnectWindow)
	r.dl.set(dkey{dkReconnect, id}, by)
	r.broadcast(ConnectionChanged{Player: id, ReconnectBy: &by})
	return nil
}

// reconnectExpired removes someone who didn't come back in time. In a game
// that removes their whole team.
func (r *Room) reconnectExpired(at time.Time, id engine.PlayerID) {
	m := r.members[id]
	if m == nil || m.removed || m.connected {
		return
	}
	if r.phase == PhaseLobby {
		r.leaveLobby(at, id, "disconnected")
		return
	}
	_ = r.dropTeam(at, m.team)
}
