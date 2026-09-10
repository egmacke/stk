package operations

import "testing"

func TestMergeStackHistoryKeepsLandedEntries(t *testing.T) {
	prior := []stackEntry{
		{Number: 1, Branch: "api"},
		{Number: 2, Branch: "service"},
		{Number: 3, Branch: "ui"},
	}
	live := []stackEntry{
		{Number: 3, Branch: "ui"},
		{Number: 4, Branch: "polish"},
	}
	state := func(n int) string {
		switch n {
		case 1:
			return "merged"
		case 2:
			return "closed"
		}
		return ""
	}
	got := mergeStackHistory(prior, live, state)
	want := []struct {
		number int
		landed string
	}{{1, "merged"}, {2, "closed"}, {3, ""}, {4, ""}}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Number != w.number || got[i].Landed != w.landed {
			t.Fatalf("entry %d = %+v, want #%d %q", i, got[i], w.number, w.landed)
		}
	}
}

func TestMergeStackHistoryDropsOpenStrays(t *testing.T) {
	prior := []stackEntry{{Number: 9, Branch: "elsewhere"}, {Number: 1, Branch: "api"}}
	live := []stackEntry{{Number: 1, Branch: "api"}}
	got := mergeStackHistory(prior, live, func(int) string { return "" })
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("an open pull request outside the stack was kept: %+v", got)
	}
}

func TestParseStackComment(t *testing.T) {
	body := renderStackComment([]stackEntry{
		{Number: 1, Branch: "api", Landed: "merged"},
		{Number: 2, Branch: "service", Current: true},
	}, 2)
	got := parseStackComment(body)
	if len(got) != 2 || got[0].Number != 1 || got[0].Branch != "api" || got[1].Number != 2 {
		t.Fatalf("round trip lost entries: %+v", got)
	}
}
