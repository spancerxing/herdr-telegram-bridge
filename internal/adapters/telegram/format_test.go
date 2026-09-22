package telegram

import "testing"

func TestTruncateCountsUTF16(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"fits", "abc", 3, "abc"},
		{"cut", "abcdef", 3, "ab…"},
		{"astral costs two", "a😀b", 3, "a…"}, // 😀 is two units, so b cannot fit
		{"astral fits", "😀", 2, "😀"},
		{"cut at line end", "line1\nline2", 6, "line1…"},
		{"empty", "", 3, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.max); got != c.want {
			t.Errorf("%s: truncate(%q, %d) = %q, want %q", c.name, c.in, c.max, got, c.want)
		}
	}
}

func TestTruncateName(t *testing.T) {
	if got := truncateName("  "); got != defaultTopicName {
		t.Errorf("empty name = %q, want %q", got, defaultTopicName)
	}
	long := make([]byte, 0, 300)
	for i := 0; i < 300; i++ {
		long = append(long, 'x')
	}
	got := truncateName(string(long))
	if n := fitUTF16([]rune(got), topicNameMax); n != len(got) || len(got) != topicNameMax {
		t.Errorf("long name = %d units, want exactly %d", len(got), topicNameMax)
	}
}
