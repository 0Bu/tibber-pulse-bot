# tibber-pulse-bot Helm chart

Deploys [`tibber-pulse-bot`](https://github.com/0Bu/tibber-pulse-bot) into a
Kubernetes cluster: a single Deployment that connects to a local Tibber Pulse
Bridge over WebSocket and republishes meter readings to MQTT.

## Prerequisites

- Kubernetes ≥ 1.24, Helm ≥ 3.10
- Network reachability from the cluster to the Pulse Bridge (LAN-local)
- An MQTT broker reachable from the cluster (e.g. Mosquitto in the same cluster)
- The 9-character bridge admin password (printed on the bridge sticker)
- `webserver_force_enable` must be `TRUE` on the bridge — set it once via the
  bridge's AP-mode console

## Install

The two pieces of secret/host info — bridge IP and bridge password — must be
supplied at install time. There is no committed default for either.

There are three mutually exclusive ways to supply the bridge password —
the chart fails the install if none is set.

### Inline password (quick / dev)

```bash
helm install tibber-pulse-bot ./chart \
  --set pulse.host=192.168.107.118 \
  --set pulse.password=AD56-54BA \
  --set mqtt.host=mosquitto.default.svc.cluster.local
```

The chart renders a `Secret` with the password and mounts it into the
container as `TIBBER_PULSE_PASSWORD`.

### Sealed-Secret ciphertext in `values.yaml` (recommended for GitOps)

Mirrors the [`telegraf` chart in
rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster/tree/main/telegraf):
the encrypted password is checked into git, the in-cluster
[bitnami/sealed-secrets](https://github.com/bitnami-labs/sealed-secrets)
controller decrypts it into a normal `Secret` at apply-time.

Encrypt the password with `kubeseal --raw` (one value, no Secret wrapper):

```bash
RELEASE=tibber-pulse-bot
NAMESPACE=default

CIPHER=$(echo -n 'AD56-54BA' | kubeseal --raw \
  --name "$RELEASE" \
  --namespace "$NAMESPACE" \
  --controller-namespace kube-system)

helm install "$RELEASE" ./chart \
  --namespace "$NAMESPACE" \
  --set pulse.host=192.168.107.118 \
  --set pulse.sealedSecret.encryptedPassword="$CIPHER" \
  --set mqtt.host=mosquitto.default.svc.cluster.local
```

For an ArgoCD/GitOps workflow, paste the same `$CIPHER` value into
`values.yaml` under `pulse.sealedSecret.encryptedPassword:` and commit it —
the ciphertext is bound to `name=<release>` + `namespace=<ns>` and useless
to anyone without the controller's private key.

If you encrypted with `kubeseal --scope namespace-wide` or `cluster-wide`,
also set `pulse.sealedSecret.scope` to match.

### Pre-existing Secret (recommended for prod / GitOps)

Create the secret out-of-band (sealed-secrets, external-secrets, sops-flux,
manual `kubectl`, …) with one key:

```bash
kubectl create secret generic tibber-pulse \
  --from-literal=TIBBER_PULSE_PASSWORD=AD56-54BA
```

Then point the chart at it — the password value in `values.yaml` is ignored:

```bash
helm install tibber-pulse-bot ./chart \
  --set pulse.host=192.168.107.118 \
  --set pulse.existingSecret=tibber-pulse \
  --set mqtt.host=mosquitto.default.svc.cluster.local
```

## Configuration

Top-level keys in `values.yaml`:

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/0bu/tibber-pulse-bot` | Container image |
| `image.tag` | chart `appVersion` | Tag override |
| `pulse.host` | `""` (required) | Bridge IP / hostname |
| `pulse.node` | `1` | Bridge node id |
| `pulse.password` | `""` | Inline password (dev only) |
| `pulse.sealedSecret.encryptedPassword` | `""` | `kubeseal --raw` ciphertext — chart renders a `SealedSecret` |
| `pulse.sealedSecret.scope` | `""` | Override SealedSecret scope (`namespace-wide`, `cluster-wide`) |
| `pulse.existingSecret` | `""` | Name of existing Secret with key `TIBBER_PULSE_PASSWORD` |
| `mqtt.host` | `""` (required) | MQTT broker host |
| `mqtt.port` | `1883` | MQTT broker port |
| `mqtt.topic` | `tibber/pulse` | Topic prefix |
| `mqtt.clientID` | `tibber-pulse-bot` | MQTT client id |
| `mode` | `push` | `push` (WebSocket) or `poll` (HTTP) |
| `pollInterval` | `10s` | Poll cadence (poll mode only) |
| `wsIdleTimeout` | `60s` | Reconnect WS if no message arrives |
| `reconnectDelay` | `1s` | Delay before reconnecting after WS drop |
| `quiet` | `false` | Suppress per-update stdout one-liner |
| `resources` | 10m / 16Mi req, 64Mi limit | Container resource requests/limits |

The chart fails the install if `pulse.host` or `mqtt.host` is empty, so
typos surface during `helm install`/`helm upgrade` rather than at runtime.

## Verify

```bash
kubectl logs -l app.kubernetes.io/name=tibber-pulse-bot -f
```

Expected output (push mode, MQTT enabled):

```
mode=push, host=192.168.107.118, output=mqtt://mosquitto:1883/tibber/pulse + compact stdout
19:46:08 P=0.000W   Eimp=2423174.800Wh Eexp=253615.600Wh
19:46:11 P=4.000W   Eimp=2423174.800Wh Eexp=253615.600Wh
```

Subscribe to verify MQTT topics from inside the cluster:

```bash
kubectl run -it --rm mqtt-sub --image=eclipse-mosquitto --restart=Never -- \
  mosquitto_sub -h mosquitto -t 'tibber/pulse/#' -v
```

## ArgoCD / GitOps

Mirrors the layout used in the
[rpi-k3s-cluster](https://github.com/0Bu/rpi-k3s-cluster) parent-charts:
this chart is self-contained (no upstream subchart) and can be referenced
from an ArgoCD `Application` with `path: chart` of this repo. Use
`pulse.existingSecret` together with sealed-secrets so no plaintext password
ever lands in git.

## Uninstall

```bash
helm uninstall tibber-pulse-bot
```

Removes the Deployment and the chart-managed Secret (if `pulse.password` was
used). Externally-managed secrets are left untouched.
