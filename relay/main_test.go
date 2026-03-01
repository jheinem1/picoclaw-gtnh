package main

import (
	"reflect"
	"testing"
)

func TestSplitForMC(t *testing.T) {
	parts := splitForMC("one two three four five six", 10, 4)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %#v", parts)
	}
	for _, p := range parts {
		if len([]rune(p)) > 10 {
			t.Fatalf("part too long: %q", p)
		}
	}
}

func TestSplitForMC_LongWord(t *testing.T) {
	parts := splitForMC("supercalifragilisticexpialidocious", 8, 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts due to maxParts cap, got %#v", parts)
	}
	for _, p := range parts {
		if len([]rune(p)) > 8 {
			t.Fatalf("part too long: %q", p)
		}
	}
}

func TestExtractCandidateTerms(t *testing.T) {
	got := extractCandidateTerms("greg what does cassiterite refine into?")
	want := []string{"cassiterite"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected terms: got=%#v want=%#v", got, want)
	}

	got = extractCandidateTerms("greg what does it take to turn yellow garnet dust into titanium?")
	want = []string{"yellow garnet dust", "titanium"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected turn-into terms: got=%#v want=%#v", got, want)
	}

	got = extractCandidateTerms("greg, how much steel do we need to make a tier 2 steam purifier?")
	want = []string{"tier 2 steam purifier", "how much steel do we need to make a tier 2 steam purifier"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected make terms: got=%#v want=%#v", got, want)
	}
}
