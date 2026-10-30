package ui

import (
	"context"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/cli/browser"
	"github.com/gen2brain/beeep"
)

func init() {
	// The browser package echoes helper output to stdout, which would corrupt the TUI.
	browser.Stdout = io.Discard
	browser.Stderr = io.Discard
	beeep.AppName = "Calamares"
}

func openBrowser(url string) error { return browser.OpenURL(url) }

func writeClipboard(s string) error { return clipboard.WriteAll(s) }

func notify(title, msg string) error { return beeep.Notify(title, msg, "") }

// notificationLifetime bounds how long a notification waits to be clicked.
const notificationLifetime = 10 * time.Minute

// notifyWithLink shows a desktop notification that opens url in the browser
// when clicked. Where notify-send can't do that, the url is part of the text.
func notifyWithLink(title, msg, url string) error {
	if runtime.GOOS == "linux" {
		if path, err := exec.LookPath("notify-send"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), notificationLifetime)
			defer cancel()
			out, err := exec.CommandContext(ctx, path, "--app-name=Calamares", "--action=default=Open in browser", "--action=open=Open in browser", title, msg).Output()
			if err == nil {
				if strings.TrimSpace(string(out)) != "" {
					return openBrowser(url)
				}
				return nil
			}
		}
	}
	return notify(title, msg+"\n"+url)
}
