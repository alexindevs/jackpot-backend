package room

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/alexindevs/jackpot-backend/internal/engine"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunnerDrivesInputsAndRealTimers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LobbyCountdown = 80 * time.Millisecond
	cfg.PreRoundChat = 80 * time.Millisecond
	cfg.HoldTimer = 40 * time.Millisecond
	room, _ := New(cfg, "live", pid(0), rand.New(rand.NewPCG(1, 2)), time.Now())

	var mu sync.Mutex
	var seen []Out
	has := func(match func(Out) bool) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, o := range seen {
			if match(o) {
				return true
			}
		}
		return false
	}
	rn := NewRunner(room, time.Now, func(o []Out) {
		mu.Lock()
		seen = append(seen, o...)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { rn.Run(ctx); close(finished) }()

	// Several goroutines submit at once: the runner serialises them.
	var wg sync.WaitGroup
	for i := 1; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rn.Submit(pid(i), Join{}); err != nil {
				t.Errorf("join %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if err := rn.Submit(pid(1), Join{}); err != ErrAlreadyMember {
		t.Errorf("duplicate join = %v", err)
	}

	waitFor(t, "the countdown to start the game", func() bool {
		return has(func(o Out) bool { _, ok := o.Msg.(GameStarting); return ok })
	})
	waitFor(t, "the chat to end and a round to start", func() bool {
		return has(func(o Out) bool {
			ge, ok := o.Msg.(GameEvent)
			if !ok {
				return false
			}
			_, started := ge.Event.(engine.RoundStarted)
			return started
		})
	})
	waitFor(t, "the hold timer to pass for a stalling holder", func() bool {
		return has(func(o Out) bool {
			ge, ok := o.Msg.(GameEvent)
			if !ok {
				return false
			}
			p, isPass := ge.Event.(engine.Passed)
			return isPass && p.Auto
		})
	})

	cancel()
	<-finished
	if err := rn.Submit(pid(0), Leave{}); err != ErrClosed {
		t.Errorf("after stopping, Submit = %v, want ErrClosed", err)
	}
}

func TestRunnerStopsWhenTheRoomCloses(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EmptyRoomGrace = 30 * time.Millisecond
	room, _ := New(cfg, "short", pid(0), rand.New(rand.NewPCG(3, 4)), time.Now())
	rn := NewRunner(room, time.Now, func([]Out) {})
	finished := make(chan struct{})
	go func() { rn.Run(context.Background()); close(finished) }()

	if err := rn.Submit(pid(0), Disconnect{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the runner should stop once the room closes")
	}
	if room.Phase() != PhaseClosed {
		t.Errorf("phase = %v", room.Phase())
	}
}
