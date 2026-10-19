// Package engine is the Jackpot game engine: a pure, deterministic state
// machine. It does no I/O and owns no timers. Callers (the room runner) feed
// it Commands one at a time, in the order the room's event queue decided, and
// get back the Events that happened. See docs/rules.md for the rules it
// implements.
package engine

import (
	"errors"
	"fmt"
)

// NumTypes is the number of card types.
const NumTypes = 4

// CardType is one of the four card types.
type CardType uint8

const (
	Circle CardType = iota
	Triangle
	Cross
	Square
)

func (c CardType) String() string {
	switch c {
	case Circle:
		return "circle"
	case Triangle:
		return "triangle"
	case Cross:
		return "cross"
	case Square:
		return "square"
	}
	return fmt.Sprintf("card(%d)", uint8(c))
}

// PlayerID identifies a player. It is whatever the identity layer decides;
// the engine only needs it to be unique within a game.
type PlayerID string

// TeamID identifies a team.
type TeamID int

// Hand counts the cards held, per type. Cards of one type are
// indistinguishable, so counts are the whole story.
type Hand [NumTypes]int

// Total is the number of cards in the hand.
func (h Hand) Total() int {
	n := 0
	for _, c := range h {
		n += c
	}
	return n
}

// Types lists the types with at least one card, in type order.
func (h Hand) Types() []CardType {
	var out []CardType
	for t, c := range h {
		if c > 0 {
			out = append(out, CardType(t))
		}
	}
	return out
}

// Distinct is the number of types with at least one card.
func (h Hand) Distinct() int { return len(h.Types()) }

// HasFourOfAKind reports whether some type has four or more cards.
func (h Hand) HasFourOfAKind() bool {
	for _, c := range h {
		if c >= 4 {
			return true
		}
	}
	return false
}

// Phase is the game's coarse state.
type Phase int

const (
	// PhaseWaiting is between rounds (and before the first one). The private
	// team chat happens here; the room runner sends StartRound when it ends.
	PhaseWaiting Phase = iota
	PhasePlaying
	PhaseOver
)

// Config holds the tunable numbers.
type Config struct {
	// SpareCopies is how many cards of each type the deck holds beyond one per
	// player. The deck has players+SpareCopies of each type. Passing never
	// draws from the pool, so the spares only matter for resets. One is the
	// minimum (the extra passing card).
	SpareCopies     int
	SuspicionPoints int // per player, refreshed every round
	WinsToFinish    int // round wins that end the game (J-A-C-K-P-O-T)
}

// DefaultConfig matches docs/rules.md.
func DefaultConfig() Config {
	return Config{SpareCopies: 7, SuspicionPoints: 3, WinsToFinish: 7}
}

// Letters is what round wins spell.
const Letters = "JACKPOT"

// Errors returned by Apply. A command that returns an error leaves the state
// untouched.
var (
	ErrGameOver                = errors.New("game is over")
	ErrWrongPhase              = errors.New("command not allowed in this phase")
	ErrUnknownCommand          = errors.New("unknown command")
	ErrUnknownPlayer           = errors.New("unknown or removed player")
	ErrUnknownTeam             = errors.New("unknown or removed team")
	ErrNotHolder               = errors.New("player does not hold the passing card")
	ErrBadCard                 = errors.New("invalid card type")
	ErrNoSuchCard              = errors.New("player does not hold that card")
	ErrSelfSuspect             = errors.New("cannot suspect yourself")
	ErrNoPoints                = errors.New("no suspicion points left")
	ErrBusy                    = errors.New("player is busy resolving an earlier suspicion")
	ErrNoPending               = errors.New("nothing to answer")
	ErrBadReveal               = errors.New("invalid reveal")
	ErrProclaimRevoked         = errors.New("player may no longer proclaim")
	ErrPartnerHoldsPassingCard = errors.New("cannot proclaim while your partner holds the passing card")
)
