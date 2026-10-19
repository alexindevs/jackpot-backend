package room

import "github.com/alexindevs/jackpot-backend/internal/engine"

// deliver sends one engine event to everyone, hiding what each recipient may
// not see. Everything the engine reports is public except two things:
//   - the type of a passed card (only the passer and the receiver know it)
//   - the options in a reveal prompt (only the player who must choose)
//
// Spectators with the card perk see both.
func (r *Room) deliver(ev engine.Event) {
	for _, to := range r.audience() {
		if msg := r.redact(ev, to); msg != nil {
			r.emit(to, msg)
		}
	}
}

func (r *Room) redact(ev engine.Event, to engine.PlayerID) Message {
	switch e := ev.(type) {
	case engine.Passed:
		if to != e.From && to != e.To && !r.seesCards(to) {
			e.Card = CardHidden
		}
		return GameEvent{Event: e}
	case engine.RevealRequested:
		if to != e.Target && !r.seesCards(to) {
			return nil
		}
	}
	return GameEvent{Event: ev}
}

// sendHandUpdates tells each player their new hand if it changed, and gives
// perk spectators every change.
func (r *Room) sendHandUpdates(before map[engine.PlayerID]engine.Hand) {
	for _, id := range r.order {
		m := r.members[id]
		after := r.game.Hand(id)
		if m.removed || after == before[id] {
			continue
		}
		msg := HandUpdate{Player: id, Hand: after}
		r.emit(id, msg)
		for _, to := range r.audience() {
			if to != id && r.seesCards(to) {
				r.emit(to, msg)
			}
		}
	}
}
