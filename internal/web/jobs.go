// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/lngstck/stackctl/internal/install"
)

// Job models one long-running, admin-triggered operation (app install/update
// or stackctl self-update) so the web UI can show a live progress view instead
// of a multi-minute blocking request (issue #1).
//
// A Job is driven through install.Reporter: Step() advances the checklist, Log()
// appends a raw output line (docker pull progress). The browser polls
// /jobs/{id}/status while the work runs in a goroutine that holds the op-lock.
//
// stackctl is single-admin and the op-lock allows only one mutating operation
// at a time, so at most one job is ever running; the store keeps recently
// finished jobs around so their result page still resolves.

// StepStatus is the lifecycle state of a single checklist step.
type StepStatus string

const (
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

// Step is one entry in the progress checklist.
type Step struct {
	Name   string     `json:"name"`
	Status StepStatus `json:"status"`
}

// maxJobLogLines caps the rolling raw-output buffer kept per job.
const maxJobLogLines = 400

// Job holds the live state of one operation. All field access goes through the
// mutex because the worker goroutine writes while poll requests read.
type Job struct {
	ID      string
	Kind    string // "install" | "update" | "selfupdate"
	AppID   string
	Title   string
	BackURL string // where the "fertig" button links (e.g. /apps/open-webui)

	mu         sync.Mutex
	steps      []*Step
	log        []string
	done       bool
	success    bool
	errMsg     string
	startedAt  time.Time
	finishedAt time.Time

	// Result payload, populated on completion.
	secretsToShow map[string]string
	messages      []string
	newVersion    string // selfupdate
	restarting    bool   // selfupdate triggers a service restart
}

// Step marks the current running step done and begins a new one. It satisfies
// install.Reporter.
func (j *Job) Step(name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n := len(j.steps); n > 0 && j.steps[n-1].Status == StepRunning {
		j.steps[n-1].Status = StepDone
	}
	j.steps = append(j.steps, &Step{Name: name, Status: StepRunning})
}

// Log appends a raw output line, trimming the buffer to the cap. Satisfies
// install.Reporter.
func (j *Job) Log(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.log = append(j.log, line)
	if len(j.log) > maxJobLogLines {
		j.log = j.log[len(j.log)-maxJobLogLines:]
	}
}

// setResult records the post-install payload (secrets to show once, messages)
// for rendering on the completed job page.
func (j *Job) setResult(secrets map[string]string, messages []string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.secretsToShow = secrets
	j.messages = messages
}

// setSelfUpdate records self-update-specific result fields.
func (j *Job) setSelfUpdate(newVersion string, restarting bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.newVersion = newVersion
	j.restarting = restarting
}

// setRestarting flags that a stackctl restart follows this job (used by
// restore, which restarts so the process reloads config/state from disk). The
// job page then polls /healthz and returns once the service is back.
func (j *Job) setRestarting(restarting bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.restarting = restarting
}

// finish closes out the job, marking the trailing step done or failed.
func (j *Job) finish(success bool, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n := len(j.steps); n > 0 && j.steps[n-1].Status == StepRunning {
		if success {
			j.steps[n-1].Status = StepDone
		} else {
			j.steps[n-1].Status = StepFailed
		}
	}
	j.done = true
	j.success = success
	j.errMsg = errMsg
	j.finishedAt = time.Now()
}

// jobSnapshot is the JSON shape returned to the polling browser.
type jobSnapshot struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Title         string            `json:"title"`
	BackURL       string            `json:"back_url"`
	Steps         []*Step           `json:"steps"`
	Log           []string          `json:"log"`
	Done          bool              `json:"done"`
	Success       bool              `json:"success"`
	Error         string            `json:"error,omitempty"`
	ElapsedSec    int               `json:"elapsed_sec"`
	SecretsToShow map[string]string `json:"secrets_to_show,omitempty"`
	Messages      []string          `json:"messages,omitempty"`
	NewVersion    string            `json:"new_version,omitempty"`
	Restarting    bool              `json:"restarting,omitempty"`
}

// snapshot returns a consistent copy for rendering or JSON encoding.
func (j *Job) snapshot() jobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	end := j.finishedAt
	if end.IsZero() {
		end = time.Now()
	}
	steps := make([]*Step, len(j.steps))
	for i, s := range j.steps {
		cp := *s
		steps[i] = &cp
	}
	logCopy := make([]string, len(j.log))
	copy(logCopy, j.log)

	return jobSnapshot{
		ID:            j.ID,
		Kind:          j.Kind,
		Title:         j.Title,
		BackURL:       j.BackURL,
		Steps:         steps,
		Log:           logCopy,
		Done:          j.done,
		Success:       j.success,
		Error:         j.errMsg,
		ElapsedSec:    int(end.Sub(j.startedAt).Seconds()),
		SecretsToShow: j.secretsToShow,
		Messages:      j.messages,
		NewVersion:    j.newVersion,
		Restarting:    j.restarting,
	}
}

// jobStore keeps the running job plus a handful of recently finished ones so
// their result pages still resolve after the worker exits.
type jobStore struct {
	mu   sync.Mutex
	jobs map[string]*Job
	// insertion order for light pruning
	order []string
}

const maxRetainedJobs = 8

func newJobStore() *jobStore {
	return &jobStore{jobs: make(map[string]*Job)}
}

// create registers and returns a new job in the running state.
func (s *jobStore) create(kind, appID, title, backURL string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()

	j := &Job{
		ID:        newJobID(),
		Kind:      kind,
		AppID:     appID,
		Title:     title,
		BackURL:   backURL,
		startedAt: time.Now(),
	}
	s.jobs[j.ID] = j
	s.order = append(s.order, j.ID)
	for len(s.order) > maxRetainedJobs {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.jobs, oldest)
	}
	return j
}

func (s *jobStore) get(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

func newJobID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ensure Job satisfies install.Reporter at compile time.
var _ install.Reporter = (*Job)(nil)
