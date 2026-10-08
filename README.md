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

## Namespace Layout

The operator and the workloads it manages live in **separate namespaces**:

| Namespace | Contains |
|-----------|----------|
| `pega-operator` | The operator controller pod, its CSV, Subscription, and OperatorGroup |
| `pega` | The `PegaPlatform` CR and everything it creates — PostgreSQL, OpenSearch, Kafka, SRS, the installer Job, application tiers, Routes, and the Pega container images |

The CSV carries an `operatorframework.io/suggested-namespace: pega-operator` annotation, so the install form defaults to that namespace — but you can install anywhere you like; it is a default, not a constraint.

The operator's `OperatorGroup` uses `spec: {}` (AllNamespaces scope). This is deliberate: the controller-runtime manager in `main.go` sets no cache restriction, so it watches every namespace, and the CSV grants cluster-scoped RBAC. Declaring AllNamespaces keeps the manifest consistent with actual behavior and lets you put the `PegaPlatform` CR in any namespace you want — `pega` is just the convention used throughout this guide.

> If you prefer a single namespace for everything, put both the operator and the CR in the same place and replace `pega` with your namespace in the image paths below. Nothing in the operator requires the split.

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

**Create the namespaces and push images:**

This deployment uses two namespaces — see [Namespace Layout](#namespace-layout) above. Images go in `pega`, alongside the workloads that consume them.

```bash
oc new-project pega-operator   # operator runs here
oc new-project pega            # Pega workloads and images live here

# Tag and push each image (adjust versions to match your Pega release)
podman tag pega:25.1.1 $REGISTRY/pega/pega:25.1.1
podman push $REGISTRY/pega/pega:25.1.1

podman tag pega-installer:25.1.1 $REGISTRY/pega/installer:25.1.1
podman push $REGISTRY/pega/installer:25.1.1

podman tag pega-srs:1.40.0 $REGISTRY/pega/srs:1.40.0
podman push $REGISTRY/pega/srs:1.40.0
```

**Verify the image streams were created:**

```bash
oc get imagestream -n pega
```

> **If your images are already in a different namespace**, you don't need to re-upload them. Copy the tags server-side — this is instant, even for the multi-gigabyte installer:
>
> ```bash
> oc tag <source-ns>/installer:25.1.1 pega/installer:25.1.1 --reference-policy=local
> ```
>
> The `--reference-policy=local` flag matters. Without it the tag defaults to `Source`, which resolves back to the original namespace's registry path and requires the `pega` service account to hold `system:image-puller` on that namespace — otherwise pods fail with `ImagePullBackOff`.

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

**Confirm the PackageManifest was created:**

```bash
oc get packagemanifest pega-operator -n openshift-marketplace
```

A `READY` catalog is **not** sufficient on its own. OLM only builds a PackageManifest if the catalog's bundle entry carries full CSV metadata, and the operator will not appear in the console without one. If the catalog is `READY` but this returns `NotFound`, see [Common Pitfalls](#common-pitfalls).

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

1. Switch the **Project** selector at the top of the page to **`pega`**. This is the namespace the CR and all workloads will be created in — not `pega-operator`, where the operator itself runs.
2. Navigate to **Ecosystem > Installed Operators** and select **Pega Platform Operator**
3. Click the **Pega Platform** tab, then **Create PegaPlatform**
4. The form is pre-populated with sensible defaults. Fill in the required fields:
   - **Pega Web/Batch Tier Image** — your internal registry path (e.g. `image-registry.openshift-image-registry.svc:5000/pega/pega:25.1.1`)
   - **Pega Installer Image** — your internal registry path for the installer
   - **Pega SRS Image** — your internal registry path for SRS
   - **Image Registry URL** — the base registry path (e.g. `image-registry.openshift-image-registry.svc:5000/pega`)
   - **Database Username / Password** — credentials for the managed PostgreSQL
   - **Admin Password** — initial password for `administrator@pega.com`
5. Configure the **Platform Tiers** section. The default example includes a `web` tier and a `batch` tier. Make sure each tier with ingress enabled has a **unique Route Prefix** (e.g. `pega` for web, `pega-batch` for batch) — two tiers sharing a prefix generate the same hostname and one Route will be rejected
6. Click **Create**

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
  namespace: pega
spec:
  pegaImage: image-registry.openshift-image-registry.svc:5000/pega/pega:25.1.1
  imageRegistry:
    url: image-registry.openshift-image-registry.svc:5000/pega
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
    image: image-registry.openshift-image-registry.svc:5000/pega/srs:1.40.0
    replicas: 2
  installer:
    image: image-registry.openshift-image-registry.svc:5000/pega/installer:25.1.1
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
oc get pods -n pega -w
```

See the [API Reference](#api-reference) below for all configurable fields.

### Step 4: Access the Application

Once the phase reaches `Ready`, the operator creates OpenShift Routes for each tier with ingress enabled:

```bash
oc get routes -n pega
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
| `url` | `string` | — | **Required.** Registry URL (e.g. `image-registry.openshift-image-registry.svc:5000/pega`) |
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
| `driverDownloadImage` | `string` | `registry.access.redhat.com/ubi9/ubi-minimal:latest` | Image for the init container that downloads the JDBC driver. Override to a mirrored image on disconnected clusters. See [JDBC driver handling](#jdbc-driver-handling). |

### JDBC driver handling

The operator never lets the Pega installer download the JDBC driver itself. Instead it adds an init container to the installer Job that fetches `database.driverUri` into a shared volume, then hands the installer a local `file://` path.

This is automatic — `database.driverUri` already defaults to the PostgreSQL driver, so no configuration is required.

The indirection exists because **the Pega installer image's `curl` cannot complete a TLS handshake on FIPS-enabled clusters**, failing with `curl: (35) Insufficient randomness`. Since GovCloud and many regulated environments run FIPS, delegating the download to a UBI-based container (whose OpenSSL is FIPS-validated) makes the operator work in both modes with a single code path. It mirrors the `global.downloadContainer` option in Pega's own Helm chart.

It also enables disconnected installs: point `driverUri` at an internal mirror and `driverDownloadImage` at a mirrored UBI, and nothing reaches the public internet.

To supply the driver yourself — for example baked into a custom installer image — set `database.driverUri` to an empty string. No init container is added, and Pega's installer skips driver handling entirely.

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
oc delete pegaplatform pega -n pega

# Delete PVCs if you want to remove persistent data
oc delete pvc --all -n pega

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
| **Catalog** (`pega-operator-catalog`) | OLM package index | **Any time the bundle changes.** `catalog.yaml` embeds a base64 copy of the CSV, so a CSV edit is not live until the catalog is regenerated and rebuilt |

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

**Order matters.** The catalog embeds a base64 copy of the CSV, so the bundle must be pushed *before* the catalog is regenerated from it. Building the catalog from a stale `catalog.yaml` silently ships the previous CSV.

```bash
# 1. Operator image
podman build --no-cache --platform linux/amd64 \
  -t quay.io/<your-repo>/pega-operator:v0.1.0 -f Dockerfile .
podman push quay.io/<your-repo>/pega-operator:v0.1.0

# 2. Bundle image
podman build --no-cache \
  -t quay.io/<your-repo>/pega-operator-bundle:v0.1.0 -f bundle.Dockerfile .
podman push quay.io/<your-repo>/pega-operator-bundle:v0.1.0

# 3. Regenerate catalog.yaml from the bundle you just pushed.
#    Never hand-edit the olm.bundle stanza — OLM needs the full CSV
#    metadata (olm.gvk, olm.bundle.object, relatedImages) to build a
#    PackageManifest, and without one the operator never appears in the UI.
cat > catalog/pega-operator/catalog.yaml <<'YAML'
---
schema: olm.package
name: pega-operator
defaultChannel: alpha
icon:
  base64data: ""
  mediatype: image/png
description: Deploys and manages Pega Platform on OpenShift with PostgreSQL, OpenSearch, Kafka, and SRS.
---
schema: olm.channel
name: alpha
package: pega-operator
entries:
  - name: pega-operator.v0.1.0
YAML
podman run --rm --platform linux/amd64 --entrypoint /bin/opm \
  registry.redhat.io/openshift4/ose-operator-registry-rhel9:v4.17 \
  render quay.io/<your-repo>/pega-operator-bundle:v0.1.0 -o yaml \
  >> catalog/pega-operator/catalog.yaml

# 4. Catalog image (file-based catalog — do NOT use `opm index add`)
podman build --no-cache \
  -t quay.io/<your-repo>/pega-operator-catalog:v0.1.0 -f catalog.Dockerfile .
podman push quay.io/<your-repo>/pega-operator-catalog:v0.1.0
```

> **Important:** Do not bump the image tag just to force a fresh pull. The CSV specifies `imagePullPolicy: Always`, so OpenShift nodes always pull the latest image for the same tag.

#### 3. Redeploy on the cluster

For a clean redeploy (recommended when testing significant changes):

```bash
# Remove the existing deployment
oc delete pegaplatform pega -n pega --ignore-not-found
oc delete subscription.operators.coreos.com pega-operator -n pega-operator --ignore-not-found
oc delete csv pega-operator.v0.1.0 -n pega-operator --ignore-not-found
oc delete installplan --all -n pega-operator --ignore-not-found
oc delete catalogsource pega-operator-catalog -n openshift-marketplace --ignore-not-found
oc delete pvc --all -n pega  # Only if you want to reset persistent data

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
| CatalogSource `READY` but no PackageManifest, operator absent from the console | `catalog.yaml` hand-written with only an `olm.package` property. OLM cannot build a PackageManifest without the CSV metadata and will not pull the bundle image to find it | Regenerate `catalog.yaml` with `opm render` (see below), rebuild and push the catalog, then delete and re-apply the CatalogSource |
| Console shows stale CR defaults after a CSV edit | Catalog still serving the previously embedded CSV | Rebuild the **bundle first**, then re-render `catalog.yaml` from the new bundle, then rebuild the catalog — in that order |
| `ImagePullBackOff` after copying images between namespaces with `oc tag` | Tag defaults to `referencePolicy: Source`, which resolves back to the source namespace and requires `system:image-puller` there | Re-tag with `--reference-policy=local` so the registry serves the image through the destination namespace |
| Installer fails with `curl: (35) Insufficient randomness` / `Could not download jar` | FIPS-enabled cluster. The Pega installer image's curl cannot complete a TLS handshake; the cluster's network is fine | Already handled — the operator downloads the driver in a UBI init container and passes a local `file://` path. If you see this, the operator predates that fix. See [JDBC driver handling](#jdbc-driver-handling) |
| Installer Job stays `Failed` and is never retried | `reconcileInstaller` returns an error on a failed Job rather than recreating it, to avoid masking a real installation failure | Delete the Job (`oc delete job <name>-installer -n <ns>`); the operator builds a fresh one on the next reconcile |

## License

Copyright Pegasystems Inc.
