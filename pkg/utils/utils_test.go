package utils

import (
	"testing"
)

func TestCleanFolderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{ // left: original title, right: cleaned title
		{"KonoSuba: God’s blessing on this wonderful world!", "KonoSuba - God’s blessing on this wonderful world!"},
		{"Konosuba: An Explosion on This Wonderful World!", "Konosuba - An Explosion on This Wonderful World!"},
		{"Danmachi: Is It Wrong to Try to Pick Up Girls in a Dungeon?", "Danmachi - Is It Wrong to Try to Pick Up Girls in a Dungeon"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := CleanFolderName(tt.input)
			if got != tt.expected {
				t.Errorf("\nInput:    %s\nExpected: %s\nGot:      %s", tt.input, tt.expected, got)
			}
		})
	}
}
