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
	// StartNow lets the room's creator skip the countdown, once there are at
	// least two teams and nobody is left without a partner.
	StartNow       struct{}
	InviteToRoom   struct{ To engine.PlayerID }
	InvitePartner  struct{ To engine.PlayerID }
	AcceptPartner  struct{ From engine.PlayerID }
	DeclinePartner struct{ From engine.PlayerID }
)

// Chat. TeamChat is the private chat between rounds; Chat is the public chat
// everyone in the room can read.
type (
	Ready    struct{ Ready bool }
	TeamChat struct{ Text string }
	Chat     struct{ Text string }
)

// Resume asks for everything sent to the sender after sequence number After.
type Resume struct{ After int64 }

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
func (StartNow) isInput()       {}
func (InviteToRoom) isInput()   {}
func (InvitePartner) isInput()  {}
func (AcceptPartner) isInput()  {}
func (DeclinePartner) isInput() {}
func (Ready) isInput()          {}
func (TeamChat) isInput()       {}
func (Chat) isInput()           {}
func (Resume) isInput()         {}
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
	Seq int64 // this recipient's sequence number for the message; 0 if not numbered
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

	// GameStarting says who is on which team (a team's id is its index in
	// Teams) and where everyone sits, clockwise. Demoted players were left
	// without a partner and are now spectators. SuspicionPoints is what each
	// player gets at the start of every round, and WinsToFinish is how many
	// round wins end the game, so a client can set up its scoreboard without
	// waiting for a snapshot.
	GameStarting struct {
		Teams           [][2]engine.PlayerID
		Demoted         []engine.PlayerID
		Seats           []engine.PlayerID
		SuspicionPoints int
		WinsToFinish    int
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

	// ChatMessage is one line of the public chat. ID counts up from 1 within
	// a game, so a client can ask for "everything after the last one I saw".
	ChatMessage struct {
		ID   int
		From engine.PlayerID
		Text string
		At   time.Time
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

	// Missed is the reply to Resume: the numbered messages the recipient has
	// not seen, oldest first. Complete is false when the room no longer keeps
	// them all, so the history has a gap (the snapshot still has the state).
	Missed struct {
		From, To int64
		Complete bool
		Messages []Out
	}
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
func (ChatMessage) isMessage()        {}
func (GameEvent) isMessage()          {}
func (HandUpdate) isMessage()         {}
func (TimerStarted) isMessage()       {}
func (ConnectionChanged) isMessage()  {}
func (RoomClosed) isMessage()         {}
func (Snapshot) isMessage()           {}
func (Missed) isMessage()             {}
