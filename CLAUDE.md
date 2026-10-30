# Working on Calamares

Calamares is a terminal command center for GitHub, written in Go with Bubble Tea. The UI is in
`internal/ui`, the GitHub client in `internal/gh`.

## After every change

- Run `go vet ./...` and `go test ./...`.
- Rebuild the binary: `go build -o bin/calamares.exe ./cmd/calamares`, and say how to start it.

## Mockup first

- Before you change anything the user can see (layout, menus, keys, colours, wording), make a visual
  mockup of it first and discuss it. Show it directly in the conversation (the inline visual widget
  in the Claude desktop app): today next to the proposal, why it is better, and the open questions.
- End a mockup with your open questions, and give each one your own proposal and why. The user can
  then answer "yes" or only correct what they disagree with.
- Never save mockups as files in the repository (no HTML pages in `design/`); they are not tracked.
- Do not write the code until the user has said yes to the mockup.
- Why: the user judges the result by how it looks and feels, and a change is much cheaper to
  correct on a mockup than in finished, tested and screenshotted code.

## Screenshots

- The README is a short showcase with a handful of screenshots (hero, pull request, issues, run,
  three themes) and one GIF of the pipeline in motion, not a tour of every screen; the full guide
  lives in `docs/usage.md`.
- After a change to the UI, retake them with `bash demo/_src/shots.sh` and check they show the new
  look. Add a screenshot only when it sells something new, and keep the README short.

## Tasks

- When told to pick up a new task, read `TODO.md` (at the root) and take the **first**
  task under "To do".
- When a task is finished, move it to the "Done" section at the bottom of that file.

## Demos

- Make a demo video **only when asked for one**. Do not make one by default after a change.
- When asked: videos go in `demo/` (git-ignored), named `YYYY-MM-DD_HHMM_what-it-shows.mp4`, and
  are recorded key by key from the real app with invented data (the scripts are in `demo/_src/`).

## Design

- The UI has **not been approved yet**; see `TODO.md` for what is still open.
- The look is the "Cockpit" direction: numbered panels with the title in the border, the panel in
  focus in the accent blue, a table of one line per run (`z`: cards of two lines with a rule
  between them), a preview of what is selected, key caps for keys.
- Colour rules: white for what you read (titles); light text for all data in a row (workflow,
  branch, who, times); dim grey only for labels, column heads and hints; the theme's accent for
  focus (cursor, frame in focus, the view on display) and for the icons of the places in the
  header; status colours for status (green passed, red failed, amber running, orange waits for a
  person). No purple for waiting.
- Icons carry the colour of what they stand for, and the text beside them stays light: gold repos,
  tags and numbers (`#57`), lime branches, commits and versions (`cRef`), teal people, pink comments,
  steel times. Pull request and issue icons follow GitHub: green open, violet merged or done, red
  closed, grey draft. Labels are chips in their own colour. Use `ico`, `fact`, `iconCell` and `labelChip`.
- The view on display in a strip is filled and in brackets: `[ Assigned 3 ]`. What a screen wants
  from you sits in a callout (a bar in its colour on a tint of it) with the keys that act on it.
- Layers, not outlines: the header is a filled band (logo, name and status on the first line, the
  places on the second, a rule under them); a detail panel beside a list is a filled panel. In a list a
  status is crisp: its glyph and its word in its colour, no block behind it (`statusCell`). Counters
  at the top of a screen and the status in a panel are rounded pills: the
  colour on a dark tint of it, with Nerd Font round ends (`statusChip`, `statusPill`). Every column
  of a list and every piece of metadata (branch, clock, lock, repo, person, pull request) carries
  its icon in its colour (`iconText`, `numberCell`, `titleCell`, `steelCell`); a quiet row (cancelled,
  skipped, read) is dim from end to end. The places in the header carry no counts.
- Our style: **a screen that lists things also tells you about the one under the cursor.** Calamares
  is a GitHub command center, so a screen is not only a pretty list: it shows what you need to know
  at a glance. Give every list screen (a) a title line with chips counting what matters (failing,
  running, waits for you, ready to merge), (b) the list, one line per entry, and (c) a filled panel
  at the right (from 110 columns) for the selected entry: its facts, how it is doing, what to do
  next, and related things we already hold (runs on a branch, history of a workflow, its pull
  requests). Prefer data already loaded; an extra request must be cheap, cached and read once.
  Ask of every new screen: *what would someone want to know without opening this?* It must also
  look good: layers and hairlines instead of boxes, icons on metadata, crisp statuses in lists and pills for counters.
- Words: a title is a noun or a noun phrase that names what is in the section ("Failures",
  "Runner usage", "Run history"), in sentence case and white, never a phrase like "Why it grew" or a
  judgement like "Broken now". A section that needs it has a one-line subtitle in grey that says
  what it holds. Facts in a panel are labelled fields (`field`: "Used by  5 repositories"), not
  bare values. Column heads are short nouns in grey capitals ("ITEM", "STATUS"), not "A / B".
  Counts over percentages where you can count ("9 of 17 runs"), and one set of status words
  (Failing, Outdated, Disabled, Healthy) instead of jargon such as "behind" or "▼ 44 pts".
- Icons come from a Nerd Font (`icons: nerd` in the config, the default); `icons: plain` uses
  ordinary symbols for terminals without one.

## Code

- A screen lists its keys once as bindings (`Keys`); the key bar, the help and the read-only check
  come from that list. Don't add a separate hint list.
- Make every change on its own branch off `main`, never on `main` itself. Create the branch before
  the first edit.
- Commit messages follow Conventional Commits; `feat:` and `fix:` on `main` publish a release.
- Examples, tests, fixtures and demo data use invented names only (`acme/webshop`, `sam`, `staging`):
  no real organizations, repositories, people, hosts or environments, including our own.
