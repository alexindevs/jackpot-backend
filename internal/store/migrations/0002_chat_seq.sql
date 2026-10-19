-- Number each chat message within its game (1, 2, 3, ...). Clients use the
-- number to ask for "everything after the last message I saw", whether they
-- got the messages live or by polling.
ALTER TABLE chat_messages ADD COLUMN seq int;
UPDATE chat_messages SET seq = id WHERE seq IS NULL;
ALTER TABLE chat_messages ALTER COLUMN seq SET NOT NULL;
CREATE UNIQUE INDEX chat_messages_game_seq ON chat_messages (game_id, seq);

-- Finding a room's most recent game by its room id.
CREATE INDEX games_room ON games (room, id DESC);
