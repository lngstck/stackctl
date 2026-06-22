// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package docker

import (
	"reflect"
	"testing"
)

// runStreaming must forward each output line live and also return the full
// combined output and a zero exit code on success.
func TestRunStreamingForwardsLines(t *testing.T) {
	var got []string
	code, out := runStreaming(func(l string) { got = append(got, l) }, "printf", "a\nb\nc\n")

	if code != 0 {
		t.Fatalf("exit code: want 0, got %d", code)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streamed lines: want %v, got %v", want, got)
	}
	if out != "a\nb\nc\n" {
		t.Fatalf("combined output: want %q, got %q", "a\nb\nc\n", out)
	}
}

// A non-zero exit is surfaced as a non-zero code; a nil onLine is tolerated.
func TestRunStreamingNonZeroExit(t *testing.T) {
	code, _ := runStreaming(nil, "false")
	if code == 0 {
		t.Fatalf("exit code: want non-zero, got 0")
	}
}

// A missing binary yields code -1 and surfaces the error text as output.
func TestRunStreamingMissingBinary(t *testing.T) {
	code, out := runStreaming(nil, "stackctl-no-such-binary-xyz")
	if code != -1 {
		t.Fatalf("exit code: want -1, got %d", code)
	}
	if out == "" {
		t.Fatalf("expected error text in output, got empty")
	}
}
