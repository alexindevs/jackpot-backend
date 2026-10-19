# Architecture

Decisions are the author's. Anything marked **unverified** is an inference from start.ng's docs and has to be tested before relying on it.

## Layers

1. **Engine** (`internal/engine`, built): a pure state machine. Commands in, events out. No I/O, no timers, deterministic given a seeded RNG. Covered by unit tests and a randomized test that plays hundreds of games and checks card conservation after every command.
2. **Room runner** (next): one goroutine per room owns the engine state and reads one ordered command queue. It owns every timer: hold timer (60 s), prompt timeouts, the 90 s reconnect window, the 2 minute pre-round chat, and the 100 to 200 ms collection window with a random tie-break.
3. **Transport**: WebSockets. Hidden information is filtered per role (player, spectator) before anything leaves the server. For example, only the receiver of a pass sees the card type.
4. **Persistence**: Postgres.

## Language and storage

- **Go.**
- **SQL (Postgres)**, not NoSQL. The data is relational: players, teams, rounds, and who proclaimed and who held the cards for each win. MVP and "legendary duos" leaderboards are aggregates over that. Chat is kept for 24 hours via an `expires_at` column and a periodic sweep.
- Run a **single instance** first, with rooms in memory. Several instances would need sticky routing or a shared pub/sub layer. Redis would only come in at that point.

## What start.ng provides

From https://start.ng/docs.md:

- A game's frontend is **static files** at `<name>.start.ng`. start.ng never runs server code, so **this server must be hosted separately**, publicly, with TLS. The page is https, so the socket has to be `wss://`.
- **Identity and character:** player id, name and a style object, via a hidden connect page or a per-game session token that lasts 2 hours. Every game must show the player's character or a profile card.
- **Leaderboards:** one best run per player, up to 5 numeric stats, saved by the player's own session. This doesn't fit "wins by proclaiming" or "wins as a duo", so our stats live in our database.
- **Multiplayer rooms:** a generic position-relay service (messages like `move` and `wave`, up to 50 players). It can't run our game logic or hold hidden hands, so we use our own socket server.

## Auth plan (unverified)

1. The frontend gets the player's game session as described in the docs.
2. It sends the token to our server when it opens the socket.
3. The server calls `GET https://api.start.ng/games/{game}/player` with `Authorization: Bearer <session>`. The reply is `{ id, name, style }`.
4. The server **never trusts a name or style sent by the client**. It uses what start.ng returned.
5. Sessions last 2 hours, so reconnecting after that needs a fresh session.

## Unverified assumptions

Test these as soon as a stub game is live at a `*.start.ng` address. Per the docs, sign-in doesn't work from a local file.

1. The player endpoint can be called **from a server**. The docs only describe browser use.
2. A page on `*.start.ng` may open a **WebSocket to our own domain**. The docs don't mention any restriction, but I don't know.
3. The game's **name** is first come, first served ("the first person to put a name live owns it"), so claim it early.
4. The player's `face` image only comes from the connect page. The player endpoint returns `style` but not `face`, so other players' characters have to be drawn from `style`.

## Frontend contract notes

- Allow the WebSocket origin `https://<name>.start.ng`.
- Consider reusing start.ng's close codes: `4401` sign-in needed, `4403` room full, `4409` joined from another tab.
- Guests (no id, named "Guest") can't be told apart, so the server needs its own handle for them if guests may play. **Open.**

## Open decisions

- Whether guests can play, and how they reconnect.
- Where this server is hosted.
- The game's name on start.ng.
- Timeouts for the reveal and forced-pass prompts.
