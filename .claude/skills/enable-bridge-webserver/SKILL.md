---
name: enable-bridge-webserver
description: >
  Provision a new Tibber Pulse Bridge for tibber-pulse-bot by enabling its
  local HTTP server — i.e. setting param 39 / webserver_force_enable to TRUE.
  Use when the user has a fresh/factory bridge and wants the bot to reach
  /data.json or /ws, or asks to "enable the local web server", "flip param 39",
  "set webserver_force_enable", or "set up a new Pulse bridge". This is the
  automatable companion to the README "Enabling the local web server" section.
---

# Enable the Tibber Pulse Bridge local web server (param 39)

Out of the box the bridge keeps its local HTTP server **off**, so the bot's
`/data.json` (poll) and `/ws` (push) endpoints are dead. This is governed by
`param 39 / webserver_force_enable`. You only do this **once per bridge**.

## What this skill can and cannot automate

Three of the steps are physical and **cannot** be done from a shell — guide the
user through them and wait for confirmation before continuing:

- **Entering AP mode** (hold the pinhole button while powering on).
- **Joining the bridge's temporary WiFi** (an OS-level WiFi switch on the
  machine that will talk to the bridge).
- **Replugging** to leave AP mode.

What **is** automated: detecting that the bridge is reachable, flipping param
39, and — the real success gate — verifying `/data.json` answers `200` once the
bridge is back on the LAN.

> The machine running these checks joins the bridge's AP WiFi for steps 1–3
> (`10.133.70.1` is only routable from that network); the final verify (step 5)
> runs from the normal LAN. (If ever run from a headless host that can't join
> the AP, do the UI part on a phone/laptop and run only step 5 here.)

## Inputs to collect first

- **Bridge admin password / WiFi key**: the 9-char code on the sticker,
  format `XXXX-XXXX`. Same value for the AP WiFi WPA2 key, the AP-console login,
  and the later HTTP Basic auth. Never echo it into a committed file or the
  repo — treat it like `.env` (see project CLAUDE.md "Security").
- **Bridge LAN IP** (for the final verify): its DHCP address once it rejoins
  home WiFi. Ask the user, or have them check the router.

## Procedure

### 1. Enter AP mode (physical — instruct, then wait)

Tell the user, verbatim:

1. Unplug the bridge from the wall outlet.
2. With a paperclip/SIM tool, **press and hold** the pinhole button on the side.
3. **Plug it back in** while still holding.
4. Keep holding ~10 s until the LED blinks **blue**, then release.

> Releasing as soon as it turns blue matters: holding ~30 s longer on some
> firmware triggers a **factory reset** of the WiFi credentials.

Wait for the user to confirm the LED is blinking blue.

### 2. Join the bridge AP (physical/OS-level — instruct, then wait)

- WiFi SSID: **`Tibber Bridge`** or **`TibberBridge-XXXXXX`** (suffix = sticker
  EUI).
- WPA2 password: the **same 9-char sticker code**.
- Gateway after joining: **`10.133.70.1`**.

Wait for the user to confirm they're connected. Then confirm reachability
yourself before touching anything:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://10.133.70.1/
```

A `200`/`401` means the AP web server is up. A timeout means the WiFi join
didn't take — stop and have the user recheck before proceeding.

### 3. Flip param 39 (automated — pick the first method that works)

The exact write transport differs across bridge firmware, so **verify by
re-reading, don't assume**. Try in this order:

**Method A — browser automation (preferred when a browser MCP is available and
this machine is on the AP WiFi).** Drive the bridge's own UI, which is known to
work on every firmware:

1. Navigate to `http://10.133.70.1/`, log in `admin` / `<sticker code>`.
2. Open the **Params** tab. Find `param_id 39  webserver_force_enable` (a
   `bool`, currently `FALSE`), set it **TRUE**, and click **Store params to
   flash** at the bottom.
3. On newer firmware where 39 is hidden from the Params list, open the
   **Console** tab and run:
   ```
   param_set webserver_force_enable true
   param_store
   ```
   Then re-read with `param_get webserver_force_enable` and confirm it shows
   `true`/`1`.
4. While in the UI, note the **EUI** on the **Nodes** page (16 hex chars) — the
   user may want it later for HA device naming.

**Method B — no browser available.** Do not blind-POST a guessed endpoint.
Instead, inspect what this firmware actually serves and adapt:

```bash
# See what the AP console/params surface looks like on THIS firmware.
curl -s -u admin:<sticker code> http://10.133.70.1/ | head -c 400
curl -s -u admin:<sticker code> http://10.133.70.1/params/ | head -c 400
curl -s -u admin:<sticker code> http://10.133.70.1/console/ | head -c 400
```

If you can identify the param-write / console call from the responses (or the
network requests the UI makes), issue it, then re-read param 39 to confirm
`true`. If you cannot determine it with confidence, **fall back to manual UI**:
hand the user the exact clicks from Method A step 1–3 and wait for them to
confirm "Store params to flash" succeeded. Do not guess.

### 4. Leave AP mode (physical — instruct, then wait)

Tell the user: **unplug and replug** the bridge (no button this time). It boots
into normal mode, rejoins home WiFi, and the web server now answers on its LAN
IP. Wait for confirmation, and have them rejoin their normal WiFi too.

### 5. Verify (automated — this is the source of truth)

From the LAN, against the bridge's DHCP IP:

```bash
scripts/verify.sh <bridge-lan-ip> <sticker code>
```

(or directly:)

```bash
curl -u admin:<sticker code> -I "http://<bridge-lan-ip>/data.json?node_id=1"
# expect: HTTP/1.1 200 OK   Content-Type: text/text
```

Interpret:

- **`200`** → done. param 39 stuck; the bot can now poll/push. Tell the user the
  bot is ready and remind them the password lives in `.env` / the sealed secret,
  never in git.
- **`401 Unauthorized`** → wrong password (the web server *is* up). Recheck the
  sticker code.
- **No response / timeout** → bridge didn't rejoin the LAN, or param 39 didn't
  persist. Confirm its IP (router/DHCP), and if it's reachable but still off,
  the flash store in step 3 didn't take — repeat from step 1.
- **HTML shell from `/`** but you queried `/nodes/1/data` → that's the SPA
  route, not the data API. Use `/data.json?node_id=1` (project CLAUDE.md notes
  this trap).

## Notes

- Reference: README "Enabling the local web server"; project CLAUDE.md "Bridge
  protocol facts" and "Verification protocol".
- Don't add `:latest`-style assumptions about firmware; the verify curl is the
  only reliable confirmation.
