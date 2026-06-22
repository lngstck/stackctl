// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import "testing"

func TestJobStepLifecycle(t *testing.T) {
	j := &Job{ID: "x"}
	j.Step("eins")
	j.Step("zwei") // marks "eins" done, "zwei" running

	snap := j.snapshot()
	if len(snap.Steps) != 2 {
		t.Fatalf("steps: want 2, got %d", len(snap.Steps))
	}
	if snap.Steps[0].Status != StepDone {
		t.Errorf("step 0: want done, got %s", snap.Steps[0].Status)
	}
	if snap.Steps[1].Status != StepRunning {
		t.Errorf("step 1: want running, got %s", snap.Steps[1].Status)
	}

	j.finish(true, "")
	snap = j.snapshot()
	if !snap.Done || !snap.Success {
		t.Fatalf("after finish: done=%v success=%v", snap.Done, snap.Success)
	}
	if snap.Steps[1].Status != StepDone {
		t.Errorf("trailing step after success: want done, got %s", snap.Steps[1].Status)
	}
}

func TestJobFinishFailureMarksTrailingStepFailed(t *testing.T) {
	j := &Job{ID: "x"}
	j.Step("läuft")
	j.finish(false, "kaputt")
	snap := j.snapshot()
	if snap.Success || snap.Error != "kaputt" {
		t.Fatalf("want failure with message, got success=%v err=%q", snap.Success, snap.Error)
	}
	if snap.Steps[0].Status != StepFailed {
		t.Errorf("trailing step: want failed, got %s", snap.Steps[0].Status)
	}
}

func TestJobLogCap(t *testing.T) {
	j := &Job{ID: "x"}
	for i := 0; i < maxJobLogLines+50; i++ {
		j.Log("line")
	}
	if got := len(j.snapshot().Log); got != maxJobLogLines {
		t.Fatalf("log cap: want %d, got %d", maxJobLogLines, got)
	}
}

func TestJobStorePruning(t *testing.T) {
	st := newJobStore()
	var ids []string
	for i := 0; i < maxRetainedJobs+3; i++ {
		ids = append(ids, st.create("install", "app", "t", "/apps").ID)
	}
	// The oldest 3 should have been evicted.
	for _, id := range ids[:3] {
		if _, ok := st.get(id); ok {
			t.Errorf("expected job %s to be pruned", id)
		}
	}
	for _, id := range ids[3:] {
		if _, ok := st.get(id); !ok {
			t.Errorf("expected job %s to be retained", id)
		}
	}
}
