package ui

import "fmt"

// All user-facing text of the chat UI lives in this file, so a translation
// is a change to one file (see ROADMAP.md, "translations").

const (
	strTagline         = "a tiny terminal chat · /help for help"
	strOnline          = "online"
	strConnecting      = "connecting…"
	strConnected       = "connected"
	strDisconnected    = "disconnected"
	strScrollHint      = "PgUp/PgDn scroll"
	strScrolledUp      = "scrolled up · PgDn"
	strNewMessages     = "↓ new messages"
	strNotConnected    = "not connected, message not sent"
	strSendFailed      = "message not sent"
	strNobodyToReply   = "nobody to reply to yet"
	strEmptyRoom       = "you are the only one here"
	strServerRestart   = "the server is restarting"
	strReplaced        = "you logged in from another window; this one is now offline. Ctrl-C quits."
	strAuthFailed1     = "login refused: the server does not know this name and key."
	strAuthFailed2     = "run `pling pubkey` and send the line to whoever runs the server."
	strBadVersion      = "the server speaks a different protocol version; update pling."
	strNotFound        = "there is no pling server at that address (HTTP 404). Check `server` in your config."
	strQuitHint        = "Ctrl-C quits."
	strRateLimited     = "slow down: too many messages (max 20 per 10 seconds)"
	strTooLarge        = "message too long (max 4 KB)"
	strBadFrame        = "the server did not understand that"
	strUnknownTarget   = "that user is not online"
	strMsgUsage        = "usage: /msg <name> <text>"
	strReplyUsage      = "usage: /r <text>"
	strMeUsage         = "usage: /me <action>"
	strShrug           = `¯\_(ツ)_/¯`
	strSelectHint      = "tip: hold Shift to select text with the mouse"
	strHistoryLoadFail = "could not read local history"
	strHistorySaveFail = "could not save to local history"
)

func strWelcome(host, name string) string {
	return fmt.Sprintf("connected to %s as %s", host, name)
}

func strDisconnectedRetry(reason string, seconds int) string {
	return fmt.Sprintf("disconnected (%s), retrying in %d s", reason, seconds)
}

func strConnectingAttempt(host string, attempt int) string {
	if attempt <= 1 {
		return fmt.Sprintf("connecting to %s…", host)
	}
	return fmt.Sprintf("connecting to %s… (attempt %d)", host, attempt)
}

func strJoined(name string) string { return name + " joined" }
func strLeft(name string) string   { return name + " left" }

func strTyping(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0] + " is typing…"
	case 2:
		return names[0] + " and " + names[1] + " are typing…"
	default:
		return fmt.Sprintf("%d people are typing…", len(names))
	}
}

func strOnlineCount(n int) string { return fmt.Sprintf("%d online", n) }

func strWho(names []string) string {
	return fmt.Sprintf("online (%d): %s", len(names), joinNames(names))
}

func strNotOnline(name string) string { return name + " is not online" }

func strUnknownCommand(cmd string) string {
	return fmt.Sprintf("unknown command %s, try /help", cmd)
}

func strRolls(spec string, rolls []int, total int) string {
	if len(rolls) == 1 {
		return fmt.Sprintf("rolls %s: %d", spec, total)
	}
	s := ""
	for i, r := range rolls {
		if i > 0 {
			s += " + "
		}
		s += fmt.Sprint(r)
	}
	return fmt.Sprintf("rolls %s: %s = %d", spec, s, total)
}

func strRollUsage(err error) string {
	return fmt.Sprintf("usage: /roll [NdM], e.g. /roll 2d6 (%v)", err)
}

func strNotifyTitle(from string, private bool) string {
	if private {
		return "pling: " + from + " (private)"
	}
	return "pling: " + from
}

// helpLines is the output of /help.
var helpLines = []string{
	"commands:",
	"  /msg <name> <text>   private message",
	"  /r <text>            reply to the last private message",
	"  /me <action>         action, e.g. /me waves",
	"  /roll [NdM]          roll dice, e.g. /roll 2d6 (default 1d6)",
	"  /shrug [text]        adds " + strShrug,
	"  /who                 who is online",
	"  /clear               clear the screen (history is kept)",
	"  /help                this list",
	"  /quit                quit (or Ctrl-C)",
	"keys: Tab completes names · PgUp/PgDn or mouse wheel scroll · Enter sends",
	"start a line with // to send a literal /",
}

func joinNames(names []string) string {
	s := ""
	for i, n := range names {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}
