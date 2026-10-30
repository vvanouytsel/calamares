// Command calamares is a terminal command center for GitHub.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
	"github.com/vvanouytsel/calamares/internal/gitctx"
	"github.com/vvanouytsel/calamares/internal/ui"
)

// version is set by the release build (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "calamares:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		repo     = flag.String("repo", "", "open this repository (owner/name) directly")
		branch   = flag.String("branch", "", "filter runs to this branch (with --repo)")
		host     = flag.String("host", "", "GitHub host, e.g. github.example.com (default from config: github.com)")
		readonly = flag.Bool("readonly", false, "disable every action that changes something (rerun, cancel, dispatch…)")
		home     = flag.Bool("home", false, "start on the repo list even inside a git checkout")
		asciiF   = flag.Bool("ascii", false, "same as --icons ascii")
		iconsF   = flag.String("icons", "", "nerd (icons of a Nerd Font, the default), plain (ordinary symbols) or ascii (plain characters only)")
		refresh  = flag.Int("refresh", 0, "refresh interval in seconds for running work (default 3)")
		theme    = flag.String("theme", "", "colour theme for this run: cockpit, midnight, ember, aurora, neon, slate or paper (default from config; :theme picks and saves)")
		animsF   = flag.String("animations", "", "full (the default), subtle (only spinners and pulses) or off")
		noAnim   = flag.Bool("no-animations", false, "same as --animations off")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Calamares — a terminal command center for GitHub

Usage:
  calamares [flags]

Run it inside a git checkout to open that repository's runs for your
current branch, or anywhere else to start on the list of your repositories.

Flags:
`)
		flag.PrintDefaults()
		p, _ := config.Path()
		fmt.Fprintf(flag.CommandLine.Output(), "\nConfig file: %s\nAuth: GH_TOKEN / GITHUB_TOKEN, or your `gh auth login` session.\n", p)
	}
	flag.Parse()

	if *showVer {
		fmt.Println("calamares", resolveVersion())
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	if *host != "" {
		cfg.Host = *host
	}
	if *iconsF != "" {
		cfg.Icons, cfg.ASCII = *iconsF, false
	}
	if *asciiF {
		cfg.Icons = "ascii"
	}
	cfg.NormalizeIcons()
	if *refresh > 0 {
		cfg.RefreshSeconds = max(*refresh, 2)
	}

	client, err := gh.New(cfg.Host)
	if errors.Is(err, gh.ErrNoToken) {
		return fmt.Errorf("%w\n\n  Log in once with:  gh auth login\n  or export a token: GH_TOKEN=<token with repo + workflow scopes>", err)
	}
	if err != nil {
		return err
	}

	opts := ui.Options{Version: resolveVersion(), ReadOnly: *readonly, Theme: *theme, Animations: *animsF}
	if *noAnim {
		opts.Animations = "off"
	}
	switch {
	case *repo != "":
		if strings.Count(*repo, "/") != 1 {
			return fmt.Errorf("--repo must look like owner/name, got %q", *repo)
		}
		opts.StartRepo, opts.Branch = *repo, *branch
	case !*home:
		if ctx, ok := gitctx.Detect(cfg.Host); ok {
			opts.StartRepo, opts.Branch, opts.FromCwd = ctx.Repo, ctx.Branch, true
		}
	}

	// The response cache is kept between sessions so screens appear instantly.
	cachePath, cacheErr := gh.CachePath(cfg.Host)
	if cacheErr == nil {
		_ = client.LoadCache(cachePath)
		opts.SaveCache = func() { _ = client.SaveCache(cachePath) }
	}

	// 120 frames a second (Bubble Tea's ceiling) keeps the glows smooth on fast
	// screens; the renderer only writes a frame that changed, so idle costs nothing.
	progOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithReportFocus(), tea.WithFPS(120)}
	p := tea.NewProgram(ui.NewScreen(ui.New(client, cfg, opts)), progOpts...)
	_, err = p.Run()
	if cacheErr == nil {
		_ = client.SaveCache(cachePath)
	}
	return err
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
