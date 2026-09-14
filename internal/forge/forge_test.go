package forge

import "testing"

func TestParseRepo(t *testing.T) {
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://github.com/owner/name.git", "owner/name", true},
		{"https://github.com/owner/name", "owner/name", true},
		{"git@github.com:owner/name.git", "owner/name", true},
		{"ssh://git@github.com/owner/name.git", "owner/name", true},
		{"git@github.acme.example:team/tool.git", "github.acme.example/team/tool", true},
		// Parsing succeeds for any host; only the caller decides gh can serve
		// it, and a non-github.com host keeps its prefix.
		{"https://gitlab.com/owner/name.git", "gitlab.com/owner/name", true},
		{"/srv/git/bare.git", "", false},
		{"../sibling", "", false},
		{"https://github.com/owner", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ParseRepo(c.url)
		if ok != c.ok {
			t.Fatalf("ParseRepo(%q) ok = %v, want %v", c.url, ok, c.ok)
		}
		if ok && got.String() != c.want {
			t.Fatalf("ParseRepo(%q) = %q, want %q", c.url, got.String(), c.want)
		}
	}
}

func TestIsGitHub(t *testing.T) {
	for _, c := range []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"github.acme.example", true},
		{"gitlab.com", false},
		{"bitbucket.org", false},
	} {
		if got := (Repo{Host: c.host}).IsGitHub(); got != c.want {
			t.Fatalf("IsGitHub(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}
