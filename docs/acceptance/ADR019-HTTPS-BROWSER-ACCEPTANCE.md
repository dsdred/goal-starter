# ADR 019 — Native HTTPS / Secure Origin: Browser Acceptance Record

**Purpose:** record what ADR 019 slice 5 actually proved, what it did not prove, and what still requires the Owner's own hands. *(Written 2026-10-01, when result C was still in the third category; the Owner's own hands supplied it on 2026-10-03 and section C now records that run.)*

**Source of truth:** repository bytes at HEAD `7125cc3f1030a67815401ed6dccb2a04b56b21bb` (`test(adr019): add native HTTPS browser acceptance`, parent `9a019a8880778e36cf11f1a1de1c2eb6da0b127f`, 7 paths, 923 insertions / 8 deletions), published as the plain fast-forward `9a019a8..7125cc3 -> main`. Design contract: [ADR 019 — Native HTTPS / Secure Origin](../adr/019-native-https-secure-origin.md). The per-slice evidence ledger lives in [`ROADMAP.md`](../../ROADMAP.md); this file records only slice 5. **Result C concerns a different artifact:** the acceptance binary built from `66f3dc4ba2e10b904bc0d832e8501b17b005725e` (slice 5's post-publication documentation reconciliation, parent `7125cc3`), whose Go, test, fixture and workflow bytes are byte-identical to the SHA above — `git diff --stat 7125cc3..66f3dc4 -- internal cmd tests testdata .github` is empty.

**Date of this record:** 2026-10-01. **Result C updated 2026-10-03** after the Owner's manual run; sections A and B are unchanged from 2026-10-01 except the cross-references to C made necessary by that update.

---

## The three results, kept separate

| # | Result | Verdict | What it means |
|---|--------|---------|---------------|
| **A** | Automated HTTPS browser acceptance | **PASS** | The maintained Playwright/Chromium suite `tests/browser/https.cjs` runs against a real `https://<hostname>:<port>` GoAl listener and every one of its 58 checks passes, alongside the full 14-suite chain. |
| **B** | Exact-SHA Publication Gate | **PASS** | CI run `36899769739` for `headSha 7125cc3f1030a67815401ed6dccb2a04b56b21bb`, attempt 1, conclusion `success`, **7/7 jobs, no rerun**. |
| **C** | Manual OWNER-EXPORT-01 acceptance | **PASS** (2026-10-03) | The Owner repeated the flow on a real machine at `https://aids:8443`, and personally completed the native Windows **Save As** dialog; the saved bundle and its SHA-256 were then verified independently. Its 2026-09-29 failure is closed, and nothing in A or B is part of this verdict — C stands on the Owner's own run. |

**A + B are not C.** An automated headless run cannot complete an operating-system file dialog: under headless Chromium the native picker is *installed* and *invoked*, and then **cancelled silently** — no OS dialog is presented to a human. Every statement in section A below is therefore about the secure origin, the cookie rule, the fallback path and the *handler side* of the picker call, never about a saved file chosen by a person. **C has since been supplied on that human leg by the Owner's own hands (2026-10-03, section C), and it stands on that run alone** — A and B remain separate evidence classes and were never extended to cover it.

---

## A — Automated HTTPS browser acceptance: PASS

### What ran

- Suite: `tests/browser/https.cjs` — **new in this commit**, 465 lines, sections `0`–`10`, **58 checks**.
- Server under test: the real `goal` binary built from these bytes, configured with `tls.enabled: true` (`webPort 19489` for HTTP, `tls.port 19490` for HTTPS, `authEnabled: true`), i.e. HTTP and HTTPS listeners **coexisting** on one host exactly as §D2/§D6 require.
- Certificate: generated per run by `testdata/tls-fixture` (self-signed ECDSA P-256, `-dns goaltls.invalid -hours 24`), written into the suite's temp workspace and removed on every exit path — the `finally` at `tests/browser/https.cjs:455-458`, the generator's catch at `:37-40`, and the failed-bring-up abort at `:154` (cleanup function defined at `:126-132`).
- Origin: `goaltls.invalid` — an RFC 6761 reserved TLD that is never publicly resolvable and never HSTS-preloaded, mapped to loopback only inside Chromium via `--host-resolver-rules=MAP …`, with `--no-proxy-server` mandatory (`:20-27`). Node's health probe uses the loopback address behind it, because resolver rules live in Chromium's network stack only.
- Browser: headless Chromium in CI (`ignoreHTTPSErrors: true` on the contexts that need it; one context deliberately runs with it `false` for the negative TLS control).

### Evidence

| Evidence | Observed |
|----------|----------|
| CI `Browser Acceptance (Chromium, headless)` job `110495728821` (run `36899769739`, attempt 1) | **749** raw `PASS \|` lines / **0** raw `FAIL \|` lines over **14 suites**; the new suite's own summary line is `Native HTTPS / secure origin: TOTAL 58 \| PASS 58 \| FAIL 0` |
| Local re-run over the same bytes (correction pass, after F-1…F-6/F-8 fixes) | **749** raw `PASS \|` / **0** `FAIL \|`, `https.cjs` **58/58** |
| Linux race step (obligation 18), job `110495728674` | literal `CGO_ENABLED=1 go test -race -timeout 20m ./...` → 24 package result lines = **20 `ok` + 4 `[no test files]`**, **0** `DATA RACE` / `FAIL` / `panic` / `(cached)`; durations prove slice 5's Go tests ran under the detector (`internal/application/portable 1.170s`, `internal/webui/handlers 474.527s`, `internal/config 48.741s`, `internal/webui/security 24.942s`) |
| Windows plain run, job `110495729298` | 20 `ok` + 4 `[no test files]`, 0 `FAIL`, no `runtime.preemptM` crash |

Per repository convention the browser figure is the **raw `PASS \|` line count of the owning job log**, never the arithmetic sum of the printed `TOTAL` lines (`core.cjs` prints `TOTAL 49` as a subtotal of its own cumulative suite before its final `TOTAL 57`; summing all 15 printed `TOTAL` lines would double-count it).

### What each section proves

| Suite section | Proven property | ADR 019 anchor |
|---------------|-----------------|----------------|
| `0.1`–`0.7` | Both listeners are healthy; the §D19 HTTPS startup line names `tls=enabled`, `cert=`, `expires=` and `san=`; the HTTP line carries no TLS attributes; **no key material or key path in the startup log**; plaintext to `tls.port` is refused (status 400), not served | §D15, §D19, §D22 |
| `1.1`–`1.3` | The tested origin is the mapped **non-loopback host name**, and over it `isSecureContext === true` with the SPA really rendered over TLS | §Context, §D18 |
| `2.1`–`2.3` | The same host over plain HTTP reports `isSecureContext === false` and `typeof showSaveFilePicker === "undefined"` — the member is **not installed at all**, it does not merely reject | §Context |
| `3.1`, `3.2` | **False-PASS control:** `http://127.0.0.1` reports `secure=true, picker=function`, so a loopback origin would silently pass a secure-context test; neither tested host string is potentially trustworthy | §Context (host-string rule) |
| `4.1`–`4.3` | On the HTTPS origin the picker **is** installed and it is the browser-native implementation — the suite installs no stub anywhere | Owner decision (no `showSaveFilePicker` substitution) |
| `5.1`–`5.6` | HTTP download fallback on the insecure origin: exactly one browser download, name `goal-portable-config.json`, the export request really reached the server, button restored, success toast shown | §D18 property 4 / backward compatibility |
| `6.1`–`6.7` | Picker-first contract on the secure origin, proved **non-vacuously**: a MutationObserver on the export button's `disabled` attribute (`:260-266`) shows `toggles=4` for the real click and `toggles=0 downloads=0 requests=0` for the inert negative control (`6.2`), so "the handler ran" is separated from "nothing happened"; the headless native dialog then **cancels silently** — no download, no fetch before a chosen destination, no toast | §D27, OWNER-EXPORT-01 client half |
| `7.1`–`7.13` | Connection-derived `Secure` on **every** emit path: non-`Secure` over HTTP for `goal_session` (set) and `goal_csrf_token`, `Secure` over TLS for both, jar agrees with the emit, only `Secure` changes (`HttpOnly`/`SameSite`/`Path`/host-only scope survive), a spoofed `X-Forwarded-Proto` does not flip the flag, and one jar + one host means **the connection, not the host name**, set it. Session-cookie values are redacted in the log (`:86`, applied `:304`) | §D18, §D20, obligation 9/10 |
| `8.1`–`8.5` | The measured §D18 **one-way session ratchet** in the browser: after an HTTPS login the HTTP page is unauthenticated (401), GoAl still emits a non-`Secure` cookie there, the jar refuses the downgrade and keeps the `Secure` session, and the HTTPS session survives the attempt | §D18 (C3/C5a) |
| `9.1`–`9.3` | **No false PASS from TLS/hostname errors:** without `ignoreHTTPSErrors` the load is a hard `net::ERR_CERT_AUTHORITY_INVALID`; an unmapped name is `net::ERR_NAME_NOT_RESOLVED` (so a PASS proves the mapping is live); boundary: a non-SAN name still loads under bypass, which is why the certificate identity is read from the §D19 log line, not from the page | obligation 13 |
| `10.1`–`10.6` | Reproducibility guards: explicit `--no-proxy-server`, resolver rules rather than a hosts-file edit, reserved TLD, and the control that an ambient proxy breaks the mapped origin (`ERR_PROXY_CONNECTION_FAILED`) — the reason `10.1` is mandatory; no unexpected console errors (filter is narrow, `:436-443`), no 5xx | Linux CI / Windows reproducibility |

### Obligation coverage added by this slice

- **Test obligation 12** (TLS never enters the Portable bundle) — **7 new Go test functions** in this commit:
  `TestBundleSchema_HasNoTLSField`, `TestExportBundle_CarriesNoTLSConfiguration`, `TestPEMMaterialScannerIsNotVacuous` (`internal/application/portable/export_test.go`), `TestParseBundle_TopLevelTLSRejected`, `TestParseBundle_NestedTLSTemplateRejected` (`internal/application/portable/portable_test.go`), `TestExport_TLSConfiguredInstallExportsNoTLSMaterial`, `TestImport_BundleCannotInstallTLSMaterial` (`internal/webui/handlers/portable_test.go`).
  Coverage is by **absence of PEM key/certificate material**, scanning string values at any nesting depth (so a `template`/`env` value cannot smuggle a key), plus a non-vacuity test that proves the scanner actually detects planted PEM material. The verification set ran these together with the pre-existing slice-3 test `TestLogin_TLSEnabledConfigStillNonSecureOverHTTP` (`internal/webui/handlers/auth_cookie_secure_test.go:144`) — 8 tests in total.
- **Test obligation 13** — sections `1`–`4` and `9` (self-signed HTTPS + `ignoreHTTPSErrors` ⇒ `isSecureContext true`, picker installed; HTTP ⇒ capability absent, fallback taken).
- **Test obligation 14**, browser half — sections `7`–`8` reproduce the coexistence sequence on one host name and assert the documented observable outcome. Per the ADR this pins the *consequence*; it is not a claim that GoAl implements the protection.
- **Test obligation 18** — race evidence from Linux CI (see the table above), Windows and Linux builds, full maintained browser suite.

---

## B — Exact-SHA Publication Gate: PASS

| Item | Value |
|------|-------|
| Commit under publication | `7125cc3f1030a67815401ed6dccb2a04b56b21bb` |
| Parent | `9a019a8880778e36cf11f1a1de1c2eb6da0b127f` |
| Push | plain fast-forward `9a019a8..7125cc3 -> main` — no force, no amend, no additional commit |
| Workflow / run | `CI`, run `36899769739`, event `push`, `headSha` verified equal to the commit above |
| Attempt | **1** — no rerun |
| Conclusion | `success`, **7/7 jobs** |

Jobs (attempt 1, all `success`): `Build (ubuntu-latest)` `110495728649` · `Test (Linux, race)` `110495728674` · `Vulnerability Check` `110495728711` · `Lint` `110495728743` · `Build (windows-latest)` `110495728750` · `Browser Acceptance (Chromium, headless)` `110495728821` · `Test (Windows)` `110495729298`.

**Not tagged, not released.** `git tag --points-at HEAD` is empty for this commit; the newest tag and release remain `v2.1.0`. Publication is not acceptance — result B says nothing about result C.

---

## C — Manual OWNER-EXPORT-01 acceptance: PASS (2026-10-03)

**History.** OWNER-EXPORT-01 (Settings → Portable Configuration → *Скачать* → choose file name → choose destination → save) failed the Owner's manual acceptance on **2026-09-29** as **CASE B**: at the Owner's then-current plain-HTTP non-loopback origin (`http://aids:8088`) the browser does not install `showSaveFilePicker` at all, so the save dialog was absent. The picker-first client slice was nevertheless committed (`b164be4`) and published; it remained **NOT OWNER PASS** until the 2026-10-03 run recorded in this section. ADR 019 §D27 states the closure path: acceptance is **repeated** once a supported `https://<hostname>:<port>` exists, and the final native-dialog completion **cannot be automated** — Playwright cannot drive an OS file dialog.

**Status as this section was first written (2026-10-01):** the server half of the precondition is delivered and published (slices 1–3: a real HTTPS listener; slice 3: `Secure` derived from the connection), and result A proves the secure origin, the installed native picker and the picker-first handler behavior against it. **OWNER-EXPORT-01 is still OPEN.** So is slice 5's acceptance purpose: the remaining work in this slice is the manual repetition, not more automation. *(That was the accurate state on 2026-10-01 and is superseded by the run below; it is quoted here rather than deleted because the closure path it describes is the one that was executed.)*

**Verdict 2026-10-03: OWNER PASS.** The Owner repeated the failed 2026-09-29 acceptance on a real machine, in a headed browser, at a real non-loopback secure origin (`https://aids:8443`), and completed the native Windows **«Сохранить как»** dialog by hand — file name chosen, destination chosen, saved — and the exported bundle exists at the destination the Owner chose and parses as Bundle v1 JSON. Every one of steps 1–7 below is closed. This is the leg results A and B could never perform, and neither of them is it.

### Evidence classes, kept separate

Three kinds of statement appear below and they are not interchangeable: **Owner-confirmed** = what the Owner reported having observed or done on that machine; **agent-verified** = what this agent independently observed against a live artifact, a file, or a repository byte, without relying on the report; **code-derived** = a conclusion obtained by reading the maintained code, not by observing the run.

### Owner-confirmed observations

| # | Item | As reported by the Owner |
|---|------|--------------------------|
| C1 | Machine / browser | Windows 11 Version 25H2 Build 26200.9550; Google Chrome 154.0.8037.58 64-bit Stable |
| C2 | Origin and context | `https://aids:8443`, self-signed certificate `DNS:aids`, address bar shows **«Не защищено»**, page reports `isSecureContext === true` |
| C3 | Running build | reported by the live server to the Owner over HTTPS: `version=acceptance-66f3dc4`, `gitCommit=66f3dc4ba2e10b904bc0d832e8501b17b005725e` |
| C4 | §D19 startup line | `starting HTTPS server addr=0.0.0.0:8443 tls=enabled cert=C:\tools\goal_dev\accept.crt expires=2026-10-03T19:04:52Z san=DNS:aids` |
| C5 | The dialog | a real Windows **«Сохранить как»** window appeared and the Owner personally completed it (name + destination + Save) |
| C6 | Exported artifact | `goal-portable-config.json`, 6138 bytes, SHA-256 `9A394577148B0665714DEAFEECA788A707B68AF6B67FBC2653C4FF531D4BFCCC`; JSON parses; `format=goal-portable-config`, `version=1`, runtimes 5, models 4, pipelines 2 |
| C7 | Import | the exported file was imported into **another, completely clean GoAl instance**; the UI reported **«добавлено 11, пропущено 0»** |

### Agent-verified facts (independent of the report)

| # | Verified | Observed |
|---|----------|----------|
| V1 | The process serving the Owner's browser is the acceptance build | `GET https://aids:8443/api/v1/version` from this machine answered `version=acceptance-66f3dc4`, `gitCommit=66f3dc4ba2e10b904bc0d832e8501b17b005725e`, `buildTime=2026-10-02T01:31:32+05:00`, `goVersion=go1.25.13` (the pinned `.go-version`), `os=windows`, `arch=amd64` — the same triple the artifact below reports about itself |
| V2 | The artifact | `E:\PRJ\goal-acceptance\adr019-slice5-66f3dc4\goal-windows-amd64.exe` — 10 793 472 B, SHA-256 `3bb5fde5f399c203d5848603487ad111848600d071f70e1bf64ef431d8401ef9`, built outside the tree with `vcs.revision=66f3dc4ba2e1…`, `vcs.modified=false`, reproducible (a second build was byte-identical); re-hashed 2026-10-03 with the same value |
| V3 | The wire certificate **is** the certificate the log named | Node `tls.connect` to `aids:8443`: subject == issuer == `O=GoAl test fixture, CN=aids` (self-signed), SAN `DNS:aids`, `notAfter=2026-10-03T19:04:52Z` — byte-equal to the reported §D19 `expires=` — `notBefore` exactly 24 h earlier with that Organization, i.e. the output of `testdata/tls-fixture` (`main.go:69-79`: `CommonName=dnsNames[0]`, `Organization=["GoAl test fixture"]`, 1-hour backdate, `-hours` default 24); negotiated `TLSv1.3 / TLS_AES_128_GCM_SHA256`, above §D22's TLS 1.2 floor |
| V4 | The origin was genuinely non-loopback | `aids` resolves to link-local `fe80::ba98:e5be:b672:3768` on this machine with **no hosts-file entry** — a real LAN name, not the Chromium-internal mapping result A uses (this narrows limitation 3) |
| V5 | The saved bytes | `C:\Users\dsdred\Downloads\goal-portable-config.json` — 6138 B, SHA-256 `9a394577148b0665714deafeeca788a707b68af6b67fbc2653c4ff531d4bfccc` = C6, mtime 2026-10-03 01:17:05 +05:00, inside the certificate's session window (00:04–19:04 local on 2026-10-03), re-hashed identically 2026-10-03; top-level keys exactly `format, version, runtimes, models, pipelines` — **no `tls` field**, so test obligation 12 holds on a real production artifact — and 5+4+2 = 11 entities, the number C7 reports as added |
| V6 | The limit of V5 | `C:\tools\goal-portable-config.json` and `C:\tools\goal_dev` do **not** exist on this machine, consistent with the clarification that that copy and the verification shell live on the `aids` server: the client-side file is agent-verified, the server-side copy is Owner-confirmed only |
| V7 | The build-SHA deviation is harmless | step 1 named `7125cc3`; the Owner ran a build of `66f3dc4`. `git diff --stat 7125cc3..66f3dc4 -- internal cmd tests testdata .github` is **empty** — `66f3dc4` is the docs-only descendant (8 documentation paths, zero Go bytes) — so the binary carried exactly the Go bytes result A tested |

### Code-derived conclusions (read from maintained code, not observed in the run)

- **The login leg.** `/api/v1/export` is `requireAuth`-gated and `/api/v1/import` is `requireAuthCSRF`-gated (`internal/webui/handlers/routes.go:224-225`), while `/api/v1/version` is deliberately unauthenticated (`:156-157`), and one `http.Server` per listener serves the same mux (`internal/webui/server.go:297-341`). A completed export therefore **entails** an authenticated session on the TLS listener; the version probe alone proves nothing about auth.
- **A saved file is the signature of a completed dialog.** `portableExport()` opens the picker **before** the fetch, returns silently on `AbortError` with nothing created, and writes only through `createWritable()`/`write()`/`close()` on the handle the user chose, with the success toast after `close()` (`internal/webui/static/app.js:2513-2578`). Bytes on disk at the Owner's chosen destination cannot be produced by the fallback path here (the origin is secure, so the picker branch is taken), and no request carries the destination path (§D3/§D8).
- **«добавлено 11, пропущено 0» is a server-validated result.** The Import button renders `disabled` (`internal/webui/templates/index.html:498-499`) and is enabled only by a successful `dry_run` plan reporting `can_import` (`app.js:2717`, `:2737`, `:2740-2752`); the sentence is `portable.import.success` (`internal/webui/static/i18n/ru.json:319`), rendered only on `r.ok` (`app.js:2811-2819`).
- **Why the cross-store clarification is decisive, not merely plausible.** `PlanGraphImport` classifies by **store ID**: an ID already in the store ⇒ `ImportStatusExisting` ⇒ counted in `Skipped`; a name collision ⇒ `Blocked`, never `Created` (`internal/storage/import_plan.go:108-153`). So `added 11 / skipped 0` is what a bundle applied to a store holding none of those 11 IDs produces, and is impossible for a re-import into the source store — a case pinned separately by `internal/application/portable/import_test.go:187-219` (re-describing existing state ⇒ created 0).

**The two discrepancies this record stopped on, and how the Owner adjudicated them (2026-10-03).** Neither was written around; both were reported back and resolved before any PASS was recorded.

1. **Destination path.** The first report gave `C:\tools\goal-portable-config.json`, which does not exist on this machine (V6), while a byte-identical 6138 B file was sitting in this user's Downloads (V5). *Resolution:* the browser ran on the Owner's computer and the native dialog wrote `C:\Users\dsdred\Downloads\goal-portable-config.json`; the Owner then copied that file to the dev server as `C:\tools\goal-portable-config.json`, and the PowerShell verification ran on the server. Both copies hash to `9A394577…BFCCC`. What this agent verified is the client-side copy; the server-side copy is the Owner's statement.
2. **Import target.** `added 11 / skipped 0` read as a same-store re-import, which the classification code makes impossible (last code-derived bullet). *Resolution:* the import was performed into **another, completely clean GoAl instance** — a **cross-store** import, not a repeat into the source store — so the reported plan outcome is exactly the expected one. Consequence for the contract: step 6's Import-validation leg is satisfied as a **cross-store round trip**; the same-store re-import leg is **not** claimed by this manual run and remains covered by the automated suite alone.

**The procedure as specified on 2026-10-01, kept verbatim as the acceptance contract that was executed:**

1. Build the acceptance binary from the exact published SHA `7125cc3f1030a67815401ed6dccb2a04b56b21bb` (deferred out of the documentation gate; it is a separate step), and record its path and SHA-256.
2. Provide a certificate whose SAN covers the hostname actually used in the browser (per §D14 a self-signed certificate is excluded from the production trust story; for acceptance the Owner may add the exception in the browser or use a private CA — whichever is used must be recorded, because trust is not GoAl's behavior).
3. Configure `tls{enabled:true, port, certFile, keyFile}` in `goal.json` with **absolute** cert/key paths, start GoAl, and confirm the §D19 startup line names the HTTPS listener with `cert=`, `expires=` and `san=`.
4. In a real (headed) browser, open `https://<hostname>:<port>` — never a loopback address for this check — and complete: login → Settings → Portable Configuration → **Скачать** → native **Save As** appears → choose file name → choose destination folder → **Save** → verify the file exists at the chosen destination and is valid JSON.
5. Verify the browser reports `isSecureContext true` on that origin, and record the address bar state (padlock / warning) as observed.
6. Then, in the same session: import the saved file back through Settings → Portable Configuration → Import (dry-run validate → confirm) and record the outcome — this is the Import-validation leg of the §D27 flow.
7. Record the OS, browser name and version, exact origin URL, binary SHA-256, and the observed result of each step. Any deviation is a finding to classify, not to smooth over.

### Steps 1–7, closed

| Step | Requirement (2026-10-01 wording above) | Outcome 2026-10-03 | Evidence class |
|------|----------------------------------------|--------------------|----------------|
| 1 | Build from the exact published SHA; record path and SHA-256 | **CLOSED.** Built from `66f3dc4ba2e1…` — the docs-only descendant of the named `7125cc3`, with its Go/test/fixture/workflow bytes proven byte-identical (V7); path and SHA-256 recorded (V2) and matched by the live server (V1) | agent-verified |
| 2 | A certificate whose SAN covers the hostname actually used; trust handling recorded | **CLOSED.** SAN `DNS:aids` covers the hostname `aids` used in the browser; self-signed, no OS trust-store enrollment, exception handled in the browser — §D14/§D17 posture unchanged | Owner-confirmed + agent-verified (V3, V4) |
| 3 | `tls{enabled,port,certFile,keyFile}` with **absolute** cert/key paths; the §D19 startup line confirmed | **CLOSED.** The line carries §D19's field order — `addr`, then `tls=enabled cert=… expires=… san=DNS:aids` — with an absolute `certFile` and **no key path and no key material**; its `expires=` equals the wire certificate's `notAfter` | Owner-confirmed + agent-verified (V3) |
| 4 | Headed browser on a non-loopback origin: login → Settings → Portable Configuration → **Скачать** → native **Save As** → name → destination → **Save** → file exists and is valid JSON | **CLOSED.** The dialog appeared and the Owner completed it (C5); the artifact exists at the chosen destination, 6138 B, hash as reported, Bundle v1 keys, 5/4/2 entities (C6, V5) | Owner-confirmed + agent-verified + code-derived (login leg, write leg) |
| 5 | `isSecureContext true` recorded, with the address-bar state as observed | **CLOSED.** `isSecureContext === true` **while** the address bar reads «Не защищено» — the exact split result A pins in sections `1`–`4`: the picker follows the secure **context**, not certificate trust | Owner-confirmed (C2) |
| 6 | Import the saved file back (dry-run validate → confirm); record the outcome | **CLOSED as a cross-store round trip** into a clean instance: «добавлено 11, пропущено 0» — a gated, server-validated plan result (C7 + the gating/classification rows above). The same-store re-import leg is **not** claimed by this run | Owner-confirmed + code-derived |
| 7 | Record OS, browser name/version, exact origin URL, binary SHA-256 and each step's result; any deviation is a finding, not something to smooth over | **CLOSED.** All recorded above; the two deviations were stopped on and adjudicated (see the discrepancies) before the verdict was written | Owner-confirmed + agent-verified |

**What this PASS closes and what it does not.** It closes **OWNER-EXPORT-01** (choose the output file name and destination during export) and with it **ADR 019 slice 5's acceptance purpose** — the manual half that automation cannot perform. It does **not** close **Manual Owner Acceptance** for the release, which stays **OPEN / BLOCKED**, now on **OWNER-IMPORT-01** (ADR 018 implementation NOT STARTED) and the formal closure of **OWNER-UX-01** — not on this finding. It does not close trust-store/CA work (§D14/§D17), and it does not resume ADR 015 (still FROZEN).

**Recorded limits of the manual leg itself, so the PASS is not read as wider than it is:** the clean second instance's own origin, TLS posture and dataDir were not reported, so the import leg is proven *authenticated and server-validated* but not proven to have run over TLS; and the fixture certificate is at the end of its 24-hour lifetime (`notAfter 2026-10-03T19:04:52Z`), so any future re-run needs a freshly generated one.

---

## Scope limitations — what this record does NOT prove

1. *(Restated 2026-10-03.)* Section A does not prove that a real operating-system Save As dialog was completed by anyone, and never did: headless Chromium installs and invokes the native picker and then **cancels it silently** (`6.4`: no browser download, `count=0`). That human leg is result C, performed by the Owner on 2026-10-03 and recorded above; the automated and manual evidence classes stay separate and neither substitutes for the other.
2. It does not certify trust — **in either result**. In A the fixture certificate is self-signed for `goaltls.invalid` and `ignoreHTTPSErrors` is enabled per context on the contexts that need it; in C the Owner's own browser showed **«Не защищено»** on a self-signed `CN=aids` certificate with no OS trust-store enrollment. Trust-store, private-CA and OS-level enrollment work is explicitly out of scope (§D14, §D17) and remains unmeasured; a completed export on a secure context is not a validated certificate chain.
3. *(Narrowed 2026-10-03 to section A.)* Result A does not prove the tested hostname is reachable on a real LAN: its hostname is a reserved TLD mapped to loopback inside Chromium only, and the value tested is the **host string**, which is exactly what Chromium's secure-context decision depends on (§Context), plus the real TLS connection. Result C did use a real LAN name — `aids`, resolving link-local with no hosts-file entry (V4) — so the manual leg exercised a genuinely non-loopback origin over the network.
4. It does not decide proxy identity semantics. `7.7` asserts only that the *browser-side emit* did not flip `Secure`; header delivery through the browser stack is not proven there, and the Go-level forwarded-header independence stays pinned by slice 3's Go tests. §D20 (no trusted-proxy behavior) is unchanged, and PROXY-01 remains open debt in `BACKLOG.md`.
5. It adds **no** production behavior. This commit touches zero non-test Go bytes outside test files: production code (`internal/webui/server.go`, `internal/webui/security/*`, `internal/config/*`, handlers) is byte-identical to the published slice-4/slice-3 state; the only Go changes are `_test.go` files and the `testdata/tls-fixture` generator.
6. *(Closed 2026-10-03.)* When this record was written, slice 5 still owed its documentation/tracking reconciliation and the repeated OWNER-EXPORT-01 acceptance under their own gates. Both have since been delivered: the reconciliation was published as `66f3dc4`, the acceptance binary was built from those bytes, and result C is now **PASS**, so slice 5 is closed end to end — as far as an automated record can close it, plus the Owner's own run, which is what closed the manual half.
7. Obligation 12's coverage is an **absence** proof for TLS material in the portable bundle at the tested shapes (schema field, export content, nested import templates, round trip through the real handler with a configured install). It is not a claim that the bundle format can never carry a secret — the pre-existing portable-security debt rows in `BACKLOG.md` remain.

---

## Related state

- **Slice chain (all published):** 1 `353269b` (+ `710fb93`) · 2 `2d30d97` (+ `e2d3fff`) · 3 `0042b74` (+ `ee16f50`) · interop evidence `bc73c74` · 4 `f759332` (+ `9a019a8`) · **5 implementation `7125cc3`** (+ reconciliation `66f3dc4`). Full provenance: [`ROADMAP.md`](../../ROADMAP.md) P1 → Native HTTPS / TLS.
- **Open alongside this record:** Manual Owner Acceptance (**OPEN / BLOCKED**, now on OWNER-IMPORT-01 and the formal closure of OWNER-UX-01 — **no longer on OWNER-EXPORT-01**, which is **OWNER PASS 2026-10-03**), ADR 015 (FROZEN), `BACKLOG.md` items `PROXY-01`, `APP-SHUTDOWN-01`, `CFG-WARN-01`, ADR 018 implementation (NOT STARTED).
- **Design decisions:** unchanged by this slice — D1–D27, P1–P8, §D18, §D20, §D26 and the 18 test obligations of [ADR 019](../adr/019-native-https-secure-origin.md) are exactly as accepted.
