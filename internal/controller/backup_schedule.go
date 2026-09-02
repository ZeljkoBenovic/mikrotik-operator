package controller

import (
	"fmt"
	"sort"
	"strings"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	"github.com/robfig/cron/v3"
)

func nextBackupTime(expr string, from time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, fmt.Errorf("backup schedule is empty")
	}
	schedule, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse backup schedule %q: %w", expr, err)
	}
	next := schedule.Next(from)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("backup schedule %q has no next runtime", expr)
	}
	return next, nil
}

func backupSnapshotName(policy string, at time.Time) string {
	suffix := fmt.Sprintf("-%d", at.Unix())
	maxPrefix := 63 - len(suffix)
	prefix := policy
	if maxPrefix < 1 {
		return strings.Trim(suffix, "-")
	}
	if len(prefix) > maxPrefix {
		prefix = strings.Trim(prefix[:maxPrefix], "-")
	}
	if prefix == "" {
		prefix = "snapshot"
		if len(prefix) > maxPrefix {
			prefix = prefix[:maxPrefix]
		}
	}
	return prefix + suffix
}

func snapshotsToDelete(snapshots []api.MikroTikBackup, keep int32) []api.MikroTikBackup {
	if keep < 1 {
		keep = api.DefaultBackupRetention
	}
	var captured, empty []api.MikroTikBackup
	for _, snapshot := range snapshots {
		if snapshot.Spec.IsPolicy() {
			continue
		}
		if snapshot.DeletionTimestamp != nil && !snapshot.DeletionTimestamp.IsZero() {
			continue
		}
		if snapshotHasExport(snapshot) {
			captured = append(captured, snapshot)
			continue
		}
		empty = append(empty, snapshot)
	}
	sortSnapshotsOldestFirst(captured)
	sortSnapshotsOldestFirst(empty)

	toDelete := make([]api.MikroTikBackup, 0)
	if len(empty) > 1 {
		toDelete = append(toDelete, empty[:len(empty)-1]...)
	}
	if int32(len(captured)) > keep {
		overflow := len(captured) - int(keep)
		toDelete = append(toDelete, captured[:overflow]...)
	}
	return toDelete
}

func snapshotHasExport(snapshot api.MikroTikBackup) bool {
	return strings.TrimSpace(snapshot.Status.Export) != ""
}

func sortSnapshotsOldestFirst(snapshots []api.MikroTikBackup) {
	sort.Slice(snapshots, func(i, j int) bool {
		ti, tj := snapshotOrderTime(snapshots[i]), snapshotOrderTime(snapshots[j])
		if ti.Equal(tj) {
			return snapshots[i].Name < snapshots[j].Name
		}
		return ti.Before(tj)
	})
}

func snapshotHasCapture(snapshots []api.MikroTikBackup, name string) bool {
	for _, snapshot := range snapshots {
		if snapshot.Name != name {
			continue
		}
		if snapshotHasExport(snapshot) && snapshot.Status.CapturedAt != nil && !snapshot.Status.CapturedAt.IsZero() {
			return true
		}
	}
	return false
}

func latestOwnedSnapshot(snapshots []api.MikroTikBackup) *api.MikroTikBackup {
	var latest *api.MikroTikBackup
	for i := range snapshots {
		snapshot := &snapshots[i]
		if snapshot.Spec.IsPolicy() {
			continue
		}
		if latest == nil || snapshotOrderTime(*latest).Before(snapshotOrderTime(*snapshot)) {
			latest = snapshot
		}
	}
	return latest
}

func snapshotOrderTime(snapshot api.MikroTikBackup) time.Time {
	if snapshot.Status.CapturedAt != nil && !snapshot.Status.CapturedAt.IsZero() {
		return snapshot.Status.CapturedAt.Time
	}
	if !snapshot.CreationTimestamp.IsZero() {
		return snapshot.CreationTimestamp.Time
	}
	// A snapshot created in this reconcile may not have CreationTimestamp yet.
	// Treat it as newest so retention cannot delete it immediately.
	return time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
}

func exportSizeWarning(size int) string {
	if size > api.MaxExportBytes {
		return fmt.Sprintf("export is %d bytes, which exceeds the %d-byte etcd-safe limit", size, api.MaxExportBytes)
	}
	if size >= api.WarnExportBytes {
		return fmt.Sprintf("export is %d bytes; Kubernetes objects should stay well under 1.5MiB", size)
	}
	return ""
}
