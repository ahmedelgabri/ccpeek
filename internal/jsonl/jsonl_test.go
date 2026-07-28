package jsonl

import (
	"strings"
	"testing"
)

func collect(t *testing.T, input string, max int) (lines map[int]string, over map[int]int64) {
	t.Helper()
	lines = map[int]string{}
	over = map[int]int64{}
	err := Scan(strings.NewReader(input), max,
		func(n int, line []byte) error {
			lines[n] = string(line)
			return nil
		},
		func(n int, size int64) error {
			over[n] = size
			return nil
		})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return lines, over
}

func TestScanSkipsOversizedLines(t *testing.T) {
	huge := strings.Repeat("x", 200_000)
	input := "first\n" + huge + "\nsecond\n"
	lines, over := collect(t, input, 1024)

	if lines[1] != "first" || lines[3] != "second" {
		t.Errorf("valid lines lost around the oversized one: %v", lines)
	}
	if len(lines) != 2 {
		t.Errorf("lines = %v, want exactly the two valid ones", lines)
	}
	if over[2] != int64(len(huge))+1 {
		t.Errorf("oversized report = %v, want line 2 with %d bytes", over, len(huge)+1)
	}
}

func TestScanUnterminatedTail(t *testing.T) {
	lines, over := collect(t, "a\nb", 1024)
	if lines[1] != "a" || lines[2] != "b" || len(over) != 0 {
		t.Errorf("lines = %v over = %v", lines, over)
	}
}

func TestScanUnterminatedOversizedTail(t *testing.T) {
	huge := strings.Repeat("y", 5000)
	lines, over := collect(t, "a\n"+huge, 1024)
	if lines[1] != "a" || len(lines) != 1 {
		t.Errorf("lines = %v", lines)
	}
	if over[2] != int64(len(huge)) {
		t.Errorf("over = %v, want line 2 with %d bytes", over, len(huge))
	}
}
