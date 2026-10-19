# REST API

Read-only. Gameplay is on the WebSocket (`docs/protocol.md`). Base path `/api`. Responses are JSON with camelCase fields, and lists are always arrays (never `null`).

CORS is allowed for the hosts in `JACKPOT_ORIGINS` (by default `jackpot.start.ng`). Only `GET` and `OPTIONS` exist; anything else is `405`.

Errors look like `{"error":{"code":"...","message":"..."}}`. Codes: `bad_request` (400), `unauthorized` (401), `not_a_player` (403), `not_found` (404), `method_not_allowed` (405), `internal` (500), `database_unavailable` and `auth_unavailable` (503).

The rooms list works without a database. Everything else answers `503 database_unavailable` if the server has no `DATABASE_URL`.

## `GET /api/rooms`

The rooms running right now, so a home page can show them before anyone signs in. Not cached.

```json
[{ "id": "k3m9xq", "creator": "7", "phase": "lobby", "players": 3, "teams": 1, "spectators": 0, "maxPlayers": 10, "joinable": true }]
```

`phase` is `lobby`, `chat`, `playing`, `over` or `closed`. `teams` counts partners already paired.

## `GET /api/leaderboards/mvp?role=proclaimer|holder&limit=20`

Players ranked by rounds won in a role: `proclaimer` shouted "Jackpot" for their partner, `holder` held the four of a kind. `role` is required. `limit` is 1 to 100 (default 20). Equal wins share a rank. Cached 10 seconds.

```json
[{ "rank": 1, "wins": 12, "player": { "id": "7", "name": "Trini", "style": { "hair": "braids" } } }]
```

`name` falls back to the id if the player's profile has never been saved. `style` is the start.ng character.

## `GET /api/leaderboards/duos?limit=20`

Pairs of partners ranked by games won together, then by fewer games played (a better record ranks higher). Forfeits, abandoned games and games still running don't count; a draw counts as a game played but not won. Cached 10 seconds.

```json
[{ "rank": 1, "wins": 4, "games": 6, "players": [{ "id": "7", "name": "Trini" }, { "id": "9", "name": "Ozi" }] }]
```

## `GET /api/games/{id}`

A game's summary: who played, on which team, how it ended, and each round's winners.

```json
{
  "id": 42, "room": "k3m9xq", "startedAt": "2026-10-19T12:00:00Z", "endedAt": "2026-10-19T12:40:00Z",
  "status": "finished", "endReason": "wins", "winnerTeam": 1, "draw": false, "countsForStats": true,
  "players": [{ "team": 0, "player": { "id": "7", "name": "Trini" } }],
  "rounds": [{ "round": 1, "team": 1, "wonAt": "...", "proclaimer": { "id": "7", "name": "Trini" }, "holder": { "id": "9", "name": "Ozi" } }]
}
```

`status` is `playing`, `finished` or `abandoned`. `endReason` is `wins`, `agreement` or `forfeit`. `winnerTeam` is null for a draw or an unfinished game. Cached 10 seconds.

## `GET /api/games/{id}/chat`

The game's public chat from the last 24 hours, oldest first, for settling disputed wins. Needs `Authorization: Bearer <start.ng game session>` and only the players of that game may read it (`403 not_a_player` otherwise). Never cached.

```json
[{ "id": 1, "body": "I said it first", "at": "...", "player": { "id": "7", "name": "Trini" } }]
```

Chat is stored for 24 hours and then deleted. The public chat feature itself is not built yet, so this is empty until it is.
