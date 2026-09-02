package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestMikroTikBackupSpec_IsPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		schedule string
		want     bool
	}{
		{name: "empty is snapshot", schedule: "", want: false},
		{name: "whitespace is snapshot", schedule: "  ", want: false},
		{name: "cron is policy", schedule: "0 * * * *", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MikroTikBackupSpec{Schedule: tt.schedule}.IsPolicy()
			if got != tt.want {
				t.Fatalf("IsPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMikroTikBackupSpec_RetentionCount(t *testing.T) {
	t.Parallel()
	three := int32(3)
	zero := int32(0)
	tests := []struct {
		name      string
		retention *int32
		want      int32
	}{
		{name: "default when unset", retention: nil, want: DefaultBackupRetention},
		{name: "default when zero", retention: &zero, want: DefaultBackupRetention},
		{name: "explicit count", retention: &three, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MikroTikBackupSpec{Retention: tt.retention}.RetentionCount()
			if got != tt.want {
				t.Fatalf("RetentionCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMikroTikBackupSpec_RemoteEnabled(t *testing.T) {
	t.Parallel()
	if (MikroTikBackupSpec{}).RemoteEnabled() {
		t.Fatal("nil remote must not be enabled")
	}
	if (MikroTikBackupSpec{Remote: &BackupRemoteSpec{}}).RemoteEnabled() {
		t.Fatal("enabled false must not report enabled")
	}
	if !(MikroTikBackupSpec{Remote: &BackupRemoteSpec{Enabled: true}}).RemoteEnabled() {
		t.Fatal("enabled true must report enabled")
	}
}

func TestMikroTikRestoreSpec_Confirmed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		confirm string
		want    bool
	}{
		{name: "empty", confirm: "", want: false},
		{name: "true is not enough", confirm: "true", want: false},
		{name: "lowercase restore", confirm: "restore", want: false},
		{name: "RESTORE", confirm: RestoreConfirmValue, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MikroTikRestoreSpec{Confirm: tt.confirm}.Confirmed()
			if got != tt.want {
				t.Fatalf("Confirmed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMikroTikRestoreSpec_UsesInlineConnection(t *testing.T) {
	t.Parallel()
	if (MikroTikRestoreSpec{}).UsesInlineConnection() {
		t.Fatal("missing connection must be false")
	}
	partial := MikroTikRestoreSpec{Connection: &RestoreConnectionSpec{Address: "192.0.2.1"}}
	if partial.UsesInlineConnection() {
		t.Fatal("address without secret must be false")
	}
	ok := MikroTikRestoreSpec{Connection: &RestoreConnectionSpec{
		Address:           "192.0.2.1",
		CredentialsSecret: corev1.LocalObjectReference{Name: "creds"},
	}}
	if !ok.UsesInlineConnection() {
		t.Fatal("address and secret must be true")
	}
}
