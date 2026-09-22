package telegram

import (
	"strings"
	"unicode/utf16"
)

// textMax leaves headroom below Telegram's 4096-unit message limit. This
// plugin sends one message and truncates rather than splitting, so this is
// the whole length policy.
const textMax = 4096 - 64

// topicNameMax is Telegram's limit on a forum topic name, in UTF-16 units.
const topicNameMax = 128

const defaultTopicName = "agent"

// fitUTF16 returns how many leading runes of rs fit into max UTF-16 code
// units — the unit Telegram measures text in, so an astral emoji costs two
// — always at least one so a caller can make progress.
func fitUTF16(rs []rune, max int) int {
	units := 0
	for i, r := range rs {
		u := utf16.RuneLen(r)
		if u < 0 {
			u = 1
		}
		if units+u > max && i > 0 {
			return i
		}
		units += u
	}
	return len(rs)
}

// truncate cuts text to max UTF-16 units and marks the cut with an
// ellipsis; reserving one unit for the marker keeps the result in budget.
func truncate(text string, max int) string {
	rs := []rune(text)
	n := fitUTF16(rs, max)
	if n == len(rs) {
		return text
	}
	return strings.TrimRight(string(rs[:n-1]), " \t\n") + "…"
}

// truncateName trims a topic name to Telegram's limit and substitutes a
// default for an empty one.
func truncateName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultTopicName
	}
	r := []rune(s)
	if n := fitUTF16(r, topicNameMax); n < len(r) {
		return string(r[:n])
	}
	return s
}
