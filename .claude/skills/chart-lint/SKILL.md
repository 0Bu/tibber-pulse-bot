---
name: chart-lint
description: Lint and render the tibber-pulse-bot Helm chart across all deployment scenarios. Validates the 3 mutually exclusive password modes (inline, sealedSecret, existingSecret), failure when multiple or zero passwords are set, verbose flag rendering, the Home Assistant expireAfter knob, and resource overrides.
disable-model-invocation: true
---

# chart-lint

Comprehensive validation of the `tibber-pulse-bot` Helm chart templates.

## 1. Syntax & Schema Linting

```bash
helm lint chart
```

Expect zero errors and zero failures.

## 2. Password Mode Verification (Must Test All 3)

The chart requires the bridge password to be supplied via **exactly one** mechanism:

### a. Inline plain password (dev only)

```bash
helm template test-inline chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.password=dummy-9char
```

### b. SealedSecret ciphertext (recommended for GitOps)

```bash
helm template test-sealed chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.sealedSecret.encryptedPassword=AgBi1234ciphertext...
```

### c. Reference existing Secret

```bash
helm template test-existing chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.existingSecret=my-pulse-secret
```

## 3. Negative Tests (Must Fail Closed)

Verify that the chart rejects ambiguous or missing configurations:

```bash
# Missing password (all empty) -> MUST FAIL
helm template test-fail-empty chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local 2>&1 | grep -q "exactly one" && echo "PASS: rejected empty password"

# Multiple passwords set -> MUST FAIL
helm template test-fail-multiple chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.password=dummy \
  --set pulse.existingSecret=my-secret 2>&1 | grep -q "exactly one" && echo "PASS: rejected multiple passwords"
```

## 4. Optional Feature Knobs

Verify that `--set verbose=true` passes `-v` and `--set fullnameOverride=custom-name` overrides resource names:

```bash
helm template test-features chart \
  --set pulse.host=192.168.1.10 \
  --set mqtt.host=mosquitto.default.svc.cluster.local \
  --set pulse.password=dummy \
  --set verbose=true \
  --set fullnameOverride=custom-bot | grep -E '(\- -v|name: custom-bot)'
```

## 5. Home Assistant expiration knob

`homeAssistant.expireAfter` maps to `--expire-after` and is only rendered when
non-zero (`0` = the bot's auto default, so no arg). A negative value must still
render — it disables `expire_after`:

```bash
for v in 0 45 -1; do
  echo "expireAfter=$v:"
  helm template test-expire chart \
    --set pulse.host=192.168.1.10 \
    --set mqtt.host=mosquitto.default.svc.cluster.local \
    --set pulse.password=dummy \
    --set homeAssistant.discovery=true \
    --set homeAssistant.expireAfter=$v | grep -E -- '--(ha-discovery|expire-after)' || true
done
```

Expect no `--expire-after` line for `0`, `--expire-after=45` and
`--expire-after=-1` for the other two.
