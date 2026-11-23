package main

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Change replaces the range [From, To) of a document with Insert.
//
// Positions are UTF-16 code units, matching CodeMirror's string indices, so a
// character outside the BMP (an emoji, say) occupies two positions. A list of
// changes is ordered and every position refers to the document as it was
// before any change in the list was applied.
type Change struct {
	From   int    `json:"from"`
	To     int    `json:"to"`
	Insert string `json:"insert"`
}

// Step is one version of a document: the changes that take version n to n+1.
type Step struct {
	Changes []Change `json:"changes"`
}

// Doc is a document held as UTF-16 code units so that Change positions index
// it directly. Converting to a string happens only when persisting or sending
// a snapshot, never per edit.
type Doc []uint16

func NewDoc(s string) Doc {
	return appendUTF16(make(Doc, 0, len(s)), s)
}

func (d Doc) String() string {
	return string(utf16.Decode(d))
}

func appendUTF16(dst Doc, s string) Doc {
	for _, r := range s {
		dst = utf16.AppendRune(dst, r)
	}
	return dst
}

// utf16Len is the number of UTF-16 code units in s.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

var errInvalidChange = errors.New("invalid change")

// checkChanges validates changes against a document of the given length and
// returns the length after applying them.
func checkChanges(length int, changes []Change) (int, error) {
	prevTo := 0
	newLength := length
	for _, c := range changes {
		if c.From < prevTo || c.To < c.From || c.To > length {
			return 0, errInvalidChange
		}
		prevTo = c.To
		newLength += utf16Len(c.Insert) - (c.To - c.From)
	}
	return newLength, nil
}

// CheckSteps validates steps against d without touching it and returns the
// length d would have after all of them. A caller that wants to refuse an
// update by its resulting size must decide before applying: applying may
// write into d's storage.
func (d Doc) CheckSteps(steps []Step) (int, error) {
	length := len(d)
	for _, s := range steps {
		var err error
		if length, err = checkChanges(length, s.Changes); err != nil {
			return 0, err
		}
	}
	return length, nil
}

// ApplySteps applies steps in order. It validates every step before touching
// the document, so on error d is unchanged. The returned Doc may share
// storage with d, and d itself may be overwritten: use d only through the
// returned Doc afterwards.
func (d Doc) ApplySteps(steps []Step) (Doc, error) {
	if _, err := d.CheckSteps(steps); err != nil {
		return d, err
	}
	return d.applyChecked(steps), nil
}

// applyChecked applies steps that CheckSteps accepted for d.
func (d Doc) applyChecked(steps []Step) Doc {
	for _, s := range steps {
		d = d.apply(s.Changes)
	}
	return d
}

// apply assumes changes are valid for d. It works from the last change to the
// first so earlier positions stay correct without being translated.
func (d Doc) apply(changes []Change) Doc {
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		d = slices.Replace(d, c.From, c.To, appendUTF16(nil, c.Insert)...)
	}
	return d
}

const untitled = "Untitled"

const maxTitleRunes = 80

// deriveTitle returns the first non-blank line of a markdown document with
// leading heading markers removed, or "Untitled" if there is none.
func deriveTitle(content string) string {
	for line := range strings.Lines(content) {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > maxTitleRunes {
			t = string([]rune(t)[:maxTitleRunes])
		}
		return t
	}
	return untitled
}
