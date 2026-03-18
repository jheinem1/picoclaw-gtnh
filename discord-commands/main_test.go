package main

import "testing"

func TestSplitNames(t *testing.T) {
	got := splitNames("Alice, Bob;Carol  Dana")
	want := []string{"Alice", "Bob", "Carol", "Dana"}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: got=%#v want=%#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitNames mismatch at %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("unexpected short truncate: %q", got)
	}
	if got := truncate("this string is longer", 10); got != "this st..." {
		t.Fatalf("unexpected long truncate: %q", got)
	}
}

func TestWrapCodeBlock(t *testing.T) {
	got := wrapCodeBlock(`{"ok":true}`)
	want := "```json\n{\"ok\":true}\n```"
	if got != want {
		t.Fatalf("unexpected json code block: %q", got)
	}
}

func TestFormatWikiSearchOutput(t *testing.T) {
	raw := `{"ok":true,"query":"electric blast furnace","total_hits":3,"results":[{"title":"Electric blast furnace","url":"https://wiki.gtnewhorizons.com/wiki/Electric_blast_furnace"},{"title":"Electric Blast Furnace","url":"https://wiki.gtnewhorizons.com/wiki/Electric_Blast_Furnace"}],"source":"wiki.gtnewhorizons.com/w/api.php"}`
	got := formatWikiSearchOutput(raw)
	if got == raw {
		t.Fatalf("expected formatted output, got raw json")
	}
	if got != "electric blast furnace (3 hits)\n1. Electric blast furnace - https://wiki.gtnewhorizons.com/wiki/Electric_blast_furnace\n2. Electric Blast Furnace - https://wiki.gtnewhorizons.com/wiki/Electric_Blast_Furnace" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}
