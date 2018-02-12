package main

import "testing"

func TestMakeSherpaLink(t *testing.T) {
	testTable := []struct {
		input string
		output string
	}{
		{"", ""},
		{"1234-5678", SHERPAURI+"1234-5678/"},
		{"12345678", SHERPAURI+"1234-5678/"},
		{"12345678,abcd-efgh", SHERPAURI+"1234-5678/,"+SHERPAURI+"abcd-efgh/"},
	}

	for _, tt := range testTable {
		realOutput := makeSherpaLink(tt.input)
		if realOutput != tt.output {
			t.Errorf("makeSherpaLink(%v) => %v, want %v", tt.input, realOutput, tt.output)
		}
	}
}
