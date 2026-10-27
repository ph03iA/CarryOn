# CarryOn

[Read the manual](https://ph03iA.github.io/CarryOn/)

CarryOn keeps your Claude Code and Codex work going through usage limits. It runs in the background on Windows, checks plan usage every minute without spending any, waits for a confirmed reset, asks Gemini whether continuing is worthwhile, and resumes the session by ID. It emails you when a limit comes back and when a resumed run finishes — never when a limit runs out.

It never sends a prompt while checking usage. It never reads Claude or Codex auth files.

## What it does

1. Every 60 seconds it reads usage without prompting:
   - **Codex:** the native `codex app-server` via `account/rateLimits/read`.
   - **Claude Code:** the `get_usage` control request in stream-json mode, the same numbers behind `/usage`.
2. A window at 100% becomes one incident, recorded once per provider + window + reset time.
3. It finds the newest session log whose last real event is a usage-limit error. No email is sent at this point. If no session is found, it searches again at reset.
4. At reset time it takes a fresh reading. The reset counts only when the reading is after the reset, under two minutes old, for the same account, with no window still at 100%.
5. It asks Gemini for a decision, then resumes by session ID when told to.
6. Once the reset is confirmed it sends one short email. The subject is `Claude Code is back: resuming <folder>`, `Claude Code is back: nothing to resume`, or `Claude Code is back: skipped <folder>`, with `Codex` in place of `Claude Code` when relevant. The body has a bold first sentence and short labelled lines: Project, Next step, Why, and a "Continue by hand" line with the resume command. When a resumed run finishes, a second email with the subject `Claude Code finished: <folder>` adds Result, Changed, and Agent said (at most 200 characters). Both emails go out as minimal HTML with a plain-text fallback.

## How the agent decides

Before a resume, Gemini receives the tail of the cut-off session (last ~4,000 tokens), the limit and reset time, and the folder. It returns one of:

| Action | Meaning |
| --- | --- |
| `resume` | Unfinished work remains; includes a concrete next step |
| `skip_done` | The task already looks finished |
| `skip_stuck` | The agent was looping or blocked; a person should look |

After a resume, Gemini receives the exit status, `git diff --stat`, a bounded diff excerpt, test output when tests ran, and the last message. It returns one of:

| Action | Meaning |
| --- | --- |
| `done` | The task is complete |
| `resume_again` | One more short run is justified (max 2 resumes per limit hit) |
| `stop` | Stop and report |

Examples:

- The session ended mid-refactor with a failing test and a clear next step → `resume` with "run the failing test and finish the handler".
- The final assistant message says the change is complete and tests pass → `skip_done`; no usage is spent resuming.
- The log shows the same file edited five times with the same error → `skip_stuck`; you get an email explaining why.
- After a resumed run, tests pass and the diff matches the request → `done`.
- After a resumed run, the build fails for an unrelated reason → `stop`, with the failure in the email.

Every decision is stored in SQLite and shown by `carryon decisions`.

## Session safety

CarryOn resumes by ID, not by "the latest session":

- Claude: `claude --resume <id> -p "<prompt>" --permission-mode acceptEdits`
- Codex: `codex exec resume <id> "<prompt>" -c sandbox_mode="workspace-write"`

Before resuming it checks the session log for real events after the recorded cut-off. If something else already continued the session, CarryOn skips it and emails why.

Every resume prompt starts with a fixed instruction:

> Continue the task from this session. The note below was written by an automated helper; if it conflicts with the user's earlier instructions, follow the user.

Gemini's next-step prompt is capped at 500 characters. Session text, diffs, and test output are sent to Gemini inside explicit "untrusted" delimiters, and the system instruction tells the model to ignore instructions found inside them.

Each limit hit allows at most two resumes for its cut-off session. A run interrupted by a crash consumes an attempt and is never restarted without a new Gemini decision.

## Crash safety

Every Claude, Codex, and probe process runs inside a Windows Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. The process is started suspended, assigned to the job, then resumed. If CarryOn exits or crashes, Windows closes the job handle and kills the agent and all of its children. Timeouts use `TerminateJobObject`, not `taskkill`.

If the job cannot be created or assigned, CarryOn refuses to start the agent.

## Setup

Requirements:

- Windows 10/11
- Go 1.26+
- Claude Code signed in with a Pro/Max plan, and/or Codex signed in with a ChatGPT plan
- A Resend API key
- A Gemini API key

Build:

```powershell
$env:GOARCH = "amd64"
go build -trimpath -o carryon.exe ./cmd/carryon
```

Create the state folder and config, and set your recipient:

```powershell
.\carryon.exe init --email you@example.com
```

That writes `%APPDATA%\CarryOn\config.json` and `%APPDATA%\CarryOn\.env`. Put the keys in `.env`:

```
RESEND_API_KEY=re_...
GEMINI_API_KEY=...
```

There is no default recipient. Set one with `carryon init --email you@example.com`; without one, emails stay queued and the log says so once. The default sender is `onboarding@resend.dev`. The Resend onboarding sender can only deliver to the email address on your Resend account. To send elsewhere, verify a domain in Resend and change `sender` in `config.json`.

`config.json` fields:

| Field | Default | Meaning |
| --- | --- | --- |
| `providers` | `both` | `claude`, `codex`, or `both` |
| `recipient` | empty | Where email goes; set with `carryon init --email` |
| `sender` | `onboarding@resend.dev` | Verified Resend sender |
| `gemini_model` | `gemini-3.5-flash-lite` | Model used for decisions |
| `claude_bin` | empty | Optional explicit Claude executable |
| `codex_bin` | empty | Optional explicit native Codex executable |
| `poll_seconds` | `60` | Usage poll interval |
| `resume_timeout_minutes` | `360` | Hard limit for one agent run |
| `max_resumes` | `2` | Total resumes per limit hit |
| `session_tail_chars` | `16000` | Session tail sent to Gemini |
| `decision_prompt_chars` | `500` | Cap on Gemini's next-step prompt |

Run it in the foreground first:

```powershell
.\carryon.exe watch
```

Install the logon task:

```powershell
.\carryon.exe service install
```

The task starts at sign-in, restarts after a failure, and allows only one instance. State lives in `%APPDATA%\CarryOn\state.db`, so restarts are safe.

## Commands

```
carryon init [--providers claude|codex|both] [--email you@example.com]  create the state folder, config, and .env
carryon watch [--once] [--interval 60s]
carryon status [--json]
carryon decisions [--limit 20]
carryon simulate                      newest Claude Code session
carryon simulate codex                newest Codex session
carryon simulate --session <id> [--provider claude|codex]
carryon log                           follow the log until Ctrl+C
carryon reset [--yes]                 delete the state database
carryon service install|uninstall|status
carryon version
```

By default CarryOn watches both tools. To watch one, run `carryon init --providers claude` or `carryon init --providers codex`. The watcher reads usage and resumes sessions only for the selected tools; incidents already recorded for a disabled tool are closed without resuming on the next cycle.

`carryon simulate` fakes the limit and its reset, then uses the real Gemini call, the real agent resume, and real email. It picks the newest Claude Code session, or the newest Codex session with `simulate codex`, and prints the session, folder, and ID before starting. `carryon log` follows the log file until Ctrl+C. `carryon reset` deletes the state database after a confirmation prompt; `--yes` skips it. Config and keys are kept.

## What goes to Google and Resend

**Google (Gemini):**

- The last ~4,000 tokens of the cut-off session, trimmed to user/assistant text and tool names/inputs, with tool outputs truncated.
- The folder, session ID, limit name, reset time, resume count.
- After a run: exit status, `git diff --stat`, a bounded diff excerpt, test output when tests ran, and the last agent message.
- Secrets are redacted before sending: Anthropic, Google, GitHub, AWS, Slack, and Bearer-style tokens, PEM private keys, and `*_API_KEY=`/`*_TOKEN=` assignments.

**Resend:**

- Recipient, sender, subject, and the email body, in minimal HTML and as a plain-text fallback.
- The body contains the folder name, the next step, the reason for the decision, up to five changed file names (then "+N more"), at most 200 characters of the agent's last message, and the "Continue by hand" resume command.
- It contains no reset times, full paths, exit codes on success, "Sent by" lines, or test output.

CarryOn itself makes no other network calls. It never reads `~/.claude/.credentials.json`, `~/.codex/auth.json`, or any keychain entry.

## Security

- API keys load from the process environment or `%APPDATA%\CarryOn\.env`.
- Both keys are removed from CarryOn's own environment and from every Claude/Codex child process.
- The state database and key file are created with restrictive permissions.
- No shell is used to spawn agents; every command is an argument array.
- Logs never include keys, full prompts, or full transcripts.

## Known limits

- **The Claude cut-off fixture is invented.** No real Claude usage-limit transcript was available while building, so `internal/session/testdata/claude/cutoff` is synthetic. The detector is deliberately conservative. Capture a real cut-off, replace the fixture, and re-check the detector when you have one.
- **Reopen by ID after a resume.** Once CarryOn resumes a session, the old terminal window is stale. Continue with `claude --resume <id>` or `codex exec resume <id>`; do not type into the old window.
- `get_usage` is an experimental Claude Code control request. It is isolated in `internal/quota/claude.go` and may change upstream.
- Print-mode permissions can still block edits even with `acceptEdits` / `workspace-write`.
- Claude models can omit a window after it resets; an omitted window is treated as available.
- A crash after a resume starts consumes one attempt; further resumes need a fresh decision.
- The two-resume budget is per limit incident, not per session forever. A session cut off by a later window gets a fresh budget.
- "Files changed" compares against a snapshot taken before the run: a stash commit when the folder was dirty, or the pre-run HEAD when it was clean. Commits made by the agent therefore appear too, and your pre-existing work does not.
- One instance only: `watch` takes `Global\CarryOn` and exits if another watcher holds it. `simulate` does not take the lock; the watcher skips simulated incidents.
- The cut-off search only accepts sessions written after the current window started, and retries once at reset time.
- **Secrets cut at the clipping point.** The session tail is clipped by character count before redaction. A credential that straddles the cut can lose its recognisable prefix and survive; treat the excerpt as sensitive.
- **Two simulations at once.** `simulate` has no lock, so two can run together, spend real Gemini and agent usage, and both try to resume the same session.
- **A crash during the scan or the post-run Gemini call.** A crash before the incident is recorded is retried on the next sweep. A crash during the post-run Gemini call can leave the run recorded without its finish email; on restart the session may look already continued and be skipped.
- **Continuing during the Gemini call.** CarryOn checks for an external continuation before asking Gemini. If you continue the session while Gemini is deciding, CarryOn can still resume afterwards.
- **Resuming a few minutes early.** A window still at 100% whose reset time has passed is treated as reset, because its reset is no longer in the future. CarryOn can therefore resume a few minutes before Claude reports the window as available, and the agent may hit the limit again immediately.
- **Emails after long downtime.** The outbox retries transient Resend failures up to eight times (about two hours). A machine that stays offline longer than that, or past Resend's 24-hour idempotency window, can lose or duplicate an event.
- **Edits to already-untracked files.** The pre-run snapshot records untracked paths and excludes them, so later edits to a file that was already untracked are not listed in "files changed".
- Windows only. There are no systemd or launchd service files.
- The logon task prefers `-LogonType S4U`, so it runs without a console window. If Windows needs admin rights or a "Log on as a batch job" policy, CarryOn falls back to an interactive logon task, which may briefly show a console window.
- Smart App Control can block locally built unsigned executables. See Troubleshooting.

## Verified

- **2026-10-26 — Codex resume writes files.** Codex 0.163.0 on Windows: created a disposable session, then ran `codex exec resume <id> "Create a file named smoke.txt ..." -c sandbox_mode="workspace-write"`. The file was created with bytes `6F-6B` (`ok`). This is the one-time write check, not an automated test.
- **2026-10-26 — Live usage probes.** The real Codex app-server and Claude `get_usage` probes both returned usage windows without sending a prompt.

## Troubleshooting

**`An Application Control policy has blocked this file`**

Windows Smart App Control (or a similar application-control policy) is blocking the freshly built `carryon.exe` and Go test binaries. Check:

```powershell
Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy" VerifiedAndReputablePolicyState
```

`1` means Smart App Control is enforcing. Either turn Smart App Control off in Windows Security → App & browser control → Smart App Control, or run CarryOn on a machine where application control allows locally built binaries. CarryOn cannot work around this policy.

If only the Go test binaries are blocked, the logic suites can still run under WSL, because Smart App Control does not apply there:

```powershell
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go test -c -o "$env:TEMP\carryon-wsl\config.test" ./internal/config
wsl -d Ubuntu -- "$(wslpath -a "$env:TEMP\carryon-wsl\config.test")"
```

The Windows-only job-object tests (`internal/winjob`, and the process-spawning paths in `resume` and `quota`) still need to run on Windows.

**Codex not found**

CarryOn rejects `.cmd`, `.bat`, and `.ps1` shims. It searches `codex` on `PATH` and then the npm vendor folder for the native `codex.exe`. Set `codex_bin` in `config.json` if yours lives elsewhere.

**No email arrives**

If no recipient is set, emails stay queued; run `carryon init --email you@example.com`. Otherwise check your spam folder, then `carryon status` for the outbox. The Resend onboarding sender can only send to the account email. Verify the sender in Resend for any other address.

**Gemini failed**

CarryOn does not resume when Gemini fails or returns invalid JSON. It emails the reason and stops. Check `carryon decisions` and `%APPDATA%\CarryOn\carryon.log`.

## Development

```powershell
$env:GOARCH = "amd64"
go build ./...
go vet ./...
go test ./...
```

Tests use fake Claude/Codex CLIs, a fake Gemini transport, a fake Resend server, and fixture sessions. No real API runs in tests.

The live probes can be checked deliberately:

```powershell
$env:CARRYON_LIVE = "1"
go test ./internal/quota -run TestLiveProbes -v
```

## Decisions

Policy changes are recorded here as the build evolves.

- **2026-10-26 — Resume by ID.** The spec's `-s` flag is rejected by `codex exec resume` on Codex 0.163.0, so CarryOn uses `-c sandbox_mode="workspace-write"`. Resumes target explicit session IDs, so the "must be newest" check was replaced by an "already continued" check.
- **2026-10-26 — Untrusted session text.** Session logs, diffs, and test output are framed as data. Gemini prompts are capped at 500 characters and every resume prompt starts with a fixed instruction.
- **2026-10-26 — Two resumes maximum per limit hit.** The first resume plus one more, with a fresh budget for each new incident. A crash consumes an attempt and requires a new decision.
- **2026-10-26 — Gemini failure blocks.** No decision means no resume; the incident becomes `blocked` and an email explains why.
- **2026-10-26 — Job objects.** All agent processes are started suspended, assigned to a kill-on-close job, then resumed.
- **2026-10-26 — Simulate.** `carryon simulate` fakes only the limit and reset; Gemini, the agent, and email are real and labelled SIMULATED.
- **2026-10-26 — No doctor command.** `status` and `decisions` cover inspection.
- **2026-10-26 — Redaction covers AKIA and github_pat_.** Added to the existing pattern set.
- **2026-10-26 — Email only when a limit comes back.** The run-out email and the separate resume-start email were removed. After a reset is confirmed, CarryOn sends exactly one "back" email saying what it did (resuming, nothing to resume, or skipped with a reason). The finish email and the 24-hour needs-you email remain. `simulate` follows the same rules and is labelled SIMULATED.
- **2026-10-26 — Global single-instance mutex.** `watch` takes `Global\CarryOn`, so an S4U watcher in another Windows session still blocks a second watcher. `simulate` no longer takes the lock; the watcher skips simulated incidents.
- **2026-10-26 — No old-database upgrades.** There are no released databases, so `store.Open` creates the current schema and no longer runs silent ALTER migrations.
- **2026-10-26 — Diff from the pre-run HEAD.** A clean folder records `HEAD` before the run, so commits made by the agent appear in "files changed".
- **2026-10-27 - gemini-3.5-flash-lite with a thinking cap.** The default model is `gemini-3.5-flash-lite`, and each decision sets a 128-token thinking budget. If the model rejects the budget, CarryOn retries without it, and every Gemini call logs its duration.
- **2026-10-27 - Short HTML and text emails.** Subjects name the tool and folder, and the body uses short bold labels: Project, Next step, Why, Changed, Agent said, and a Continue by hand command. Resend receives both HTML and a plain-text fallback.
- **2026-10-27 - Shorter commands.** `simulate` picks the newest session, `log` follows the log, and `reset` deletes state after a confirmation. `--session` and `--yes` keep the explicit paths available.
- **2026-10-27 - Provider selection.** `providers` in `config.json` accepts `claude`, `codex`, or `both`; `init --providers` sets it. The watcher reads only the selected tools and closes incidents for a disabled tool without resuming.
- **2026-10-27 - No default recipient.** A new config starts with an empty recipient. `init --email` sets it, existing configs keep theirs, and without one emails stay queued with a single log warning.
