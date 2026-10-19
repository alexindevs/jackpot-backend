// Command server runs the Jackpot game server.
//
// Configuration comes from environment variables:
//
//	JACKPOT_ADDR        listen address (default ":8080")
//	JACKPOT_GAME        the start.ng game name (default "jackpot")
//	JACKPOT_ORIGINS     comma-separated hosts allowed to open a socket
//	                    (default "<game>.start.ng")
//	JACKPOT_START_NG    start.ng API base URL (default "https://api.start.ng")
//	JACKPOT_DEV         "1" accepts "dev:<name>" tokens and localhost origins.
//	                    For local testing only: anyone can claim any name.
//
// The server speaks plain WebSocket. Put a reverse proxy in front of it to
// terminate TLS: browsers on https pages need wss://.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/transport"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	game := env("JACKPOT_GAME", "jackpot")
	addr := env("JACKPOT_ADDR", ":8080")
	origins := strings.Split(env("JACKPOT_ORIGINS", game+".start.ng"), ",")
	dev := os.Getenv("JACKPOT_DEV") == "1"

	var auth transport.Authenticator = transport.StartNG{BaseURL: env("JACKPOT_START_NG", "https://api.start.ng"), Game: game}
	if dev {
		log.Warn("JACKPOT_DEV=1: anyone can sign in as anyone. Never run this in production.")
		auth = transport.Dev{}
		origins = append(origins, "localhost:*", "127.0.0.1:*")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := transport.NewManager(ctx, transport.Config{
		Game:           game,
		Room:           room.DefaultConfig(),
		Auth:           auth,
		OriginPatterns: origins,
	})
	srv := &http.Server{Addr: addr, Handler: m.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr, "game", game, "origins", origins, "dev", dev)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		m.CloseAll()
		_ = srv.Shutdown(shutdown)
		m.Wait()
	}
}
