package engine

// Command is something that happens to the game. The room runner turns
// player actions and timer expiries into Commands.
type Command interface{ isCommand() }

// StartRound deals a new round. Sent when the pre-round chat ends.
type StartRound struct{}

// Pass is the holder passing one card to the next player. It also answers a
// ForcedPassRequested.
type Pass struct {
	Player PlayerID
	Card   CardType
}

// AutoPass is the server passing for the holder: the hold timer ran out, or
// the forced-pass prompt timed out. It passes the odd-one-out card.
type AutoPass struct{ Player PlayerID }

// Suspect spends one of By's suspicion points on Target.
type Suspect struct{ By, Target PlayerID }

// Reveal answers a RevealRequested with the types the player shows.
type Reveal struct {
	Player PlayerID
	Types  []CardType
}

// AutoReveal is the server revealing for a player whose prompt timed out. It
// shows the types they hold the fewest of, to give away as little as possible.
type AutoReveal struct{ Player PlayerID }

// Proclaim is By calling "Jackpot" for their partner.
type Proclaim struct{ By PlayerID }

// RemoveTeam removes a team (drop-out, or the reconnect window ran out).
type RemoveTeam struct{ Team TeamID }

// VoteEnd records a player's vote on ending the game early.
type VoteEnd struct {
	Player PlayerID
	Agree  bool
}

func (StartRound) isCommand() {}
func (Pass) isCommand()       {}
func (AutoPass) isCommand()   {}
func (Suspect) isCommand()    {}
func (Reveal) isCommand()     {}
func (AutoReveal) isCommand() {}
func (Proclaim) isCommand()   {}
func (RemoveTeam) isCommand() {}
func (VoteEnd) isCommand()    {}
