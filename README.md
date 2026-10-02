# Pega Platform Operator for OpenShift

A Kubernetes operator that automates the deployment and lifecycle management of [Pega Platform](https://www.pega.com/products/pega-platform) on Red Hat OpenShift. Deploy a complete Pega stack — database, search, streaming, and application tiers — with a single custom resource.

## Components Managed

| Component | Description | Managed | External |
|-----------|-------------|---------|----------|
| **PostgreSQL** | Relational database for Pega rules and data schemas | Deploys a PostgreSQL pod with persistent storage | Provide a JDBC connection URL |
| **OpenSearch** | Search engine cluster for SRS | Deploys a 3-node StatefulSet with persistent storage | Provide an OpenSearch endpoint |
| **Apache Kafka** | Event streaming via Strimzi/AMQ Streams | Creates Kafka and KafkaNodePool CRs (requires AMQ Streams operator) | Provide a bootstrap server address |
| **SRS** | Pega Search and Reporting Service | Deploys SRS with configurable replicas | — |
| **Pega Installer** | One-time schema creation/upgrade job | Runs an init job before deploying tiers | — |
| **Platform Tiers** | Web, batch, and custom tier Deployments | Deploys with Services, Routes, and HPAs | — |

## Deployment Phases

The operator reconciles through ordered phases. Each phase includes health checks before advancing:

```
Pending → Database → Search → Streaming → SRS → Installing → Deploying → Ready
```

Monitor progress with:

```bash
oc get pegaplatform pega -o jsonpath='{.status.phase}'
```

---

## Prerequisites

- **OpenShift 4.14+**
- **AMQ Streams / Strimzi operator** installed on the cluster (if using managed Kafka)
- **Pega container images** obtained from Pega and loaded into an accessible registry
- **`oc` CLI** logged in to your cluster with cluster-admin privileges

---

## Installation

### Step 1: Load Pega Images into the OpenShift Internal Registry

Pega Platform images are proprietary and must be obtained from Pega. Once you have them locally, push them to the OpenShift internal registry in your target namespace.

**Expose the internal registry (if not already):**

```bash
oc patch configs.imageregistry.operator.openshift.io/cluster --type merge \
  -p '{"spec":{"defaultRoute":true}}'
```

**Get the registry route:**

```bash
REGISTRY=$(oc get route default-route -n openshift-image-registry -o jsonpath='{.spec.host}')
```

**Log in to the registry:**

```bash
podman login $REGISTRY -u $(oc whoami) -p $(oc whoami -t)
```

**Create the target namespace and push images:**

```bash
oc new-project pega-operator

# Tag and push each image (adjust versions to match your Pega release)
podman tag pega:25.1.1 $REGISTRY/pega-operator/pega:25.1.1
podman push $REGISTRY/pega-operator/pega:25.1.1

podman tag pega-installer:25.1.1 $REGISTRY/pega-operator/installer:25.1.1
podman push $REGISTRY/pega-operator/installer:25.1.1

podman tag pega-srs:1.40.0 $REGISTRY/pega-operator/srs:1.40.0
podman push $REGISTRY/pega-operator/srs:1.40.0
```

**Verify the image streams were created:**

```bash
oc get imagestream -n pega-operator
```

### Step 2: Install the Operator via OLM

**Apply the CatalogSource:**

```bash
oc apply -f config/olm/catalogsource.yaml
```

Wait for the catalog to become ready:

```bash
oc get catalogsource pega-operator-catalog -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}'
# Expected output: READY
```

**Apply the OperatorGroup and Subscription:**

```bash
oc apply -f config/olm/operatorgroup.yaml
oc apply -f config/olm/subscription.yaml
```

Wait for the operator to install:

```bash
oc get csv -n pega-operator -w
# Wait for pega-operator.v0.1.0 to show PHASE: Succeeded
```

Verify the operator pod is running:

```bash
oc get pods -n pega-operator -l control-plane=controller-manager
```

### Step 3: Create the PegaPlatform Custom Resource

Create a file called `pega-cr.yaml` with your configuration (see the [full example](#full-example-cr) and [API Reference](#api-reference) below):

```yaml
apiVersion: pega.openshift.io/v1alpha1
kind: PegaPlatform
metadata:
  name: pega
  namespace: pega-operator
spec:
  pegaImage: image-registry.openshift-image-registry.svc:5000/pega-operator/pega:25.1.1
  imageRegistry:
    url: image-registry.openshift-image-registry.svc:5000/pega-operator
  database:
    managed: true
    username: pega
    password: changeme
    storageSize: "100Gi"
  search:
    managed: true
    replicas: 3
    storageSize: "50Gi"
    javaOpts: "-Xms1g -Xmx1g"
  stream:
    enabled: true
    managed: true
    replicas: 3
    storageSize: "100Gi"
    replicationFactor: 3
  srs:
    enabled: true
    image: image-registry.openshift-image-registry.svc:5000/pega-operator/srs:1.40.0
    replicas: 2
  installer:
    image: image-registry.openshift-image-registry.svc:5000/pega-operator/installer:25.1.1
    adminPassword: Admin123!
  tiers:
    - name: web
      nodeType: WebUser
      replicas: 2
      resources:
        requests: { cpu: "3", memory: "12Gi" }
        limits: { cpu: "4", memory: "12Gi" }
      ingress:
        enabled: true
        routePrefix: pega
        tls: { enabled: true, termination: edge }
      hpa:
        enabled: true
        minReplicas: 2
        maxReplicas: 5
        targetCPUUtilization: 70
    - name: batch
      nodeType: "BackgroundProcessing,Search,Batch,RealTime,Custom1,Custom2,Custom3,Custom4,Custom5,BIX"
      replicas: 2
      resources:
        requests: { cpu: "2", memory: "12Gi" }
        limits: { cpu: "4", memory: "12Gi" }
      ingress:
        enabled: true
        routePrefix: pega-batch
        tls: { enabled: true, termination: edge }
```

**Apply it:**

```bash
oc apply -f pega-cr.yaml
```

**Monitor the deployment:**

```bash
# Watch the phase progression
oc get pegaplatform pega -w

# Watch all pods come up
oc get pods -n pega-operator -w
```

### Step 4: Access the Application

Once the phase reaches `Ready`, the operator creates OpenShift Routes for each tier with ingress enabled:

```bash
oc get routes -n pega-operator
```

The web tier is accessible at `https://<routePrefix>.apps.<cluster-domain>`. Log in with `administrator@pega.com` and the admin password you configured.

---

## Full Example CR

A complete CR with all options explicitly set is available in the CSV's `alm-examples` annotation. You can also create one from the OpenShift console by navigating to **Operators > Installed Operators > Pega Platform Operator > Create PegaPlatform**.

---

## API Reference

### PegaPlatformSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `pegaImage` | `string` | Yes | Pega web/batch tier container image |
| `imageRegistry` | [`ImageRegistrySpec`](#imageregistryspec) | Yes | Container image registry settings |
| `database` | [`DatabaseSpec`](#databasespec) | Yes | Database configuration |
| `search` | [`SearchSpec`](#searchspec) | Yes | OpenSearch configuration |
| `stream` | [`StreamSpec`](#streamspec) | Yes | Kafka streaming configuration |
| `srs` | [`SRSSpec`](#srsspec) | Yes | Search and Reporting Service configuration |
| `installer` | [`InstallerSpec`](#installerspec) | Yes | Pega installer job configuration |
| `tiers` | [`[]TierSpec`](#tierspec) | Yes | Platform tier definitions |

---

### ImageRegistrySpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | `string` | — | **Required.** Registry URL (e.g. `image-registry.openshift-image-registry.svc:5000/pega-operator`) |
| `pullSecretNames` | `[]string` | `[]` | Names of image pull secrets for private registries |

---

### DatabaseSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `managed` | `bool` | `true` | Deploy a managed PostgreSQL instance. Set to `false` for an external database. |
| `url` | `string` | — | JDBC connection URL. Required when `managed: false`. |
| `driverClass` | `string` | `org.postgresql.Driver` | JDBC driver class |
| `type` | `string` | `postgres` | Database type. One of: `postgres`, `mssql`, `oracledate`, `udb`, `db2zos` |
| `username` | `string` | — | Database username |
| `password` | `string` | — | Database password |
| `credentialsSecret` | `string` | — | Name of a Secret containing `DB_USERNAME` and `DB_PASSWORD` keys. Overrides `username`/`password`. |
| `driverUri` | `string` | `https://jdbc.postgresql.org/download/postgresql-42.7.5.jar` | JDBC driver download URL |
| `rulesSchema` | `string` | `rules` | Pega rules schema name |
| `dataSchema` | `string` | `data` | Pega data schema name |
| `storageSize` | `string` | `100Gi` | PVC size for managed PostgreSQL |
| `storageClass` | `string` | — | StorageClass for the managed PostgreSQL PVC |
| `image` | `string` | `registry.redhat.io/rhel8/postgresql-12:latest` | PostgreSQL container image (managed mode only) |

---

### SearchSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `managed` | `bool` | `true` | Deploy a managed OpenSearch cluster. Set to `false` for an external endpoint. |
| `url` | `string` | — | OpenSearch endpoint URL. Required when `managed: false`. |
| `port` | `int32` | `9200` | OpenSearch HTTP port |
| `protocol` | `string` | `http` | Connection protocol. One of: `http`, `https` |
| `username` | `string` | — | OpenSearch username |
| `password` | `string` | — | OpenSearch password |
| `credentialsSecret` | `string` | — | Name of a Secret containing `username` and `password` keys |
| `replicas` | `int32` | `3` | Number of OpenSearch replicas (managed mode) |
| `storageSize` | `string` | `30Gi` | PVC size per OpenSearch node |
| `storageClass` | `string` | — | StorageClass for OpenSearch PVCs |
| `javaOpts` | `string` | `-Xms512m -Xmx512m` | JVM options for OpenSearch |
| `image` | `string` | `docker.io/opensearchproject/opensearch:2.19.1` | OpenSearch container image |

---

### StreamSpec

Requires the **AMQ Streams** (Strimzi) operator to be installed when `managed: true`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Enable Kafka streaming |
| `managed` | `bool` | `true` | Deploy Kafka via Strimzi. Set to `false` for an external Kafka cluster. |
| `bootstrapServer` | `string` | — | Kafka bootstrap server address. Required when `managed: false`. |
| `securityProtocol` | `string` | `PLAINTEXT` | Security protocol. One of: `PLAINTEXT`, `SSL`, `SASL_PLAINTEXT`, `SASL_SSL` |
| `replicas` | `int32` | `3` | Number of Kafka broker replicas (managed mode) |
| `storageSize` | `string` | `100Gi` | PVC size per Kafka broker |
| `storageClass` | `string` | — | StorageClass for Kafka PVCs |
| `version` | `string` | `4.1.0` | Kafka version |
| `saslMechanism` | `string` | `PLAIN` | SASL mechanism. One of: `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`. Only used with `SASL_PLAINTEXT` or `SASL_SSL`. |
| `replicationFactor` | `int32` | `3` | Topic replication factor |

---

### SRSSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Enable SRS deployment |
| `image` | `string` | — | **Required.** SRS container image |
| `replicas` | `int32` | `2` | Number of SRS replicas |
| `deploymentName` | `string` | `pega-search` | SRS deployment name referenced by Pega tiers |
| `authEnabled` | `bool` | `false` | Enable authentication between SRS and Pega |

---

### InstallerSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `image` | `string` | — | **Required.** Pega installer container image |
| `adminPassword` | `string` | — | Initial password for `administrator@pega.com` |
| `adminPasswordSecret` | `string` | — | Name of a Secret containing an `ADMIN_PASSWORD` key. Overrides `adminPassword`. |
| `upgradeType` | `string` | `in-place` | Upgrade type. One of: `in-place`, `zero-downtime`, `custom`, `out-of-place-rules`, `out-of-place-data` |

---

### TierSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | `string` | — | **Required.** Tier name (e.g. `web`, `batch`) |
| `nodeType` | `string` | — | **Required.** Pega node type (e.g. `WebUser`, `BackgroundProcessing,Search,Batch`) |
| `replicas` | `int32` | `1` | Number of replicas |
| `resources` | `ResourceRequirements` | — | Pod CPU and memory requests/limits |
| `javaOpts` | `string` | — | JVM options for this tier |
| `ingress` | [`TierIngressSpec`](#tieringressspec) | — | Route configuration |
| `hpa` | [`HPASpec`](#hpaspec) | — | Horizontal Pod Autoscaler configuration |
| `service` | [`TierServiceSpec`](#tierservicespec) | — | Kubernetes Service configuration |

---

### TierIngressSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Enable OpenShift Route creation |
| `domain` | `string` | — | Full hostname override. Leave empty to auto-generate from `routePrefix` + cluster domain. |
| `routePrefix` | `string` | `pega` | Prefix for the auto-generated Route hostname (e.g. `pega` produces `pega.apps.cluster.example.com`). **Use a unique prefix per tier** to avoid hostname conflicts. |
| `tls` | [`TLSSpec`](#tlsspec) | — | TLS termination configuration |

### TLSSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Enable TLS on the Route |
| `termination` | `string` | `edge` | TLS termination type. One of: `edge`, `reencrypt`, `passthrough` |

---

### HPASpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | `bool` | `true` | Enable Horizontal Pod Autoscaler |
| `minReplicas` | `int32` | `1` | Minimum number of replicas |
| `maxReplicas` | `int32` | `5` | Maximum number of replicas |
| `targetCPUUtilization` | `int32` | `70` | Target CPU utilization percentage |

---

### TierServiceSpec

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | `int32` | `80` | Service port |
| `targetPort` | `int32` | `8080` | Container target port |
| `httpEnabled` | `bool` | `true` | Enable HTTP |

---

## Status

The `PegaPlatform` status provides visibility into the deployment lifecycle:

| Field | Description |
|-------|-------------|
| `phase` | Current phase: `Pending`, `Database`, `Search`, `Streaming`, `SRS`, `Installing`, `Deploying`, `Ready`, `Failed` |
| `conditions` | Standard Kubernetes conditions for each component (`DatabaseReady`, `SearchReady`, `StreamReady`, `SRSReady`, `InstallerComplete`, `PlatformReady`) |
| `webURL` | URL to access the Pega web interface (populated when Ready) |
| `message` | Human-readable description of the current state |
| `installerJobName` | Name of the installer Job |

```bash
# View full status
oc get pegaplatform pega -o yaml

# Quick phase check
oc get pega
```

---

## Uninstalling

Remove the deployment in reverse order:

```bash
# Delete the PegaPlatform CR (removes all managed workloads)
oc delete pegaplatform pega -n pega-operator

# Delete PVCs if you want to remove persistent data
oc delete pvc --all -n pega-operator

# Remove the operator
oc delete subscription.operators.coreos.com pega-operator -n pega-operator
oc delete csv pega-operator.v0.1.0 -n pega-operator
oc delete installplan --all -n pega-operator

# Remove the catalog
oc delete catalogsource pega-operator-catalog -n openshift-marketplace
```

---

## Development

### Building from Source

```bash
# Build the operator binary
make build

# Build and push the operator image
podman build --no-cache --platform linux/amd64 -t quay.io/<your-repo>/pega-operator:v0.1.0 -f Dockerfile .
podman push quay.io/<your-repo>/pega-operator:v0.1.0

# Build and push the OLM bundle
podman build --no-cache -f bundle.Dockerfile -t quay.io/<your-repo>/pega-operator-bundle:v0.1.0 .
podman push quay.io/<your-repo>/pega-operator-bundle:v0.1.0

# Build and push the FBC catalog
podman build --no-cache -f catalog.Dockerfile -t quay.io/<your-repo>/pega-operator-catalog:v0.1.0 .
podman push quay.io/<your-repo>/pega-operator-catalog:v0.1.0
```

### Project Structure

```
.
├── api/v1alpha1/          # CRD types and API definitions
├── bundle/                # OLM bundle (CSV, CRD manifests, metadata)
├── catalog/               # File-based catalog (FBC) for OLM
├── config/
│   ├── crd/               # Generated CRD bases
│   ├── manager/           # Operator deployment manifest
│   ├── olm/               # CatalogSource, OperatorGroup, Subscription
│   └── rbac/              # Generated RBAC manifests
├── internal/controller/   # Reconciliation logic per component
├── Dockerfile             # Operator container image
├── bundle.Dockerfile      # OLM bundle image
└── catalog.Dockerfile     # FBC catalog image
```

## License

Copyright Pegasystems Inc.
