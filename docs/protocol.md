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

Any request except `ping` and `list_rooms` may also carry a `"key"`: a random string of at most 64 characters, fresh for each logical action. If the connection drops before the reply arrives, send the same request again with the **same key**, even on a new connection: the server answers with the original result instead of running it a second time (a retried `suspect` costs one point, not two). Keys belong to the player and are remembered for 10 minutes (the latest 64). Use `key`, not `id`, for this: `id` counters restart when the page reloads.

| `type` | Fields | Notes |
| --- | --- | --- |
| `ping` | | replies `pong` |
| `list_rooms` | | replies `rooms` |
| `signal` | `gesture` | send a gesture (an `id` from the snapshot's `gestures`); only during a round |
| `my_games` | | replies `my_games`: your paused games (below) |
| `resume_game` | `room` | go back into one of your paused games |
| `resume` | `after` | after reconnecting: replays what you missed (see below). `after` is the last `s` you saw, or 0 |
| `create_room` | | replies `room_created`; you are in the room |
| `join` | `room` | become a player (lobby only) |
| `spectate` | `room` | watch |
| `leave` | | leave the lobby, drop out of a game (instant), or stop watching |
| `start_now` | | creator only; needs 2+ teams and nobody without a partner |
| `invite_room` | `to` | tell someone about the room |
| `invite_partner` | `to` | `to` may be someone not in the room yet; accepting joins them |
| `accept_partner` / `decline_partner` | `from` | |
| `ready` | `ready` (bool) | skip the rest of the chat; everyone has to click |
| `team_chat` | `text` | the private team chat, only between rounds; 1 to 500 characters |
| `chat` | `text` | the public chat (see below); players only; 1 to 500 characters |
| `pass` | `card` | holder only |
| `suspect` | `target` | player id |
| `reveal` | `types` (list of cards) | answers a `reveal_requested` |
| `proclaim` | | |
| `vote_end` | `agree` (bool) | |

Replies: `{"t":"ok","id":...}`, or `{"t":"error","id":...,"d":{"code":"...","message":"..."}}`. Codes include `bad_message`, `not_in_room`, `already_in_room`, `no_such_room`, `room_full`, `not_joinable`, `not_player`, `not_creator`, `too_few_players`, `unpaired_players`, `already_paired`, `no_invite`, `already_invited`, `wrong_phase`, `not_holder`, `no_such_card`, `no_points`, `self_suspect`, `busy`, `nothing_to_answer`, `bad_reveal`, `proclaim_revoked`, `partner_holds_passing_card`, `bad_text`, `slow_down`, `game_over`.

A rejected request changes nothing and sends nothing else.

## Messages (server to client)

Shape: `{"t": "<name>", "d": {...}}`. Game events are `{"t":"game_event","kind":"<name>","d":{...}}`.

**Account and rooms**

- `paused`: `{until, players}`. The game is paused until `until` (then it is abandoned). Nothing can be played; chat still works.
- `resumed`: the game is running again, with fresh timers.
- `signal`: `{from, gesture, scope}`. `scope` is `team` (only you and your partner see it) or `room`; on your own signals it is empty. Numbered with `s`. To show it, find the gesture in the snapshot's `gestures` and write `<name> <seen>`.
- `heartbeat`: `{serverTime}`, sent every 20 seconds whether or not anything else is happening. If nothing at all arrives for about 50 seconds, treat the connection as dead and reconnect.
- `hello`: `{you:{id,name,style}, serverTime}`. Use `serverTime` to correct clock differences when showing countdowns.
- `profile`: `{id,name,style}`. The name and character for a player id, sent before the messages that mention them.
- `rooms`: a list of `{id,creator,phase,players,teams,spectators,maxPlayers,joinable}`.
- `room_created`: `{room}`.
- `snapshot`: the whole room as you may see it, sent when you enter and when you reconnect. It also carries `seq` and `gestures: [{id,label,seen,group}]`, the signal vocabulary (about 200, in groups). `{room,creator,phase,pausedUntil?,players:[{id,connected,partner,ready,removed,team}],spectators,timers:[{kind,player,endsAt}],game}`. `game` is `{round,phase,holder,seats,wins,points,yourHand,hands,pendingReveals,forcedPass}`; `yourHand` is only for players, and `hands` (everyone's) only for spectators.
- `room_closed`: `{reason}`.

**Lobby**: `member_joined {player}`, `member_left {player,reason}`, `room_invite {from,room}`, `partner_invite {from,to,room}`, `partner_declined {by}`, `team_formed {a,b}`, `team_broken {a,b}`, `countdown_started {endsAt}`, `countdown_cancelled`, `game_starting {teams:[[a,b]],demoted:[],seats:[...],suspicionPoints,winsToFinish}`. A team's id is its index in `teams`. `seats` is the ring order, clockwise: the holder passes to the next entry, wrapping around. `suspicionPoints` (3) is what every player has at the start of every round, and `winsToFinish` (7) ends the game. Demoted players are now spectators. If the ring is reshuffled between rounds you get a `seats_reshuffled` game event with the new order.

Invitations are only delivered to people who are connected at that moment. They are not queued.

**Between rounds**: `chat_started {round,endsAt}`, `ready_changed {player,ready}`, `team_chat_message {from,text}` (the private team chat; only you and your partner get it).

**Public chat**: `chat_message {id, from, text, at}`. Everyone in the room gets it: players, spectators and removed players. `id` counts up from 1 within the game, so a client can ask for "everything after the last one I saw". Only players still in the game may post (`chat`), from the moment the game starts until the room closes, including between rounds and after the game ends. Spectators and removed players can read but get `not_player` if they post, and there is no public chat in the lobby (`wrong_phase`). Text is trimmed and kept to one line (line breaks become spaces). Each player can post about once a second with a burst of 5; beyond that the server answers `slow_down` and drops the line. `snapshot.chat` carries the last 50 lines, oldest first, so a client that reconnects catches up without asking.

**Playing**

- `hand_update {player,hand}`: your whole hand, sent when it changes. `hand` is four counts, one per card type.
- `timer_started {kind,player,endsAt}`: `kind` is `hold`, `reveal`, `forced` or `reconnect`.
- `connection_changed {player,connected,reconnectBy}`.
- `game_event` kinds: `seats_reshuffled {seats}`, `round_started {round,holder}`, `passed {from,to,card,auto}`, `suspicion_raised {by,target,pointsLeft,required}`, `reveal_requested {target,required,options}` (only the target gets it), `suspicion_freed {target,revealed}`, `suspicion_failed {target,hand}`, `forced_pass_requested {player}`, `player_reset {player,minted}`, `false_proclaim {by,pointsLeft,revoked}`, `round_won {team,proclaimer,holder,wins,letter}`, `team_removed {team,members}`, `vote_changed {player,agree}`, `game_over {result:{winner,draw,reason,countsForStats,wins}}`.

The card in a `passed` event is only real for the player who passed it and the player who received it; everyone else gets `255`.

## Catching up after a reconnect

Messages that say something happened (game events, chat, joins and leaves, invites, connection changes) carry `s`: a number that counts up by one for each message sent to *you* in this room. Snapshots, hand updates and timers have no `s`: they describe the present and a snapshot replaces them.

After reconnecting you get a `snapshot` first. It has `seq`, your `s` at that moment. Everything in it is current. To also get the history you missed, send `{"type":"resume","after":L}` where `L` is the last `s` you saw before the drop (0 if none). Besides `ok`, you get:

`{"t":"missed","d":{"from":L+1,"to":N,"complete":true,"messages":[{"s":..,"t":..,"kind"?:..,"d":..}, ...]}}`

The messages are the originals, oldest first. Those with `s` at or below the snapshot's `seq` are history only: the snapshot already includes their effect, so show them in logs or the chat pane but don't apply them to state a second time. `complete:false` means the server keeps only the last 500 messages per player and yours fell off the end: rely on the snapshot and treat the history as having a gap. Chat has its own catch-up too (the snapshot's `chat`, and the REST `after` parameter).

## Paused games

A game that has started is saved. If the server restarts, or everyone is disconnected for a minute, the game is **paused**, not lost. After signing in you won't be put back in it. Ask for `my_games`:

`{"t":"my_games","d":[{"room","round","until","players":[{id,name,style,team,here}]}]}`

`here` is whether that player is back. Show the list, and `resume_game {room}` for the one the player picks; you then get a `snapshot` (with `pausedUntil` set) and are in the room. The game continues, with new timers, once **every** player still in it has resumed (everyone gets `resumed`). While paused, game actions answer `paused`. `resume_game` answers `no_such_game` if the game isn't yours, is over or was abandoned, and `already_in_room` if you have a current room. A paused game isn't your current room until you resume it, and it comes back to the list if you disconnect. Paused games don't appear in `list_rooms`.

After a server restart, **the numbers in `s` start again from zero**. So always set your "last `s` seen" to the snapshot's `seq` when a snapshot arrives, and only then apply `s` ordering. Never ignore a message because its `s` is lower than a value remembered from before the snapshot.

## When the socket is down: polling

The public chat is delivered live over the socket. A client whose socket is down can keep up by polling the REST API about once a minute, using the number of the last line it saw:

```
GET /api/rooms/{room}/chat?after=<last id seen>
Authorization: Bearer <start.ng game session>
```

It returns the lines numbered above `after` (see `docs/api.md`). Stop polling when the socket is back: the `snapshot` it sends on reconnect already includes the recent chat. Polling works for the players of the game; spectators get the chat over the socket only.

## What a client should do on a bad connection

1. **Watchdog.** The server sends a `heartbeat` every 20 seconds. If nothing at all arrives for about 50 seconds, close the socket and reconnect.
2. **Reconnect with backoff and jitter.** Wait about 1s, then 2s, 4s and so on up to ~30s, each time a random amount between half and the full delay, so a server restart doesn't get every client at once. Reset the delay after a connection has stayed up for ~30s.
3. **Refresh the start.ng session before reconnecting.** Sessions last about 2 hours, so a long game can outlive the token. Fetch a fresh one, then send `auth` with it. Close code 4401 means the token was refused.
4. **Retry with the same `key`.** Give each logical action a random `key` and reuse it if you send the action again after a drop.
5. **Catch up.** Note the last `s` you saw. After the `snapshot`, send `resume` with it (see above). Close code 4409 means another tab took over: don't reconnect on your own.

## Values and types

- **Ids are strings.** A player's id is start.ng's numeric id written as text (start.ng's `217` is `"217"` on this socket and in the API). Compare them as strings.
- `snapshot.phase` (the room) is one of `lobby`, `chat`, `playing`, `over`, `closed`. `chat` is the private team chat before a round; it is shown while `game.phase` is `waiting`.
- `snapshot.game.phase` (the engine) is one of `waiting` (between rounds), `playing`, `over`.
- `snapshot.game` is `null` until the game starts. Inside it, `yourHand` is `null` for spectators and removed players, and `hands` is `null` for everyone except spectators.
- Team ids are numbers: the index into `game_starting.teams`. `snapshot.players[].team` is `0` in the lobby and means nothing until the game has started.
- `timer_started.kind` is `hold`, `reveal`, `forced` or `reconnect`. Times (`endsAt`, `reconnectBy`, `serverTime`) are RFC 3339 UTC strings.
- Lists are `[]`, never `null`.

## Field notes

- `round_won {team, proclaimer, holder, wins, letter}`: `team` is the winning team's id. `wins` is **that team's new total** number of round wins (a number), and `letter` is the letter it earned (`JACKPOT[wins-1]`). The scoreboard in a snapshot, `game.wins`, is a map from team id to count (JSON keys are strings: `{"0":2,"1":1}`).
- `game_over {result}`: `result.winner` is a team id and means nothing when `result.draw` is true. `result.reason` is `wins`, `agreement` or `forfeit`. `result.wins` is a map from team id to count. `result.countsForStats` is false for a forfeit.
- `suspicion_raised {by, target, pointsLeft, required}`: `pointsLeft` is the suspector's remaining points, and `required` is how many different types the target must show.
- `passed {from, to, card, auto}`: `auto` is true when the server passed for a holder who ran out of time (or didn't answer a forced-pass prompt).
- `false_proclaim {by, pointsLeft, revoked}`: `revoked` is true when `by` was at 0 points, which removes their right to proclaim until the next round.
- `player_reset {player, minted}`: after a reset the player's new hand is one of each type. `minted` is normally empty.
- `hand_update` arrives for your own hand whenever it changes, including at the start of each round, after a pass you made or received, and after a reset. It is the source of truth for your hand.

## Limits

Messages are capped at 8 KB. A client may send about 20 messages a second (bursts of 40); beyond that the connection is closed. A client that stops reading is disconnected rather than waited for.
