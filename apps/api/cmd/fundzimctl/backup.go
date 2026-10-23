package main

// fundzimctl backup record (Stage 18, docs/stage-18/backup-recovery.md): store backup and restore-drill evidence in
// app.ops_backup_runs. The manifest's MAC is verified first (the same derivation as scripts/backup/manifest.py), so a
// tampered or foreign backup set cannot be recorded as evidence.
//
//	fundzimctl backup record -key KEYFILE -dir BACKUP_DIR                       a successful backup
//	fundzimctl backup record -key KEYFILE -dir BACKUP_DIR -report DRILL.json    a restore drill (outcome from its result)

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
)

func backupCmd(args []string) error {
	if len(args) == 0 || args[0] != "record" {
		return errors.New("usage: fundzimctl backup record -key KEYFILE -dir BACKUP_DIR [-report DRILL.json]")
	}
	fs := flag.NewFlagSet("backup record", flag.ContinueOnError)
	keyFile := fs.String("key", "", "backup key file (verifies the manifest MAC)")
	dir := fs.String("dir", "", "backup directory (manifest.json, manifest.hmac)")
	report := fs.String("report", "", "restore-drill report (records a RESTORE_DRILL instead of a BACKUP)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *keyFile == "" || *dir == "" {
		return errors.New("-key and -dir are required")
	}
	body, err := os.ReadFile(filepath.Join(*dir, "manifest.json"))
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(*dir, "manifest.hmac"))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	lbl := hmac.New(sha256.New, []byte(strings.TrimSpace(string(raw))))
	lbl.Write([]byte("fundzim/backup-manifest/v1"))
	mac := hmac.New(sha256.New, lbl.Sum(nil))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(got), []byte(strings.TrimSpace(string(want)))) {
		return errors.New("manifest MAC does not verify: not recorded")
	}
	var m struct {
		BackupID    string         `json:"backup_id"`
		Environment string         `json:"environment"`
		StartedAt   time.Time      `json:"started_at"`
		FinishedAt  time.Time      `json:"finished_at"`
		Durations   map[string]any `json:"durations_s"`
		Files       map[string]any `json:"files"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	sum := sha256.Sum256(body)
	run := operations.BackupRun{Kind: "BACKUP", BackupID: m.BackupID, Environment: m.Environment, Outcome: "SUCCEEDED", StartedAt: m.StartedAt,
		FinishedAt: m.FinishedAt, ManifestSHA256: sum[:], Details: map[string]any{"durations_s": m.Durations, "files": len(m.Files)}}
	if *report != "" {
		var rep struct {
			BackupID        string           `json:"backup_id"`
			Result          string           `json:"result"`
			RecoverySeconds float64          `json:"recovery_seconds"`
			Steps           []map[string]any `json:"steps"`
			Checks          []map[string]any `json:"checks"`
		}
		rb, err := os.ReadFile(*report)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(rb, &rep); err != nil {
			return fmt.Errorf("report: %w", err)
		}
		if rep.BackupID != m.BackupID || len(rep.Steps) == 0 {
			return errors.New("the report does not belong to this backup")
		}
		first, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(rep.Steps[0]["started_at"]))
		last, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(rep.Steps[len(rep.Steps)-1]["finished_at"]))
		outcome := "FAILED"
		if rep.Result == "PASSED" {
			outcome = "SUCCEEDED"
		}
		checks := map[string]any{}
		for _, c := range rep.Checks {
			checks[fmt.Sprint(c["check"])] = c["outcome"]
		}
		run = operations.BackupRun{Kind: "RESTORE_DRILL", BackupID: m.BackupID, Environment: m.Environment, Outcome: outcome, StartedAt: first,
			FinishedAt: last, ManifestSHA256: sum[:], Details: map[string]any{"recovery_seconds": rep.RecoverySeconds, "checks": checks}}
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := db.NewPool(ctx, db.Options{URL: url, MaxConns: 2, ConnectTimeout: 10 * time.Second, AppName: "fundzimctl-backup"})
	if err != nil {
		return err
	}
	defer pool.Close()
	created, err := (&operations.Service{Pool: pool, Clock: clock.System}).RecordBackupRun(ctx, run)
	if err != nil {
		return err
	}
	fmt.Printf("{\"recorded\": %t, \"kind\": %q, \"backup_id\": %q, \"outcome\": %q}\n", created, run.Kind, run.BackupID, run.Outcome)
	return nil
}
