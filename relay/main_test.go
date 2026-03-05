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

func TestNeedsVerification_SteamThroughputQuestion(t *testing.T) {
	if !needsVerification("greg what pipes that can handle steam have higher throughput than potin fluid pipes in MV?") {
		t.Fatalf("expected steam throughput question to require verification")
	}
}

func TestFormatCoordinatesForMC_TupleCount(t *testing.T) {
	in := "Chests with glass: (-1173,20,3525):63; (-1256,50,-8798):57"
	got := formatCoordinatesForMC(in)
	want := "Chests with glass: [x:-1173, y:20, z:3525] count=63, [x:-1256, y:50, z:-8798] count=57"
	if got != want {
		t.Fatalf("unexpected format\n got=%q\nwant=%q", got, want)
	}
}

func TestFormatCoordinatesForMC_Dim(t *testing.T) {
	in := "Chest at (-10,64,30) dim0 count=12"
	got := formatCoordinatesForMC(in)
	want := "Chest at [x:-10, y:64, z:30, dim:0] count=12"
	if got != want {
		t.Fatalf("unexpected format\n got=%q\nwant=%q", got, want)
	}
}
