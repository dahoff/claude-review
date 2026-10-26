package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRemapLines(t *testing.T) {
	old := []string{"a", "b", "c", "d", "e"}

	tests := []struct {
		name               string
		new                []string
		start, end         int
		wantStart, wantEnd int
		wantChanged        bool
	}{
		{"unchanged", old, 2, 3, 2, 3, false},
		{"lines inserted above", []string{"x", "y", "a", "b", "c", "d", "e"}, 2, 3, 4, 5, false},
		{"lines deleted above", []string{"c", "d", "e"}, 3, 4, 1, 2, false},
		{"lines inserted below", []string{"a", "b", "c", "x", "d", "e"}, 2, 3, 2, 3, false},
		{"commented line rewritten", []string{"x", "a", "B", "c", "d", "e"}, 2, 2, 3, 3, true},
		{"line inserted inside the range", []string{"a", "b", "x", "c", "d", "e"}, 2, 3, 2, 4, true},
		{"commented lines deleted", []string{"a", "d", "e"}, 2, 3, 2, 2, true},
		{"everything deleted", []string{""}, 4, 5, 1, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, changed := remapLines(old, tt.new, tt.start, tt.end)
			assert.Equal(t, tt.wantStart, start, "start")
			assert.Equal(t, tt.wantEnd, end, "end")
			assert.Equal(t, tt.wantChanged, changed, "changed")
		})
	}
}
