-- Live games are saved as their setup plus every command applied to them, so a
-- restart (or everyone dropping off) can pause a game instead of ending it.
-- The engine is deterministic: the setup and the commands rebuild the game.
ALTER TABLE games ADD COLUMN creator       text;
ALTER TABLE games ADD COLUMN setup         jsonb;        -- null once the game's data has expired
ALTER TABLE games ADD COLUMN last_event_at timestamptz;

CREATE TABLE game_events (
    game_id bigint      NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    seq     int         NOT NULL,                        -- 1, 2, 3 ... within the game
    at      timestamptz NOT NULL,
    command jsonb       NOT NULL,
    PRIMARY KEY (game_id, seq)
);
