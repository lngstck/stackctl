// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"sync"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

// snapState and commitState must be safe to run concurrently — a background
// install/update job commits while reader handlers snapshot. Run under -race
// to catch any unsynchronised access to the shared state maps (which would
// otherwise crash the process with a concurrent map read/write).
func TestStateSnapshotCommitNoRace(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	st := config.NewState()
	st.Containers["seed"] = &config.ContainerState{ID: "seed", Name: "Seed"}
	s := &Server{state: st}

	var wg sync.WaitGroup
	// Writers.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				working := s.snapState()
				working.Containers["app"] = &config.ContainerState{ID: "app"}
				_ = s.commitState(working)
			}
		}(w)
	}
	// Readers.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				st := s.snapState()
				for id := range st.Containers { // iterate to provoke a race if any
					_ = id
				}
			}
		}()
	}
	wg.Wait()
}
