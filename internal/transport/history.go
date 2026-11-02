package transport

import (
	"context"
	"errors"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

var errHistoryUnavailable = errors.New("the list of finished games is not available right now")

// finishedGames answers the finished_games request: the player's last games
// that ran to the end, newest first. Without a database there is none.
func (m *Manager) finishedGames(user engine.PlayerID) reply {
	if m.cfg.History == nil {
		return reply{t: "finished_games", d: []any{}}
	}
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	games, err := m.cfg.History(ctx, user)
	if err != nil {
		return errReply(errHistoryUnavailable)
	}
	return reply{t: "finished_games", d: games}
}
