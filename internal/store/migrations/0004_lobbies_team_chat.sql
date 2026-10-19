-- Lobbies are saved as they change, so a restart doesn't lose them. A row is
-- removed when the room stops being a lobby (the game starts, or it closes).
CREATE TABLE lobbies (
    room       text        PRIMARY KEY,
    creator    text        NOT NULL,
    state      jsonb       NOT NULL,   -- members, pairs and open partner invitations
    updated_at timestamptz NOT NULL
);

-- Private team chat, kept like the public chat: until 24 hours after the game
-- ends, so disputes can be settled. seq is the line's number within the game.
CREATE TABLE team_chat_messages (
    game_id    bigint      NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    seq        int         NOT NULL,
    team       int         NOT NULL,
    round      int         NOT NULL,
    player_id  text        NOT NULL,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (game_id, seq)
);
