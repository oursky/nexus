package project

import "testing"

func TestNormalizeRepoURL_CanonicalizesGitURLForms(t *testing.T) {
	cases := map[string]string{
		"https://github.com/oursky/nexus.git": "github.com/oursky/nexus",
		"https://github.com/oursky/nexus":     "github.com/oursky/nexus",
		"git@github.com:oursky/nexus.git":     "github.com/oursky/nexus",
		"github.com/oursky/nexus.git":         "github.com/oursky/nexus",
		"github.com/oursky/nexus":             "github.com/oursky/nexus",
	}
	for in, want := range cases {
		if got := NormalizeRepoURL(in); got != want {
			t.Fatalf("NormalizeRepoURL(%q) = %q, want %q", in, got, want)
		}
	}
}
