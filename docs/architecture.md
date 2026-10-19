# Architecture

Decisions are the author's. Anything marked **unverified** is an inference from start.ng's docs and has to be tested before relying on it.

## Layers

1. **Engine** (`internal/engine`, built): a pure state machine. Commands in, events out. No I/O, no timers, deterministic given a seeded RNG. Covered by unit tests and a randomized test that plays hundreds of games and checks card conservation after every command.
2. **Room** (`internal/room`, built): everything that happens over time around the engine. It has no goroutines or clock of its own. You give it inputs with a timestamp (`Handle`), tell it time has passed (`Tick`), and ask when it next needs attention (`NextDeadline`); each call returns the messages to send. That makes every timer testable without sleeping. It covers the lobby (joining, partner invites, the 2 minute countdown, random pairing), the pre-round chat with the Ready button, the hold timer (60 s), the reveal and forced-pass prompts (30 s), the 90 s reconnect window, instant drop-outs, the 1 minute empty-room close, per-recipient hiding of private information, and snapshots for reconnecting players. A thin `Runner` drives one room from a single goroutine with real time; inputs are handled strictly in arrival order, so there is no collection window.
3. **Transport** (`internal/transport`, built): WebSockets, using `github.com/coder/websocket`. A `Manager` owns every room and connection: it authenticates the first message, keeps one connection per player (a second tab replaces the first, close code 4409), keeps a player in only one room, lists rooms, sends each player their own messages through a bounded queue (a client that can't keep up is dropped, never waited for), and rate-limits clients. The room already decides who may see what, so the transport only translates between JSON and room inputs and messages. The wire format is in `docs/protocol.md`. `cmd/server` runs it; it is configured by environment variables, listens on plain `ws://`, and expects a reverse proxy for TLS.
4. **Persistence and REST** (`internal/store`, `internal/api`, built): Postgres through `pgx` with plain SQL (no ORM), migrations embedded in the binary and applied on start. A room reports games starting, rounds being won and games ending through a callback; a `Writer` queues those and saves them on its own goroutine, so a slow or dead database can never stall a game (a full queue drops records and counts them). The REST API is documented in `docs/api.md`. `compose.yml` runs Postgres (and optionally the server); the README has the commands.

## Not built yet

- Signals (the gesture vocabulary and the 20% partner-only roll) and the public chat with its 24 hour storage.

## Language and storage

- **Go.**
- **SQL (Postgres)**, not NoSQL. The data is relational: players, teams, rounds, and who proclaimed and who held the cards for each win. MVP and "legendary duos" leaderboards are aggregates over that. Chat and saved game data are cleared 24 hours after a game ends by a periodic sweep (see below).
- Run a **single instance** first. Rooms run in memory, but a started game is also **saved as events**: the engine is deterministic, so its setup (rules, teams, the two random seeds) plus every command applied since the start rebuild it exactly (`engine.Replay`). The room reports each command (`CommandApplied`) and the async writer stores it in `game_events`. On start, `internal/live` loads the unfinished games, replays them, and brings them back as **paused** rooms (`room.Restore`), which resume when their players return. A game with a hole in its log comes back at the last complete step; one that can't be rebuilt is abandoned. Lobbies are not event-sourced (they have no engine): the room reports its state each time it changes (`LobbyState`) and the writer keeps the latest in `lobbies`; it is removed when the room stops being a lobby. Private team chat is stored line by line in `team_chat_messages`. Several instances would need sticky routing or a shared pub/sub layer; Redis would only come in at that point.
- **Retention:** 24 hours after a game ends, a sweeper deletes its events, setup and chat. The summary rows stay for good. Chat is therefore kept until 24 hours after the game ends, not per message.

## What start.ng provides

From https://start.ng/docs.md:

- A game's frontend is **static files** at `<name>.start.ng`. start.ng never runs server code, so **this server must be hosted separately**, publicly, with TLS. The page is https, so the socket has to be `wss://`.
- **Identity and character:** player id, name and a style object, via a hidden connect page or a per-game session token that lasts 2 hours. Every game must show the player's character or a profile card.
- **Leaderboards:** one best run per player, up to 5 numeric stats, saved by the player's own session. This doesn't fit "wins by proclaiming" or "wins as a duo", so our stats live in our database.
- **Multiplayer rooms:** a generic position-relay service (messages like `move` and `wave`, up to 50 players). It can't run our game logic or hold hidden hands, so we use our own socket server.

## Auth plan (unverified)

1. The frontend gets the player's game session as described in the docs.
2. It sends the token to our server as the first WebSocket message (never in the URL, which would end up in logs).
3. The server calls `GET https://api.start.ng/games/{game}/player` with `Authorization: Bearer <session>`. The reply is `{ id, name, style }`.
4. The server **never trusts a name or style sent by the client**. It uses what start.ng returned.
5. Sessions last 2 hours, so reconnecting after that needs a fresh session.

## Unverified assumptions

Test these as soon as a stub game is live at a `*.start.ng` address. Per the docs, sign-in doesn't work from a local file.

1. The player endpoint can be called **from a server**. The docs only describe browser use.
2. A page on `*.start.ng` may open a **WebSocket to our own domain**. The docs don't mention any restriction, but I don't know. What could stop it:
   - A `Content-Security-Policy` `connect-src` header on start.ng's pages. This is the most likely blocker, and the docs don't say whether start.ng sets one.
   - Mixed content: the page is https, so `ws://` is blocked. We need `wss://` on a real domain with a valid certificate.
   - Our own side: the VPS firewall, or a reverse proxy not forwarding the WebSocket upgrade headers, or an Origin check that rejects `https://jackpot.start.ng`.
   - Browsers don't do CORS preflights for WebSockets, so the server's Origin check is the only gate on our side.
3. The game's name is **`jackpot`**, and it isn't published yet. Names are first come, first served ("the first person to put a name live owns it"), so publish a stub early to claim it.
4. The player's `face` image only comes from the connect page. The player endpoint returns `style` but not `face`, so other players' characters have to be drawn from `style`.

## Frontend contract notes

- Allow the WebSocket origin `https://<name>.start.ng`.
- Consider reusing start.ng's close codes: `4401` sign-in needed, `4403` room full, `4409` joined from another tab.
- **Guests can't play.** The socket requires a valid signed-in session. A guest is turned away with close code `4401`.

## Decided

- Hosting: a VPS (which provider and how it's deployed is not decided).
- Game name: `jackpot`.
- No guests.

## Open decisions

- Server domain and TLS: a `nip.io` name can serve a stub for testing. For production, a domain the author controls is safer (see the conversation notes: shared rate limits and a third-party DNS dependency).
- Deck size for resets (see Known risks in `docs/rules.md`).
- Timeouts for the reveal and forced-pass prompts.
