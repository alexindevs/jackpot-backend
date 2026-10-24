# Playtest feedback

From the first friends-and-family playtest, as summarised by the game's author. Nothing here is built unless a status says so. Names are the players who raised or voted on a point.

Status markers:
- **Open**: raised, not decided.
- **Decided**: the author decided it; not built yet.
- **Idea**: floated, no decision.
- **Check**: could be a bug or a misunderstanding; needs looking at.

"Where" says which repo the work lands in: **backend** (this repo), **frontend** (`alexindevs/jackpot`), or both.

---

## Bugs and friction

| # | Item | Status | Where |
| --- | --- | --- | --- |
| B1 | **Suspect double-fire** (Rafe): tapping suspect twice suspects twice. Lock the button until the server answers, or debounce. | **Built** (frontend `4390878`). Lock the button from the first tap until the server replies (`ok` or `error`); no debounce, which would add delay to a race. The cause: the frontend gave every request a fresh idempotency key, so two taps were two separate requests, and the backend's keys only protect a *retry of the same request*. No backend rule is added. The engine already rejects a second suspicion on a target who still owes a reveal or a forced pass (`ErrBusy`, no point charged). Accepted gap: two players suspecting a guilty target at the same instant can both pay, because that suspicion resolves at once and leaves nothing pending. | frontend (the lock) |
| B2 | **Could only watch** (Onyeka, 22:21). | Check. A started room only offers Watch: `join` is lobby-only (`not_joinable` once the game starts). Likely cause: they opened the room after it started. Not confirmed. | both (see R-series below) |
| B3 | **A link problem** (Bella, 20:03, screenshot). | **Fixed** by the author's diagnosis: the lobby closed after 1 minute with nobody connected, so a link to it led nowhere. A lobby now stays open for 20 minutes empty (`LobbyEmptyGrace`); a started game still pauses after 1 minute. | backend (done) |
| B4 | **People didn't know when a game started.** | **Built** (frontend `37c19b4`): a "Game starting" banner, the tab title changes while the tab is hidden, and a start sound (see U1). The server already sent `game_starting` and `chat_started`; no backend change. | frontend (a clear start signal: sound, banner, tab title) |
| B5 | **People left rooms, so new ones had to be made repeatedly.** | Open. A lobby has no "who's here" strip beyond the player list; no rematch or play-again. | frontend; backend if a rematch should keep the same room and teams |
| B6 | **A rejected jackpot call** (ThePrimeJnr): his partner held four of a kind and the call was ruled false. | **Decided**; built in the frontend but not committed yet. A call is rejected without penalty (errors `partner_holds_passing_card` and `proclaim_revoked`) while the partner holds the passing card or the right to call is revoked. A call that really fails is a public `FalseProclaim` event: it costs a suspicion point and, at 0 points, removes the right to call for the rest of the round. The likely cause of this report, unconfirmed: an opponent's suspicion reset the partner (a four-of-a-kind hand shows only 1 type) just before the call arrived. The backend already sends specific codes, so no backend change. The frontend replaces the generic popup with one reason line, e.g. "Your partner no longer holds four of a kind. 2 points left." | frontend |

## UX (the main theme)

| # | Item | Status | Where |
| --- | --- | --- | --- |
| U1 | **Sound**, asked for by several people; Rafe suggested 8-bit. | **Built** (frontend `37c19b4`), awaiting the author's listen. Synthesised in the browser, so no audio files or licensing. Chat sounds always play, spectators hear only join, chat, game start, round results and game end, and signals play one identical quiet cue. Mute and volume on every screen; a test page lists every cue. | frontend |
| U2 | **Menus are hard to reach and close** (Rafe). | Open | frontend |
| U3 | **Signals are too slow to find in a menu** (Tobe: "by the time I find my signal, someone has caught me"). Ideas: a sidebar; a few signals on standby chosen before the game, including a pair agreed with your partner. | Idea | frontend; backend if "standby signals" should be chosen and stored per team before the game |
| U4 | **Signals visible in the team chat** (Rafe), because a new player knows none of them. | Open | frontend (and maybe backend, if the signals sent should be echoed into the team chat log) |
| U5 | **Public chat in the lobby** (Tobe). Today there is none: public chat opens when the game starts. | Open. A rule change: see rules.md section 10. | both |
| U6 | **More immersion** (Rafe): cards visibly moving, seeing your own card. Reference: the iOS Uno game. | Idea | frontend |
| U7 | **Shorten the card-pass time** (Tobe). The hold timer is 60 seconds (`HoldTimer`), a backend setting. | **Decided**: keep the 60 second hold timer. No change. | none |

## Game design

| # | Item | Status | Where |
| --- | --- | --- | --- |
| G1 | **7 rounds is too long.** Kiane voted for 4; Onyeka voted for optional; both were floated. | **Decided**; backend built (not committed), frontend not built. The room's creator picks a long game (7 wins, the default) or a short one (4 wins, spelling J-A-C-K) when opening the room: `create_room` takes `short`. The room list, the snapshot and `game_starting.winsToFinish` all carry the length. | backend (done); frontend (a length choice when creating a room, show it in the room list and lobby, and draw the scoreboard from `winsToFinish` instead of seven fixed letters) |
| G2 | **Suspect points: 3 vs 5.** Tobe only had 3 and felt penalised. Raising to 5 was discussed. | **Decided**: keep 3. No change. | none |
| G3 | **A cooldown that refills suspect and jackpot attempts.** Proposed: 5 minutes. Tobe thinks that is too much: about 1 minute, or 3. The chat ends with the poll "All in favor of 5 minutes?" and **no votes recorded**. | **Decided** and built on the backend (not the cooldown that was first proposed). Points do not refill. Instead, a false Jackpot call made at 0 suspicion points locks that player out of calling for 3 minutes (`ProclaimLockout`), after which the right comes back. It used to last the rest of the round. The server sends a `timer_started` of kind `proclaim` when the lockout starts and a `proclaim_restored` event when it ends; it also ends at the next round. The poll in the chat recorded no votes, so this is the author's call. | backend (done); frontend (show the lockout countdown and say why Jackpot is disabled) |
| G4 | **Mid-game joins.** People can join **between rounds**; catching up is their own responsibility. | Decided, not built. Today `join` is lobby-only; a started game offers Watch only. | backend (a join between rounds, which changes the ring seating and teams, so it needs thought) and frontend |
| G5 | **Tournaments.** "Would be nice." | Idea | both |

---

## Notes

- A few items hinge on the same thing: a game that has started can't be joined, so latecomers can only watch (B2, B5, G4).
- G1, G2, G3 and U7 are all numbers the author can change in `room.DefaultConfig()` / `engine.DefaultConfig()` once decided. G3 is the only one that needs new rules code.
- Poll results in the chat were not recorded. Ask the players again, or decide.
