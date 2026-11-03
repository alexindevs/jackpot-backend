# What's new in Jackpot

Everything since the first playtest with real people, in plain words. Items marked *needs the new app* also need the matching update to the game screen; they work as soon as both are live.

## Play with bots

- **Add bots to any room.** One person, or two, can press "Add bots" and fill the table. Bots can be your partner or your opponents.
- **Bots look like people, but you can tell.** They have real Nigerian names and a small "bot" tag next to them. They can't use the public chat.
- **They play like people do.** A bot takes 5 to 15 seconds to act, never instantly. It signals with the same kinds of gestures you do, tells its partner when it has four of a kind, and calls Jackpot when its partner signals four of a kind.
- **They are watching you.** Bots may suspect you if you repeat the same signal, use the signal you used to win last time, or suddenly start signalling after a quiet spell. They also sometimes suspect whoever just got the passing card. They don't suspect every time, and two bots never accuse the same player at once.
- **A bot partner can read your code.** If you pair with a bot, it writes its signals into your team chat in plain words, so you can tell what its gestures mean.
- **A person always hosts.** If every person leaves a bot game and doesn't come back, the game ends.
- **Bot games don't count.** They stay off every leaderboard and every duo ranking, so nobody can farm wins against them.

## Joining and leaving

- **Join a game that's already running.** If you opened a room after it started, you're no longer stuck watching. Ask to play, wait in line, and you're paired into a new team between rounds.
- **Unpair in the lobby.** If you paired with the wrong person, you can break the pair and choose someone else. *needs the new app*
- **Lobbies stay open longer.** An empty lobby now waits 20 minutes before closing, so a link shared with a friend still works when they open it.
- **A game survives a restart.** If the server restarts, your game comes back paused and carries on where it left off. Lobbies and team chats come back too.
- **Reconnecting is smoother.** If your connection drops, you catch up on what you missed instead of seeing a half-updated table.

## Rules and fairness

- **Choose a long or short game.** The person who opens the room picks 7 wins (the default) or 4 wins, spelling J-A-C-K.
- **No more double suspicions.** Tapping "Suspect" twice only suspects once.
- **Fairer false calls.** Calling Jackpot while your partner holds the passing card is turned down without a penalty. A call that is really wrong still costs a suspicion point. If that call is made at 0 points, you can't call Jackpot for 3 minutes. The message now reads: "Your partner does not hold four of a kind."
- **Suspicion points stay as they were.** Three per round, no refills. That was a playtest question and it stays as it is.
- **The hold timer stays at 60 seconds.** Also kept as it was.

## Results and history

- **Every team is named in the result.** When a game ends you see who was on each team, including teams that dropped out partway.
- **Your finished games.** A new list shows the games you've played: when, who won, how many rounds and who you played with. Bot games are marked. *needs the new app*
- **The result stays put.** If you reload after a game ends, you still see how it ended.

## Sound and signals

- **Sound.** Eight-bit style sounds for the start of a game, chat, passes and results, with a mute button and volume on every screen.
- **A clear start.** A "Game starting" banner, a sound, and the tab title changes if the game starts while you're on another tab.
- **200 signals** to choose from, to your partner only or to the whole room.

## Behind the scenes

- **Safer and sturdier.** Updated the server and its database driver to clear known security issues. A repeated vote to end the game early is ignored. Starting a game twice by accident is harmless, and the records a game is rebuilt from are never dropped.
- **Dev tools stay on dev machines.** The developer sign-in is refused on a live server.
- **Tested more.** Games are now tested end to end, including long simulated games with only bots, a person who walks away, and a game paused and resumed.

## Still to come

- A "play again" option that keeps the same room and teams.
- Public chat in the lobby (undecided).
- A way to add more than one bot team in one tap. *the server already supports it*
