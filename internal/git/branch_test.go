package git

import "testing"

func TestParseTrack(t *testing.T) {
	cases := []struct {
		in     string
		ahead  int
		behind int
		gone   bool
	}{
		{"", 0, 0, false},
		{"gone", 0, 0, true},
		{"ahead 3", 3, 0, false},
		{"behind 2", 0, 2, false},
		{"ahead 3, behind 2", 3, 2, false},
		{"  ahead 1 , behind 10 ", 1, 10, false},
		{"nonsense", 0, 0, false},
	}
	for _, c := range cases {
		var b BranchInfo
		parseTrack(c.in, &b)
		if b.Ahead != c.ahead || b.Behind != c.behind || b.UpstreamGone != c.gone {
			t.Errorf("parseTrack(%q) = ahead %d behind %d gone %v; want %d/%d/%v",
				c.in, b.Ahead, b.Behind, b.UpstreamGone, c.ahead, c.behind, c.gone)
		}
	}
}
