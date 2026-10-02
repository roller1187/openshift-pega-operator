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

### Step 3a: Deploy Pega Platform from the OpenShift Console

The easiest way to deploy is through the OpenShift web console with a guided form:

1. Navigate to **Operators > Installed Operators** and select **Pega Platform Operator**
2. Click the **Pega Platform** tab, then **Create PegaPlatform**
3. The form is pre-populated with sensible defaults. Fill in the required fields:
   - **Pega Web/Batch Tier Image** — your internal registry path (e.g. `image-registry.openshift-image-registry.svc:5000/pega-operator/pega:25.1.1`)
   - **Pega Installer Image** — your internal registry path for the installer
   - **Pega SRS Image** — your internal registry path for SRS
   - **Image Registry URL** — the base registry path (e.g. `image-registry.openshift-image-registry.svc:5000/pega-operator`)
   - **Database Username / Password** — credentials for the managed PostgreSQL
   - **Admin Password** — initial password for `administrator@pega.com`
4. Configure the **Platform Tiers** section. The default example includes a `web` tier and a `batch` tier. Make sure each tier with ingress enabled has a **unique Route Prefix** (e.g. `pega` for web, `pega-batch` for batch)
5. Click **Create**

Monitor the deployment from the console under **Workloads > Pods**, or from the CLI:

```bash
oc get pegaplatform pega -w
```

### Step 3b: Deploy Pega Platform from the CLI

For scripted or repeatable deployments, create a YAML file with your full configuration.

**Create `pega-cr.yaml`:**

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

See the [API Reference](#api-reference) below for all configurable fields.

### Step 4: Access the Application

Once the phase reaches `Ready`, the operator creates OpenShift Routes for each tier with ingress enabled:

```bash
oc get routes -n pega-operator
```

The web tier is accessible at `https://<routePrefix>.apps.<cluster-domain>`. Log in with `administrator@pega.com` and the admin password you configured.

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

---

## Operator Development

> **This section is for operator developers** who need to modify the operator's behavior, add new features, or fix bugs. If you are only deploying Pega Platform, you can skip this section entirely.

### Project Structure

```
.
├── api/v1alpha1/                # CRD type definitions (PegaPlatformSpec, status, etc.)
│   └── types.go                 # All API types — start here when adding new CR fields
├── internal/controller/         # Reconciliation logic, one file per component
│   ├── pegaplatform_controller.go  # Main reconcile loop, phase orchestration
│   ├── database.go              # Managed PostgreSQL deployment
│   ├── search.go                # Managed OpenSearch StatefulSet + services
│   ├── streaming.go             # Kafka via Strimzi (KRaft mode, unstructured CRs)
│   ├── srs.go                   # Search and Reporting Service deployment
│   ├── installer.go             # One-time Pega schema installer job
│   └── platform.go              # Web/batch tier Deployments, Services, Routes, HPAs
├── bundle/                      # OLM bundle
│   ├── manifests/
│   │   ├── pega-operator.clusterserviceversion.yaml  # CSV — RBAC, deployment spec, UI form
│   │   └── pega.openshift.io_pegaplatforms.yaml      # Generated CRD
│   └── metadata/
│       └── annotations.yaml
├── catalog/                     # File-based catalog (FBC) for OLM
│   └── pega-operator/
│       └── catalog.yaml         # Package, channel, and bundle references
├── config/
│   ├── crd/bases/               # controller-gen output (CRD YAML)
│   ├── manager/                 # Operator Deployment manifest (used by kustomize)
│   ├── olm/                     # CatalogSource, OperatorGroup, Subscription
│   └── rbac/                    # controller-gen output (RBAC YAML)
├── Dockerfile                   # Operator container image (multi-stage, cross-compiled)
├── bundle.Dockerfile            # OLM bundle image (FROM scratch)
├── catalog.Dockerfile           # FBC catalog image (uses opm serve)
└── main.go                      # Entrypoint — manager setup, scheme registration
```

### Development Workflow

Every change to the operator follows the same cycle: **edit → build → push → redeploy**. There are three container images that must stay in sync:

| Image | Purpose | When to rebuild |
|-------|---------|-----------------|
| **Operator** (`pega-operator`) | The controller binary | Any Go code change |
| **Bundle** (`pega-operator-bundle`) | OLM metadata (CSV, CRD) | CRD field changes, RBAC changes, CSV edits |
| **Catalog** (`pega-operator-catalog`) | OLM package index | Only if `catalog/pega-operator/catalog.yaml` changes (rare) |

When in doubt, rebuild all three. It takes under two minutes.

### Step-by-Step: Making a Code Change

#### 1. Edit the code

Most changes fall into one of these categories:

**Adding a new CR field:**
1. Add the field to the appropriate struct in `api/v1alpha1/types.go` with kubebuilder annotations
2. Run `make manifests` to regenerate the CRD YAML in `config/crd/bases/`
3. Copy the updated CRD to the bundle: `cp config/crd/bases/pega.openshift.io_pegaplatforms.yaml bundle/manifests/`
4. Use the new field in the relevant controller file under `internal/controller/`
5. Update `alm-examples` in the CSV if the field should appear in the console form

**Changing reconciliation logic:**
1. Edit the appropriate file in `internal/controller/`
2. If you need new Kubernetes API access (e.g. a new resource type), add RBAC markers to `pegaplatform_controller.go` and update the CSV's `clusterPermissions` section

**Changing RBAC permissions:**
1. Add kubebuilder RBAC markers to `internal/controller/pegaplatform_controller.go`
2. Run `make manifests` to regenerate RBAC
3. **Also** update the `clusterPermissions` section in `bundle/manifests/pega-operator.clusterserviceversion.yaml` — OLM uses the CSV for RBAC, not the generated role

#### 2. Build and push all three images

Always use `--no-cache` to avoid stale layers. The operator image must be cross-compiled for `linux/amd64` if you are building on macOS (ARM).

```bash
# 1. Operator image
podman build --no-cache --platform linux/amd64 \
  -t quay.io/<your-repo>/pega-operator:v0.1.0 -f Dockerfile .
podman push quay.io/<your-repo>/pega-operator:v0.1.0

# 2. Bundle image
podman build --no-cache \
  -t quay.io/<your-repo>/pega-operator-bundle:v0.1.0 -f bundle.Dockerfile .
podman push quay.io/<your-repo>/pega-operator-bundle:v0.1.0

# 3. Catalog image (file-based catalog — do NOT use `opm index add`)
podman build --no-cache \
  -t quay.io/<your-repo>/pega-operator-catalog:v0.1.0 -f catalog.Dockerfile .
podman push quay.io/<your-repo>/pega-operator-catalog:v0.1.0
```

> **Important:** Do not bump the image tag just to force a fresh pull. The CSV specifies `imagePullPolicy: Always`, so OpenShift nodes always pull the latest image for the same tag.

#### 3. Redeploy on the cluster

For a clean redeploy (recommended when testing significant changes):

```bash
# Remove the existing deployment
oc delete pegaplatform pega -n pega-operator --ignore-not-found
oc delete subscription.operators.coreos.com pega-operator -n pega-operator --ignore-not-found
oc delete csv pega-operator.v0.1.0 -n pega-operator --ignore-not-found
oc delete installplan --all -n pega-operator --ignore-not-found
oc delete catalogsource pega-operator-catalog -n openshift-marketplace --ignore-not-found
oc delete pvc --all -n pega-operator  # Only if you want to reset persistent data

# Reinstall
oc apply -f config/olm/catalogsource.yaml
# Wait for READY:
oc get catalogsource pega-operator-catalog -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}'

oc apply -f config/olm/operatorgroup.yaml
oc apply -f config/olm/subscription.yaml
# Wait for CSV Succeeded, then create the CR
```

For a quick iteration (operator code only, no CRD/RBAC changes):

```bash
# Just restart the operator pod to pick up the new image
oc delete pod -n pega-operator -l control-plane=controller-manager
```

### Key Architecture Decisions

**Phased reconciliation:** The main reconcile loop in `pegaplatform_controller.go` steps through phases sequentially. Each phase function returns `(ready bool, err error)`. If not ready, the controller requeues after 30 seconds. Once all phases are ready, it requeues every 60 seconds for drift detection.

**createOrUpdate pattern:** The `createOrUpdate` helper in `search.go` handles idempotent resource management — it creates if not found, updates if changed, and skips if unchanged. This is used across all controllers.

**OpenSearch on OpenShift:** The OpenSearch StatefulSet requires special handling:
- A ServiceAccount with a RoleBinding to `system:openshift:scc:nonroot-v2` (OpenSearch runs as UID 1000)
- `podManagementPolicy: Parallel` to avoid readiness deadlock during cluster bootstrap
- `publishNotReadyAddresses: true` on the headless Service for DNS resolution before pods pass readiness
- FQDN seed hosts (`pod-name.service.namespace.svc.cluster.local`) for reliable cluster discovery

**Kafka via unstructured CRs:** The streaming controller uses `unstructured.Unstructured` objects to create Strimzi `Kafka` and `KafkaNodePool` CRs. This avoids importing Strimzi's Go types as a dependency.

### Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| `Exec format error` on the operator pod | Built on macOS ARM without `--platform linux/amd64` | Always pass `--platform linux/amd64` to `podman build` |
| RBAC errors after adding new resource access | CSV `clusterPermissions` not updated | Update both the kubebuilder markers AND the CSV manually |
| Stale operator code running after push | OLM cached the old CSV/installplan | Delete subscription + CSV + installplan, then reinstall |
| OpenSearch pods stuck at 0/1 Ready | Missing `publishNotReadyAddresses` or sequential pod management | Ensure headless Service has `publishNotReadyAddresses: true` and StatefulSet uses `Parallel` pod management |
| Route hostname conflict (Rejected) | Two tiers using the same `routePrefix` | Use unique `routePrefix` per tier (e.g. `pega`, `pega-batch`) |

## License

Copyright Pegasystems Inc.
