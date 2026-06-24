# Kubernetes

Oompa can be deployed as a Kubernetes Deployment using the container image published to GitHub Container Registry.

## Container Image

```
ghcr.io/qinqon/oompa:latest
```

The image includes Go, `gh` CLI, Claude Code CLI, and git. It runs as a non-root user (UID 1000) and is compatible with OpenShift's random UID assignment.

## Example Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: oompa
  namespace: oompa
  labels:
    app: oompa
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: oompa
  template:
    metadata:
      labels:
        app: oompa
    spec:
      containers:
        - name: oompa
          image: ghcr.io/qinqon/oompa:latest
          args:
            - --repo=myorg/myrepo
            - --clone-dir=/work
            - --log-level=debug
            - --poll-interval=2m
          env:
            - name: GITHUB_APP_ID
              valueFrom:
                secretKeyRef:
                  name: oompa-github
                  key: app-id
            - name: GITHUB_APP_INSTALLATION_ID
              valueFrom:
                secretKeyRef:
                  name: oompa-github
                  key: installation-id
            - name: GITHUB_APP_PRIVATE_KEY_PATH
              value: /secrets/github/app.pem
            - name: GOOGLE_APPLICATION_CREDENTIALS
              value: /secrets/gcp/credentials.json
            - name: CLOUD_ML_REGION
              value: us-east5
            - name: ANTHROPIC_VERTEX_PROJECT_ID
              valueFrom:
                secretKeyRef:
                  name: oompa-gcp
                  key: project-id
          volumeMounts:
            - name: github-app-key
              mountPath: /secrets/github
              readOnly: true
            - name: gcp-credentials
              mountPath: /secrets/gcp
              readOnly: true
            - name: work
              mountPath: /work
          resources:
            requests:
              cpu: 500m
              memory: 512Mi
            limits:
              cpu: "2"
              memory: 2Gi
      volumes:
        - name: github-app-key
          secret:
            secretName: github-app-key
        - name: gcp-credentials
          secret:
            secretName: gcp-credentials
        - name: work
          emptyDir: {}
```

## Secrets

Create the required secrets:

```bash
# GitHub App private key
kubectl create secret generic github-app-key \
  --from-file=app.pem=/path/to/private-key.pem \
  -n oompa

# GCP credentials
kubectl create secret generic gcp-credentials \
  --from-file=credentials.json=/path/to/credentials.json \
  -n oompa

# GitHub App IDs
kubectl create secret generic oompa-github \
  --from-literal=app-id=123456 \
  --from-literal=installation-id=78901234 \
  -n oompa

# GCP project
kubectl create secret generic oompa-gcp \
  --from-literal=project-id=my-gcp-project \
  -n oompa
```

## Design Notes

- `strategy.type: Recreate` ensures only one instance runs at a time (oompa uses sequential processing)
- `emptyDir` for `/work` is sufficient since oompa rebuilds state from GitHub on startup
- The container runs as a non-root user and supports OpenShift's random UID assignment
- Git credentials are configured system-wide in the container using the `GH_TOKEN` environment variable
