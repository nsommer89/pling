package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/nsommer89/pling/internal/client"
)

// notifier turns incoming messages into a terminal bell and/or a desktop
// notification via notify-send, depending on the `notify` setting.
type notifier struct {
	mode       string
	notifySend string // path to notify-send, empty if not installed
}

func newNotifier(mode string) notifier {
	n := notifier{mode: mode}
	if mode == client.NotifyDesktop || mode == client.NotifyBoth {
		n.notifySend, _ = exec.LookPath("notify-send")
	}
	return n
}

func (n notifier) bell() bool {
	// Without notify-send, "desktop" falls back to the bell so nothing is
	// silently lost.
	return n.mode == client.NotifyBell || n.mode == client.NotifyBoth ||
		(n.mode == client.NotifyDesktop && n.notifySend == "")
}

// notify returns the commands for one notification.
func (n notifier) notify(title, body string) tea.Cmd {
	var cmds []tea.Cmd
	if n.bell() {
		cmds = append(cmds, tea.Raw("\a"))
	}
	if n.notifySend != "" && (n.mode == client.NotifyDesktop || n.mode == client.NotifyBoth) {
		path := n.notifySend
		body = escapeMarkup(xansi.Truncate(body, 200, "…"))
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = exec.CommandContext(ctx, path, "--app-name=pling", "--", title, body).Run()
			return nil
		})
	}
	return tea.Batch(cmds...)
}

// escapeMarkup escapes the little HTML subset notification daemons
// interpret in the body.
func escapeMarkup(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
