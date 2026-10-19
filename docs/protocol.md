# WebSocket protocol

The server speaks JSON over one WebSocket at `/ws`. It listens on plain `ws://`; put a reverse proxy in front for TLS, because a page served over https needs `wss://`.

Field names are camelCase. Lists are always arrays, never `null`. Card types are numbers: `0` circle, `1` triangle, `2` cross, `3` square. A card the recipient may not see is `255`.

## Connecting

The first message must be the sign-in, sent within 5 seconds:

```json
{ "type": "auth", "token": "<start.ng game session>" }
```

The token is never put in the URL. The server checks it with start.ng and takes the player's id, name and character from start.ng's reply, never from the client. Guests can't connect.

Close codes: `4401` sign-in needed or refused, `4409` the same player connected from another tab (this socket was replaced), `1013` sign-in is unavailable, try again, `1008` too many messages or too slow to keep up.

A player may be in only one room at a time. Reconnecting with the same account puts you straight back in your room with a `snapshot`.

## Requests (client to server)

Every request may carry an `"id"` of any JSON type. It is echoed in the reply. The sender is always the signed-in player; there is no field for it.

| `type` | Fields | Notes |
| --- | --- | --- |
| `ping` | | replies `pong` |
| `list_rooms` | | replies `rooms` |
| `create_room` | | replies `room_created`; you are in the room |
| `join` | `room` | become a player (lobby only) |
| `spectate` | `room` | watch |
| `leave` | | leave the lobby, drop out of a game (instant), or stop watching |
| `start_now` | | creator only; needs 2+ teams and nobody without a partner |
| `invite_room` | `to` | tell someone about the room |
| `invite_partner` | `to` | `to` may be someone not in the room yet; accepting joins them |
| `accept_partner` / `decline_partner` | `from` | |
| `ready` | `ready` (bool) | skip the rest of the chat; everyone has to click |
| `team_chat` | `text` | only between rounds; 1 to 500 characters |
| `pass` | `card` | holder only |
| `suspect` | `target` | player id |
| `reveal` | `types` (list of cards) | answers a `reveal_requested` |
| `proclaim` | | |
| `vote_end` | `agree` (bool) | |

Replies: `{"t":"ok","id":...}`, or `{"t":"error","id":...,"d":{"code":"...","message":"..."}}`. Codes include `bad_message`, `not_in_room`, `already_in_room`, `no_such_room`, `room_full`, `not_joinable`, `not_player`, `not_creator`, `too_few_players`, `unpaired_players`, `already_paired`, `no_invite`, `already_invited`, `wrong_phase`, `not_holder`, `no_such_card`, `no_points`, `self_suspect`, `busy`, `nothing_to_answer`, `bad_reveal`, `proclaim_revoked`, `partner_holds_passing_card`, `bad_text`, `game_over`.

A rejected request changes nothing and sends nothing else.

## Messages (server to client)

Shape: `{"t": "<name>", "d": {...}}`. Game events are `{"t":"game_event","kind":"<name>","d":{...}}`.

**Account and rooms**

- `hello`: `{you:{id,name,style}, serverTime}`. Use `serverTime` to correct clock differences when showing countdowns.
- `profile`: `{id,name,style}`. The name and character for a player id, sent before the messages that mention them.
- `rooms`: a list of `{id,creator,phase,players,teams,spectators,maxPlayers,joinable}`.
- `room_created`: `{room}`.
- `snapshot`: the whole room as you may see it, sent when you enter and when you reconnect. `{room,creator,phase,players:[{id,connected,partner,ready,removed,team}],spectators,timers:[{kind,player,endsAt}],game}`. `game` is `{round,phase,holder,seats,wins,points,yourHand,hands,pendingReveals,forcedPass}`; `yourHand` is only for players, and `hands` (everyone's) only for spectators.
- `room_closed`: `{reason}`.

**Lobby**: `member_joined {player}`, `member_left {player,reason}`, `room_invite {from,room}`, `partner_invite {from,to,room}`, `partner_declined {by}`, `team_formed {a,b}`, `team_broken {a,b}`, `countdown_started {endsAt}`, `countdown_cancelled`, `game_starting {teams:[[a,b]],demoted:[],seats:[...],suspicionPoints,winsToFinish}`. A team's id is its index in `teams`. `seats` is the ring order, clockwise: the holder passes to the next entry, wrapping around. `suspicionPoints` (3) is what every player has at the start of every round, and `winsToFinish` (7) ends the game. Demoted players are now spectators. If the ring is reshuffled between rounds you get a `seats_reshuffled` game event with the new order.

Invitations are only delivered to people who are connected at that moment. They are not queued.

**Between rounds**: `chat_started {round,endsAt}`, `ready_changed {player,ready}`, `team_chat_message {from,text}`.

**Playing**

- `hand_update {player,hand}`: your whole hand, sent when it changes. `hand` is four counts, one per card type.
- `timer_started {kind,player,endsAt}`: `kind` is `hold`, `reveal`, `forced` or `reconnect`.
- `connection_changed {player,connected,reconnectBy}`.
- `game_event` kinds: `seats_reshuffled {seats}`, `round_started {round,holder}`, `passed {from,to,card,auto}`, `suspicion_raised {by,target,pointsLeft,required}`, `reveal_requested {target,required,options}` (only the target gets it), `suspicion_freed {target,revealed}`, `suspicion_failed {target,hand}`, `forced_pass_requested {player}`, `player_reset {player,minted}`, `false_proclaim {by,pointsLeft,revoked}`, `round_won {team,proclaimer,holder,wins,letter}`, `team_removed {team,members}`, `vote_changed {player,agree}`, `game_over {result:{winner,draw,reason,countsForStats,wins}}`.

The card in a `passed` event is only real for the player who passed it and the player who received it; everyone else gets `255`.

## Values and types

- **Ids are strings.** A player's id is start.ng's numeric id written as text (start.ng's `217` is `"217"` on this socket and in the API). Compare them as strings.
- `snapshot.phase` (the room) is one of `lobby`, `chat`, `playing`, `over`, `closed`. `chat` is the private team chat before a round; it is shown while `game.phase` is `waiting`.
- `snapshot.game.phase` (the engine) is one of `waiting` (between rounds), `playing`, `over`.
- `snapshot.game` is `null` until the game starts. Inside it, `yourHand` is `null` for spectators and removed players, and `hands` is `null` for everyone except spectators.
- Team ids are numbers: the index into `game_starting.teams`. `snapshot.players[].team` is `0` in the lobby and means nothing until the game has started.
- `timer_started.kind` is `hold`, `reveal`, `forced` or `reconnect`. Times (`endsAt`, `reconnectBy`, `serverTime`) are RFC 3339 UTC strings.
- Lists are `[]`, never `null`.

## Limits

Messages are capped at 8 KB. A client may send about 20 messages a second (bursts of 40); beyond that the connection is closed. A client that stops reading is disconnected rather than waited for.
