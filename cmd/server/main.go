// Command server runs the Jackpot game server.
//
// Configuration comes from environment variables:
//
//	JACKPOT_ADDR        listen address. If unset, the PORT variable that most hosts
//	                    (Render, Heroku, Fly) provide is used, else ":8080"
//	JACKPOT_GAME        the start.ng game name (default "jackpot")
//	JACKPOT_ORIGINS     comma-separated hosts allowed to open a socket or call
//	                    the API from a browser (default "<game>.start.ng")
//	JACKPOT_START_NG    start.ng API base URL (default "https://api.start.ng")
//	DATABASE_URL        Postgres connection string. Without it the server still
//	                    plays games, but nothing is saved and the leaderboard
//	                    endpoints answer 503.
//	JACKPOT_DEV         "1" accepts "dev:<name>" tokens and localhost origins.
//	                    For local testing only: anyone can claim any name.
//
// The server speaks plain HTTP and WebSocket. Put a reverse proxy in front of
// it to terminate TLS: browsers on https pages need wss:// and https://.
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

	"github.com/alexindevs/jackpot-backend/internal/api"
	"github.com/alexindevs/jackpot-backend/internal/room"
	"github.com/alexindevs/jackpot-backend/internal/store"
	"github.com/alexindevs/jackpot-backend/internal/transport"
)

const chatRetention = 24 * time.Hour

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	game := env("JACKPOT_GAME", "jackpot")
	addr := os.Getenv("JACKPOT_ADDR")
	if addr == "" {
		if port := os.Getenv("PORT"); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
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

	roomCfg := room.DefaultConfig()
	var (
		db        *store.Store
		writer    *store.Writer
		apiStore  api.Store // stays a nil interface when there is no database
		onSignIn  func(transport.Identity)
		sweepDone = make(chan struct{})
	)
	if url := os.Getenv("DATABASE_URL"); url != "" {
		var err error
		if db, err = store.Open(ctx, url); err != nil {
			return err
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			return err
		}
		if n, err := db.AbandonUnfinished(ctx, time.Now()); err != nil {
			return err
		} else if n > 0 {
			log.Warn("marked games cut off by the last shutdown as abandoned", "games", n)
		}
		writer = store.NewWriter(db, log, 1024)
		roomCfg.OnRecord = writer.Record
		onSignIn = func(id transport.Identity) {
			writer.Seen(store.Player{ID: string(id.ID), Name: id.Name, Style: id.Style})
		}
		apiStore = db
		go func() {
			defer close(sweepDone)
			db.RunChatSweeper(ctx, time.Minute, chatRetention, log)
		}()
		log.Info("database connected")
	} else {
		close(sweepDone)
		log.Warn("DATABASE_URL is not set: games are not saved and leaderboards are off")
	}

	m := transport.NewManager(ctx, transport.Config{
		Game:           game,
		Room:           roomCfg,
		Auth:           auth,
		OriginPatterns: origins,
		OnSignIn:       onSignIn,
		Log:            log,
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler(api.Config{
		Store:         apiStore,
		Rooms:         m,
		Auth:          auth,
		Origins:       origins,
		ChatRetention: chatRetention,
	}))
	mux.Handle("/", m.Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr, "game", game, "origins", origins, "dev", dev, "database", db != nil)

	var result error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
		stop()
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.CloseAll()
	_ = srv.Shutdown(shutdown)
	m.Wait()
	<-sweepDone
	if writer != nil {
		if err := writer.Close(shutdown); err != nil {
			log.Error("some records were not saved before shutdown", "err", err)
		}
		if n := writer.Dropped(); n > 0 {
			log.Error("records were dropped because the database queue was full", "dropped", n)
		}
	}
	return result
}
