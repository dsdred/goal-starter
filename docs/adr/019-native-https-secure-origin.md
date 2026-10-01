# ADR 019: Native HTTPS / Secure Origin

**Status:** **Accepted** — Owner decisions D1–D9 of the 2026-09-29 secure-origin design gate, then Owner decisions P1–P8 of the 2026-09-29 reconciliation gate, whose required cookie re-analysis was resolved from real browser measurement (§Context, §D18). **Implementation status (reconciled 2026-10-01, after slice 4): slices 1 (config schema + validation), 2 (dual-listener lifecycle) and 3 (connection-derived `Secure` cookies + the extended non-loopback warning) are COMPLETED — IMPLEMENTED, TESTED, COMMITTED AND PUBLISHED, and slice 4 (documentation reconciliation with the published slices 1–3) is COMPLETED — IMPLEMENTED, COMMITTED AND PUBLISHED, documentation-only, so its exact-SHA CI run re-verified the unchanged Go bytes rather than testing new code; slice 5 (HTTPS browser acceptance + ROADMAP/BACKLOG reconciliation) is NOT STARTED and is the only remaining implementation slice, and OWNER-EXPORT-01 remains OPEN pending it. Slice 4 is `f759332cdcea0deec71954c9843c95d8bb0c18a3` (parent `bc73c74f907139e7f9d28145a1c9cc2ee352c059`), published as the plain fast-forward `bc73c74..f759332 -> main` and verified for that exact SHA by CI run `36843329049`, attempt 1, 7/7 jobs, no rerun, not tagged, not released; the full provenance ledger of every slice stays in `ROADMAP.md`.** The label this line carried at the 2026-09-29 gates — "Implementation NOT STARTED. Design record only; no GoAl behavior described here is in force" — is kept as their then-state and is superseded: §D3/§D4 and the §D5–D7 absolute-path rule, §D8 and the validation-time §D9–D13 rows are in force at HEAD since slice 1, and §D15, §D19's listener-naming startup logs, §D22 and §D24's HTTP-only baseline are in force since slice 2, as are §D18's connection-derived `Secure` cookie rule and §D19's extended warning wording since slice 3. **Still not in force:** slice 5's HTTPS browser acceptance and the §D18 browser rows it owes; §D14 and §D23 remain deliberately unimplemented. The documentation rows of slice 4 are in force since its publication: `docs/SECURITY.md`, `docs/CONFIGURATION.md`, `docs/API.md`, the `docs/ARCHITECTURE.md` / `docs/ARCHITECTURE_RU.md` ADR tables and both user guides are reconciled with slices 1–3 at HEAD. *(The "slices 3–5 are NOT STARTED, and slice 3 … is the next implementation slice" label this line carried at the 2026-09-30 reconciliation after slices 1–2, and the "Still not in force: §D18's cookie rule and §D19's extended warning wording (slice 3)" clause written underneath it, and the "slices 4–5 are NOT STARTED, and slice 4 (documentation) is the next implementation slice" label with the "Still not in force: the documentation rows of slice 4" clause that this line carried between slice 3's publication and slice 4's, were each accurate when written and are superseded here as tracking truth only.)* No design decision D1–D27 was altered by any implementation slice, and this file carries only the status wording — the full commit and CI evidence ledger of each slice lives in `ROADMAP.md`; a status line may name a published slice's commit and exact-SHA run to identify the state it reports, without reproducing that ledger. **State as recorded at the two 2026-09-29 gates, kept word-for-word and scoped to that time — not the current state of this file:** This ADR is created and maintained as an **uncommitted, untracked working-tree file** because the repository is currently carrying the uncommitted OWNER-EXPORT-01 Slice 1 changes; see §Governance boundary. *(That sentence recorded the file status as of the two 2026-09-29 gates. At the 2026-09-30 documentation boundary the Owner authorized this document into version control together with the ROADMAP/BACKLOG reconciliation its §Governance boundary anticipated — no design decision in this ADR was altered. That boundary was committed as `6e5ae01…` and published on 2026-09-30 under exact-SHA CI run `36623670568`, so the sentence above is history, not this file's status.)* Acceptance here settles the contract; it does not authorize implementation, commit or publication.
**Date:** 2026-09-29
**Related:** ADR 001 (single binary), ADR 003 (embedded web UI), ADR 006 (credentials), ADR 007 (audit), ADR 009 (hot reload), ADR 011 (Windows service), ADR 014 (Portable Configuration), ADR 018 (restore semantics), **ADR 012 — never materialized, remains a historical reference only**

## Context

OWNER-EXPORT-01 requires that **Скачать / Download** lets the Owner choose the destination folder and file name. That flow is the browser File System Access API (`showSaveFilePicker`), which is `[SecureContext]`-only: on a non-secure origin the member is **not installed at all**, so it cannot even reject.

Measured facts (2026-09-29, real headed Chrome 153, forensic harness outside the repository):

| Page origin | `isSecureContext` | `typeof showSaveFilePicker` | Native Save As | Ordinary download events |
|---|---|---|---|---|
| `http://aids:8088` (Owner's real usage) | `false` | `"undefined"` | impossible | 1 |
| `http://127.0.0.1:<port>` | `true` | `"function"` | presented | 0 |
| `https://<hostname>:<port>` with an **untrusted** certificate, warning bypassed | `true` | `"function"` | presented | 0 |
| `https://<hostname>:<port>` with an untrusted certificate, **not** bypassed | page does not load (`net::ERR_CERT_AUTHORITY_INVALID`) | — | — | — |

Origin trustworthiness is decided by the browser from the URL **host string and scheme** (`127.0.0.0/8`, `::1`, `localhost`, `*.localhost`, `https`, `file`); there is **no DNS-resolution step**, so a hostname that happens to point at a loopback address is not exempt, and a LAN hostname over plain HTTP is not a secure context.

Consequence: the Owner's normal usage model — browser on another workstation → `http://aids:8088` — can never satisfy OWNER-EXPORT-01 with browser-native means. The blocker is **secure-origin availability**, not the picker implementation.

This ADR reopens the direction that `ROADMAP.md` records as **PAUSED** since 2026-09-06 (the planned "ADR 012" TLS document was never materialized in `docs/adr/`). The pause was a release-scope isolation decision, not a rejection; `ROADMAP.md` lists the direction under P1 with its own design gate: *"Native HTTPS / TLS: binary serves HTTPS directly (cert/key config, HTTP/HTTPS mode toggle, Secure cookie, TLS version defaults, diagnostics); no reverse proxy required"*. This is that design gate.

### Measured cookie semantics (2026-09-29, real Chrome 153)

The reconciliation gate refused to reason from an assumption about scheme isolation, so the transitions below were measured with a scratch harness **outside the repository** (never committed, no GoAl code involved, no repository file touched): one process serving plain HTTP on `http://aidsck:18081` and HTTPS on `https://aidsck:18443` with a self-signed `DNS:aidsck` certificate, `Set-Cookie` attributes copied exactly from `session.go` / `csrf.go`, the hostname mapped to loopback by `--host-resolver-rules`, and every step observed from **both** the browser jar (`context.cookies()`) and what the server actually received. Browser: the machine's own stable **Chrome 153.0.0.0** (`navigator.userAgent` captured from the same session), launched headed through the repository's Playwright with `ignoreHTTPSErrors` and a throw-away profile in `%TEMP%`. HTTP and HTTPS on one host share **one cookie jar** — a cookie's key is (name, domain, path), and scheme and port are **not** part of it.

| Probe | Action | Observed result |
|---|---|---|
| C1 | HTTP sets `goal_session=HTTP1` (`Secure=false`) | the browser **sends it to `https://aidsck:18443`** — jar key is scheme-independent |
| R1 | HTTP sets `goal_session=R1HTTP` (`Secure=false`) → visit HTTPS → return to HTTP, HTTPS re-issuing nothing | the cookie is received by **both** schemes on **both** legs; jar keeps one `secure=false` entry — a non-`Secure` cookie is scheme-agnostic in both directions |
| C2 | HTTPS sets `goal_session=HTTPS2` (`Secure=true`), same name/path | jar holds **one** entry: `HTTPS2`, `secure=true` — it **replaced** the non-`Secure` cookie |
| C3 | Visit HTTP again | `goal_session` is **not** sent (`received=""`); HTTPS still receives `HTTPS2` |
| C5a | HTTP tries to set `goal_session=HTTP3` (`Secure=false`) over the existing `Secure` cookie | **refused** — jar still `HTTPS2 secure=true` (secure-cookie overwrite protection) |
| C5b | HTTP clears (`MaxAge=-1`, no `Secure`) against the `Secure` cookie | **cookie survives** — jar still `HTTPS2` |
| C5c | HTTPS sets `Secure=true`, then HTTPS clears it | jar empty — a `Secure` cookie is cleared by a matching HTTPS `Set-Cookie` with `MaxAge=-1` |
| D2 | **HTTPS** sets the same name/path with `Secure=false` | **accepted** — jar becomes `S2INSECURE secure=false`, then it is sent over **both** schemes (the `Secure` flag is not sticky) |
| D4 | HTTPS clears with no `Secure` attribute | accepted; jar empty |
| C4 | `goal_csrf_token` (`HttpOnly=false`, `SameSite=Strict`) through the same transitions | identical one-way behavior; after an HTTPS `Secure=true` replacement it is absent from the HTTP request **and** from `document.cookie` on the HTTP page |
| R1 | HTTP sets `goal_session=R1HTTP` (`Secure=false`), visits HTTPS, then returns to HTTP — HTTPS re-issues **nothing** | `goal_session=R1HTTP` is received **by both** schemes on both legs; the jar keeps one `secure=false` entry — a non-`Secure` cookie is genuinely scheme-agnostic, in both directions |

## Product goal

GoAl can provide a **supported secure-origin deployment directly from the single GoAl binary** for normal remote/LAN Web UI use, e.g. `https://aids:<https-port>`, so that browser capabilities requiring a secure context — including OWNER-EXPORT-01's destination/name selection — work in the Owner's real usage model.

## Scope

In scope: TLS enablement, listener ownership, certificate/key contract, startup validation and failure semantics, cookie/CSRF/auth interaction, reverse-proxy boundary, service behavior on Windows and Linux, migration/rollback, test obligations, documentation impact, implementation slicing.

Out of scope (each with its own reason):

- **GoAl as a private CA; automatic OS trust-store installation** — Owner decision D4. Future design work only if an ADR proves it necessary.
- **Generated self-signed certificates as the normal production story** — Owner decision D5. Deferred, see §D14.
- **ACME / automatic certificate management** — `ROADMAP.md` keeps this as a dependent Later item; it needs a publicly resolvable name, which `aids` is not.
- **Full proxy-identity redesign** (`X-Forwarded-For` trust model) — see §D20 and §Open questions 2; classified separately.
- **Any change to Portable Configuration content** — TLS material must never enter the bundle (§D25).
- **OWNER-IMPORT-01 / ADR 018 behavior** — untouched.
- **Mandatory authentication** — this ADR does not silently enable auth or mutate Owner configuration (Owner decision D7).

## Repository evidence used for these decisions

| Fact | Evidence |
|---|---|
| Exactly one plain HTTP listener; no TLS path anywhere (design-time baseline — the cited range is `server.go:211-252` at the pre-slice-2 bytes; slice 1 added the `tls` config schema and slice 2 replaced this single-listener shape with the §D15 dual-listener lifecycle, while the repository still has no certificate path, and the `Secure` cookie derivation this row reports absent has since been delivered by slice 3) | `internal/webui/server.go:211-252` — `http.Server{Addr: ListenAddress:WebPort}`, `server.ListenAndServe()`, `select { <-ctx.Done() / serverErr }`, `server.Shutdown(10s)` |
| No TLS/cert/key/scheme config field exists (design-time baseline; implementation slice 1 adds `tls`, §D3) | `internal/config/config.go:17-30` at published `d7191f8` — the same field list is `:17-33` in the slice 1 bytes, which append `TLS *TLSConfig` |
| Unknown config keys are ignored on load | `config.go:164,220,244` — `json.Unmarshal` with no `DisallowUnknownFields` |
| **But** saving rewrites the file from the struct, so keys absent from the struct are dropped | `config.go:296-300` (`func Save` → `json.MarshalIndent(cfg, …)`), called by `internal/webui/handlers/system.go:251` |
| Optional nested config block precedent | `config.go:120` `HealthCheck *RuntimeHealthCheck \`json:"healthCheck,omitempty"\`` — nil = disabled |
| Session cookie is hard-coded non-secure with the TLS TODO | `internal/webui/security/session.go:125-134` (`Secure: false, // will be set to true in middleware for HTTPS`, `SameSite: Lax`, `HttpOnly: true`) |
| CSRF cookie | `internal/webui/security/csrf.go:109-121` — `goal_csrf_token`, `HttpOnly: false` (must stay JS-readable for the double-submit copy), `SameSite: Strict`; scope: applied only when `authEnabled=true` (`docs/SECURITY.md:59-61`) |
| The session **clear** path omits both `Secure` and `SameSite` | `internal/webui/security/session.go:139-148` — `ClearSessionCookie` sets only `Name`/`Value:""`/`Path:"/"/`HttpOnly:true`/`MaxAge:-1`, so its attribute set does **not** match the cookie it clears once `Secure` is derived (§D18 rule covers this path) |
| Non-loopback + `authEnabled=false` is warned, never blocked | `internal/config/validate.go:26-32` + `validateLocalhostOnly` at `:320-331`; `docs/SECURITY.md:69-74` |
| Audit and login rate limiting deliberately key on the TCP peer | `internal/webui/handlers/helpers.go:147-153`, `handlers/routes.go:307`, tests `routes_rate_limit_test.go:108`, `audit_integration_test.go:253` |
| The request log already trusts `X-Forwarded-For` | `internal/webui/logger/logger.go:70`, `internal/webui/middleware/logging.go:59` — inconsistent with the row above |
| SPA is scheme-agnostic (relative URLs only) | `internal/webui/static/app.js` — `fetch('/api/v1/…')`, `new EventSource('/api/v1/instances/…/logs/stream')` at `:1117-1122`; no absolute origin |
| Service and foreground share one startup path | `cmd/goal/main.go:82,128-136` (`RunApp: runApplication(configPath, ctx)`) → `:214-309` (`webui.NewApp` → `app.Run(ctx)`) |
| Service path must be absolute / deterministically anchored | `docs/SECURITY.md:166` (ADR 011 pre-flight rule); `deploy/systemd/goal.service:8` |
| Single binary is a product contract | `docs/ARCHITECTURE.md:154` (ADR 0001), `docs/adr/003-webui-embedded-fs.md:47`, AGENTS.md |
| Server configuration is explicitly outside Portable Configuration | `docs/adr/014-portable-config-variables.md:557` ("Export `goal.json` / server settings — **Rejected**") |
| Current documented answer is "use a reverse proxy", and no such guidance exists | `docs/SECURITY.md:165,193`; `ROADMAP.md:209` unchecked; zero `nginx|caddy|IIS|traefik` matches in `docs/`; `deploy/` holds only the systemd unit |

## Decision

### D1 — TLS enable/disable model

TLS is **opt-in and off by default**. The whole feature is gated by one optional config block (§D3). Absent, empty, or `enabled:false` ⇒ GoAl behaves exactly as it does today: one plain HTTP listener, non-secure session cookie. No auto-detection, no "enable if cert files exist" magic.

### D2 — Listener ownership

- HTTP keeps its existing ownership: `listenAddress` + `webPort`, unchanged meaning (Owner decision D6).
- HTTPS is a **separate, second listener**: `listenAddress` + `tls.port`. It reuses the same bind address deliberately — a host that must be reachable on the LAN is reachable on both ports; splitting bind addresses per scheme adds configurability without a use case.
- GoAl never redirects HTTP → HTTPS in this contract (D6), and never disables HTTP implicitly when TLS is enabled.

### D3 — Configuration schema

**Option A (chosen): a nested optional `tls` object.**

```json
{
  "tls": {
    "enabled": true,
    "port": 8443,
    "certFile": "C:\\certs\\aids.crt",
    "keyFile": "C:\\certs\\aids.key"
  }
}
```

Go shape: `TLS *TLSConfig \`json:"tls,omitempty"\`` — `nil` means disabled, exactly the existing `healthCheck,omitempty` pattern (`config.go:120`).
Inside the block, `port` is a `*int`: the semantics table below fixes two different diagnostics for an absent key and for an explicit `0`, and a plain `int` cannot tell them apart. This is the representation the accepted schema needs, not an operator-facing choice — it adds no key, no default and no knob.

Rationale and rejected options:

| Option | Verdict | Why |
|---|---|---|
| **A — nested `tls` block** | **chosen** | Matches an existing in-repo pattern for an optional, self-contained feature with an internal `enabled` flag; one atomic additive key keeps `Validate`, `Save` and the future Settings UI honest; nothing top-level is renamed, so migration is a pure addition |
| B — top-level `httpsPort`, `tlsCertFile`, `tlsKeyFile`, `tlsEnabled` | rejected | Four new top-level keys in a file whose top level today is 11 flat identity/server keys; "enabled" implied by presence-of-path is the ambiguity that produces false-success |
| C — `listeners: [{scheme, address, port, …}]` array | rejected | Restructures `listenAddress`/`webPort`, i.e. breaks the compatibility baseline D6 requires, and is more general than a two-scheme product needs |

No additional knobs are added: no `minVersion`, no cipher list, no curve list, no per-listener address (§D22 explains why they are constants). "Do not add unnecessary configurability."

**Zero / absent semantics (exhaustive; no implicit default can open a listener).** `Enabled bool` cannot distinguish "absent" from `false`, and that distinction is deliberately **not** introduced: every non-true shape means *disabled*, which is today's behavior and can never expose a second listener.

| Config shape | Meaning | Startup outcome |
|---|---|---|
| `tls` key absent | disabled | proceeds; single HTTP listener (unchanged) |
| `"tls": {}` | disabled | proceeds, **no warning** — after unmarshal this is the *same struct* as `enabled:false`, and inventing a distinguishable "present but empty" state would require a `*bool` knob for no benefit |
| `"tls": { "enabled": false }` | disabled | proceeds; no warning (explicit opt-out) |
| `"tls": { "port": 0 }` with `enabled` absent, or `{ "enabled": false, "port": 0 }` | disabled | proceeds, **no warning** — a zero port is an inert value, so it is the `{}` case, not the configured-but-disabled case below. Clarification of the ratified `port *int` representation, not a new decision: the pointer exists ONLY so the two `enabled:true` port diagnostics below can differ; presence never turns a disabled shape into a warning or a failure |
| `"tls": { "enabled": false, "port": 8443, "certFile": …, "keyFile": … }` | disabled | proceeds; **warning** `tls configured but disabled` — inert values are never used, never partially applied (this is the one distinguishable disabled shape: a non-zero field while `enabled` is false) |
| `"tls": { "enabled": true }` with **no** `port` | invalid | **startup fails** (`tls.port is required when tls.enabled is true`) — no default port is chosen for an opt-in listener (§D4) |
| `"tls": { "enabled": true, "port": 0 }` | invalid | **startup fails** (`tls.port out of range 1-65535`) — `0` is not "auto" |
| `"tls": { "enabled": true, "port": -1 }` / `"port": 70000` | invalid | **startup fails**, same range rule |
| `"tls": { "enabled": true }` with **no** `certFile` | invalid | **startup fails** (`tls.certFile is required`) |
| `"tls": { "enabled": true }` with **no** `keyFile` | invalid | **startup fails** (`tls.keyFile is required`) |
| `enabled: true` + port + both paths, files valid | enabled | proceeds with **two** listeners |
| unknown key inside `tls` (e.g. `certificate`, `httpsPort`) | parsed-and-ignored on load | **fails only indirectly**: config load has no `DisallowUnknownFields` (`config.go:164,220,244`), so the typo surfaces as the missing required field above, and §D24 records that a later save drops the unknown key entirely |

Key matching is `encoding/json`'s own: an exact tag name wins, and failing that a case-insensitive match is used. So `certfile`, `CERTFILE` and `certFile` all populate the same field — a case variant is an alias of the real key, not an unknown key, and the next `config.Save` rewrites it in the canonical spelling. Only a key that matches no field at all (like `certificate`, or `httpsPort` against `port`) is silently dropped. Enabling still requires an explicit truthy `enabled`, so no key spelling can open a listener implicitly.

The rule that keeps this table honest is §D8: **`enabled == true` requires every field to be present, valid, absolute and loadable**; anything else is disabled, and disabled never reads the other fields.

### D4 — HTTPS port semantics

`tls.port` is required when `tls.enabled` is true (no implicit default port for an opt-in listener; an operator must see which port they opened). Validated `1–65535`, same range rule as `webPort`. It is a **restart-required** field, in the same class as `listenAddress`/`webPort` (`docs/CONFIGURATION.md:230-231`).

### D5–D7 — Certificate path, private-key path, absolute/relative rules

- `certFile` — PEM certificate chain, required when enabled.
- `keyFile` — PEM private key, required when enabled.
- **Both paths must be absolute.** A relative path is a validation **error**, not a warning. Reason: the Windows Service control manager and systemd do not guarantee a usable working directory, and ADR 011's published rule (`docs/SECURITY.md:166`) already requires every path a service depends on to be absolute or deterministically anchored to an absolute working directory. A TLS key resolved against the SCM working directory is the exact failure mode that rule exists to prevent.
- Paths are **never** accepted through the HTTP API or the Settings UI in the first slice (§D16 and slice 1 of §Implementation slicing); they are configured by an operator who can already place files on the host.
- GoAl does not search, generate, or pick up certificates from any location.

**Certificate chain contents (`certFile`).** GoAl uses **normal Go certificate semantics and nothing else**: the PEM file may contain a **leaf only** or a **leaf followed by intermediate certificate(s) in leaf-first order**, and Go serves the chain exactly as it appears in the file (`tls.X509KeyPair` — the same call the validation step already uses, §D8/D9). Consequences of that, stated normatively:

- GoAl adds **no** chain assembly, no issuer lookup, no ACME/CA issuance, no automatic intermediate download, and no re-ordering. A server cannot build a chain it was not given.
- If the file carries only the leaf, the browser receives only the leaf and must already hold the intermediate to build a path. Therefore the **operator** must supply the full chain whenever the leaf is not directly trusted — this is an operator-responsibility line, not a GoAl feature, and it is documented next to the SAN duty (§D17).
- Validation stays exactly §D8/§D9: pair loads, leaf validity window checked against the local clock, **no** chain-of-trust verification and **no** SAN/hostname check by GoAl (Go's server side performs neither; §D17 explains why a check would be meaningless — GoAl cannot know the names clients use).
- Extra non-certificate PEM blocks in `certFile` and trailing junk after the used blocks are not specially handled: `tls.X509KeyPair` errors on input it cannot parse as a certificate/key pair, which is §D9's "invalid PEM ⇒ startup fails".

### D8 — Startup validation

When `tls.enabled` is true, `Config.ValidateFull()` gains a TLS section that runs **before** any listener is opened: block present and complete, `port` range, both files exist and are readable, both parse, pair matches, validity window. Any failure ⇒ **startup fails** with a message naming the field and the file path (never the content). This matches the existing fail-closed precedent (`MigrateCredentials` aborts startup if it cannot persist) and satisfies "the service must not claim HTTPS is available when it is not".

### D9–D13 — Failure semantics (complete table)

| Case | Outcome | Rationale |
|---|---|---|
| `tls.enabled=true`, block missing/incomplete | **startup fails** | explicit request, silent drop = false success |
| certificate file missing | **startup fails** | as above |
| key file missing | **startup fails** | as above |
| unsupported / invalid PEM (either file) | **startup fails** | `tls.X509KeyPair` error, surfaced verbatim minus content |
| certificate / private key mismatch | **startup fails** | `tls.X509KeyPair` reports the mismatch |
| certificate expired | **startup fails** | Go does **not** check `NotAfter` at load time; this ADR mandates an explicit check against the local clock |
| certificate not yet valid | **startup fails** | explicit `NotBefore` check, same reasoning |
| HTTPS port already occupied | **startup fails**, **no listener is left serving** (§D15) | bind-before-serve |
| HTTP port already occupied | **startup fails** (unchanged from today) | existing behavior |
| `tls.port == webPort` (same address) | **startup fails**, message names both fields | cannot be intended; today `validateAddress` only checks bindability of one address |
| key file world-readable (POSIX) | **warning only**, startup proceeds | mode is not reliable on Windows; a hard failure would break normal SMB/ACL layouts |
| hostname/SAN does not cover the address clients use | **GoAl does not check** — browser-side failure | §D17: identity responsibility is the operator's |
| TLS disabled (`nil` / `enabled:false`) + stray `certFile` set | **warning only** ("configured but not used") | avoids a trap without failing on inert config |

There is deliberately **no** "HTTPS fails, HTTP continues" state in the first slice: half-running security config is the misleading partial state the Owner's §4.25 forbids.

### D14 — Self-signed / development mode

Not implemented and not designed as a product feature in this ADR. If it is added later it must, per Owner decision D5, be explicit in config, documented as warning-bearing, and must never be presented as trusted TLS. Recorded here as the only sanctioned place for it: a future, separate decision. The measured fact that constrains it: a bypassed interstitial still yields `isSecureContext === true` — i.e. the capability works, transport *authentication* does not.

### D15 — Startup failure atomicity, listener lifecycle

1. Resolve config → validate (D8).
2. `net.Listen("tcp", …)` for **every** enabled listener **before** serving anything. On the first bind error, close every already-bound listener and fail startup. No port is published while another intended port failed.
3. Serve each bound listener on its own `http.Server` (`ReadHeaderTimeout`/`ReadTimeout`/`IdleTimeout`/`MaxHeaderBytes` identical to today's values; HTTPS servers additionally carry `TLSConfig` with `MinVersion: tls.VersionTLS12`).
4. `Run(ctx)` waits on the existing `select` shape extended to: context cancellation **or** any listener's serve error. A serve error after successful bind is a hard error returned to the caller (service supervisor / foreground), not a log line.
5. Shutdown: **one shared deadline** (today's 10 s) across all listeners — `Shutdown` each concurrently against the same `shutdownCtx`, aggregated error — so total stop latency is unchanged by adding a listener, which matters for the SCM stop deadline (ADR 011).
6. Structured startup log names both listeners separately (§D19).

**Runtime atomicity after successful startup (the §5 question).** The configured listener set is **one application capability**. If any `Serve` loop returns after startup succeeded, that return is treated as loss of the capability: GoAl initiates **coordinated shutdown of every remaining listener** and returns an aggregated error to the caller (foreground process exit code / service supervisor), exactly as today's single listener does with `return fmt.Errorf("serve HTTP: %w", err)` (`server.go:231-236`). The surviving listener is **not** left serving. A panic inside a handler is **not** this case — `http.Server` recovers handler panics per connection and the loop keeps running — so the rule concerns a `Serve` call that actually returns. Rationale: the alternative would silently degrade an operator's declared intent (HTTP+HTTPS) into partial success, which is the false-success shape §D8–§D13 exists to prevent, and it would be invisible to the SCM/systemd supervisor, which only sees a running service. Practical consequence: `Run(ctx)`'s existing `select` grows one case per listener, and the failure path reuses the same cancellation and shutdown code that context cancellation already uses, so shutdown ordering, the single 10 s deadline (step 5) and audit `instance.stop` semantics are unchanged.

An `http.Server` whose `Serve` returns `http.ErrServerClosed` is the *coordinated shutdown already in progress*, not a failure: that case must be swallowed rather than reported as an error, otherwise a normal stop looks like a crash.

### D16 — API / UI implications

None required in the first slice: no new endpoint, no new request/response field, no Settings-UI editing of TLS (see §D5 rationale). The UI already reads `/api/v1/version`, which stays unchanged. Adding cert details to any endpoint is **rejected** for slice 1 — it would publish certificate metadata on a surface that today answers unauthenticated (`/api/v1/health`, `/api/v1/version`), and it is unnecessary for the operator workflow. If a future slice wants to show TLS state in Settings, it must go through the authenticated surface and expose only a boolean plus expiry, never a path.

### D17 — Trust, SAN and operator responsibilities (security contract)

The ADR separates five things that are routinely conflated:

| Layer | Provided by GoAl's native HTTPS? | Notes |
|---|---|---|
| transport encryption | yes | between browser and GoAl |
| **browser trust** (no warning) | **no** — operator-supplied chain must validate | GoAl cannot make an untrusted chain trusted; Owner decisions D4/D5 keep GoAl out of CA business |
| secure-context browser APIs (FSA) | yes, as a consequence of the `https:` URL being trustworthy to the browser | an untrusted-but-bypassed certificate also reaches this state (measured) — at the cost of layer 2 |
| authentication | **no** | `authEnabled` is independent; HTTPS ≠ secure admin panel |
| authorization / CSRF / session protection | partially (cookie flags, §D18) | single admin user model unchanged |

**Operator responsibilities, stated normatively:** if warning-free remote browser use is desired, the operator-supplied certificate must chain to a root the client browsers trust, and its **SAN must cover every name or address clients actually use** — `DNS:aids` for `https://aids:8443`, and separately `IP:192.168.3.245` for address access. GoAl performs no hostname/SAN validation and cannot know the names clients will use. Certificate renewal, expiry monitoring and replacement are operator duties; GoAl's contribution is failing loudly at startup rather than serving a dead/expired intent.

### D18 — Secure-cookie contract (corrected by measurement)

**Rule (normative, slice 1).** Every `Set-Cookie` GoAl emits — session set, session **clear**, CSRF set — carries `Secure` **if and only if the connection that produced that response had `r.TLS != nil`**. It is evaluated per response, never per config value. This closes the `session.go:131` TODO without a config knob and without pretending to know about proxies (GoAl does not read `X-Forwarded-Proto`, §D20). The rule covers the clear path on purpose: measured D2/D4 show that a trustworthy connection **can** overwrite or clear a `Secure` cookie with a non-`Secure` one, so a clear path that kept today's hard-coded `Secure:false` would silently downgrade the jar entry.

**Namespace (the correction).** HTTP and HTTPS on the same host are **one cookie jar**: a cookie's key is (name, domain, path) — neither the scheme nor the port participates (measured C1 and C4). The previous wording in this ADR called them "separate credential contexts"; that was wrong and is retracted. GoAl's cookies are host-only (`session.go:129`, `csrf.go:113` set `Path:"/"` and never set `Domain`), so `http://aids:8088` and `https://aids:8443` address the same entries by design, while `https://192.168.3.245:8443` and `https://aids:8443` are **different** jars (different host strings) and never share a session.

**Transition semantics — observed, and normative for this contract.** The three sequences the gate required map onto these rows exactly: **HTTP → HTTPS** = C1; **HTTPS → HTTP** = C3; **HTTP → HTTPS → HTTP** has **two** different outcomes and both were measured — if HTTPS did not re-issue the cookie, the same non-`Secure` cookie travels back down and the HTTP session continues (sequence R1), while if HTTPS issued a `Secure` cookie in between, the return to HTTP is unauthenticated (C3) **and** cannot re-login (C5a). Both `goal_session` (rows C1–C5, R1) and `goal_csrf_token` (row C4) are covered.

| # | Transition | Observed behavior | Contract reading |
|---|---|---|---|
| C1 | HTTP login (`Secure=false`) → open HTTPS | cookie **is** sent to HTTPS | the HTTP-issued session authenticates over TLS; no re-login |
| C2 | HTTPS login (`Secure=true`) after that | jar holds **one** entry — it **replaced** the non-`Secure` cookie | one name, one value; the later write wins |
| C3 | HTTPS login → open HTTP | cookie **not** sent (`received=""`) | the user appears **logged out on HTTP** |
| C5a | HTTP login attempt while a `Secure` cookie exists | browser **refuses the write**; jar unchanged | HTTP login becomes a **no-op** until that cookie expires or is cleared over HTTPS — a security property (no network downgrade/session-fixation) and the coexistence hazard of §Open questions 1 |
| C5b | HTTP logout against a `Secure` cookie | cookie **survives** | HTTP cannot clear it (and never receives it) |
| C5c / D4 | HTTPS logout | cookie removed (with or without `Secure` on the clear) | clearing works from the scheme that set it |
| D2 | **HTTPS** response with `Secure=false` | **accepted**; jar loses `Secure` and then travels over both schemes | why every emit path must use the same `r.TLS != nil` rule |
| C4 | `goal_csrf_token` through the same transitions | identical one-way behavior; after a `Secure` replacement, `document.cookie` is **empty** on the HTTP page | mixed-scheme use of an authenticated session is not cosmetic — the double-submit header cannot be produced over HTTP, so unsafe requests there fail |

**Required properties.**

1. **Security:** a cookie emitted with `Secure=true` is never transmitted over plain HTTP — verified (C3, C5a, C5b).
2. **Compatibility:** when TLS is disabled GoAl emits no `Secure` cookie at all, so the jar is exactly as shipped and none of the transitions above is reachable; HTTP-only behavior is unchanged.
3. **No new mechanism:** cookie names (`goal_session`, `goal_csrf_token`), `Path:"/"`, host-only scope (no `Domain`), `HttpOnly` values, and `SameSite` values (`Lax` session / `Strict` CSRF) all stay **unchanged**. No second cookie name, no scheme-specific namespace, no `__Host-`/`__Secure-` prefix — a prefix would force `Secure` and break the HTTP compatibility baseline Owner decision D6 requires.
4. **Server side stays single-store:** sessions remain the one in-memory token store (`session.go`), not partitioned by scheme; a token issued over HTTP and presented over HTTPS is the same session (that is C1, and it is accepted deliberately).

**Deployment states (the four the gate required):**

| Deployment | Session cookie | Result |
|---|---|---|
| HTTP only (today, unchanged) | `Secure:false` | byte-identical behavior to shipped |
| HTTPS only in practice (operator opened HTTPS and uses it) | `Secure:true` | upgrade, no config beyond `tls.enabled` |
| HTTP + HTTPS coexistence | per response (`r.TLS != nil`) | **one shared jar, one-way ratchet**: HTTP→HTTPS carries the session up; HTTPS→HTTP does not carry it down, and while the `Secure` cookie lives an HTTP login cannot install its own cookie (C5a). The supported operator rule is therefore *work in one scheme per browser*, not *one session everywhere*. |
| External HTTPS reverse proxy terminating TLS | `r.TLS == nil` at GoAl ⇒ `Secure:false` | same as today's documented proxy guidance; recorded as a limitation of that alternative, not a regression |

**C6 — does GoAl need different names, paths, domains, or another mechanism? No.** The measured jar is coherent under one name per cookie, and the only defect coexistence exposes (a live `Secure` cookie blocks HTTP writes) is precisely the downgrade protection a secure deployment wants. A per-scheme name or path would "fix" it only by splitting sessions, which contradicts Owner decision D6 (HTTP stays backward compatible) and rule 3 above. Slice 1 documents the ratchet; it does not code around it.

### D19 — Warnings, authentication requirements, logging, diagnostics

- The existing `authEnabled=false` + non-loopback warning (`validate.go:26-32`) is **extended in wording, not in force**: it must fire for the HTTPS listener as well, and its text must say that an unauthenticated admin API over the LAN is exposed regardless of scheme. **Startup is still not blocked** and no configuration is mutated (Owner decision D7). Making non-loopback binding require auth is a separate product decision (Owner decision P6; see §Open questions).
- Startup log must name both listeners distinctly, e.g. `starting HTTP server addr=…` and `starting HTTPS server addr=… (tls enabled, cert=<path>, expires=<RFC3339>, san=<list>)`. **Private-key material must never appear in any log line, error message, or diagnostic**; failure messages name the *path* and the *reason*, never the contents. Test obligation 8.
- Shutdown/serve logs distinguish which listener errored.

### D20 — Reverse-proxy boundary

A reverse proxy remains a supported **deployment alternative**, explicitly *not* the primary product path (Owner decision D3): the single-binary secure-origin path must not require nginx/Caddy/IIS/Traefik. GoAl adds **no trusted-proxy semantics** in this contract: it does not honor `X-Forwarded-Proto`, `Forwarded`, or `X-Real-IP` for security decisions. Consequence stated plainly: behind a TLS-terminating proxy the session cookie remains non-`Secure` (§D18), so proxy deployments inherit today's exact cookie posture.

Recorded accurately, without expanding this ADR into a proxy-identity redesign (§Open questions 2): the repository is **already inconsistent** — audit (`helpers.go:147-153`) and login rate limiting (`routes.go:307`, test `routes_rate_limit_test.go:108`) deliberately use the TCP peer, while the request log (`logger.go:70`, `middleware/logging.go:59`) reads `X-Forwarded-For`. Any change there is separate work and must be classified on its own merits.

### D21 — Windows Service and Linux behavior

- No new path: service mode runs the same `runApplication` (`cmd/goal/main.go:128-136` → `:214-309`), so TLS validation, fail-closed startup and dual listeners behave identically under SCM and systemd.
- Because of that shared path, the absolute-path rule (§D5–D7) is what keeps the service installable: LocalSystem's working directory must never be relied on. Key readability at boot is an operator provisioning duty (file present, service account can read it, no interactive prompt — GoAl never prompts).
- A fail-closed startup under the SCM surfaces as a service start failure, which is the intended loud outcome rather than a half-listening service.
- `deploy/systemd/goal.service` needs no unit change; documentation gains the key-permission expectation only.

### D22 — Minimum TLS version and cipher policy

`MinVersion: tls.VersionTLS12`, as a **constant**, not configuration. Cipher suites: Go's `crypto/tls` defaults (TLS 1.2 + TLS 1.3, modern suites) — no `CipherSuites` override. Rationale: the repository's own guidance for this direction says "prefer safe Go defaults unless repository requirements justify explicit configuration"; no requirement in this ADR justifies an operator-facing crypto knob, and one invites downgrade misconfiguration. TLS 1.3 is served wherever the client supports it. `ServerName`, client authentication (mTLS) and ALPN/h2 settings are left at Go defaults and are not configurable.

### D23 — Certificate renewal / reload

**Restart required.** `tls.*` is not a hot field; ADR 009's hot surface stays `logLevel` only. Renewal procedure is documented (replace files → restart → startup validation confirms the new chain and logs the new expiry). Live reload is deferred (§Open questions 4): a swapped listener or a mid-flight `GetCertificate` closure adds atomicity questions that the first slice does not need, because renewal is rare and restart is already the contract for `listenAddress`/`webPort`.

### D24 — HTTP backward compatibility and migration

- Absent `tls` ⇒ byte-identical behavior; every documented HTTP workflow keeps working, including `curl -b goal_session=… http://…/api/v1/export|import` (`docs/USER_GUIDE.md:806-831`) and the health probe used by the UI's connection indicator.
- Enabling TLS is additive and reversible: delete the `tls` block (or set `enabled:false`) and restart ⇒ prior state exactly.
- **Rollback hazard, recorded:** `config.Save` rewrites `goal.json` from the struct (`config.go:296-300`, called by `handlers/system.go:251`). Keys **absent from the struct are dropped**. So (a) telling operators to add a `tls` block before the release that parses it would let any settings save silently erase it, and (b) downgrading the binary past the TLS release and then saving settings loses the block. Documentation must state both, and the schema and the docs must ship in the same release.

### D25 — Portable Configuration exclusion

TLS configuration is **server configuration**, already excluded from Portable Configuration by ADR 014 (`docs/adr/014-portable-config-variables.md:557`). Consequences: the export bundle gains no `tls` key; **certificate paths, certificate content and any key material must never enter a bundle, an import plan, an export response, or a log**; the import path must not create or accept TLS config. `ROADMAP.md:157`'s existing constraint (secret-safe export must account for future TLS private keys) is satisfied by this exclusion.

### D26 — Relationship to ACME and to future work

ACME (`ROADMAP.md:216`) stays a **dependent Later item** and is not designed here: HTTP-01/TLS-ALPN validation needs a publicly resolvable hostname, which `aids` is not. Also left open by design: GoAl-as-private-CA, an HTTP-disable mode, TLS-in-Settings-UI, live cert reload, per-listener bind addresses, mTLS.

### D27 — Relationship to OWNER-EXPORT-01

OWNER-EXPORT-01 remains a **client/UI capability** (Owner decision D1): Slice 1's picker-first implementation, its two i18n keys and its `tests/browser/portable.cjs` capability branches stay valid and are the accepted client half. It is **not** OWNER PASS, and nothing in it is committed by this ADR (Owner decision D9). *(Status clarification 2026-09-30: those picker bytes were subsequently committed and published by OWNER-EXPORT-01's own gates, never by ADR 019; the "not OWNER PASS" half is still current — that acceptance FAILED on 2026-09-29 and can only be repeated by slice 5, which is NOT started.)* When an ADR 019 implementation provides a supported `https://aids:<port>`, OWNER-EXPORT-01 acceptance is **repeated there** with the required flow: Settings → Portable Configuration → Скачать → native Save As → choose filename → choose destination → save → select the saved JSON for Import validation. The final native-dialog Save completion cannot be automated (Playwright cannot drive native file dialogs), so that obligation stays manual.

## Implementation slicing (status 2026-10-01: slices 1–4 implemented, committed and published; slice 5 NOT started)

Dependency-ordered; each slice carries its own commit/publication gates. Slice-level status only — the full provenance of each published slice (commits, CI runs, evidence) is recorded in `ROADMAP.md`, not in this slicing list; a status line in this record's header may name a published slice's commit and exact-SHA run to identify the state it reports, without reproducing that ledger.

1. **Config schema + validation + failure semantics** *(implemented — committed and published)* — `internal/config` only: `TLS` struct, `omitempty` wiring, `ValidateFull` additions per §D8/§D9, port-collision check, expiry checks, absolute-path rule, unit tests for every table row. No listener change.
2. **Dual-listener lifecycle** *(implemented — committed and published)* — `internal/webui/server.go`: bind-before-serve, per-listener `http.Server`, `TLSConfig{MinVersion}`, serve-error propagation, shared-deadline shutdown, listener-naming diagnostics. Tests for atomicity and shutdown.
3. **Cookie + warning contract** *(implemented — committed and published)* — `internal/webui/security/session.go` (`SetSessionCookie` **and** `ClearSessionCookie`) + `csrf.go` (`SetCSRFCookie`): `Secure` derived from `r.TLS != nil`; `session.go:131` TODO removed; extended non-loopback warning wording in `internal/config/validate.go`. Tests for all four deployment states and the §D18 transitions.
4. **Documentation** *(implemented — committed and published)* — `docs/CONFIGURATION.md` (new `tls` block + restart class + the §D3 zero/absent table), `docs/SECURITY.md` (replace "No HTTPS in binary" at `:193`, bind table `:69-74`, cookie table `:14` with the `Secure` rule, **the §D18 session ratchet under coexistence**, operator trust/SAN/chain duties, key permissions, rollback hazard), `docs/USER_GUIDE.md` + `docs/USER_GUIDE_RU.md` (how to obtain a supported secure origin; the one-origin-per-browser guidance; revise the export paragraphs written this cycle), `docs/API.md` only if any observable surface changes (slice plan says it does not — **delivered outcome, reconciled 2026-10-01: the Owner confirmed that conditional as met, because slice 3 changed the observable `Set-Cookie` `Secure` attribute, so `docs/API.md` gained exactly one normative paragraph and no endpoint or shape change; the plan's own expectation is recorded above unchanged**), `docs/ARCHITECTURE.md` ADR table (which today stops at 014 — as of this design gate; slice 4 delivered the 0019 row).
5. **Browser/HTTPS acceptance + reconciliation** *(NOT started)* — new self-signed `https://<hostname>:<port>` fixture in `tests/browser/` proving the non-loopback secure-context branch; `ROADMAP.md`/`BACKLOG.md` reconciliation; acceptance binary for the repeated OWNER-EXPORT-01 acceptance.

Slices 1–2 changed no cookie, no warning wording, no documentation surface and no browser fixture; items 3–5 above were therefore still fully owed at that point *(updated 2026-10-01: slice 3 has since delivered item 3, so items 4–5 are what remains owed; updated again the same day after slice 4's own gates: slice 4 has since delivered item 4, so item 5 is what remains owed)*.

## Test obligations (for the implementation slices)

1. Each §D9 row as a table-driven startup test: expected error/warning/success, and **no listener left bound** on failure.
2. `tls.port == webPort` rejected with a message naming both fields.
3. Expired and not-yet-valid certificates rejected (asserts the explicit clock check).
4. Cert/key mismatch and malformed PEM rejected; message contains the path and reason, never file content.
5. Relative cert/key path rejected; absolute accepted.
6. Absent `tls` ⇒ single-listener behavior identical (regression baseline).
7. Two-listener shutdown within one shared deadline, including with an open `EventSource` log stream.
8. Log-privacy assertion: generated key material never appears in captured slog output across the failure matrix.
9. Cookie flags on **every emit path**: `Secure` present iff the emitting connection had `r.TLS != nil`, for `goal_session` set **and clear** and for `goal_csrf_token`; CSRF stays `HttpOnly:false`/`SameSite:Strict`; session stays `HttpOnly:true`/`SameSite:Lax`; `Path:"/"` and host-only scope (no `Domain`) unchanged; the four §D18 deployment states covered.
10. `X-Forwarded-Proto` / `X-Forwarded-For` / `X-Real-IP` do not flip `Secure` or the audit/rate-limit identity (extend the existing `routes_rate_limit_test.go:108` posture).
11. `config.Save` round-trip preserves the `tls` block; unknown-key tolerance documented by test where feasible.
12. Portable export/import contain no `tls`, cert path or key material (ADR 014 exclusion asserted).
13. Browser: `https://<hostname>` with a self-signed cert + `ignoreHTTPSErrors` ⇒ `isSecureContext true`, `typeof showSaveFilePicker === "function"`; `http://<hostname>` ⇒ capability absent, fallback branch taken. Native-dialog completion remains a **manual** Owner acceptance obligation.
14. §D18 transition matrix. GoAl can only assert what it **emits** — the jar behavior after that belongs to the browser — so each measured row (C1, C2, C3, C5a, C5b, C5c, D2) gets (a) a Go-side assertion on the `Set-Cookie` attributes for the emitting scheme and (b) one maintained browser test that reproduces the coexistence sequence on a single host name and asserts the observable outcome the contract documents: an HTTPS login is followed by an HTTP page that is **not** authenticated and cannot install a session cookie. That test pins the documented consequence; it must not be written as an assertion that GoAl implements the protection.
15. §D3 zero/absent table: one case per row, asserting fail / warn / proceed, and specifically that no shape other than `enabled: true` with a complete, valid block can open a listener.
16. Chain contents: a leaf-only `certFile` and a leaf+intermediate `certFile` both load and serve; the chain GoAl presents equals the blocks in the file (no assembly, no reordering, no fetching).
17. Runtime listener loss: forcing one `Serve` to return produces coordinated shutdown of the sibling listener and an aggregated error to the caller; a shutdown already in progress (`http.ErrServerClosed`) is **not** reported as a failure.
18. Standard gates: `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./...` (race evidence from Linux CI, per repository convention), Windows and Linux builds, full maintained browser suite.

## Consequences

- GoAl gains a second listener and a crypto surface: more startup failure modes (all loud by design), key-file custody as an operator responsibility, and documentation that must stop saying "TLS is not terminated inside GoAl".
- Backward compatibility is preserved by construction; nothing changes for an operator who does not add the block.
- Coexistence gains a **documented one-way session ratchet** (§D18): HTTPS is the scheme that keeps working, and a browser that has logged in over HTTPS cannot log in again over plain HTTP until that cookie expires or is cleared over HTTPS. `docs/SECURITY.md` and both `docs/USER_GUIDE*.md` must state this plainly; an operator who wants both schemes usable should treat one as the working origin.
- OWNER-EXPORT-01 becomes satisfiable in the Owner's real usage model without a proxy, a tunnel, or a browser preference, and without a mixed-task commit.
- The ADR does **not** by itself make a deployment secure: `authEnabled=false` over a LAN — the Owner's currently observed posture (`http://aids:8088` answering `auth_disabled:true`) — remains an open configuration concern (Owner decision D7: no action on the Owner machine was authorized by this gate).

## Owner decisions P1–P8 — disposition (2026-09-29 reconciliation gate)

| # | Owner decision | Disposition in this ADR |
|---|---|---|
| P1 | Connection-derived `Secure`; do not force it because TLS is configured | adopted (§D18). The "separate credential contexts" wording is **retracted**: measurement shows one jar keyed by (name, domain, path), and §D18 now states the real HTTP↔HTTPS transitions with the required security and compatibility properties |
| P2 | Fail-closed startup for the whole intended listener set | adopted (§D9–§D13, §D15, §Runtime atomicity in §D15) — no partial-success serving state, in either the startup or the runtime direction |
| P3 | `certFile`/`keyFile` must be absolute, consistently in foreground, Windows Service and systemd | adopted (§D5–§D7, §D21); relative paths are validation errors |
| P4 | Trust-store / CA work is **not authorized** and stays future work | adopted (§Scope out-of-scope, §D14, §D17); no measurement of trust installation was performed and none is required for slice 1 |
| P5 | Proxy identity is **separate** security/technical debt; ADR019 must not decide security from forwarded headers | adopted (§D20) — no `X-Forwarded-Proto`/`Forwarded`/`X-Real-IP` trust; the existing audit-vs-request-log `X-Forwarded-For` inconsistency is recorded as debt owned by a different task (see §Open questions 2) |
| P6 | Non-loopback without auth stays **warning-only** in ADR019 | adopted (§D19) — wording extended, force unchanged, no silent auth enablement; HTTPS is stated as transport protection that does **not** authenticate an admin surface |
| P7 | Do not touch `ROADMAP.md` in this gate; reconcile ADR019 tracking only through a clean, separately gated documentation boundary | adopted (§Governance boundary) |
| P8 | TLS configuration is `goal.json`-only in slice 1; no Settings cert/key controls, no API that accepts cert/key paths, no key material through API/UI | adopted (§D16, §D5–§D7) |

## Open questions / deferred

1. **No open Owner decision blocks slice 1.** The corrected cookie analysis produced one user-visible consequence worth naming rather than burying: under HTTP+HTTPS coexistence on one host, a live `Secure` session cookie **blocks a later plain-HTTP login in the same browser** (measured C5a/C5b — the browser refuses to overwrite or clear a `Secure` cookie from a non-secure context). This is the required security property working as specified, and Owner decision D6 already fixes the answer: HTTP behavior must remain backward compatible, so GoAl does **not** refuse or degrade HTTP login when TLS is configured. It is therefore a **documented operator consequence** (§D18, documentation slice 4), not a product decision. If the Owner ever wants the opposite posture (HTTPS-only login enforced by the binary), that is a new, separate gate.
2. Proxy-identity inconsistency (audit/rate-limit TCP-peer vs request-log `X-Forwarded-For`, §D20) — classified per P5 as **separate security/technical debt**, owned outside ADR019; it must be tracked in `BACKLOG.md` by whichever documentation boundary the Owner authorizes (not this gate, per P7).
3. Whether per-user (non-administrator) trust installation is honored by Chrome on Windows remains **unmeasured**, and stays unmeasured per P4. It blocks any future CA/trust-install design, not slice 1.
4. Live certificate reload; HTTP-disable mode; Settings-UI TLS fields; mTLS; per-listener bind addresses; `__Host-`/`__Secure-` cookie-name prefixes (rejected by §D18 item 3 and C6, but a name change is a migration decision for any future slice) — all deferred.

## Governance boundary

**File status at the 2026-09-29 design and reconciliation gates, recorded verbatim as a then-state and not as current state:** This ADR file is an **untracked working-tree document**; neither the design gate nor the reconciliation gate committed anything (Owner decision §10 "Do NOT commit anything", P7 "DO NOT modify ROADMAP.md in this gate"). The 2026-09-29 reconciliation gate modified **only** this file — the eight uncommitted OWNER-EXPORT-01 Slice 1 paths and every `.go` file are byte-identical to their state at gate start.

**The 2026-09-29 gate-time position, kept verbatim and not current state:** `ROADMAP.md` is deliberately **not** edited: it currently carries the uncommitted OWNER-EXPORT-01 Slice 1 changes, and writing the "Native HTTPS REOPENED / ADR 019 ACCEPTED" reconciliation into the same file would fold ADR 019 tracking into the Slice 1 commit, which is the mixed-task commit §7 forbids. The reconciliation is therefore an explicit obligation of the ADR 019 documentation and acceptance slices (items 4–5 of §Implementation slicing) or of a separately gated docs commit. *(Reconciled 2026-09-30: that separately gated docs commit ran, was committed as `6e5ae01…` and published the same day, so `ROADMAP.md` now carries the ADR 019 record and the words above are the 2026-09-29 position.)*

**Documentation boundary (2026-09-30) — the separately gated docs commit anticipated above.** OWNER-EXPORT-01 Slice 1 was committed (`b164be4`) and published, and the ROADMAP tracking-truth corrections that followed it were committed (`2601972`, `e1d9ffb`) and published, so the mixed-commit hazard that made this file untracked no longer exists. Under its own Owner gate this boundary brought this ADR into version control, recorded **ADR 019: ACCEPTED — IMPLEMENTATION NOT STARTED** in `ROADMAP.md` (P1 → Native HTTPS / TLS design gate, plus the dated reopening of the 2026-09-06 PAUSED status), and opened the §D20 / §Open questions 2 proxy-identity item as its own `BACKLOG.md` debt entry — the disposition Owner decision P5 required and P7 deferred to exactly this kind of gate. Nothing here changes a design decision, an Owner decision D1–D9 or P1–P8, the measured cookie contract, the slicing, or the test obligations; the two paragraphs above are kept verbatim as the record of how the 2026-09-29 gates ran — the dated labels on those two paragraphs were added by the 2026-09-30 post-publication wording reconciliation, which changed no sentence of them, only the scope under which they read. This boundary's own publication provenance: committed as `6e5ae012cd2ef33ba21cf4b784eee102b19fd030` and published the same day as the fast-forward `e1d9ffb..6e5ae01 -> main`, verified by exact-SHA CI run `36623670568` (event push, attempt 1, 7/7 jobs). Implementation, commit and publication of **TLS behavior** remain subject to their own separate Owner gates; this boundary is docs/tracking only.

## Acceptance contract (design + reconciliation gates)

- Owner decisions D1–D9 of the 2026-09-29 design gate are reflected without contradiction: capability-only ownership of EXPORT-01 (§D27), reopened native HTTPS (§Context/D1), single-binary preservation (§D2, reverse proxy demoted to alternative in §D20), operator-supplied cert/key as primary with no CA and no trust-store writes (§D17, D4/D5 constraints), self-signed excluded from the production trust story (§D14), HTTP coexistence with no redirect and unchanged `webPort` meaning (§D24, D6), auth never silently enabled (§D19, D7), and the `session.go` TODO reconciled by a stated cookie rule (§D18, D8).
- Owner decisions P1–P8 of the 2026-09-29 reconciliation gate are dispositioned in §Owner decisions P1–P8, and the cookie contract was rewritten from **measured** browser behavior rather than from an assumption about scheme isolation (§Context measurement table, §D18).
- No TLS code, config field, listener change, cookie change or certificate exists in the repository as a result of either gate. *(Status clarification 2026-09-30: this sentence is about the two 2026-09-29 gates and remains true of them — neither gate wrote code. TLS code does now exist in the repository, but only as the output of this ADR's own later implementation gates: slice 1 added the `tls` config schema and its validation in `internal/config`, slice 2 added the dual-listener lifecycle in `internal/webui/server.go`. Still absent at HEAD as of that clarification: any cookie change or `Secure` derivation, the extended warning wording, and any certificate, key or browser fixture — those were slices 3–5 work, and slice 3 has since delivered the cookie change, the connection-derived `Secure` rule and the extended warning wording, while the documentation rows and the HTTPS browser fixture remain slices 4–5 work.)* *(Reconciled 2026-10-01 after slice 4's publication: of that clause the documentation rows are now delivered, committed and published, so only the HTTPS browser fixture remains owed, as slice 5.)*
- ADR 012 is neither created nor referenced as an artifact; the number 019 is used because 014 records that 012's number is reserved by the historical paused-direction reference and 018 is taken.
- Status is **Accepted** because no architecture or product decision required to build slice 1 remains open. What the cookie analysis exposed is a *consequence* of decisions the Owner already made (D6 + P1), and it is documented instead of coded around (§Open questions 1). Reopening triggers are named there: any wish to enforce HTTPS-only login from the binary, or to change cookie names/paths, is a new gate.
- Verdict line of the two 2026-09-29 gates, kept verbatim as their then-state: **ADR019: ACCEPTED — IMPLEMENTATION NOT STARTED.** Current implementation status (reconciled 2026-09-30, then updated 2026-10-01 after slice 3's publication and again the same day after slice 4's): **ACCEPTED — slices 1–4 COMPLETED, COMMITTED AND PUBLISHED (slice 4 documentation-only, at `f759332cdcea0deec71954c9843c95d8bb0c18a3`, exact-SHA CI `36843329049` attempt 1, 7/7 jobs, no rerun, not tagged, not released); slice 5 NOT STARTED and the only remaining implementation slice; OWNER-EXPORT-01 remains OPEN pending slice 5.** The 2026-09-30 wording of this clause — "slice 1 and slice 2 COMPLETED … slices 3–5 NOT STARTED; slice 3 is the next implementation slice" — and its 2026-10-01 after-slice-3 wording — "slices 1–3 COMPLETED, COMMITTED AND PUBLISHED; slices 4–5 NOT STARTED; slice 4 (documentation) is the next implementation slice" — were each accurate when written and are superseded as tracking truth only.
