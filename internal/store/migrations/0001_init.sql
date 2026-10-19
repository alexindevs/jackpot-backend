-- Players we have seen. Names and looks come from start.ng at sign-in and are
-- refreshed every time, so leaderboards show the current ones.
CREATE TABLE players (
    id         text PRIMARY KEY,           -- the start.ng player id
    name       text        NOT NULL,
    style      jsonb,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE games (
    id               bigserial PRIMARY KEY,
    room             text        NOT NULL,
    started_at       timestamptz NOT NULL,
    ended_at         timestamptz,
    status           text        NOT NULL DEFAULT 'playing'
                     CHECK (status IN ('playing', 'finished', 'abandoned')),
    end_reason       text CHECK (end_reason IN ('wins', 'agreement', 'forfeit')),
    winner_team      int,                      -- null for a draw, or an unfinished game
    draw             boolean     NOT NULL DEFAULT false,
    counts_for_stats boolean     NOT NULL DEFAULT false  -- false for forfeits
);
CREATE INDEX games_started_at ON games (started_at DESC);

-- Who played, and on which team. No foreign key to players on purpose: a game
-- is recorded even if the player's profile hasn't been saved yet.
CREATE TABLE game_players (
    game_id   bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    player_id text   NOT NULL,
    team      int    NOT NULL,
    PRIMARY KEY (game_id, player_id)
);
CREATE INDEX game_players_player ON game_players (player_id);

-- One row per round won: who proclaimed, and who held the four of a kind.
CREATE TABLE rounds (
    game_id    bigint      NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round      int         NOT NULL,
    team       int         NOT NULL,
    proclaimer text        NOT NULL,
    holder     text        NOT NULL,
    won_at     timestamptz NOT NULL,
    PRIMARY KEY (game_id, round)
);
CREATE INDEX rounds_proclaimer ON rounds (proclaimer);
CREATE INDEX rounds_holder ON rounds (holder);

-- Public chat, kept for 24 hours so disputed wins can be checked. A job
-- deletes older rows.
CREATE TABLE chat_messages (
    id         bigserial PRIMARY KEY,
    game_id    bigint      NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    player_id  text        NOT NULL,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_messages_game ON chat_messages (game_id, id);
CREATE INDEX chat_messages_created ON chat_messages (created_at);
