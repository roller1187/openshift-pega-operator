package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:resource:shortName=pega

type PegaPlatform struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PegaPlatformSpec   `json:"spec,omitempty"`
	Status PegaPlatformStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type PegaPlatformList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PegaPlatform `json:"items"`
}

type PegaPlatformSpec struct {
	// Database configuration for the Pega rules and data schemas
	Database DatabaseSpec `json:"database"`

	// Search engine (OpenSearch) configuration
	Search SearchSpec `json:"search"`

	// Kafka streaming configuration
	Stream StreamSpec `json:"stream"`

	// Search and Reporting Service configuration
	SRS SRSSpec `json:"srs"`

	// Pega installer configuration (schema creation / upgrade)
	Installer InstallerSpec `json:"installer"`

	// Platform tier definitions (web, batch, etc.)
	Tiers []TierSpec `json:"tiers"`

	// Container image registry settings
	ImageRegistry ImageRegistrySpec `json:"imageRegistry"`

	// Pega platform container image
	// +kubebuilder:validation:Required
	PegaImage string `json:"pegaImage"`
}

// DatabaseSpec configures the relational database backend.
// Set Managed=true to have the operator deploy PostgreSQL, or provide
// external connection details.
type DatabaseSpec struct {
	// Deploy a managed PostgreSQL instance
	// +kubebuilder:default=true
	Managed bool `json:"managed,omitempty"`

	// JDBC connection URL (required when Managed=false)
	// +optional
	URL string `json:"url,omitempty"`

	// JDBC driver class
	// +kubebuilder:default="org.postgresql.Driver"
	DriverClass string `json:"driverClass,omitempty"`

	// Database type: postgres, mssql, oracledate, udb, db2zos
	// +kubebuilder:default="postgres"
	// +kubebuilder:validation:Enum=postgres;mssql;oracledate;udb;db2zos
	Type string `json:"type,omitempty"`

	// Database username
	// +optional
	Username string `json:"username,omitempty"`

	// Database password
	// +optional
	Password string `json:"password,omitempty"`

	// Reference to a Secret containing DB_USERNAME and DB_PASSWORD keys
	// +optional
	CredentialsSecret string `json:"credentialsSecret,omitempty"`

	// JDBC driver download URL
	// +kubebuilder:default="https://jdbc.postgresql.org/download/postgresql-42.7.5.jar"
	DriverURI string `json:"driverUri,omitempty"`

	// Pega rules schema name
	// +kubebuilder:default="rules"
	RulesSchema string `json:"rulesSchema,omitempty"`

	// Pega data schema name
	// +kubebuilder:default="data"
	DataSchema string `json:"dataSchema,omitempty"`

	// Storage size for managed PostgreSQL PVC
	// +kubebuilder:default="100Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// StorageClass for managed PostgreSQL PVC
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// PostgreSQL container image (managed mode only)
	// +kubebuilder:default="registry.redhat.io/rhel8/postgresql-12:latest"
	Image string `json:"image,omitempty"`
}

// SearchSpec configures the OpenSearch cluster used by SRS.
type SearchSpec struct {
	// Deploy a managed OpenSearch cluster
	// +kubebuilder:default=true
	Managed bool `json:"managed,omitempty"`

	// OpenSearch endpoint (required when Managed=false)
	// +optional
	URL string `json:"url,omitempty"`

	// OpenSearch port
	// +kubebuilder:default=9200
	Port int32 `json:"port,omitempty"`

	// Connection protocol
	// +kubebuilder:default="http"
	// +kubebuilder:validation:Enum=http;https
	Protocol string `json:"protocol,omitempty"`

	// OpenSearch username
	// +optional
	Username string `json:"username,omitempty"`

	// OpenSearch password
	// +optional
	Password string `json:"password,omitempty"`

	// Reference to a Secret containing username and password keys
	// +optional
	CredentialsSecret string `json:"credentialsSecret,omitempty"`

	// Number of OpenSearch replicas (managed mode)
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas,omitempty"`

	// PVC size per OpenSearch node
	// +kubebuilder:default="30Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// StorageClass for OpenSearch PVCs
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// JVM options for OpenSearch
	// +kubebuilder:default="-Xms512m -Xmx512m"
	JavaOpts string `json:"javaOpts,omitempty"`

	// OpenSearch container image
	// +kubebuilder:default="docker.io/opensearchproject/opensearch:2.19.1"
	Image string `json:"image,omitempty"`
}

// StreamSpec configures Kafka streaming via Strimzi/AMQ Streams.
type StreamSpec struct {
	// Enable Kafka streaming
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Deploy Kafka via Strimzi (requires AMQ Streams / Strimzi operator)
	// +kubebuilder:default=true
	Managed bool `json:"managed,omitempty"`

	// Kafka bootstrap server address (required when Managed=false)
	// +optional
	BootstrapServer string `json:"bootstrapServer,omitempty"`

	// Security protocol: PLAINTEXT, SSL, SASL_PLAINTEXT, SASL_SSL
	// +kubebuilder:default="PLAINTEXT"
	SecurityProtocol string `json:"securityProtocol,omitempty"`

	// Kafka broker replicas (managed mode)
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas,omitempty"`

	// PVC size per Kafka broker
	// +kubebuilder:default="100Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// StorageClass for Kafka PVCs
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Kafka version
	// +kubebuilder:default="4.1.0"
	Version string `json:"version,omitempty"`

	// SASL mechanism: PLAIN, SCRAM-SHA-256, SCRAM-SHA-512 (only used with SASL_PLAINTEXT or SASL_SSL)
	// +kubebuilder:default="PLAIN"
	SASLMechanism string `json:"saslMechanism,omitempty"`

	// Topic replication factor
	// +kubebuilder:default=3
	ReplicationFactor int32 `json:"replicationFactor,omitempty"`

}

// SRSSpec configures the Pega Search and Reporting Service.
type SRSSpec struct {
	// Enable SRS deployment
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// SRS container image
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// Number of SRS replicas
	// +kubebuilder:default=2
	Replicas int32 `json:"replicas,omitempty"`

	// SRS deployment name (used as a reference from Pega tiers)
	// +kubebuilder:default="pega-search"
	DeploymentName string `json:"deploymentName,omitempty"`

	// Enable authentication between SRS and Pega
	// +kubebuilder:default=false
	AuthEnabled bool `json:"authEnabled,omitempty"`
}

// InstallerSpec configures the one-time Pega schema installer job.
type InstallerSpec struct {
	// Pega installer container image
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// Initial administrator password (administrator@pega.com)
	// +optional
	AdminPassword string `json:"adminPassword,omitempty"`

	// Secret containing ADMIN_PASSWORD key
	// +optional
	AdminPasswordSecret string `json:"adminPasswordSecret,omitempty"`

	// Upgrade type: in-place, zero-downtime, custom, out-of-place-rules, out-of-place-data
	// +kubebuilder:default="in-place"
	UpgradeType string `json:"upgradeType,omitempty"`

	// Image used by the init container that downloads the JDBC driver.
	// The Pega installer's own curl cannot complete a TLS handshake on
	// FIPS-enabled clusters, so the download is delegated to this image and
	// handed to the installer as a local file. Override to a mirrored image
	// on disconnected clusters.
	// +kubebuilder:default="registry.access.redhat.com/ubi9/ubi-minimal:latest"
	DriverDownloadImage string `json:"driverDownloadImage,omitempty"`
}

// TierSpec defines a Pega platform tier (web, batch, stream, etc.).
type TierSpec struct {
	// Tier name
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Pega node type
	// +kubebuilder:validation:Required
	NodeType string `json:"nodeType"`

	// Number of replicas
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas"`

	// Pod resource requirements
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Ingress / Route configuration (typically for web tier)
	// +optional
	Ingress *TierIngressSpec `json:"ingress,omitempty"`

	// Horizontal Pod Autoscaler configuration
	// +optional
	HPA *HPASpec `json:"hpa,omitempty"`

	// JVM options
	// +optional
	JavaOpts string `json:"javaOpts,omitempty"`

	// Service configuration
	// +optional
	Service *TierServiceSpec `json:"service,omitempty"`
}

// TierServiceSpec configures the Kubernetes Service for a tier.
type TierServiceSpec struct {
	// +kubebuilder:default=80
	Port int32 `json:"port,omitempty"`

	// +kubebuilder:default=8080
	TargetPort int32 `json:"targetPort,omitempty"`

	// +kubebuilder:default=true
	HTTPEnabled bool `json:"httpEnabled,omitempty"`
}

// TierIngressSpec configures external access for a tier via an OpenShift Route.
type TierIngressSpec struct {
	// Enable Route creation
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Hostname for the Route (leave empty to auto-detect from cluster domain using routePrefix)
	// +optional
	Domain string `json:"domain,omitempty"`

	// Prefix prepended to the cluster apps domain when auto-generating the Route hostname
	// +kubebuilder:default="pega"
	RoutePrefix string `json:"routePrefix,omitempty"`

	// TLS configuration
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`
}

// TLSSpec configures TLS termination on the Route.
type TLSSpec struct {
	// Enable TLS
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// TLS termination type: edge, reencrypt, passthrough
	// +kubebuilder:default="edge"
	// +kubebuilder:validation:Enum=edge;reencrypt;passthrough
	Termination string `json:"termination,omitempty"`
}

// HPASpec configures horizontal pod autoscaling.
type HPASpec struct {
	// Enable HPA
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// +kubebuilder:default=1
	MinReplicas int32 `json:"minReplicas,omitempty"`

	// +kubebuilder:default=5
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// Target CPU utilization percentage
	// +kubebuilder:default=70
	TargetCPUUtilization int32 `json:"targetCPUUtilization,omitempty"`
}

// ImageRegistrySpec configures the container image registry.
type ImageRegistrySpec struct {
	// Registry URL (e.g. image-registry.openshift-image-registry.svc:5000/pega)
	// +kubebuilder:validation:Required
	URL string `json:"url"`

	// Image pull secret names
	// +optional
	PullSecretNames []string `json:"pullSecretNames,omitempty"`
}

// PegaPlatformStatus defines the observed state of PegaPlatform.
type PegaPlatformStatus struct {
	// Current deployment phase
	// +kubebuilder:validation:Enum=Pending;Database;Search;Streaming;SRS;Installing;Deploying;Ready;Failed
	Phase PegaPhase `json:"phase,omitempty"`

	// Standard Kubernetes conditions
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Name of the installer Job (if running or completed)
	// +optional
	InstallerJobName string `json:"installerJobName,omitempty"`

	// URL to access the Pega web interface
	// +optional
	WebURL string `json:"webURL,omitempty"`

	// Human-readable message about the current state
	// +optional
	Message string `json:"message,omitempty"`
}

// PegaPhase represents the deployment lifecycle phase.
// +kubebuilder:validation:Enum=Pending;Database;Search;Streaming;SRS;Installing;Deploying;Ready;Failed
type PegaPhase string

const (
	PhasePending    PegaPhase = "Pending"
	PhaseDatabase   PegaPhase = "Database"
	PhaseSearch     PegaPhase = "Search"
	PhaseStreaming  PegaPhase = "Streaming"
	PhaseSRS        PegaPhase = "SRS"
	PhaseInstalling PegaPhase = "Installing"
	PhaseDeploying  PegaPhase = "Deploying"
	PhaseReady      PegaPhase = "Ready"
	PhaseFailed     PegaPhase = "Failed"
)

const (
	ConditionDatabaseReady  = "DatabaseReady"
	ConditionSearchReady    = "SearchReady"
	ConditionStreamReady    = "StreamReady"
	ConditionSRSReady       = "SRSReady"
	ConditionInstallerDone  = "InstallerComplete"
	ConditionPlatformReady  = "PlatformReady"
)

func init() {
	SchemeBuilder.Register(&PegaPlatform{}, &PegaPlatformList{})
}
