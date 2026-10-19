# jackpot-backend

The game server for Jackpot, a card game for 2 to 5 teams of 2 played on [start.ng](https://start.ng/docs.md). Go, a WebSocket for gameplay, a small read-only REST API, Postgres for history and leaderboards.

- `docs/rules.md` the game rules and every decision made about them
- `docs/protocol.md` the WebSocket protocol
- `docs/api.md` the REST API
- `docs/architecture.md` how it fits together, and what is still unverified

## Layout

| Path | What |
| --- | --- |
| `internal/engine` | the rules: a pure state machine, no I/O |
| `internal/room` | one live game: lobby, timers, reconnects, who sees what |
| `internal/transport` | WebSockets, sign-in, one room per player |
| `internal/store` | Postgres: migrations, queries, the async writer |
| `internal/api` | the REST API |
| `internal/wirejson` | camelCase JSON naming shared by the socket and the API |
| `internal/e2e` | a whole game through every layer |
| `cmd/server` | the server |

## Run it locally

You need Go 1.24 and Docker.

```sh
cp .env.example .env              # then set POSTGRES_PASSWORD (letters and digits)
docker compose up -d              # Postgres on 127.0.0.1:5432

export DATABASE_URL='postgres://jackpot:<password>@127.0.0.1:5432/jackpot?sslmode=disable'
JACKPOT_DEV=1 go run ./cmd/server
```

`JACKPOT_DEV=1` lets anyone sign in as `dev:<name>` and allows localhost pages. It is for local testing only. Without `DATABASE_URL` the server still plays games, but nothing is saved and the leaderboard endpoints answer 503.

The server applies database migrations itself on every start.

To run the server in a container as well: `docker compose --profile app up -d --build`.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `JACKPOT_ADDR` | `:8080` | listen address |
| `JACKPOT_GAME` | `jackpot` | the start.ng game name |
| `JACKPOT_ORIGINS` | `<game>.start.ng` | comma-separated hosts allowed to connect from a browser |
| `JACKPOT_START_NG` | `https://api.start.ng` | start.ng API, used to check sign-ins |
| `DATABASE_URL` | none | Postgres connection string |
| `JACKPOT_DEV` | off | see above |

## Tests

```sh
go test ./...
```

The database tests are skipped unless you point them at a Postgres server. They create and drop their own throwaway databases, so use any server you don't mind them touching:

```sh
JACKPOT_TEST_DATABASE_URL='postgres://jackpot:<password>@127.0.0.1:5432/postgres?sslmode=disable' go test ./...
```

## Deploying

The server speaks plain HTTP and WebSocket. Browsers on an https page need `wss://` and `https://`, so put a reverse proxy in front that terminates TLS, for example Caddy:

```
api.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

### Render (or a similar host)

The server is ready for a platform like Render: it listens on the `PORT` the host provides, serves plain HTTP and WebSocket for the host's proxy to wrap in TLS, and pings every client every 25 seconds so idle connections aren't dropped by a proxy. On Render you would create a **Web Service** from this repo (Docker, using the `Dockerfile`) and set:

| Variable | Value |
| --- | --- |
| `JACKPOT_GAME` | `jackpot` |
| `JACKPOT_ORIGINS` | `jackpot.start.ng` |
| `DATABASE_URL` | the connection string of a Postgres database (Render's, or any other) |

Do not set `JACKPOT_DEV`. The socket URL is then `wss://<your-service>.onrender.com/ws` and the API is under `https://<your-service>.onrender.com/api`.

These points about Render come from memory and weren't checked against its current docs, so check them before relying on them:

- Web services get an `onrender.com` address with TLS, and WebSockets work on them.
- **Free instances spin down after a period without traffic and take a while to wake up.** Games live in memory, so a spin-down ends every game and lobby. Use an always-on paid instance for real play.
- Free Postgres databases may expire after a limited time.

Run **one** server instance. Rooms live in memory, and on start the server marks any game still open in the database as abandoned, which assumes nothing else is running games.
