package bot

import (
	"fmt"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// A team's code is two gestures: one that means "I'm one card short" and one
// that means "I have all four". Partners use the same gesture for "four", so a
// person only has to remember two things.
type code struct{ close, four string }

type codeKey struct {
	team  engine.TeamID
	round int
}

// easy are the gestures a code is drawn from: ordinary, quick to do, and easy
// to find in the Signal list. They match the examples the team chat shows.
var easy = []string{
	"blink_once", "blink_twice", "wink_left", "raise_eyebrow", "look_left", "sneeze", "rub_nose",
	"cough", "clear_throat", "yawn", "tap_table", "crack_knuckles", "nod", "scratch_head",
	"tap_foot", "shake_leg", "hum_anthem", "say_oya", "say_abeg", "mention_jollof",
}

// codeFor is the team's code for a round. Both bots of a bot team get the same
// one, because it is made once, the first time anyone asks. It is new every
// round and never the same two rounds running.
func (d *Driver) codeFor(team engine.TeamID, round int, gestures []room.Gesture) code {
	key := codeKey{team, round}
	if c, ok := d.codes[key]; ok {
		return c
	}
	have := map[string]bool{}
	for _, g := range gestures {
		have[g.ID] = true
	}
	var pool []string
	for _, id := range easy {
		if have[id] && id != d.prev[team].close && id != d.prev[team].four {
			pool = append(pool, id)
		}
	}
	if len(pool) < 2 { // an unusual vocabulary: take whatever there is
		pool = pool[:0]
		for _, g := range gestures {
			pool = append(pool, g.ID)
		}
	}
	var c code
	if len(pool) >= 2 {
		d.rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		c = code{close: pool[0], four: pool[1]}
	}
	if len(d.codes) > 400 { // a long game: forget the old rounds
		d.codes = map[codeKey]code{}
	}
	d.codes[key] = c
	d.prev[team] = c
	return c
}

// bluff is a gesture that is not part of the team's code, sent to make the
// other teams waste their suspicion.
func (d *Driver) bluff(gestures []room.Gesture, c code) string {
	var pool []string
	for _, g := range gestures {
		if g.ID != c.close && g.ID != c.four {
			pool = append(pool, g.ID)
		}
	}
	if len(pool) == 0 {
		return ""
	}
	return pool[d.rng.IntN(len(pool))]
}

func labelOf(gestures []room.Gesture, id string) string {
	for _, g := range gestures {
		if g.ID == id {
			return g.Label
		}
	}
	return id
}

// templates are how a bot tells a person its code. They are written to be
// read by someone who has never seen the game: what each gesture means, where
// to find it, and what the bot will do about it. %[1]s is the "one card short"
// gesture and %[2]s the "four of a kind" one.
var templates = []string{
	"Oya, our code for this round. \"%[1]s\" means I'm one card short. \"%[2]s\" means I have all four. If you get four, send \"%[2]s\" from the Signal list and I'll call Jackpot for you.",
	"Code for this round, keep it between us. \"%[1]s\" = I'm close. \"%[2]s\" = I've got four of a kind. Send \"%[2]s\" when you have yours and I'll call Jackpot for you.",
	"Here's the plan this round. I'll send \"%[1]s\" when I'm nearly there and \"%[2]s\" when I'm done. You send \"%[2]s\" when you've got four and I'll shout Jackpot for you.",
	"Quick code before we start. \"%[1]s\" is me being one card away. \"%[2]s\" is me holding four. If it's you holding four, send \"%[2]s\" and I'll call Jackpot straight away.",
	"Abeg remember this one. \"%[1]s\" means almost. \"%[2]s\" means four of a kind. Look for them in your Signal list. When you send \"%[2]s\", I call Jackpot.",
}

// postText is what a bot says to its partner, a person, before a round.
func (d *Driver) postText(c code, gestures []room.Gesture) string {
	t := templates[d.rng.IntN(len(templates))]
	return fmt.Sprintf(t, labelOf(gestures, c.close), labelOf(gestures, c.four))
}
