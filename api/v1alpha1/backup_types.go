package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// RestoreConfirmValue is the only spec.confirm value that authorizes /import.
	RestoreConfirmValue = "RESTORE"

	BackupRolePolicy   = "Policy"
	BackupRoleSnapshot = "Snapshot"

	BackupPolicyLabel             = "mikrotik.operator.io/backup-policy"
	BackupRouterNameLabel         = "mikrotik.operator.io/backup-router-name"
	BackupRouterNamespaceLabel    = "mikrotik.operator.io/backup-router-namespace"
	ConditionRemoteNotImplemented = "RemoteStorageNotImplemented"
	ConditionImportInProgress     = "ImportInProgress"

	// MaxExportBytes is below the typical 1.5MiB Kubernetes object limit so
	// metadata and conditions still fit in etcd with the export body.
	MaxExportBytes  = 1_048_576
	WarnExportBytes = 524_288

	DefaultBackupRetention int32 = 5
)

// MikroTikBackupSpec describes a one-shot snapshot or a scheduled backup policy.
// A nonempty schedule makes this object a policy that owns snapshot children
// of the same kind. An empty schedule takes one /export snapshot into status.
//
// +kubebuilder:validation:XValidation:rule="!has(self.remote) || !has(self.remote.enabled) || self.remote.enabled == false",message="remote backup storage is not implemented; spec.remote.enabled must be false"
type MikroTikBackupSpec struct {
	// RouterRef selects the MikroTikRouter whose configuration is exported.
	// +kubebuilder:validation:MinLength=1
	RouterRef string `json:"routerRef"`
	// Schedule is a standard five-field cron expression. Empty means run once.
	Schedule string `json:"schedule,omitempty"`
	// Retention is how many owned snapshot children to keep. Used by policies.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Retention *int32 `json:"retention,omitempty"`
	// Remote reserves a future FTP/SMB/S3 destination. Enabling it is rejected.
	Remote *BackupRemoteSpec `json:"remote,omitempty"`
}

type BackupRemoteSpec struct {
	Enabled bool             `json:"enabled,omitempty"`
	FTP     *BackupRemoteFTP `json:"ftp,omitempty"`
	SMB     *BackupRemoteSMB `json:"smb,omitempty"`
	S3      *BackupRemoteS3  `json:"s3,omitempty"`
}

type BackupRemoteFTP struct {
	Address           string                      `json:"address,omitempty"`
	Path              string                      `json:"path,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`
}

type BackupRemoteSMB struct {
	Share             string                      `json:"share,omitempty"`
	Path              string                      `json:"path,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`
}

type BackupRemoteS3 struct {
	Bucket            string                      `json:"bucket,omitempty"`
	Prefix            string                      `json:"prefix,omitempty"`
	Endpoint          string                      `json:"endpoint,omitempty"`
	Region            string                      `json:"region,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret,omitempty"`
}

type MikroTikBackupStatus struct {
	Role      string `json:"role,omitempty"`
	RouterRef string `json:"routerRef,omitempty"`
	// Export is a RouterOS /export script and may contain passwords and
	// certificates. It is stored in etcd on the snapshot object.
	// +kubebuilder:validation:MaxLength=1048576
	Export             string             `json:"export,omitempty"`
	ExportBytes        int64              `json:"exportBytes,omitempty"`
	ExportWarning      string             `json:"exportWarning,omitempty"`
	CapturedAt         *metav1.Time       `json:"capturedAt,omitempty"`
	LastScheduleTime   *metav1.Time       `json:"lastScheduleTime,omitempty"`
	NextScheduleTime   *metav1.Time       `json:"nextScheduleTime,omitempty"`
	SnapshotCount      int32              `json:"snapshotCount,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// MikroTikBackup stores an /export snapshot or owns scheduled snapshot
// children. The export lives only in Kubernetes; there is no RouterOS object
// to finalize, so this kind does not use a deletion finalizer.
type MikroTikBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikBackupSpec   `json:"spec"`
	Status            MikroTikBackupStatus `json:"status,omitempty"`
}

type MikroTikBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikBackup `json:"items"`
}

func (s MikroTikBackupSpec) IsPolicy() bool {
	return strings.TrimSpace(s.Schedule) != ""
}

func (s MikroTikBackupSpec) RetentionCount() int32 {
	if s.Retention == nil || *s.Retention < 1 {
		return DefaultBackupRetention
	}
	return *s.Retention
}

func (s MikroTikBackupSpec) RemoteEnabled() bool {
	return s.Remote != nil && s.Remote.Enabled
}

// MikroTikRestoreSpec applies a stored /export onto a router after confirmation.
//
// +kubebuilder:validation:XValidation:rule="(has(self.routerRef) && size(self.routerRef) > 0) != (has(self.connection) && has(self.connection.address) && size(self.connection.address) > 0 && has(self.connection.credentialsSecret) && size(self.connection.credentialsSecret.name) > 0)",message="spec must set exactly one of routerRef or connection.address with credentialsSecret"
type MikroTikRestoreSpec struct {
	BackupRef NamespacedName `json:"backupRef"`
	// RouterRef targets an existing MikroTikRouter (name or namespace/name).
	RouterRef string `json:"routerRef,omitempty"`
	// Connection targets a router that is not yet represented by a Router CR.
	Connection *RestoreConnectionSpec `json:"connection,omitempty"`
	// Confirm must equal RESTORE before the operator runs /import.
	Confirm string `json:"confirm,omitempty"`
}

type RestoreConnectionSpec struct {
	// +kubebuilder:validation:MinLength=1
	Address           string                      `json:"address"`
	Port              int32                       `json:"port,omitempty"`
	TLS               bool                        `json:"tls,omitempty"`
	CredentialsSecret corev1.LocalObjectReference `json:"credentialsSecret"`
}

type MikroTikRestoreStatus struct {
	Applied            bool               `json:"applied"`
	AppliedAt          *metav1.Time       `json:"appliedAt,omitempty"`
	BackupUID          string             `json:"backupUID,omitempty"`
	Target             string             `json:"target,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// MikroTikRestore applies a stored /export with /import. Deleting the Restore
// object does not undo device changes, so this kind does not use a finalizer.
type MikroTikRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MikroTikRestoreSpec   `json:"spec"`
	Status            MikroTikRestoreStatus `json:"status,omitempty"`
}

type MikroTikRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MikroTikRestore `json:"items"`
}

func (s MikroTikRestoreSpec) Confirmed() bool {
	return s.Confirm == RestoreConfirmValue
}

func (s MikroTikRestoreSpec) UsesInlineConnection() bool {
	return s.Connection != nil && s.Connection.Address != "" && s.Connection.CredentialsSecret.Name != ""
}
