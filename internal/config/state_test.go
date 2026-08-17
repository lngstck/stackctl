package config

import (
	"reflect"
	"testing"
)

// populatedState is a State in which every field carries a non-zero value.
// TestStateFixtureCoversEveryField keeps it that way.
func populatedState() *State {
	return &State{
		Version:        StateVersion,
		AdminPublished: true,
		Containers: map[string]*ContainerState{
			"pylearn": {
				ID:                 "pylearn",
				Name:               "PyLearn",
				VersionInstalled:   "0.8.0",
				Ports:              []int{8330},
				EnvKeys:            []string{"PYLEARN_DB_PASSWORD"},
				InstalledAt:        "2026-08-17T09:00:00Z",
				PublicEnabled:      true,
				PublicHost:         "pylearn.phoenix.learningstack.online",
				AutoUpdateDisabled: true,
			},
		},
		Ports: map[int]string{8330: "pylearn"},
	}
}

// TestStateFixtureCoversEveryField fails when a field is added to State
// without being set in populatedState. Without it the Clone test below would
// keep passing while quietly no longer covering the new field.
func TestStateFixtureCoversEveryField(t *testing.T) {
	v := reflect.ValueOf(*populatedState())
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			t.Errorf("populatedState leaves %s at its zero value — set it, then check Clone copies it",
				v.Type().Field(i).Name)
		}
	}
}

// TestCloneCopiesEveryField is the guard for the failure mode that matters:
// a scalar field missing from Clone's struct literal is not a compile error.
// It silently resets to zero the next time a background job commits its
// clone, and the symptom shows up somewhere else entirely — see the
// concurrency notes in ARCHITECTURE.md §16.5.
func TestCloneCopiesEveryField(t *testing.T) {
	orig := populatedState()
	clone := orig.Clone()

	if !reflect.DeepEqual(orig, clone) {
		t.Errorf("Clone lost data:\n orig  = %+v\n clone = %+v", orig, clone)
	}
}

func TestCloneIsDeep(t *testing.T) {
	orig := populatedState()
	clone := orig.Clone()

	clone.AdminPublished = false
	clone.Containers["pylearn"].PublicEnabled = false
	clone.Containers["pylearn"].Ports[0] = 9999
	clone.Ports[8330] = "someone-else"
	delete(clone.Containers, "pylearn")

	if !orig.AdminPublished {
		t.Error("AdminPublished on the original followed the clone")
	}
	cs, ok := orig.Containers["pylearn"]
	if !ok {
		t.Fatal("container deleted from the original")
	}
	if !cs.PublicEnabled {
		t.Error("PublicEnabled on the original followed the clone")
	}
	if cs.Ports[0] != 8330 {
		t.Errorf("Ports slice shared with the clone: %v", cs.Ports)
	}
	if orig.Ports[8330] != "pylearn" {
		t.Errorf("Ports map shared with the clone: %v", orig.Ports)
	}
}

func TestCloneNil(t *testing.T) {
	var s *State
	if got := s.Clone(); got == nil || got.Containers == nil || got.Ports == nil {
		t.Errorf("Clone of nil should yield a usable empty state, got %+v", got)
	}
}

func TestStateSaveAndReload(t *testing.T) {
	withTempStackctlDir(t)

	orig := populatedState()
	if err := orig.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !loaded.AdminPublished {
		t.Error("AdminPublished did not survive the round-trip")
	}
	if !reflect.DeepEqual(orig.Containers, loaded.Containers) {
		t.Errorf("containers round-trip:\n orig   = %+v\n loaded = %+v",
			orig.Containers["pylearn"], loaded.Containers["pylearn"])
	}
}

// TestLoadStateWithoutAdminPublished pins the default for every install that
// predates the option: the control plane is not on the internet unless
// somebody said so.
func TestLoadStateWithoutAdminPublished(t *testing.T) {
	withTempStackctlDir(t)

	s := NewState()
	s.Containers["pylearn"] = &ContainerState{ID: "pylearn"}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.AdminPublished {
		t.Error("AdminPublished defaulted to true")
	}
}
