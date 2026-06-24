// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/lngstck/stackctl/internal/backup"
	"github.com/lngstck/stackctl/internal/lock"
)

// formatBackupTime renders an RFC3339 UTC timestamp in local time for display.
func formatBackupTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Local().Format("02.01.2006, 15:04")
}

// backupsData is the template context for backups.html.tmpl.
type backupsData struct {
	PageData
	Backups []backupRow
	Message string
	IsError bool
}

// backupRow is one entry in the backup list, pre-formatted for display.
type backupRow struct {
	File       string
	CreatedAt  string // localized, human-readable
	Size       string
	AppCount   int
	Encrypted  bool
	Version    string
	IncludesDB bool
}

func (s *Server) handleBackupsPage(w http.ResponseWriter, r *http.Request) {
	data := backupsData{PageData: s.pageData("backups")}

	infos, err := backup.List()
	if err != nil {
		log.Printf("web: list backups: %v", err)
		data.Message = "Backups konnten nicht gelesen werden."
		data.IsError = true
	}
	for _, info := range infos {
		data.Backups = append(data.Backups, backupRow{
			File:       info.File,
			CreatedAt:  formatBackupTime(info.CreatedAt),
			Size:       backup.HumanSize(info.SizeBytes),
			AppCount:   len(info.Apps),
			Encrypted:  info.Encrypted,
			Version:    info.StackctlVersion,
			IncludesDB: info.IncludesDB,
		})
	}

	if msg := r.URL.Query().Get("msg"); msg != "" {
		data.Message = msg
		data.IsError = r.URL.Query().Get("err") == "1"
	}

	s.render(w, "backups.html.tmpl", data)
}

// handleBackupCreate starts a backup as an async job (op-lock held for the job
// duration, handed to the worker goroutine) and redirects to the live progress
// page — mirroring the install flow.
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	passphrase := ""
	if r.FormValue("encrypt") == "on" {
		passphrase = r.FormValue("passphrase")
		if passphrase == "" {
			http.Redirect(w, r, "/backups?msg=Verschl%C3%BCsselung+gew%C3%A4hlt%2C+aber+keine+Passphrase+angegeben.&err=1", http.StatusSeeOther)
			return
		}
	}

	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.jobs.create("backup", "", "Backup erstellen", "/backups")
	go s.runBackupJob(h, job, backup.Options{Passphrase: passphrase})
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// runBackupJob is the worker body for a backup. It reads a state snapshot for
// the manifest (backup never mutates state), runs backup.Create reporting
// progress to the job, and always releases the op-lock.
func (s *Server) runBackupJob(h *lock.Handle, job *Job, opts backup.Options) {
	defer h.Release()

	state := s.snapState()
	info, err := backup.Create(s.cfg, state, opts, job)
	if err != nil {
		log.Printf("web: backup job %s: %v", job.ID, err)
		job.finish(false, fmt.Sprintf("Backup fehlgeschlagen: %v", err))
		return
	}
	job.setResult(nil, []string{
		fmt.Sprintf("Backup %s erstellt (%s).", info.File, backup.HumanSize(info.SizeBytes)),
	})
	job.finish(true, "")
}

// handleBackupDownload streams a backup archive as a file attachment.
func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("name")
	path, err := backup.Path(file)
	if err != nil {
		http.Redirect(w, r, "/backups?msg=Backup+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(path)))
	http.ServeFile(w, r, path)
}

// handleBackupDelete removes a server-side backup.
func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("name")
	if err := backup.Delete(file); err != nil {
		log.Printf("web: delete backup %s: %v", file, err)
		http.Redirect(w, r, "/backups?msg=Backup+konnte+nicht+gel%C3%B6scht+werden&err=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/backups?msg=Backup+gel%C3%B6scht", http.StatusSeeOther)
}
