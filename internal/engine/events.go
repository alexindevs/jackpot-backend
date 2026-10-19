package engine

// Event is something that happened. Events carry full information; the
// transport layer decides who may see which fields (for example, only the
// receiver of a Passed event may see the card type).
type Event interface{ isEvent() }

// EndReason says why a game ended.
type EndReason string

const (
	EndWins      EndReason = "wins"      // a team reached WinsToFinish
	EndAgreement EndReason = "agreement" // a majority of teams agreed to stop
	EndForfeit   EndReason = "forfeit"   // only one team was left
)

type SeatsReshuffled struct{ Seats []PlayerID }

type RoundStarted struct {
	Round  int
	Holder PlayerID
}

type Passed struct {
	From, To PlayerID
	Card     CardType
	Auto     bool // the server passed for the holder
}

type SuspicionRaised struct {
	By, Target PlayerID
	PointsLeft int
	Required   int // different types the target must show
}

// RevealRequested asks the target to pick Required of Options to show.
type RevealRequested struct {
	Target   PlayerID
	Required int
	Options  []CardType
}

// SuspicionFreed means the target showed Revealed and keeps their hand.
type SuspicionFreed struct {
	Target   PlayerID
	Revealed []CardType
}

// SuspicionFailed means the target could not show enough different types.
// Hand is what they were holding, which the room sees.
type SuspicionFailed struct {
	Target PlayerID
	Hand   Hand
}

// ForcedPassRequested asks a caught holder to pass a card before resetting.
type ForcedPassRequested struct{ Player PlayerID }

// PlayerReset means the player's cards went back to the pool and they were
// dealt a fresh hand. ShortOf lists types the pool could not supply (it ran
// dry); those slots were filled with other types.
type PlayerReset struct {
	Player  PlayerID
	ShortOf []CardType
}

// FalseProclaim is a proclamation whose partner did not hold four of a kind.
type FalseProclaim struct {
	By         PlayerID
	PointsLeft int
	Revoked    bool // By may no longer proclaim
}

type RoundWon struct {
	Team       TeamID
	Proclaimer PlayerID
	Holder     PlayerID // the partner who held the four of a kind
	Wins       int
	Letter     string
}

type TeamRemoved struct {
	Team    TeamID
	Members []PlayerID
}

type VoteChanged struct {
	Player PlayerID
	Agree  bool
}

type GameOver struct{ Result Result }

func (SeatsReshuffled) isEvent()     {}
func (RoundStarted) isEvent()        {}
func (Passed) isEvent()              {}
func (SuspicionRaised) isEvent()     {}
func (RevealRequested) isEvent()     {}
func (SuspicionFreed) isEvent()      {}
func (SuspicionFailed) isEvent()     {}
func (ForcedPassRequested) isEvent() {}
func (PlayerReset) isEvent()         {}
func (FalseProclaim) isEvent()       {}
func (RoundWon) isEvent()            {}
func (TeamRemoved) isEvent()         {}
func (VoteChanged) isEvent()         {}
func (GameOver) isEvent()            {}

// Result is how a game ended.
type Result struct {
	Winner TeamID // meaningless when Draw
	Draw   bool
	Reason EndReason
	// CountsForStats is false for forfeit wins, which don't count toward
	// leaderboards or duo stats.
	CountsForStats bool
	Wins           map[TeamID]int
}
