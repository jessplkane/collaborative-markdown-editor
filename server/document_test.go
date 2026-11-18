package main

import (
	"strings"
	"testing"
)

func steps(changes ...[]Change) []Step {
	out := make([]Step, len(changes))
	for i, c := range changes {
		out[i] = Step{Changes: c}
	}
	return out
}

func TestApplySteps(t *testing.T) {
	// "😀" is outside the BMP: two UTF-16 code units, so positions after it
	// are offset by 2, not 1. This is what CodeMirror reports.
	tests := []struct {
		name string
		doc  string
		in   []Step
		want string
	}{
		{"insert", "hello", steps([]Change{{5, 5, " world"}}), "hello world"},
		{"insert at start", "world", steps([]Change{{0, 0, "hello "}}), "hello world"},
		{"delete", "hello world", steps([]Change{{5, 11, ""}}), "hello"},
		{"replace", "hello", steps([]Change{{1, 4, "ipp"}}), "hippo"},
		{"insert into empty", "", steps([]Change{{0, 0, "x"}}), "x"},
		{
			"several changes use pre-edit positions",
			"abcdef",
			steps([]Change{{0, 1, "X"}, {2, 4, ""}, {6, 6, "!"}}),
			"Xbef!",
		},
		{"sequential steps see earlier steps", "ab", steps(
			[]Change{{1, 1, "XY"}},
			[]Change{{3, 4, ""}},
		), "aXY"},
		{"insert after emoji", "a😀b", steps([]Change{{3, 3, "X"}}), "a😀Xb"},
		{"delete emoji", "a😀b", steps([]Change{{1, 3, ""}}), "ab"},
		{"insert emoji", "ab", steps([]Change{{1, 1, "😀"}}), "a😀b"},
		{"position after inserted emoji", "ab", steps(
			[]Change{{1, 1, "😀"}},
			[]Change{{3, 3, "X"}},
		), "a😀Xb"},
		{"newline", "a\nb", steps([]Change{{2, 2, "\n"}}), "a\n\nb"},
		{"no steps", "same", nil, "same"},
		{"empty step", "same", steps(nil), "same"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewDoc(tt.doc).ApplySteps(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("got %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestApplyStepsRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		in   []Step
	}{
		{"past end", steps([]Change{{6, 6, "x"}})},
		{"delete past end", steps([]Change{{3, 6, ""}})},
		{"negative from", steps([]Change{{-1, 0, "x"}})},
		{"from after to", steps([]Change{{3, 2, ""}})},
		{"unsorted", steps([]Change{{3, 3, "x"}, {1, 1, "y"}})},
		{"overlapping", steps([]Change{{1, 3, ""}, {2, 4, ""}})},
		// The second step is only valid if the first had not shrunk the doc.
		{"later step invalid after earlier step", steps(
			[]Change{{0, 4, ""}},
			[]Change{{3, 3, "x"}},
		)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := NewDoc("abcde")
			got, err := doc.ApplySteps(tt.in)
			if err == nil {
				t.Fatalf("expected an error, got %q", got.String())
			}
			// Validation happens before any mutation: the caller's doc is intact.
			if doc.String() != "abcde" || got.String() != "abcde" {
				t.Fatalf("doc changed on error: %q / %q", doc.String(), got.String())
			}
		})
	}
}

func TestInsertAtSamePositionAsPreviousChange(t *testing.T) {
	// Adjacent changes (To of one == From of the next) are valid.
	got, err := NewDoc("abc").ApplySteps(steps([]Change{{1, 2, "X"}, {2, 2, "Y"}}))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "aXYc" {
		t.Fatalf("got %q", got.String())
	}
}

func TestDeriveTitle(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"empty", "", "Untitled"},
		{"whitespace only", " \n\t\n  ", "Untitled"},
		{"plain first line", "Shopping list\n- eggs", "Shopping list"},
		{"heading marker", "# My Notes\ntext", "My Notes"},
		{"deep heading", "###### Deep", "Deep"},
		{"skips blank lines", "\n\n  \n# Late title", "Late title"},
		{"marker only line is skipped", "#\n## \nreal", "real"},
		{"hash without space", "#hashtag", "hashtag"},
		{"crlf", "# Title\r\nbody", "Title"},
		{"emoji", "# 😀 party", "😀 party"},
		{"long title truncated by runes", strings.Repeat("é", 200), strings.Repeat("é", 80)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveTitle(tt.content); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
