// Package transport carries the game over WebSockets: it authenticates
// players, keeps track of rooms, and translates between JSON on the wire and
// the room package's inputs and messages.
//
// Wire format. Every message is a JSON object.
//
// Client to server: {"type": "...", "id": <optional, echoed back>, ...fields}.
// The first message must be {"type":"auth","token":"<start.ng game session>"}.
//
// Server to client: {"t": "...", "kind": "...", "id": <echoed>, "d": {...}}.
// Replies to a request are {"t":"ok"} or {"t":"error","d":{"code","message"}}.
// Everything the room sends is passed through with "t" set to the snake_case
// message name (for example "member_joined"); engine events arrive as
// {"t":"game_event","kind":"passed","d":{...}}.
package transport

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/alexindevs/jackpot-backend/internal/engine"
	"github.com/alexindevs/jackpot-backend/internal/room"
)

// inbound is any message from a client.
type inbound struct {
	Type   string          `json:"type"`
	ID     json.RawMessage `json:"id,omitempty"`
	Token  string          `json:"token,omitempty"`
	Room   string          `json:"room,omitempty"`
	To     engine.PlayerID `json:"to,omitempty"`
	From   engine.PlayerID `json:"from,omitempty"`
	Target engine.PlayerID `json:"target,omitempty"`
	Card   *int            `json:"card,omitempty"`
	Types  []int           `json:"types,omitempty"`
	Ready  *bool           `json:"ready,omitempty"`
	Agree  *bool           `json:"agree,omitempty"`
	Text   string          `json:"text,omitempty"`
}

var errBadMessage = errors.New("malformed message")

func cardType(v int) (engine.CardType, error) {
	if v < 0 || v >= engine.NumTypes {
		return 0, fmt.Errorf("%w: card must be 0 to %d", errBadMessage, engine.NumTypes-1)
	}
	return engine.CardType(v), nil
}

// toInput converts a client message into a room input. The sender is never
// taken from the message: the connection knows who is speaking.
func toInput(in inbound) (room.Input, error) {
	switch in.Type {
	case "start_now":
		return room.StartNow{}, nil
	case "invite_room":
		return room.InviteToRoom{To: in.To}, nil
	case "invite_partner":
		return room.InvitePartner{To: in.To}, nil
	case "accept_partner":
		return room.AcceptPartner{From: in.From}, nil
	case "decline_partner":
		return room.DeclinePartner{From: in.From}, nil
	case "ready":
		if in.Ready == nil {
			return nil, fmt.Errorf("%w: ready is required", errBadMessage)
		}
		return room.Ready{Ready: *in.Ready}, nil
	case "team_chat":
		return room.TeamChat{Text: in.Text}, nil
	case "pass":
		if in.Card == nil {
			return nil, fmt.Errorf("%w: card is required", errBadMessage)
		}
		c, err := cardType(*in.Card)
		if err != nil {
			return nil, err
		}
		return room.Pass{Card: c}, nil
	case "suspect":
		return room.Suspect{Target: in.Target}, nil
	case "reveal":
		if len(in.Types) == 0 || len(in.Types) > engine.NumTypes {
			return nil, fmt.Errorf("%w: types must list 1 to %d cards", errBadMessage, engine.NumTypes)
		}
		var types []engine.CardType
		for _, v := range in.Types {
			c, err := cardType(v)
			if err != nil {
				return nil, err
			}
			types = append(types, c)
		}
		return room.Reveal{Types: types}, nil
	case "proclaim":
		return room.Proclaim{}, nil
	case "vote_end":
		if in.Agree == nil {
			return nil, fmt.Errorf("%w: agree is required", errBadMessage)
		}
		return room.VoteEnd{Agree: *in.Agree}, nil
	}
	return nil, fmt.Errorf("%w: unknown type %q", errBadMessage, in.Type)
}

// outbound is any message to a client.
type outbound struct {
	T    string          `json:"t"`
	Kind string          `json:"kind,omitempty"`
	ID   json.RawMessage `json:"id,omitempty"`
	D    any             `json:"d,omitempty"`
}

func encode(o outbound) []byte {
	o.D = wire(o.D)
	b, err := json.Marshal(o)
	if err != nil {
		// Only a bug in a message type can get here. Say so, don't crash.
		b, _ = json.Marshal(outbound{T: "error", D: errorBody{Code: "internal", Message: "could not encode message"}})
	}
	return b
}

// encodeRoom turns a room message into its wire form.
func encodeRoom(m room.Message) []byte {
	if ge, ok := m.(room.GameEvent); ok {
		return encode(outbound{T: "game_event", Kind: snake(typeName(ge.Event)), D: ge.Event})
	}
	return encode(outbound{T: snake(typeName(m)), D: m})
}

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// wire converts a message into plain JSON-ready data with camelCase field
// names ("PointsLeft" becomes "pointsLeft", "ID" becomes "id"). Map keys, such
// as player ids, are left exactly as they are. Types that know how to marshal
// themselves (times, phases, raw JSON) are passed through.
func wire(v any) any { return wireValue(reflect.ValueOf(v)) }

func wireValue(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if t := v.Type(); t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return nil
		}
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return wireValue(v.Elem())
	case reflect.Struct:
		out := map[string]any{}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if f := t.Field(i); f.IsExported() {
				out[lowerFirst(f.Name)] = wireValue(v.Field(i))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = wireValue(v.Index(i))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			out[fmt.Sprint(it.Key().Interface())] = wireValue(it.Value())
		}
		return out
	}
	return v.Interface()
}

// lowerFirst lowercases the leading capitals of a Go name: "PointsLeft" to
// "pointsLeft", "ID" to "id", "HTTPServer" to "httpServer".
func lowerFirst(s string) string {
	r := []rune(s)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	if n > 1 && n < len(r) {
		n-- // keep the capital that starts the next word
	}
	for i := 0; i < n; i++ {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

func typeName(v any) string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// snake turns "MemberJoined" into "member_joined".
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ok(id json.RawMessage) []byte { return encode(outbound{T: "ok", ID: id}) }

func fail(id json.RawMessage, err error) []byte {
	return encode(outbound{T: "error", ID: id, D: errorBody{Code: errCode(err), Message: err.Error()}})
}

var errCodes = []struct {
	err  error
	code string
}{
	{room.ErrClosed, "room_closed"}, {room.ErrNotJoinable, "not_joinable"}, {room.ErrRoomFull, "room_full"},
	{room.ErrAlreadyMember, "already_member"}, {room.ErrNotPlayer, "not_player"},
	{room.ErrAlreadyPaired, "already_paired"}, {room.ErrNoInvite, "no_invite"},
	{room.ErrDuplicateInvite, "already_invited"}, {room.ErrBadTarget, "bad_target"},
	{room.ErrWrongPhase, "wrong_phase"}, {room.ErrBadText, "bad_text"},
	{room.ErrNotCreator, "not_creator"}, {room.ErrTooFewPlayers, "too_few_players"},
	{room.ErrUnpairedPlayers, "unpaired_players"},
	{engine.ErrNotHolder, "not_holder"}, {engine.ErrNoSuchCard, "no_such_card"},
	{engine.ErrSelfSuspect, "self_suspect"}, {engine.ErrNoPoints, "no_points"},
	{engine.ErrBusy, "busy"}, {engine.ErrNoPending, "nothing_to_answer"},
	{engine.ErrBadReveal, "bad_reveal"}, {engine.ErrProclaimRevoked, "proclaim_revoked"},
	{engine.ErrPartnerHoldsPassingCard, "partner_holds_passing_card"},
	{engine.ErrWrongPhase, "wrong_phase"}, {engine.ErrUnknownPlayer, "unknown_player"},
	{engine.ErrGameOver, "game_over"}, {errBadMessage, "bad_message"},
	{errNotInRoom, "not_in_room"}, {errAlreadyInRoom, "already_in_room"},
	{errNoSuchRoom, "no_such_room"}, {errRateLimited, "rate_limited"},
}

func errCode(err error) string {
	for _, c := range errCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return "error"
}
