# Red Hat Operator Certification Roadmap

A step-by-step guide for certifying the Pega Platform Operator through Red Hat's Partner Ecosystem program and publishing it to the OperatorHub Certified Operators catalog.

---

## Overview

Red Hat's operator certification process validates that an operator meets security, quality, and compatibility standards for OpenShift. Once certified, the operator appears in the **Certified Operators** catalog on every OpenShift cluster worldwide, giving customers a supported, one-click install experience.

The process has five phases:

| Phase | What Happens | Who |
|-------|-------------|-----|
| **1. Partner Enrollment** | Register with Red Hat Partner Connect | Pega + Red Hat Ecosystem team |
| **2. Image Certification** | Certify all container images | Pega (image owner) |
| **3. Operator Validation** | Pass scorecard, best practices, and preflight checks | Operator developer |
| **4. Submission** | Submit bundle to certified-operators repo | Operator developer |
| **5. Publication** | Red Hat reviews, merges, and publishes | Red Hat certification team |

---

## Phase 1: Partner Enrollment

### 1.1 — Register on Red Hat Partner Connect

Pega needs an active account on [Red Hat Partner Connect](https://connect.redhat.com). If Pega already has a partner account (likely, given existing Red Hat ecosystem presence), this step is to associate the operator as a new product listing.

### 1.2 — Create a Certification Project

In the Partner Connect portal:

1. Navigate to **Technology Partner > Certify your product**
2. Select **Operator Bundle Image** as the certification type
3. Create a new project — this generates a **Project ID** that ties the container image certifications and operator bundle together
4. Create additional certification projects for each container image the operator ships (the operator image itself, at minimum)

### 1.3 — Identify Image Ownership

The operator references several container images. Certification responsibility depends on who owns the image:

| Image | Owner | Certification Responsibility |
|-------|-------|------------------------------|
| `pega-operator` | Red Hat / Pega (jointly) | Must be certified |
| `pega` (web/batch tier) | Pega | Pega certifies independently |
| `pega-installer` | Pega | Pega certifies independently |
| `pega-srs` | Pega | Pega certifies independently |
| `opensearchproject/opensearch` | OpenSearch community | Already available; may need Red Hat-certified alternative |
| `rhel8/postgresql-12` | Red Hat | Already certified |
| `ose-operator-registry` | Red Hat | Already certified (catalog base image) |

> **Note:** Red Hat strongly prefers that all images in a certified operator come from certified sources. For OpenSearch, the team may need to evaluate using a Red Hat-certified equivalent or certifying the community image through the partner program.

---

## Phase 2: Image Certification

### 2.1 — Meet Container Image Requirements

The operator image must satisfy Red Hat's container certification requirements:

- [x] **Based on Red Hat UBI** — Currently using `ubi9/ubi-minimal` (compliant)
- [x] **Runs as non-root** — Currently `USER 65532:65532` (compliant)
- [ ] **Required labels** — Add these to the Dockerfile:
  ```dockerfile
  LABEL name="pega-operator" \
        vendor="Pegasystems Inc." \
        version="0.1.0" \
        release="1" \
        summary="Pega Platform Operator for OpenShift" \
        description="Deploys and manages Pega Platform on OpenShift with PostgreSQL, OpenSearch, Kafka, and SRS." \
        io.k8s.display-name="Pega Platform Operator" \
        io.k8s.description="Pega Platform Operator for OpenShift" \
        io.openshift.tags="pega,operator,bpm"
  ```
- [ ] **Licenses directory** — Include a `/licenses/` directory in the image with applicable license files

### 2.2 — Run Preflight Container Check

Install the [Preflight](https://github.com/redhat-openshift-ecosystem/openshift-preflight) tool and run:

```bash
preflight check container quay.io/aromero/pega-operator:v0.1.0 \
  --certification-project-id=<PROJECT_ID>
```

Preflight validates:
- UBI base image
- Required labels
- Non-root execution
- No modified RPM content
- License files present
- Image runs successfully

Fix any failures before proceeding.

### 2.3 — Submit Container Certification Results

Upload the Preflight results to the certification project in Partner Connect. Once the container image passes, it receives a **certified** status.

---

## Phase 3: Operator Validation

### 3.1 — Run Preflight Operator Check

```bash
preflight check operator quay.io/aromero/pega-operator-bundle:v0.1.0
```

This validates:
- Bundle structure and metadata
- CSV completeness (descriptions, icons, install modes)
- CRD schema validity
- RBAC declarations
- Scorecard test execution

### 3.2 — Run Operator SDK Scorecard

```bash
operator-sdk scorecard ./bundle
```

The scorecard checks:
- CRD has validation markers and descriptions on all fields
- Status subresource is defined
- Owned CRD resources are listed in the CSV
- Bundle metadata annotations are present and correct
- Basic and OLM test suites pass

### 3.3 — Address Certification Checklist

| Requirement | Current Status | Action Needed |
|-------------|---------------|---------------|
| CSV icon (base64 PNG, min 128x128) | Empty | Add Pega logo |
| `relatedImages` in CSV | Missing | Add all image references with `@sha256:` digests |
| Image references use digests | Using tags (`:v0.1.0`) | Pin to `@sha256:` digests |
| `olm.skipRange` or `replaces` | `replaces: ""` | Define upgrade path for future versions |
| Disconnected/air-gapped support | Not validated | Add `relatedImages` and test in disconnected cluster |
| `installModes` | OwnNamespace + SingleNamespace + AllNamespaces | Already set (compliant) |
| `minKubeVersion` | `1.27.0` | Already set (compliant) |
| Meaningful CSV description | Present | Already set (compliant) |
| Maintainer contact info | `support@pega.com` | Verify with Pega |
| Categories | `Application Runtime` | Already set (compliant) |

### 3.4 — Add `relatedImages` to CSV

For disconnected cluster support, the CSV must declare all images in a `relatedImages` section. This allows OpenShift to mirror them to an internal registry.

Add to the CSV `spec`:

```yaml
relatedImages:
  - name: pega-operator
    image: quay.io/aromero/pega-operator@sha256:<digest>
  - name: opensearch
    image: docker.io/opensearchproject/opensearch@sha256:<digest>
  - name: postgresql
    image: registry.redhat.io/rhel8/postgresql-12@sha256:<digest>
```

> **Note:** Pega's proprietary images (pega, installer, SRS) are user-provided at deploy time, so they don't go in `relatedImages`. Only images the operator itself references directly belong here.

### 3.5 — Pin Image References to Digests

Replace all tag-based image references in the CSV with `@sha256:` digests:

```bash
# Get the digest for an image
skopeo inspect docker://quay.io/aromero/pega-operator:v0.1.0 | jq -r '.Digest'
```

Update the CSV deployment spec and `relatedImages` with the resolved digests.

---

## Phase 4: Submission

### 4.1 — Fork the Certified Operators Repository

```bash
git clone https://github.com/redhat-openshift-ecosystem/certified-operators.git
cd certified-operators
```

### 4.2 — Add the Operator Bundle

Create the directory structure:

```
operators/
  pega-operator/
    0.1.0/
      manifests/
        pega-operator.clusterserviceversion.yaml
        pega.openshift.io_pegaplatforms.yaml
      metadata/
        annotations.yaml
```

Copy the bundle contents:

```bash
mkdir -p operators/pega-operator/0.1.0
cp -r /path/to/pega-operator/bundle/manifests operators/pega-operator/0.1.0/
cp -r /path/to/pega-operator/bundle/metadata operators/pega-operator/0.1.0/
```

### 4.3 — Add the CI Configuration

Create `operators/pega-operator/ci.yaml`:

```yaml
cert_project_id: "<PROJECT_ID_FROM_PARTNER_CONNECT>"
```

### 4.4 — Open a Pull Request

```bash
git checkout -b pega-operator-v0.1.0
git add operators/pega-operator/
git commit -m "Add Pega Platform Operator v0.1.0"
git push origin pega-operator-v0.1.0
```

Open a PR against `redhat-openshift-ecosystem/certified-operators:main`.

### 4.5 — Automated CI Validation

Red Hat's CI pipeline automatically runs on the PR:
- Preflight container checks on all referenced images
- Preflight operator checks on the bundle
- Deployment test on a live OpenShift cluster
- Scorecard tests

The PR status checks will report pass/fail. Fix any failures and push updates to the same branch.

---

## Phase 5: Publication

### 5.1 — Red Hat Review

After CI passes, the Red Hat certification team reviews:
- Security posture
- OpenShift compatibility
- Documentation quality
- Upgrade path viability

They may request changes via PR comments. Typical turnaround is 1-2 weeks.

### 5.2 — Merge and Publish

Once approved:
- The PR is merged
- The operator appears in the **Certified Operators** catalog within 24-48 hours
- It becomes available on every OpenShift 4.x cluster worldwide

### 5.3 — Ongoing Maintenance

For future versions:
- Submit a new directory under `operators/pega-operator/<new-version>/` via PR
- Use `replaces: pega-operator.v0.1.0` in the new CSV for a clean upgrade path
- The same CI and review process applies

---

## Immediate Action Items

### Red Hat Side
- [ ] Coordinate with Katie Giglio and the Ecosystem team on partner enrollment status
- [ ] Prepare Preflight environment for validation testing
- [ ] Add required container labels and licenses to the operator Dockerfile
- [ ] Add CSV icon, `relatedImages`, and pin image digests
- [ ] Run `preflight` and `operator-sdk scorecard` and fix all findings

### Pega Side
- [ ] Confirm or create Red Hat Partner Connect account
- [ ] Assign engineering contact for technical review of the operator
- [ ] Certify Pega-owned container images (pega, installer, SRS) through Partner Connect
- [ ] Provide official Pega logo for the CSV icon (PNG, minimum 128x128)
- [ ] Review and validate the operator's Pega deployment configuration against supported patterns

---

## Useful Links

| Resource | URL |
|----------|-----|
| Red Hat Partner Connect | https://connect.redhat.com |
| Certified Operators Repository | https://github.com/redhat-openshift-ecosystem/certified-operators |
| Preflight Tool | https://github.com/redhat-openshift-ecosystem/openshift-preflight |
| Operator SDK Scorecard | https://sdk.operatorframework.io/docs/testing-operators/scorecard/ |
| Certification Policy Guide | https://redhat-connect.gitbook.io/certified-operator-guide |
| Red Hat Ecosystem Catalog | https://catalog.redhat.com |
