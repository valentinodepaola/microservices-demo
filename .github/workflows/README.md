# GitHub Actions Workflows

This page describes the CI/CD workflows for the Online Boutique app, which run in [Github Actions](https://github.com/GoogleCloudPlatform/microservices-demo/actions).

## Infrastructure

The CI/CD pipelines for Online Boutique run on standard GitHub-hosted runners (Ubuntu). 

We also host a test GKE cluster, which is where the deploy tests run. Every PR has its own namespace in the cluster.

## Workflows

**Note**: In order for the current CI/CD setup to work on your pull request, you must branch directly off the repo (no forks). This is because the Github secrets necessary for these tests aren't copied over when you fork.

### Code Tests - [ci-pr.yaml](ci-pr.yaml), [cd-main.yaml](cd-main.yaml), [ci-main.yaml](ci-main.yaml)

Go and C# unit tests run in three places:

- `ci-pr.yaml` on every commit of every open PR: Go tests for `shippingservice`, `productcatalogservice` and `frontend/validator`, and C# tests for `cartservice`.
- `cd-main.yaml` on every push to main, as the `pruebas` job, with the same list as `ci-pr.yaml`. Both the `imagenes` and `deploy` jobs `needs:` it, so a commit with failing tests is never built or deployed. See [docs/despliegue-continuo.md](../../docs/despliegue-continuo.md#las-pruebas-van-primero).
- `ci-main.yaml` on pushes to `release/*` branches, or when run manually. It does not run on main, so the tests that gate a deployment live in a single place.

### Build and Publish Images - [cd-main.yaml](cd-main.yaml)

The `imagenes` job, between `pruebas` and `deploy`. On every push to main it builds the twelve Skaffold artifacts and publishes them to `ghcr.io/valentinodepaola`, tagged with the commit SHA. It authenticates with the workflow's own `GITHUB_TOKEN` — there is no secret to manage.

It runs on `ubuntu-24.04`, not on the self-hosted runner, and builds **`linux/amd64` only**: the runner is Apple Silicon (`arm64`) and the phase B cluster is an `x86_64` EC2 instance. `packages: write` is declared at the job level, so the `deploy` job keeps the workflow-wide `contents: read` that [ADR 0008](../../docs/adr/0008-despliegue-continuo-en-dos-fases.md) requires.

The phase A deploy still builds its own images locally against Docker Desktop, so every merge builds twice. That ends when issue #37 repoints the deployment at the remote cluster. See [docs/despliegue-continuo.md](../../docs/despliegue-continuo.md#el-registro-de-imágenes-y-por-qué-el-despliegue-igual-construye-en-local).

### Deploy Tests- [ci-pr.yaml](ci-pr.yaml)

These tests run on every commit for every open PR, as well as any commit to main / any release branch. This workflow:

1. Creates a dedicated GKE namespace for that PR, if it doesn't already exist, in the PR GKE cluster.
2. Uses `skaffold run` to build and push the images specific to that PR commit. Then skaffold deploys those images, via `kubernetes-manifests`, to the PR namespace in the test cluster.
3. Tests to make sure all the pods start up and become ready.
4. Gets the LoadBalancer IP for the frontend service.
5. Comments that IP in the pull request, for staging.

### Push and Deploy Latest - [push-deploy](push-deploy.yml)

This is the Continuous Deployment workflow, and it runs on every commit to the main branch. This workflow:

1. Builds the container images for every service, tagging as `latest`.
2. Pushes those images to Google Container Registry.

Note that this workflow does not update the image tags used in `release/kubernetes-manifests.yaml` - these release manifests are tied to a stable `v0.x.x` release.

### Cleanup - [cleanup.yaml](cleanup.yaml)

**Disabled — kept for reference only.** It used to run when a PR closed, deleting the PR-specific GKE namespace in the test cluster. It targets Google's `online-boutique-ci` project and `prs-gke-cluster`, which this fork does not control, so it always failed. Its trigger is now `workflow_dispatch`; see the comment at the top of the file and [ADR 0008](../../docs/adr/0008-despliegue-continuo-en-dos-fases.md).

### Manual Release Builder - [make-release.yaml](make-release.yaml)

This workflow is manually triggered via the `workflow_dispatch` event to automate the release process. When run, it:
1. Validates the release version format.
2. Automates the build and push of container images to Google Cloud Build.
3. Automatically regenerates Kubernetes manifests and Kustomize bases.
4. Packages and pushes the Helm chart.
5. Branches and tags the repository.
6. Opens a new Pull Request targeting `main` with the release checklist.
