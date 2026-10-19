package room

import (
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

// Input is something a user (or the transport on their behalf) asks the room
// to do. The sender is passed separately to Handle.
type Input interface{ isInput() }

// Presence.
type (
	Join       struct{} // become a player (lobby only)
	Spectate   struct{} // watch the room
	Leave      struct{} // leave: lobby leave, instant drop-out in a game, or stop watching
	Connect    struct{} // a member's socket is back
	Disconnect struct{} // a member's socket dropped
)

// Lobby.
type (
	InviteToRoom   struct{ To engine.PlayerID }
	InvitePartner  struct{ To engine.PlayerID }
	AcceptPartner  struct{ From engine.PlayerID }
	DeclinePartner struct{ From engine.PlayerID }
)

// Between rounds.
type (
	Ready    struct{ Ready bool }
	TeamChat struct{ Text string }
)

// During a round. These map one to one onto engine commands.
type (
	Pass     struct{ Card engine.CardType }
	Suspect  struct{ Target engine.PlayerID }
	Reveal   struct{ Types []engine.CardType }
	Proclaim struct{}
	VoteEnd  struct{ Agree bool }
)

func (Join) isInput()           {}
func (Spectate) isInput()       {}
func (Leave) isInput()          {}
func (Connect) isInput()        {}
func (Disconnect) isInput()     {}
func (InviteToRoom) isInput()   {}
func (InvitePartner) isInput()  {}
func (AcceptPartner) isInput()  {}
func (DeclinePartner) isInput() {}
func (Ready) isInput()          {}
func (TeamChat) isInput()       {}
func (Pass) isInput()           {}
func (Suspect) isInput()        {}
func (Reveal) isInput()         {}
func (Proclaim) isInput()       {}
func (VoteEnd) isInput()        {}

// Out is one message for one recipient. The room resolves audiences and hides
// private information, so the transport only has to deliver.
type Out struct {
	To  engine.PlayerID
	Msg Message
}

// Message is anything the room sends.
type Message interface{ isMessage() }

// CardHidden replaces a card type the recipient may not see.
const CardHidden engine.CardType = 255

type (
	MemberJoined struct{ Player engine.PlayerID }
	MemberLeft   struct {
		Player engine.PlayerID
		Reason string // "left" or "disconnected"
	}
	RoomInvite struct {
		From engine.PlayerID
		Room string
	}
	PartnerInvite struct {
		From, To engine.PlayerID
		Room     string
	}
	PartnerDeclined    struct{ By engine.PlayerID }
	TeamFormed         struct{ A, B engine.PlayerID }
	TeamBroken         struct{ A, B engine.PlayerID }
	CountdownStarted   struct{ EndsAt time.Time }
	CountdownCancelled struct{}

	// GameStarting says who is on which team. Demoted players were left
	// without a partner and are now spectators.
	GameStarting struct {
		Teams   [][2]engine.PlayerID
		Demoted []engine.PlayerID
	}

	ChatStarted struct {
		Round  int
		EndsAt time.Time
	}
	ReadyChanged struct {
		Player engine.PlayerID
		Ready  bool
	}
	TeamChatMessage struct {
		From engine.PlayerID
		Text string
	}

	// GameEvent wraps an engine event, already filtered for the recipient.
	GameEvent struct{ Event engine.Event }

	// HandUpdate is a player's whole hand, sent whenever it changes.
	HandUpdate struct {
		Player engine.PlayerID
		Hand   engine.Hand
	}

	// TimerStarted tells clients about a countdown they should show.
	// Kind is "hold", "reveal", "forced" or "reconnect".
	TimerStarted struct {
		Kind   string
		Player engine.PlayerID
		EndsAt time.Time
	}

	ConnectionChanged struct {
		Player      engine.PlayerID
		Connected   bool
		ReconnectBy *time.Time
	}

	RoomClosed struct{ Reason string }
)

func (MemberJoined) isMessage()       {}
func (MemberLeft) isMessage()         {}
func (RoomInvite) isMessage()         {}
func (PartnerInvite) isMessage()      {}
func (PartnerDeclined) isMessage()    {}
func (TeamFormed) isMessage()         {}
func (TeamBroken) isMessage()         {}
func (CountdownStarted) isMessage()   {}
func (CountdownCancelled) isMessage() {}
func (GameStarting) isMessage()       {}
func (ChatStarted) isMessage()        {}
func (ReadyChanged) isMessage()       {}
func (TeamChatMessage) isMessage()    {}
func (GameEvent) isMessage()          {}
func (HandUpdate) isMessage()         {}
func (TimerStarted) isMessage()       {}
func (ConnectionChanged) isMessage()  {}
func (RoomClosed) isMessage()         {}
func (Snapshot) isMessage()           {}
