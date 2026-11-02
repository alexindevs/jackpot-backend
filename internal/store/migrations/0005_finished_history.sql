-- A player's list of finished games needs the game's length and how many rounds
-- it ran, and both must outlive the 24 hour cleanup of a game's setup. They are
-- kept on the game's own row, which stays for good.
ALTER TABLE games ADD COLUMN wins_to_finish int NOT NULL DEFAULT 7;
ALTER TABLE games ADD COLUMN rounds_played  int NOT NULL DEFAULT 0;

-- Games whose setup has not expired yet know their length; older ones keep 7.
UPDATE games SET wins_to_finish = (setup->'Config'->>'WinsToFinish')::int
 WHERE setup IS NOT NULL AND setup->'Config'->>'WinsToFinish' IS NOT NULL;

-- Rounds that were won are recorded, so they are the best count for older games.
UPDATE games g SET rounds_played = COALESCE((SELECT MAX(round) FROM rounds r WHERE r.game_id = g.id), 0);
