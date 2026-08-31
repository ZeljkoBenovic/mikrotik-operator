package controller

import (
	"strings"
	"testing"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNextBackupTime(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 8, 31, 10, 15, 0, 0, time.UTC)
	got, err := nextBackupTime("0 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("next = %s, want %s", got, want)
	}
	if _, err := nextBackupTime("not a cron", from); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := nextBackupTime("", from); err == nil {
		t.Fatal("expected empty error")
	}
}

func TestBackupSnapshotName(t *testing.T) {
	t.Parallel()
	at := time.Unix(1700000000, 0).UTC()
	got := backupSnapshotName("nightly", at)
	if got != "nightly-1700000000" {
		t.Fatalf("name = %q", got)
	}
	long := strings.Repeat("a", 80)
	got = backupSnapshotName(long, at)
	if len(got) > 63 {
		t.Fatalf("name length %d > 63: %q", len(got), got)
	}
}

func TestSnapshotsToDelete(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	older := metav1.NewTime(now.Add(-2 * time.Hour))
	newest := metav1.NewTime(now.Add(-time.Minute))
	captured := api.MikroTikBackupStatus{Export: "/ip dns\n", CapturedAt: &now}
	snapshots := []api.MikroTikBackup{
		{ObjectMeta: metav1.ObjectMeta{Name: "old", CreationTimestamp: older}, Status: api.MikroTikBackupStatus{Export: "/ip dns\n", CapturedAt: &older}},
		{ObjectMeta: metav1.ObjectMeta{Name: "mid", CreationTimestamp: now}, Status: captured},
		{ObjectMeta: metav1.ObjectMeta{Name: "new", CreationTimestamp: newest}, Status: api.MikroTikBackupStatus{Export: "/ip dns\n", CapturedAt: &newest}},
		{ObjectMeta: metav1.ObjectMeta{Name: "policy", CreationTimestamp: older}, Spec: api.MikroTikBackupSpec{Schedule: "0 * * * *"}},
	}
	got := snapshotsToDelete(snapshots, 2)
	if len(got) != 1 || got[0].Name != "old" {
		t.Fatalf("delete = %#v", got)
	}
	if got := snapshotsToDelete(snapshots[:2], 5); len(got) != 0 {
		t.Fatalf("expected none, got %#v", got)
	}
	justCreated := []api.MikroTikBackup{
		{ObjectMeta: metav1.ObjectMeta{Name: "stale", CreationTimestamp: older}, Status: api.MikroTikBackupStatus{Export: "/ip dns\n"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "fresh"}},
	}
	got = snapshotsToDelete(justCreated, 1)
	if len(got) != 0 {
		t.Fatalf("captured snapshot must survive an empty newer child, delete = %#v", got)
	}
	got = snapshotsToDelete([]api.MikroTikBackup{
		{ObjectMeta: metav1.ObjectMeta{Name: "stale", CreationTimestamp: older}},
		{ObjectMeta: metav1.ObjectMeta{Name: "fresh"}},
	}, 1)
	if len(got) != 1 || got[0].Name != "stale" {
		t.Fatalf("zero CreationTimestamp empty snapshot must be kept as newest, delete = %#v", got)
	}
}

func TestSnapshotsToDeleteKeepsCapturedWhenNewEmpty(t *testing.T) {
	t.Parallel()
	now := metav1.NewTime(time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
	older := metav1.NewTime(now.Add(-2 * time.Hour))
	got := snapshotsToDelete([]api.MikroTikBackup{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "stale", CreationTimestamp: older},
			Status:     api.MikroTikBackupStatus{Export: "/ip address\n", CapturedAt: &older},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "nightly-new", CreationTimestamp: now},
		},
	}, 1)
	if len(got) != 0 {
		t.Fatalf("must not prune captured snapshot to keep empty child, delete = %#v", got)
	}
}

func TestExportSizeWarning(t *testing.T) {
	t.Parallel()
	if exportSizeWarning(100) != "" {
		t.Fatal("small export should not warn")
	}
	if exportSizeWarning(api.WarnExportBytes) == "" {
		t.Fatal("warn-size export should warn")
	}
	if exportSizeWarning(api.MaxExportBytes+1) == "" {
		t.Fatal("oversize export should warn")
	}
}
