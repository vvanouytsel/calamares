# Using Calamares

Everything Calamares can do, key by key. For the short version, see the [README](../README.md).

## Starting

```bash
calamares                      # inside a git checkout: opens that repo, filtered to your branch
calamares --home               # always start on the repo list
calamares --repo owner/name    # open a repo directly
calamares --readonly           # disable every action that changes something
```

There are seven places, always visible at the top:

- **`1` Repos**: your repos with how the last commits on their default branch did. A repo has eight sections, switched with `tab`: **Actions** (workflows and their runs → a run → its log), **Pull requests**, **Issues** (left out when the repo has issues turned off), **Commits**, **Branches**, **Releases** (open one to see the workflow runs started for it and the files it changed, with their diffs), **Environments** (each with its protection rules and what was last deployed to it, by which run) and **Secrets** (the names of the secrets and variables its workflows can read: its own, the organization's and each environment's, and the ones defined twice; only admins may list them). The columns of the list are how the default branch is doing now (broken when the latest run of one of its workflows failed, running while the checks of its last commit run, passed otherwise), a tick per recent commit on it with how many passed, who owns the repo and the open Dependabot alerts. The owner is the first owner of the `*` rule in the repo's CODEOWNERS file (or the owner named in most rules, when there is no `*` rule) and how many others share it; the panel lists the rules. A red icon marks a repo you can push to that has no owners. The files are read once a day, 50 repos per GraphQL request, and `owner:@acme/billing` in the filter keeps the repos with that owner. The panel lists the latest run of each workflow on it. Empty sections of a repo are dim in its strip, and Issues says off when the repo has them turned off. The Health of a repo counts the finished runs that passed and says how many were cancelled, as those are not counted. The Health panel of a repo says how many runs only passed on a rerun, and which jobs failed before they did.
- **`2` Actions**: what runs and fails across all your repos with workflows, in four views: Running (where it opens), Failing, Waiting (runs that wait for a runner or for your approval) and Last 7 days. A run still going after more than twice its usual time (the median of the successful runs it is compared with) and at least 10 minutes more is likely a job that hangs and holds a runner until its timeout: its time is red, and the title counts them in a red `N running long` chip; `X` cancels one. A run stays in Running from the moment it starts until it finishes, also while a later job of it waits for a runner and GitHub calls it queued again; the row then says so. The counts come from the latest 10 runs of each repo, and the title says how far back they are complete. **Failing** lists the latest run of each workflow on the default branches that failed, each on a line of its own with its step and why it failed; `g` groups them by why: the failing step and its first line that reads like an error (or `? no error line in the log` when it logged none, told apart by step), with ids, hashes, paths and numbers left out, so one cause in eleven repos is one row with the runs under it; `enter` opens or closes a cause, `enter` on a run opens it, and `g` lists every run on a line of its own again. The panel of a cause says what those runs have in common (the step, the reusable workflow at its version, the runner image), and `R` reruns the failed jobs of them all. Failing turns red in the strip when something fails. On any run, `r` opens its repo at its workflow.
- **`3` Pull requests**: the open ones waiting for your review, then yours, then others you're involved in, with their checks and review state. Open one to read it in tabs, switched with `tab`: **Conversation** (the description, reviews and comments, rendered as markdown, with `W` to comment or reply to the comment under the cursor; folded parts such as Dependabot's release notes stay folded until `v` opens them), **Changes** (the changed files on the left, the diff of the selected one on the right, `W` to comment on a line), **Checks** (each leads to its workflow run) and **Runs** (every workflow run of its commits, newest commit first). From there you can also approve, request changes or merge. The panel names a check the base branch requires (by a ruleset or branch protection) that nothing reports, as when a job was renamed. A run's screen says how long it and each job waited for a runner, and which reusable workflows it called at which version; a run cancelled by a concurrency group names the group and the run that took its place.
- **`4` Watching**: the runs you watch with `w`, kept up to date every 10 seconds even when the terminal isn't focused. When one starts, passes or fails you get a card in the app (a raised slab that floats over the screen with a shadow, a rail and a tile in its status colour, the run and its status word, its repo, branch, who started it and why, what it is for, and then its jobs and usual time, how its time compares with the usual, or the failed step and its first error line set into a well; its bottom edge is lit for the time it has left on screen, a failure stays two minutes, `ctrl+x` dismisses the newest and `ctrl+l` opens a failure's log; older cards tuck behind the newest as a deck, two of them still showing their line, and the card in front counts the rest), and a desktop notification when one finishes while the terminal is out of focus; it opens the run in the browser when clicked (cancelled runs don't notify). `w` again stops watching. A run you rerun or start with `+` is watched by itself, and watches are still there the next time you start Calamares. `w` on a repo watches its new runs: pick every workflow (also ones added later) or some, and the default branch, every branch or branches named by patterns such as `release/*`. Its repo is read every 30 seconds, and each new run that matches shows up on Watching with a card that it started; of those runs only a failure sends a desktop notification, and a finished one leaves after two hours. Under **Activity** in the same picker you choose what else of the repo you hear about: new commits on the default branch, pull requests opened and merged, and releases published (a new watch ticks all three; a watch you already had keeps runs only until you change it). They are read with one GraphQL query per repo every 30 seconds, each change is a card (`ctrl+o` opens the commit, the pull request or the repo's releases), and a merge or a release also sends a desktop notification while the terminal is out of focus. What you did yourself is left out, and what happened while Calamares was closed is told at the next start. `w` on a pull request (in a list or on its screen) watches it: it is read every 30 seconds, and a card says when it is approved, gets a review or comments, someone else pushes to it, its checks pass or fail, it gets a conflict, becomes ready for review, or is merged or closed, with a desktop notification while the terminal is out of focus (what you did yourself is left out). The **Pull requests** view of Watching (`tab`) lists them with their state, checks and review and the latest change; its panel lists every change while you watched it. A merged or closed one stays a day. The **Repos** view of Watching lists the watched repos with what they watch, their activity and their last change, the panel listing the changes seen, with `w` to change what they watch or stop. `r` on a run opens its repo.
- **`5` Platform**: what your repos share and what keeps them running, in eight views: Overview (where it opens), Shared workflows, Shared actions, Warnings, Scheduled, Runners, Caches and Time. Each view has a title, a line under it saying what it lists, and a line saying how the list is shown (sorted, grouped); the panel at the right keeps its width from view to view. A view not read yet counts `…` in the strip until you open it. **Overview** reads all the other views and lists what needs you in two sections: **Failures** (a new version of a shared workflow that fails more often than the one before it, a scheduled workflow whose last scheduled run failed, a job no runner can take) with how long each has lasted, and **Maintenance** (versions of your shared workflows and actions that other repos have moved past, warnings such as deprecations, schedules GitHub disabled) with how many repos each concerns. Under them come the runner usage of the week and the views with nothing to report. The panel and the keys of an issue are those of its row in its own view, and `enter` opens it there. **Scheduled** lists every workflow with a `schedule:` trigger, when it runs in words (`Daily at 03:00`, `Mondays at 04:00`) and how its latest scheduled run went, the ones GitHub disabled for inactivity first (`E` enables one again). **Warnings** groups the warning annotations of the latest runs on the default branches, deprecations above all, with the actions they name. Warnings that differ only in their values (numbers, quoted values, lists) are one row, with the values shown as `…`. `enter` opens it to the runs that gave it, with the value of each, and `enter` on a run opens the run; `v` opens them all and `g` lists every run on a line of its own. **Runners** lists the jobs waiting for a runner by the labels they ask for, whether any runner has them, and the self-hosted runners with what each one runs: those of your organization (only its owners may list them), of the repos you're an admin of, and those the jobs we hold ran on. Runners that are gone, such as pods that ran one job, fold into one line of the last 24 hours: `space` opens it to which runner ran which job, `/` finds a runner or a repo, and `X` clears it. **Caches** says how full the Actions cache of each repo is, fullest first: Full from 95% of the 10 GB a repo may cache, Near limit from 80%, Healthy below that (above the limit GitHub evicts the caches used least recently, and builds lose them without failing). A row has what it uses, how many caches, how much of it is caches made for pull requests (only that pull request can use them) and its largest key; the panel splits the space by branch and pull request and lists the largest caches with when a run last used them. `X` deletes the caches made for pull requests, after asking; their next run makes them again. A cache near its limit is also listed under Maintenance on the Overview. Each repo's cache is read with two requests, at most every 30 minutes. **Time** says where the runner time of the last week went: a line per workflow, where a shared workflow sums up the jobs it ran for every repo that calls it, with its hours and a bar, how that compares with the week before (`▲ 31%`, or `▲ 6× runs` when it suddenly runs far more often), its runs and its median time per run. `space` shows its jobs (the legs of a matrix as one), `v` all of them, `g` lists the time by repo and `s` sorts it. The panel draws the hours per day of both weeks, where the time goes, why it grew, which repos run it most, how much went to runs that ran again, and what a minute off its biggest job would save; `enter` lists its runs. The finished runs of the last two weeks are read once a day (`ctrl+r` reads them again), and how long their jobs took with GraphQL, 50 runs to a request and once per run. **Shared workflows** lists the reusable workflows with a line per version in use, the name on the line of the first: how many repos are on it, how many of the runs that called it passed (`9 of 17`) and its status: `Fails more than v8` when it passes clearly less often than the version before it, `Slower than v8`, `Outdated` when other repos use a newer version, `Fails often` when fewer than 3 in 4 runs pass, `Too new to compare` with fewer than 5 finished runs, or `Healthy`. The panel of a version lists its facts (status, when it was first used, who uses it, how many runs passed on it and on the version before, how long a run takes), the failures only it has, the jobs that got slower, what changed between the two tags, the repos on it and its run history; `d` shows the commits and files. **Shared actions** does the same for actions, yours only until `u` shows everyone's. `is:behind` in the filter (or the `.` menu) keeps only the outdated versions, `g` groups them under the repo they live in and `s` sorts them (most used, most behind, failing first, last run, name). `/` keeps the tree: plain words match names, sources, versions and the repos using them, and `repo:`, `source:`, `version:` and `is:behind`, `is:failing` or `is:pinned` (a commit or a branch) narrow it further. `enter` lists the workflows that use it: repo, file, version and how its last run went; `enter` there opens that workflow's runs. `h` lists the runs that called the version under the cursor, `r` opens the repo it lives in and `c` shows the workflow file. Who uses what comes from the workflow files on each repo's default branch, read with GraphQL, 25 repos for one point of the GraphQL budget, at most once a day (`ctrl+r` on these views reads them again); a shared workflow only ever called from inside another one is listed under it. Until the files are read, shared workflows come from the last 10 runs of each repo. In Repos, the panel names the shared workflows a repo uses and the `.` menu lists them.
- **`6` Issues**: the open issues that concern you: assigned, created and mentioned. Open one to read and answer it.
- **`7` Achievements**: medals, XP and a level for what you do with Calamares (see Achievements below).
- **A person's page**: `~` on anything with a person (the one who started a run, the author of a pull request, issue,
  commit or comment) or `@sam` in the `:` box opens what they did in the last 14 days, in the repos you can see. The
  title counts their pull requests awaiting your review, their failed runs and what they merged. **Activity** lists it
  all, newest first: the pull requests and issues they opened, merged or closed, their runs and how they went, the
  commits they pushed (a line per repo and day) and their reviews; the other views list one kind each, Commits a line
  per commit. GitHub searches commits only on default branches. `u` keeps only what involves you: their pull requests
  that ask for your review, and their reviews of yours. The panel shows the line under the cursor, the review
  requests between you (theirs for you, yours for them), what they did in each repository and their activity per day.
  It is read with one GraphQL query and one commit search when you open it, and again after 5 minutes; runs come
  from what Calamares already holds.

`Enter` opens, `Esc` goes back, and the bar at the bottom shows the keys that apply to what's
selected, and where `esc` takes you. The header names where you are: the place, then each screen you
opened and the view on display in each (Actions › Running, a repo's Commits, a pull request's Changes).
Inside a run, its pipeline, its logs and a workflow file, a line under the header names the repo, the
workflow with its file, the run and its branch, and the trail names the repo when you came from a screen that
doesn't; the terminal's title follows where you are. A screen of no place, such as help or the GitHub status, leaves the place you came from outlined; `?` lists every key of the screen you're on, and the ones that work everywhere.

**Actions keeps itself up to date; other screens refresh when you ask.** Actions reads each repo at the pace
of how much goes on in it: a **busy** repo (a run is going) every 15 seconds, one **worked on lately** (pinned,
watched, pushed in the last day, a schedule that just came due, or a run of yours this week) every 2 minutes, and
one **untouched for a day or more** every 20 minutes. The line under its title counts the repos in each group,
and a repo's panel in Repos says how often it is read and why. A push check reads when each of your repos was
last pushed to every 2 minutes, on any screen (one GraphQL point per 100 repos), and a repo pushed to since its
runs were read is read at once. A repo whose runs did not change costs nothing against the rate limit, as GitHub
answers it with a 304. When less than half of the hour's requests are left, the quieter repos wait twice as long
(three times below a quarter) and the dot on the line turns amber; busy repos never wait longer. Any other screen
checks GitHub when you open it and its data is more than 5 minutes old; `ctrl+r` refreshes the screen you're on,
and `L` turns on live updates, which keep the screen on display up to date while you're at the terminal and pause
when you leave. The header says how old the data is, and
how much of your API budget is left once it runs low.

Every key means one thing on every screen. A screen that has no use for a key leaves it unused; `?`
lists the keys of the screen you're on and `.` everything you can do with what is selected, also what has
no key of its own. **Lowercase keys are always safe:** they only look, go somewhere, copy, sort or
filter. **Capitals change something on GitHub** and always ask first; the help screen lists them apart,
under CHANGE GITHUB.

**Everywhere**

| Key | Action |
|---|---|
| `1`–`7` | Repos · Actions · Pull requests · Watching · Platform · Issues · Achievements (the digits show in the header) |
| `<` `>` | Previous / next place |
| `Enter` / `→` · `Esc` | Open · go back |
| `tab` / `]` · `shift+tab` / `[` | Next / previous view: the sections of a repo, the views of Actions and Platform, the tabs of a pull request or a commit, always in the strip on the third row (in a log: the next / previous job) |
| `↑` `↓` `j` `k` · `home` `end` · `pgup` `pgdn` | Move · top and bottom · a page |
| `.` | The action menu: everything you can do with what is selected, searchable |
| `:` or `ctrl+k` | Go to any repo (yours or on GitHub), pull request, workflow or command (`:pulls`, `:runs owner/repo`, `:filter …`, `:theme`, `:access`, `:q`); with nothing typed it lists the places |
| `/` · `x` · `F` | Filter the list (see Filtering) · clear the list's filters · the saved filter for runs |
| `?` · `i` · `!` | The keys of this screen · explain what each chip, column and panel part means · messages Calamares showed this session |
| `ctrl+r` · `L` | Refresh the screen · live updates on or off for screens other than Actions |
| `ctrl+o` · `ctrl+l` · `ctrl+x` | Open the run of the newest card · its log · dismiss it |
| `ctrl+g` | GitHub status: incidents and what they did to your runs (`:status` too) |
| `ctrl+s` | Freeze the screen while you select text with the mouse |
| `ctrl+t` | The next colour theme (`:theme` opens the picker) |
| `alt+↑` `alt+↓` | Scroll the detail panel |

**Look and go** · lowercase, always safe

| Key | Action |
|---|---|
| `o` | Open in the browser |
| `y` | Copy the link (on a commit its sha, on a branch its name, in a log the line) |
| `l` | Logs of the run, job or step under the cursor (in the pipeline too) |
| `c` | The workflow file: on a run the YAML of the job under the cursor, as it was when the run started (`v` there shows the whole file with the job marked) |
| `d` | Diff. On a failed run: what differs from the last run of the workflow that passed on its branch (its commits, the reusable workflows it called, a major tag that moved, the runner image); the label names that run, `diff vs #212 ✓`. On a version of a shared workflow: `diff v7 → v8`, the commits and files between it and the version before |
| `t` | How times show. On a run: compare each duration with the usual one (the median of the last 8 successful runs), with the previous run, or not at all; the label says which (`times vs usual`). A run on the default branch is compared with runs on it, one on another branch with the other branches. In a log: timestamps |
| `p` | Run: the pipeline, a graph of the jobs and what waits for what. The arrows and `j` `k` move, `enter` opens the logs, `=` `-` zoom (`0` fits it to the screen), `z` how matrices show (framed legs, every leg, one job), `Z` full screen |
| `f` | Run: follow the step that is running; on by default while the run is going, and any movement stops it |
| `e` | Next error (`shift+e` the one before, in a log) |
| `h` | Its runs: of a scheduled workflow, of a version of a shared workflow |
| `r` | Open its repo (of a run, a workflow, a shared workflow, an action) |
| `~` | The page of the person: who started the run, wrote the pull request, issue, commit or comment (see A person's page) |
| `#` | Its pull request: of a run, or the one an issue is linked to |
| `w` | Watch a run (a card and a desktop notification when it finishes), a pull request (when it is reviewed, pushed to, merged or its checks finish) or a repo (its new runs of the workflows and branches you pick, its new commits, pull requests and releases); again to stop or change |
| `a` | Set an issue or a review aside until someone writes again |
| `s` · `g` · `u` | Sort · group · only mine (runs, your actions) |
| `z` | Layout: runs as a table or as cards, each title on a line of its own, long lines wrapped in a log |
| `v` | Fold or show all: every cause, warning or step, a whole commit message, the folded details of a comment |
| `space` · `ctrl+a` | Fold one; in a list of runs or pull requests, tick the row · tick every row shown |
| `shift+↑↓` or `J` `K` | Tick every row from where you started to where you move |
| `n` · `N` | In a log: next / previous match · on Actions: show the new runs held back while you scroll |
| `b` | Runs and commits: pick a branch |
| `m` | Actions: which repos (all with workflows, the pinned ones, or those whose workflow files use your reusable workflows and actions) |
| `*` | Pin a repo |

**Change GitHub** · capitals, always ask first

| Key | Action |
|---|---|
| `A` | Approve a pull request (or every ticked one), or a deployment that waits for you |
| `C` | Request changes |
| `M` | Merge: pick how, or auto-merge once the checks pass (or turn auto-merge off) |
| `B` | Bring the branch of a pull request up to date with its base |
| `D` | Pull request: draft ⇄ ready for review |
| `W` | Write: a comment, a reply to the comment under the cursor, a comment on the selected line of a diff; line comments wait and go out with the review |
| `V` | Resolve or unresolve the thread under the cursor |
| `T` | Labels (a picker that says what will change) |
| `@` | Assignees of an issue, reviewers of a pull request |
| `R` | Rerun: the failed jobs, this job, one job or all, with debug logging if you like; with runs ticked, all of them |
| `X` | Stop or remove, and the label says what: cancel a run (or every ticked one), close an issue or pull request, delete a branch or a draft release, clear pull request caches |
| `+` | New: issue, pull request (from a branch, or the branch you are on when started in a checkout; `:pr new` too), release, a run of a workflow (with a form built from its `workflow_dispatch` inputs) |
| `P` | Publish a draft release |
| `E` | Enable or disable a workflow |

Only in the `.` menu: download artifacts or release assets, the previous / next attempt of a rerun, the run that
took the place of a cancelled one, the runner of each job in the pipeline, a repo's Dependabot alerts and the shared
workflows it uses, all repos instead of only those with Actions, outdated versions only (or filter with
`is:behind`), a status for the runs (or filter with `status:failed`), and saving a log to your Downloads folder.

## Highlights

- **Light on your API quota** — only the push check runs in the background (a GraphQL point per 100 repos every 2 minutes), and Actions reads each repo at the pace of how much goes on in it. Your repos, whether they use Actions and how their default branch is doing come from one GraphQL sweep (25 repos per request, about 1 point each); pull requests and issues from GraphQL searches, 30 to a page and up to 100 of each kind (review requested, yours, involved; assigned, created, mentioned), and a count with a `+` says GitHub holds more. Everything is revalidated with ETags or dates, identical requests are shared, background reads are spaced out, and when GitHub asks to slow down, or less than a fifth of your hourly limit is left, Calamares pauses reading and says so; actions keep working. When GitHub doesn't answer in time, as when the connection died while the laptop slept or the network changed, that is one message counting the requests that timed out, and a connection that goes quiet is checked within seconds and replaced when it is dead.
- **Instant screens** — every list is kept between sessions, so a screen shows at once and checks GitHub only if its data is old.
- **One screen per repo** — its workflows and health at the left (success rate, a block per recent run, typical duration, what waits on you), the runs in the middle, and on a wide terminal a preview of the run under the cursor at the right; `tab` shows the repo's pull requests and commits.
- **Why it failed** — opening a failed run puts the cursor on the failing step and shows the failing test, assertion, annotations and which matrix legs passed.
- **Pull requests with their CI** — a PR's checks lead straight to the workflow run and its log; rerun the failed jobs from there.
- **Matrix grouping** — `test (ubuntu)`, `test (windows)`… are grouped so "only Windows fails" is obvious.
- **Log viewer** — steps as foldable sections, error highlighting, search, timestamps toggle, save to file.

## Filtering

Every list shows a filter line: what it's filtered by, how many rows show, and the keys that change it.
`/` filters the list as you type, using the same words as the columns. Every term must match:

| Term | Matches |
|---|---|
| `deploy` | any column containing "deploy" |
| `branch:main` | the branch contains "main" (the start of a column name is enough: `br:main`) |
| `branch:main,release/*` | either value |
| `-branch:dependabot/*` | everything except these |
| `status:failed` | failed runs; `status:passed` for the ones that passed (`failure`, `success`, `in_progress` work too) |
| `workflow:"Build and release"` | quotes keep spaces |
| `actor:@me` | your own runs |

A value with `*` must match the whole text (`release/*`); without it, any part matches.
A field the list doesn't have is pointed out while you type.

Under the box, suggestions complete the word you are typing: the column names while it starts one
(`ow` offers `owner:`), and after the colon the values that exist in the list, most common first,
with how many rows have each. `tab` completes the chosen one and cycles on repeat, `←` `→` choose,
and `tab` on an empty word lists the columns. Values with spaces are quoted for you. Values come
from the rows the screen holds; the Shared views also complete `repo:`, `source:`, `version:` and
`is:`.

The **saved filter** hides runs on every screen, also next time. Press `F` to edit it: it shows how
many recent runs it would hide, and `tab` switches it off for a while. It uses the same syntax, with
the fields `repo`, `workflow`, `branch`, `actor`, `event`, `status` and `title`, for example
`-branch:dependabot/*,renovate/* -actor:*[bot]`. `:filter …` sets it from the Go to box.

## Done for now

An issue that asked you something, or a pull request that waits for your review, counts in the chips
at the top of its screen until you act on it. `a` sets one aside: it stops counting until something new
happens on it (a new comment on an issue, any change to a pull request). `a` again brings it back.

## GitHub status

Calamares reads GitHub's public status page every two minutes; it needs no token and costs nothing
of your API budget (a GitHub Enterprise Server has none, so there it stays off). Only the parts it
shows or runs depend on count: Actions, Git operations, API, Webhooks, Packages, pull requests and
issues. While one of them has an incident, a pill in the header says what has trouble and for how
long (amber when degraded, red for an outage), the key bar offers `ctrl+g`, and the Failing view
marks the causes that started during it with a chip on top and a callout. When it is resolved, the
key bar says so and how many runs failed during it.

`ctrl+g` (or `:status`) opens the GitHub status screen: how each part is doing, then the open
incidents, the maintenance coming up and the incidents of the last 7 days. Each section is always
there, and says so when it has nothing, such as no maintenance planned. The panel shows the
timeline of GitHub's updates and what the incident did to your runs: the runs that failed while it
went on, and the ones that waited over 10 minutes for a runner. `enter` lists the failed runs, `R`
reruns them and `o` opens the incident on githubstatus.com.

## Achievements

Place `7` is a trophy case of 52 medals for what you do with Calamares, in five areas (Actions, Pull
requests, Watching and habits, Platform, Explorer) and a row of secrets that show only a hint
until you find them. What is counted, such as reruns that passed or pull requests merged, has up to
four tiers: bronze, silver, gold and platinum. The rest is earned once. Each tier is worth XP, and XP
adds up to a level with a title, shown in the header.

The arrows move through the medals, `tab` switches to the medals you are close to (Almost there) and
to the Timeline of what you unlocked, and `enter` goes to the place where the one under the cursor is
earned. An unlock shows a card at the bottom right with the medal and a burst of confetti, one at a time, in the same deck as the other cards; `ctrl+x` dismisses it.

Counting starts the day you first run a version with achievements; nothing is worked out from the
past. Only counters are kept, in `achievements.json` next to the config. `achievements: off` hides the
place, the level and the cards.

## Themes and motion

The look is depth, light and drawing instead of lines. The header is a band lit by the accent at its left,
with the name in a gradient from the accent to violet, and the place on display is a tab of the page that
rises into it. A pane is a layer, not a box: its title with a hairline that fades out beside it, in the
accent while it has the focus; the sections of a panel are the same, a callout is a bar in its colour on a
tint of it, each cause on Failing and each open job of a run sits on such a tint (with a dashed hairline when
the step logged no error line), and a folded job is a pill on one line. The views of a screen are one
capsule with the one on display filled, keys are round caps, and the row under the cursor is lit from the
accent bar at its left and fades out along the row. A running run's time has a meter against its usual time;
the panel of a run has a tile in its colour, a gauge with a white tick at the usual time and its history as
bars as high as each run took. Under a short list of running runs, Running shows the runs that just finished
and the runs of the last 24 hours, hour by hour. What floats casts a shadow: a dialog sinks the screen into
the dark and casts one, and so does a card. Without a Nerd Font the views and the place keep their brackets.

`:theme` opens a picker that previews each theme as you move (`enter` keeps and saves it, `esc` goes
back), `:theme neon` sets one directly and `ctrl+t` moves to the next. There are eight: **cockpit**,
**abyss** (the deep sea: bioluminescent cyan on squid ink), **midnight**, **ember**, **aurora**, **neon**,
**slate** (a softer dark) and **paper** (light). Icons carry the colour of what they stand for: gold repos,
lime branches and commits, teal people, pink comments.

Motion says something: what is running breathes, a loading list shimmers, dialogs ease open, a count
that goes up flares, and a row whose run just passed or failed glows once. Moving around is always
instant: the place or view you switch to is there at once and only lights up, with a sheen sweeping
across it, and the cursor bar flashes as it lands on a row. Those glows play at 120 frames a second.

In the pipeline (`p` on a run), light runs along the lines into every job that is running, the box of a
running job breathes, the chain of the job you select lights up in a wave from it, and a job that has
just passed or failed flashes in its colour. `animations: full` is the default; `subtle`
keeps the spinners and the pulse of what is running, `off` only the spinners.

## Configuration

`config.yaml` lives in your OS config folder (`%AppData%\calamares`, `~/Library/Application Support/calamares`, `~/.config/calamares`):

```yaml
host: github.com          # or your GitHub Enterprise Server host
favourites: [owner/repo]
refreshSeconds: 3         # with live updates on: fastest refresh of running work (backs off while nothing changes)
icons: nerd               # nerd: icons of a Nerd Font (the default) · plain: ordinary symbols · ascii: plain characters only (ascii: true and --ascii still work)
theme: cockpit            # cockpit · abyss · midnight · ember · aurora · neon · slate · paper (:theme picks and saves it; --theme for one run)
animations: full          # full · subtle (spinners and the pulse of what runs) · off (--animations, --no-animations)
runFilter: "-branch:dependabot/*"  # hide runs on every screen (see Filtering)
achievements: off         # hide the Achievements place, the level in the header and the unlock cards; leave it out to show them
compareDurations: median  # compare jobs with the median of their last 8 successful runs (median) or with the previous successful run (previous); leave it out for off; t in a run cycles them
```

## Development

```bash
go test ./...
go run ./cmd/calamares

# Render real screens (uses your GitHub login) to HTML for review:
CALAMARES_SNAPSHOT=1 CALAMARES_SNAPSHOT_REPO=owner/repo CALAMARES_SNAPSHOT_DIR=$PWD/bin/snap go test ./internal/ui -run TestSnapshots
```

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/). When CI
passes on `main`, semantic-release tags the next version (`fix:` → patch, `feat:` → minor,
`feat!:` or `BREAKING CHANGE:` → major) and GoReleaser publishes the binaries.
