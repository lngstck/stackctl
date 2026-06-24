// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lngstck/stackctl/internal/backup"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/update"
)

// maxUploadBytes caps an uploaded backup archive (8 GiB) so a runaway upload
// cannot fill the disk unbounded.
const maxUploadBytes = 8 << 30

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

// handleBackupUpload accepts an uploaded backup archive so it can be restored on
// a machine that did not create it. The file is stored in BackupsDir and a
// metadata sidecar is derived so it appears in the list. Restore is a separate,
// explicit step.
func (s *Server) handleBackupUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Redirect(w, r, "/backups?msg=Upload+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}
	f, hdr, err := r.FormFile("archive")
	if err != nil {
		http.Redirect(w, r, "/backups?msg=Keine+Datei+ausgew%C3%A4hlt&err=1", http.StatusSeeOther)
		return
	}
	defer f.Close()

	name := filepath.Base(hdr.Filename)
	if !backup.ValidName(name) {
		http.Redirect(w, r, "/backups?msg=Ung%C3%BCltiger+Dateiname+(erwartet+backup-...tar.gz)&err=1", http.StatusSeeOther)
		return
	}
	if err := paths.EnsureDir(paths.BackupsDir(), 0o700); err != nil {
		log.Printf("web: upload: ensure backups dir: %v", err)
		http.Redirect(w, r, "/backups?msg=Upload+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}
	dest := filepath.Join(paths.BackupsDir(), name)
	if _, err := os.Stat(dest); err == nil {
		http.Redirect(w, r, "/backups?msg=Ein+Backup+mit+diesem+Namen+existiert+bereits&err=1", http.StatusSeeOther)
		return
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		log.Printf("web: upload: create %s: %v", dest, err)
		http.Redirect(w, r, "/backups?msg=Upload+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}
	if _, err := io.Copy(out, io.LimitReader(f, maxUploadBytes)); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		log.Printf("web: upload: copy: %v", err)
		http.Redirect(w, r, "/backups?msg=Upload+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		http.Redirect(w, r, "/backups?msg=Upload+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}
	if err := backup.WriteSidecarFor(name); err != nil {
		_ = os.Remove(dest)
		log.Printf("web: upload: sidecar for %s: %v", name, err)
		http.Redirect(w, r, "/backups?msg=Datei+ist+keine+g%C3%BCltige+stackctl-Sicherung&err=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/backups?msg=Backup+hochgeladen", http.StatusSeeOther)
}

// handleBackupRestore starts a restore as an async op-lock job after a strong
// confirmation: the admin must tick the "I understand" box and type the school
// slug. A wrong/missing confirmation aborts before anything is touched.
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("name")
	if !backup.ValidName(file) {
		http.Redirect(w, r, "/backups?msg=Backup+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}
	if r.FormValue("understand") != "on" {
		http.Redirect(w, r, "/backups?msg=Bitte+best%C3%A4tige+das+%C3%9Cberschreiben&err=1", http.StatusSeeOther)
		return
	}
	if r.FormValue("confirm_slug") != s.cfg.School.Slug {
		http.Redirect(w, r, "/backups?msg=Schul-Slug+stimmt+nicht+%C3%BCberein&err=1", http.StatusSeeOther)
		return
	}
	passphrase := r.FormValue("passphrase")

	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.jobs.create("restore", "", "Wiederherstellung", "/backups")
	go s.runRestoreJob(h, job, file, passphrase)
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// runRestoreJob is the worker body for a restore. On success it restarts
// stackctl (os.Exit → systemd) so the process reloads config/state/.env fresh
// from disk — the job page detects the restart and waits on /healthz.
func (s *Server) runRestoreJob(h *lock.Handle, job *Job, file, passphrase string) {
	defer h.Release()

	if err := backup.Restore(file, passphrase, job); err != nil {
		log.Printf("web: restore job %s: %v", job.ID, err)
		job.finish(false, fmt.Sprintf("Wiederherstellung fehlgeschlagen: %v", err))
		return
	}

	if s.devMode {
		job.setResult(nil, []string{"Wiederherstellung erfolgreich. Im Dev-Modus bitte stackctl manuell neu starten."})
		job.finish(true, "")
		return
	}

	job.Step("Neustart")
	job.setRestarting(true)
	job.finish(true, "")
	// RestartService schedules os.Exit(0); systemd brings stackctl back up,
	// reloading the restored config/state/.env.
	_ = update.RestartService()
}
