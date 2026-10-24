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
| B1 | **Suspect double-fire** (Rafe): tapping suspect twice suspects twice. Lock the button until the server answers, or debounce. | **Decided**, not built. Lock the button from the first tap until the server replies (`ok` or `error`); no debounce, which would add delay to a race. The cause: the frontend gave every request a fresh idempotency key, so two taps were two separate requests, and the backend's keys only protect a *retry of the same request*. No backend rule is added. The engine already rejects a second suspicion on a target who still owes a reveal or a forced pass (`ErrBusy`, no point charged). Accepted gap: two players suspecting a guilty target at the same instant can both pay, because that suspicion resolves at once and leaves nothing pending. | frontend (the lock) |
| B2 | **Could only watch** (Onyeka, 22:21). | Check. A started room only offers Watch: `join` is lobby-only (`not_joinable` once the game starts). Likely cause: they opened the room after it started. Not confirmed. | both (see R-series below) |
| B3 | **A link problem** (Bella, 20:03, screenshot). | **Fixed** by the author's diagnosis: the lobby closed after 1 minute with nobody connected, so a link to it led nowhere. A lobby now stays open for 20 minutes empty (`LobbyEmptyGrace`); a started game still pauses after 1 minute. | backend (done) |
| B4 | **People didn't know when a game started.** | Open. The server already sends `game_starting` and `chat_started`; the frontend shows no alert. | frontend (a clear start signal: sound, banner, tab title) |
| B5 | **People left rooms, so new ones had to be made repeatedly.** | Open. A lobby has no "who's here" strip beyond the player list; no rematch or play-again. | frontend; backend if a rematch should keep the same room and teams |
| B6 | **A rejected jackpot call** (ThePrimeJnr): his partner held four of a kind and the call was ruled false. | Check. A call is rejected without penalty while the partner holds the passing card (5 cards). A false call costs a suspicion point and, at 0 points, removes the right to call for the rest of the round. Whether that is what happened is unknown from the chat. The frontend shows a generic "called Jackpot falsely" popup with no reason, so the reason is unclear to players either way. | frontend (say *why*: partner has the passing card, no points left, or not four of a kind); backend to confirm the rejection reason it sends is specific enough. |

## UX (the main theme)

| # | Item | Status | Where |
| --- | --- | --- | --- |
| U1 | **Sound**, asked for by several people; Rafe suggested 8-bit. | Open | frontend |
| U2 | **Menus are hard to reach and close** (Rafe). | Open | frontend |
| U3 | **Signals are too slow to find in a menu** (Tobe: "by the time I find my signal, someone has caught me"). Ideas: a sidebar; a few signals on standby chosen before the game, including a pair agreed with your partner. | Idea | frontend; backend if "standby signals" should be chosen and stored per team before the game |
| U4 | **Signals visible in the team chat** (Rafe), because a new player knows none of them. | Open | frontend (and maybe backend, if the signals sent should be echoed into the team chat log) |
| U5 | **Public chat in the lobby** (Tobe). Today there is none: public chat opens when the game starts. | Open. A rule change: see rules.md section 10. | both |
| U6 | **More immersion** (Rafe): cards visibly moving, seeing your own card. Reference: the iOS Uno game. | Idea | frontend |
| U7 | **Shorten the card-pass time** (Tobe). The hold timer is 60 seconds (`HoldTimer`), a backend setting. | Open | backend (one config value) |

## Game design

| # | Item | Status | Where |
| --- | --- | --- | --- |
| G1 | **7 rounds is too long.** Kiane voted for 4; Onyeka voted for optional; both were floated. | Open | backend (`WinsToFinish` is already a setting, 7 today); frontend (the scoreboard has seven fixed letters and ignores the server's `winsToFinish`). A shorter game also means choosing which letters: J-A-C-K, or something else. |
| G2 | **Suspect points: 3 vs 5.** Tobe only had 3 and felt penalised. Raising to 5 was discussed. | Open | backend (`SuspicionPoints`, 3 today) |
| G3 | **A cooldown that refills suspect and jackpot attempts.** Proposed: 5 minutes. Tobe thinks that is too much: about 1 minute, or 3. The chat ends with the poll "All in favor of 5 minutes?" and **no votes recorded**. | Open | backend (new rule: points refill over time instead of only per round); frontend (show the cooldown) |
| G4 | **Mid-game joins.** People can join **between rounds**; catching up is their own responsibility. | Decided, not built. Today `join` is lobby-only; a started game offers Watch only. | backend (a join between rounds, which changes the ring seating and teams, so it needs thought) and frontend |
| G5 | **Tournaments.** "Would be nice." | Idea | both |

---

## Notes

- A few items hinge on the same thing: a game that has started can't be joined, so latecomers can only watch (B2, B5, G4).
- G1, G2, G3 and U7 are all numbers the author can change in `room.DefaultConfig()` / `engine.DefaultConfig()` once decided. G3 is the only one that needs new rules code.
- Poll results in the chat were not recorded. Ask the players again, or decide.
