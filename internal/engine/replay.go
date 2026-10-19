package engine

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
)

// Setup is everything needed to build a game again from nothing: the rules,
// who is on which team, and the two seeds of the random generator. The engine
// is deterministic, so a Setup plus the commands applied since the start is
// the whole game.
type Setup struct {
	Config Config
	Specs  []PlayerSpec
	SeedA  uint64
	SeedB  uint64
}

// NewFromSetup creates a game from a Setup.
func NewFromSetup(su Setup) (*State, error) {
	s, err := New(su.Config, su.Specs, rand.New(rand.NewPCG(su.SeedA, su.SeedB)))
	if err != nil {
		return nil, err
	}
	s.setup = &su
	return s, nil
}

// Setup is how this game was created, if it was created from one.
func (s *State) Setup() (Setup, bool) {
	if s.setup == nil {
		return Setup{}, false
	}
	su := *s.setup
	su.Specs = append([]PlayerSpec(nil), su.Specs...)
	return su, true
}

// Replay builds a game from su and applies cmds in order. A command that
// fails here means the log doesn't belong to this setup (or the rules
// changed under it), which is an error.
func Replay(su Setup, cmds []Command) (*State, error) {
	s, err := NewFromSetup(su)
	if err != nil {
		return nil, err
	}
	for i, c := range cmds {
		if _, err := s.Apply(c); err != nil {
			return nil, fmt.Errorf("replaying command %d (%T): %w", i+1, c, err)
		}
	}
	return s, nil
}

// Active reports whether the player is still in the game (not removed).
func (s *State) Active(id PlayerID) bool {
	p, ok := s.players[id]
	return ok && p.active
}

// wireCommand is the stored form of a Command. Kind names the command; the
// other fields are used by the kinds that need them.
type wireCommand struct {
	Kind   string     `json:"k"`
	Player PlayerID   `json:"p,omitempty"`
	Target PlayerID   `json:"t,omitempty"`
	Card   *CardType  `json:"c,omitempty"`
	Types  []CardType `json:"ty,omitempty"`
	Team   *TeamID    `json:"tm,omitempty"`
	Agree  *bool      `json:"a,omitempty"`
}

// MarshalCommand turns a command into bytes that UnmarshalCommand reads back.
func MarshalCommand(c Command) ([]byte, error) {
	var w wireCommand
	switch v := c.(type) {
	case StartRound:
		w.Kind = "start_round"
	case Pass:
		w.Kind, w.Player, w.Card = "pass", v.Player, &v.Card
	case AutoPass:
		w.Kind, w.Player = "auto_pass", v.Player
	case Suspect:
		w.Kind, w.Player, w.Target = "suspect", v.By, v.Target
	case Reveal:
		w.Kind, w.Player, w.Types = "reveal", v.Player, v.Types
	case AutoReveal:
		w.Kind, w.Player = "auto_reveal", v.Player
	case Proclaim:
		w.Kind, w.Player = "proclaim", v.By
	case RemoveTeam:
		w.Kind, w.Team = "remove_team", &v.Team
	case VoteEnd:
		w.Kind, w.Player, w.Agree = "vote_end", v.Player, &v.Agree
	default:
		return nil, fmt.Errorf("cannot store a %T", c)
	}
	return json.Marshal(w)
}

// UnmarshalCommand reads a command written by MarshalCommand.
func UnmarshalCommand(b []byte) (Command, error) {
	var w wireCommand
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	need := func(ok bool, what string) error {
		if !ok {
			return fmt.Errorf("stored %s command is missing %s", w.Kind, what)
		}
		return nil
	}
	switch w.Kind {
	case "start_round":
		return StartRound{}, nil
	case "pass":
		if err := need(w.Card != nil, "its card"); err != nil {
			return nil, err
		}
		return Pass{Player: w.Player, Card: *w.Card}, nil
	case "auto_pass":
		return AutoPass{Player: w.Player}, nil
	case "suspect":
		return Suspect{By: w.Player, Target: w.Target}, nil
	case "reveal":
		return Reveal{Player: w.Player, Types: w.Types}, nil
	case "auto_reveal":
		return AutoReveal{Player: w.Player}, nil
	case "proclaim":
		return Proclaim{By: w.Player}, nil
	case "remove_team":
		if err := need(w.Team != nil, "its team"); err != nil {
			return nil, err
		}
		return RemoveTeam{Team: *w.Team}, nil
	case "vote_end":
		if err := need(w.Agree != nil, "the vote"); err != nil {
			return nil, err
		}
		return VoteEnd{Player: w.Player, Agree: *w.Agree}, nil
	}
	return nil, fmt.Errorf("unknown stored command %q", w.Kind)
}
