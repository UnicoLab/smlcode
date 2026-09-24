---
name: openshift-manifests
description: OpenShift deployment specs that pass the restricted SCC on the first apply — kustomize base/overlays, probes, limits, secrets done safely, and how to explain them to a non-DevOps developer.
triggers: openshift, ocp, oc apply, kustomize, deployment, pod, route, configmap, secret, pvc, hpa, probe, kubernetes, k8s
agents: worker, tester, reviewer, openshift-worker, openshift-tester, openshift-reviewer
paths: "openshift/**, k8s/**, manifests/**, kustomize/**, **/kustomization.yaml"
user-invocable: true
---

# OpenShift manifests

## The layout

```
openshift/
  base/
    kustomization.yaml          # resources: [every file below]
    api-deployment.yaml
    api-service.yaml
    api-route.yaml              # only for what is reached from outside
    api-configmap.yaml
    api-secret.example.yaml     # REPLACE_ME values, never real ones
  overlays/
    dev/kustomization.yaml      # resources: [../../base] + patches
    prod/kustomization.yaml
  README.md                     # what each file is, how to apply it
```

Apply with `oc apply -k openshift/overlays/dev`. Render offline, without a
cluster, with `oc kustomize openshift/overlays/dev`.

## A workload that passes the restricted SCC

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels: { app.kubernetes.io/name: api, app.kubernetes.io/part-of: shop }
spec:
  replicas: 2
  selector:
    matchLabels: { app.kubernetes.io/name: api }
  template:
    metadata:
      labels: { app.kubernetes.io/name: api, app.kubernetes.io/part-of: shop }
    spec:
      containers:
        - name: api
          image: image-registry.openshift-image-registry.svc:5000/shop/api:1.4.2
          ports: [{ containerPort: 8080 }]
          envFrom:
            - configMapRef: { name: api-config }
            - secretRef: { name: api-secrets }
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits: { cpu: 500m, memory: 512Mi }
          readinessProbe:
            httpGet: { path: /healthz, port: 8080 }
          livenessProbe:
            httpGet: { path: /healthz, port: 8080 }
            initialDelaySeconds: 15
          securityContext:
            runAsNonRoot: true
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
            seccompProfile: { type: RuntimeDefault }
```

What OpenShift does differently from plain Kubernetes, and what breaks:

- **Random UID.** Pods run as an arbitrary UID in group 0. Never set
  `runAsUser`; make writable paths group-writable (`chgrp 0 && chmod g=u` in
  the image) or mount an `emptyDir`.
- **No privileged ports.** Listen on 8080, not 80.
- **Routes, not Ingress.** A Route exposes a Service; use edge TLS:

```yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata: { name: api }
spec:
  to: { kind: Service, name: api }
  port: { targetPort: 8080 }
  tls: { termination: edge, insecureEdgeTerminationPolicy: Redirect }
```

## Secrets

Never commit a real value. Ship an example and the command that creates it:

```yaml
# api-secret.example.yaml — copy, fill in, do NOT commit the filled copy
apiVersion: v1
kind: Secret
metadata: { name: api-secrets }
type: Opaque
stringData:
  DATABASE_URL: REPLACE_ME
```

```sh
oc create secret generic api-secrets --from-literal=DATABASE_URL='postgres://…'
```

Leave the example out of `kustomization.yaml` resources, or the placeholder
overwrites the real Secret on every apply.

## Overlays

Per-environment differences — replicas, image tag, limits — go in the
overlay, never in a copy of the base:

```yaml
# overlays/prod/kustomization.yaml
resources: [../../base]
images:
  - name: image-registry.openshift-image-registry.svc:5000/shop/api
    newTag: 1.4.2
patches:
  - target: { kind: Deployment, name: api }
    patch: |
      - op: replace
        path: /spec/replicas
        value: 3
```

## Checklist before handing it over

- `oc kustomize` renders every base and overlay;
- every container: requests, limits, readiness, liveness, non-root context;
- every Service selector matches pod labels; every Route targets a Service;
- no `:latest` image in anything a prod overlay uses;
- the README says, in order, what to create by hand (secrets, the project)
  and the one command that applies the rest.
