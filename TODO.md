# TODO

Tasks are picked up from the top. When one is finished it moves to "Done" at the bottom.

## To do

- If screenshots are taken, they should be taken with the animations enabled and a nerdfont installed. If you see screenshots are outdated, ask if you need to update them, don't update them without my permission.

## Done

- Every key means one thing on every screen (written down in `keyMap`, checked by a test): lowercase keys only look, go somewhere, copy, sort or filter; capitals change GitHub and ask first (`A` approve, `X` cancel/close/delete, `W` comment, `T` labels, `+` new). Labels say what you get ("diff vs #212 ✓", "times vs usual"), follow is on while a run goes, rare actions live in the `.` menu, the help lists the capitals apart, and someone who knew the old keys is told once what moved.

- Actions covers every repo with workflows, each read at the pace of how much goes on in it (busy every 15 s, worked on lately every 2 min, untouched every 20 min), woken by a push check every 2 minutes and by schedules that come due, and slower when the requests left run low; it keeps itself up to date whatever the live setting. Your pull requests and issues read up to 100 of each kind, with a `+` when there are more.

- The new look: depth, light and drawing instead of lines. The header band is lit by the accent, the name is a gradient from the accent to violet and the place on display is a tab that rises out of the page; places keep their names wherever they fit. Panes and panel sections are layers with a fading hairline instead of boxes, callouts a bar on a tint, the views of a screen one capsule, keys round caps, and the row under the cursor is lit from its accent bar and fades out along the row. Running runs have a meter against their usual time; the run panel has a status tile, a duration gauge with a tick at the median and its history as bars as high as each run took. Under a short list Running shows the runs that just finished and the day's runs per hour. A dialog sinks the screen into the dark and casts a shadow. A new theme, abyss.

- Cards float: a card is a raised slab without an outline, rounded by half blocks, with a rail and a tile in its status colour, its status word once in the title, a failure set into a well, round key caps, a shadow that darkens what it falls on and a bottom edge lit for the time it has left. It comes in lit white and settles in its colour. Older cards tuck behind the newest as a deck of narrower, dimmer layers that still say their line, the card in front counts the rest, and an unlock is a card in the same deck with its medal and confetti.

- Actions flags a run that hangs: one still going after more than twice its usual time and 10 minutes more has a red time, and Running counts them in a red `N running long` chip.

- A Caches view on Platform: how full each repo's Actions cache is (Full, Near limit, Healthy against the 10 GB limit), how much only pull requests can use, the space per branch and the largest caches, and `X` to clear the caches made for pull requests. A cache near its limit is maintenance on the Overview.

- A watched repo tells about more than its runs: under Activity in the `W` picker you pick new commits on the default branch, pull requests opened and merged, and releases published. Each change is a card (a merge or a release also a desktop notification), Watching › Repos has ACTIVITY and LAST CHANGE columns and a panel with the changes seen, and what happened while Calamares was closed is told at the next start. Run, pull request and repo watches each have their own view on Watching.

- Pull requests can be watched: `w` on one reads it every 30 seconds and a card (and a desktop notification out of focus) says when it is approved, reviewed, commented on, pushed to by someone else, its checks pass or fail, it gets a conflict, is ready for review, merged or closed. Watching has a Pull requests view between Runs and Repos, with the changes seen in its panel. A watched run or pull request GitHub no longer has stops being watched after three 404s.
- Repos sorts with `s`: pinned then last push (the default), last push, failing first or name; the clocks in PUSHED line up and the list follows GitHub's push times.
- GitHub's 502, 503 and 504 answers and broken connections are tried again (twice, a second and three seconds later) for reads and GraphQL queries, never for actions; a background refresh that still fails keeps what it had, and the failures are one message that counts them.

- Platform opens on an Overview: what fails (a new version that fails more, a failed schedule, a job no runner can take) and what needs maintenance (outdated versions, warnings, disabled schedules), the runner usage and what is healthy. Its views have a title, a subtitle and a line saying how the list is shown, and their panels titles and labelled fields at a steady width. Shared workflows and actions are a line per version with the runs that passed and a status word, without bars, pills, tree lines or points. Schedules are said in words. Panel sections across the app have proper titles ("Run history", "Cause of increase") instead of phrases in capitals.

- The Inbox place is gone, with the reading of GitHub notifications, the unread count in the header and the notifications scope check.
- Achievements: place `7` is a trophy case of 52 pixel-art medals in five areas and a row of secrets, with tiers (bronze to platinum), XP, a level with a title in the header, an Almost there view, a Timeline and a panel per medal. An unlock shows a card with confetti, one at a time. Progress is counters in `achievements.json` next to the config; `achievements: off` hides it all.

- Alert cards in the Cockpit look: a rounded card in the colour of how the run is doing, with its repo in the border, what it is for, the branch, who started it and why, and then its jobs and usual time, how its time compares with the usual, or the failed step with its first error line, wrapped instead of cut. It glows as it arrives and its bottom edge runs out with its time on screen; a failure stays two minutes, `ctrl+x` dismisses and `ctrl+l` opens a failure's log, and older cards fold to a line each. Failing lists every run on a line of its own first; `g` groups them.

- Actions is place `2` and Pull requests `3`, and Actions opens on Running, with Failing next to it in red when something fails. A run stays in Running from the moment it starts, also while a later job waits for a runner and GitHub calls it queued again; the row and the panel say so. `s` opens the repo of a run on Watching and in its log too. `W` on a repo watches its new runs: every workflow or some, on the default branch, every branch or branches by pattern. Their runs show up on Watching by themselves (only a failure sends a notification), and a Repos view on Watching lists the watched repos. A repo with issues turned off has no Issues section.

- Durations are compared with runs on the same kind of branch: a run on the default branch with earlier runs on it, a run on another branch with runs on the other branches. The run's comparison, the bars in the panel and the time left on Watching all follow it, and the run says which branches it is compared with.

- It is always clear which repo and workflow you are in: a run, its pipeline, its logs and a workflow file have a line under the header with the repo, the workflow and its file, the run and its branch; the trail names the repo when you arrive from elsewhere, and the terminal's title follows where you are.

- A connection to GitHub that died (after sleep or a change of network) is noticed within seconds and replaced, and the requests that got no answer are one message that counts them instead of one error per repo.

- Rounded cards, as in the pipeline: the sections of every panel, each cause on Failing and each open job of a run are cards in the colour of their status (dashed for what did not happen), callouts are filled cards, folded jobs and the view or place on display are pills.

- The header says where you are: the trail names the view on display in each screen (Actions › Running, a repo's Commits, a pull request's Changes), lights for a moment when it changes and keeps both ends when it is long. The title of Actions and Platform names the view, `esc` says where it takes you, and help, messages and the GitHub status light no place. The places are now Repos, Pull requests, Actions, Watching, Platform, Inbox and Issues, and the repo screen keeps its rounded frames.

- Clearer screens: `i` on a screen explains each of its chips, columns and panel parts. Actions keeps what fails and runs (Failing, Running, Waiting, Last 7 days); a new Platform place (`5`, before Inbox and Issues) holds the shared workflows and actions, warnings, schedules, runners and runner time, and a view not read yet counts `…`. Repos says one thing about the default branch (broken, running or passed) with the latest run of each workflow in the panel. Failing no longer takes log noise for a cause, the repo screen is drawn in layers and dims its empty sections, Health counts cancelled runs aloud, the GitHub status screen always lists open incidents and planned maintenance, Dependabot's folded notes stay folded (`e` opens them), the API budget shows only once it runs low, and the panel beside a list takes the room the list leaves.

- GitHub status: GitHub's status page is read every two minutes; an incident about a part Calamares shows puts a pill in the header, marks the failing causes that started during it and is announced when it starts and ends. `ctrl+g` or `:status` opens a screen with the incidents, the maintenance coming up and what each incident did to your runs, with `R` to rerun them.

- A Time view on the Actions tab: the runner time of the last 7 days per workflow (a shared workflow sums up every caller), its jobs, the growth against the week before, why it grew, who runs it most and what a minute off its biggest job saves; `g` by repo.

- Each version of a shared workflow is compared with the version before it: a pill on a new version in the colour of how it does, a VS PREVIOUS column, and a panel with the failures only the new version has, the jobs that got slower and what changed between the tags (`=`).

- A run says how long it took and, while durations are compared, how that stands against the median or the previous successful run: a pill next to its time, a meter while it runs, and the whole run as a row under the jobs. A job's details and the pipeline footer show the same pill. The panel beside the runs list draws the run, the median and the previous run as bars on one scale.

- Warnings group like Failing and open to the runs that gave them; g lists every run on a line of its own on both. s on a run opens its repo at its workflow. Runners show what they run, with the runners that are gone (pods) folded into 24 hours of history. Generic runner and container-hook errors no longer make a cause. Warnings are read through GraphQL and scheduled runs once per repo, so far fewer requests. Typical durations also compare with the previous successful run. Mouse support is gone, so the terminal can always select text.

- A Failing view on the Actions tab (the tab opens on it when something fails): the latest failed run of each workflow on the default branches, grouped by cause, with what the runs have in common and R to rerun them all; Scheduled (and the ones GitHub switched off), Warnings (deprecations grouped by message, with the actions they name) and Runners (jobs waiting per label, and whether any runner has them) views; a scope for the repos that call your workflows and actions. Runs can be ticked and rerun or cancelled together. A run shows how long it and each job queued and the reusable workflows it called, = shows what changed since it last passed (commits, moved tags, runner image), and a run cancelled by a concurrency group names it and the run that replaced it. Pull requests name a required check nothing reports. Repos get Environments and Secrets sections, FAILING and ALERTS columns, and flaky jobs in Health. The Actions numbers say what they cover. New runs show at once in the short views and ctrl+r shows the held ones; "only mine" includes the runs you ran again.

- Run history in panels as spaced results with an old-to-new axis, the share that passed and the last failure; a gap every five in the list column. Moving through long lists no longer rebuilds them on every key, and a held key skips the cursor glow. J and K tick ranges too, and pull requests can be ticked and approved, sent back or merged together. Enter on a shared workflow or action lists the workflows that use it; r lists its runs.

- Tick a range in the Inbox with shift+↑↓, shift+home/end or shift+click; ticked rows get a checkbox and a band, and a callout says what to do with them. Shared views: one-version items on one line, a filter that keeps the tree (repo:, source:, version:, is:), behind only (b), group by source (g), sort (S), callouts for what is behind.

- Shared views redesigned: a row per workflow or action with an adoption bar ("78% on v9") and its health, versions on tree guides with their own bar and a behind pill; branches, commits and versions in lime instead of purple, which stays for merged and done.

- Shared workflows and actions by version: a line per version under each workflow, with who is on it, who is behind and how it is doing; a Shared actions view; who uses what read from the workflow files with GraphQL, once a day; helpers called only inside a shared workflow listed under it.

- Shared workflows: a Shared view on the Actions tab lists the reusable workflows your runs call, the repos and versions that call them (and who is behind) and how their calling runs went; the Repos panel names the ones a repo calls, `w` lists them.

- More colour and clearer views: icons coloured by what they stand for, label chips, the active view in brackets, a richer pull request screen and issue panel, next steps in callouts, `d` to set an issue or a review aside, a heartbeat icon for Watching, full animations by default, and a short README with a new logo (the full guide moved to `docs/usage.md`).
- Rerun a single job: `R` opens a picker (failed jobs, this job, one job, all; debug logging as a switch), and an action menu on `.` lists every action of a screen.
- Notifications with icons: chips (done, hint, info, error) in the key bar and on the Messages screen; cards when a run you watch starts, passes or fails (with the failed step); a run started with `d` is watched by itself; desktop notification only while the terminal is out of focus.
- Style the other screens in the Cockpit look, make the subtitles clear, and get the UI approved.
- The list-and-panel pattern on Pull requests (next step, checks, CI on the branch), Actions (history, usual duration, jobs), Watching (progress, time left), Inbox, Commits and the pull request screen; a ROLE column instead of WHY.
- New header (logo, name and status on a band, places with icons and counts below), status chips, icon system, and the Repos tab as a list with a detail panel and a strip of what is failing, running and waiting. The other screens still use boxes with the new colours and chips.
- Runs are a table of one line each by default, cards (with a rule between them) on `z`; waiting is orange, blue only means focus, row data is light text instead of grey.
- One strip for the views of a screen (repo sections, Actions views, pull request and commit tabs) on the third header row; `tab` or `]` next, `shift+tab` or `[` previous. Files in a diff moved from `[ ]` to `{ }`.
- Declare each key once; help, key bar and read-only check generated from it.
- One shared loading state; lists up to date before a key is handled; redraw only when needed.
- Messages screen (`!`) with a count of unread errors in the top bar.
- A rerun is watched automatically; watches survive a restart.
- Go to a pull request opens the pull request.
- Cockpit look on the repository screen, the Actions tab and the Watching tab; Nerd Font icons.
- Commits on `main` rewritten to the personal account.
- Screenshots with explanation in the README ("A tour"): repository, run, Actions, pull requests.
