# ADR 019 — Native HTTPS / Secure Origin: Browser Acceptance Record

**Purpose:** record what ADR 019 slice 5 actually proved, what it did not prove, and what still requires the Owner's own hands.

**Source of truth:** repository bytes at HEAD `7125cc3f1030a67815401ed6dccb2a04b56b21bb` (`test(adr019): add native HTTPS browser acceptance`, parent `9a019a8880778e36cf11f1a1de1c2eb6da0b127f`, 7 paths, 923 insertions / 8 deletions), published as the plain fast-forward `9a019a8..7125cc3 -> main`. Design contract: [ADR 019 — Native HTTPS / Secure Origin](../adr/019-native-https-secure-origin.md). The per-slice evidence ledger lives in [`ROADMAP.md`](../../ROADMAP.md); this file records only slice 5.

**Date of this record:** 2026-10-01.

---

## The three results, kept separate

| # | Result | Verdict | What it means |
|---|--------|---------|---------------|
| **A** | Automated HTTPS browser acceptance | **PASS** | The maintained Playwright/Chromium suite `tests/browser/https.cjs` runs against a real `https://<hostname>:<port>` GoAl listener and every one of its 58 checks passes, alongside the full 14-suite chain. |
| **B** | Exact-SHA Publication Gate | **PASS** | CI run `36899769739` for `headSha 7125cc3f1030a67815401ed6dccb2a04b56b21bb`, attempt 1, conclusion `success`, **7/7 jobs, no rerun**. |
| **C** | Manual OWNER-EXPORT-01 acceptance | **PENDING** | The Owner's own native **Save As** flow on a real browser at a real hostname has **not** been repeated since its 2026-09-29 failure. Nothing in A or B closes it. |

**A + B are not C.** An automated headless run cannot complete an operating-system file dialog: under headless Chromium the native picker is *installed* and *invoked*, and then **cancelled silently** — no OS dialog is presented to a human. Every statement in section A below is therefore about the secure origin, the cookie rule, the fallback path and the *handler side* of the picker call, never about a saved file chosen by a person.

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

## C — Manual OWNER-EXPORT-01 acceptance: PENDING

**History.** OWNER-EXPORT-01 (Settings → Portable Configuration → *Скачать* → choose file name → choose destination → save) failed the Owner's manual acceptance on **2026-09-29** as **CASE B**: at the Owner's then-current plain-HTTP non-loopback origin (`http://aids:8088`) the browser does not install `showSaveFilePicker` at all, so the save dialog was absent. The picker-first client slice was nevertheless committed (`b164be4`) and published; it remains **NOT OWNER PASS**. ADR 019 §D27 states the closure path: acceptance is **repeated** once a supported `https://<hostname>:<port>` exists, and the final native-dialog completion **cannot be automated** — Playwright cannot drive an OS file dialog.

**Status now:** the server half of the precondition is delivered and published (slices 1–3: a real HTTPS listener; slice 3: `Secure` derived from the connection), and result A proves the secure origin, the installed native picker and the picker-first handler behavior against it. **OWNER-EXPORT-01 is still OPEN.** So is slice 5's acceptance purpose: the remaining work in this slice is the manual repetition, not more automation.

**What must happen, by the Owner, on a real machine:**

1. Build the acceptance binary from the exact published SHA `7125cc3f1030a67815401ed6dccb2a04b56b21bb` (deferred out of the documentation gate; it is a separate step), and record its path and SHA-256.
2. Provide a certificate whose SAN covers the hostname actually used in the browser (per §D14 a self-signed certificate is excluded from the production trust story; for acceptance the Owner may add the exception in the browser or use a private CA — whichever is used must be recorded, because trust is not GoAl's behavior).
3. Configure `tls{enabled:true, port, certFile, keyFile}` in `goal.json` with **absolute** cert/key paths, start GoAl, and confirm the §D19 startup line names the HTTPS listener with `cert=`, `expires=` and `san=`.
4. In a real (headed) browser, open `https://<hostname>:<port>` — never a loopback address for this check — and complete: login → Settings → Portable Configuration → **Скачать** → native **Save As** appears → choose file name → choose destination folder → **Save** → verify the file exists at the chosen destination and is valid JSON.
5. Verify the browser reports `isSecureContext true` on that origin, and record the address bar state (padlock / warning) as observed.
6. Then, in the same session: import the saved file back through Settings → Portable Configuration → Import (dry-run validate → confirm) and record the outcome — this is the Import-validation leg of the §D27 flow.
7. Record the OS, browser name and version, exact origin URL, binary SHA-256, and the observed result of each step. Any deviation is a finding to classify, not to smooth over.

**Until steps 1–7 are performed and reported, neither OWNER-EXPORT-01 nor ADR 019 slice 5 acceptance is closed**, and Manual Owner Acceptance for the release stays **OPEN / BLOCKED** as recorded at the head of `ROADMAP.md`.

---

## Scope limitations — what this record does NOT prove

1. It does not prove a real operating-system Save As dialog was completed by anyone. Headless Chromium installs and invokes the native picker and then **cancels it silently** (section `6.4`: no browser download, `count=0`); the human leg is result C, still PENDING.
2. It does not certify trust. The fixture certificate is self-signed for `goaltls.invalid`; `ignoreHTTPSErrors` is enabled per context on the contexts that need it. Trust-store, private-CA and OS-level enrollment work is explicitly out of scope (§D14, §D17) and remains unmeasured.
3. It does not prove the tested hostname is reachable on a real LAN. The hostname is a reserved TLD mapped to loopback inside Chromium only; the value tested is the **host string**, which is exactly what Chromium's secure-context decision depends on (§Context), plus the real TLS connection.
4. It does not decide proxy identity semantics. `7.7` asserts only that the *browser-side emit* did not flip `Secure`; header delivery through the browser stack is not proven there, and the Go-level forwarded-header independence stays pinned by slice 3's Go tests. §D20 (no trusted-proxy behavior) is unchanged, and PROXY-01 remains open debt in `BACKLOG.md`.
5. It adds **no** production behavior. This commit touches zero non-test Go bytes outside test files: production code (`internal/webui/server.go`, `internal/webui/security/*`, `internal/config/*`, handlers) is byte-identical to the published slice-4/slice-3 state; the only Go changes are `_test.go` files and the `testdata/tls-fixture` generator.
6. It does not close slice 5 as a whole. The documentation/tracking reconciliation this record belongs to, and the acceptance binary plus the repeated OWNER-EXPORT-01 acceptance, are separate steps under their own gates.
7. Obligation 12's coverage is an **absence** proof for TLS material in the portable bundle at the tested shapes (schema field, export content, nested import templates, round trip through the real handler with a configured install). It is not a claim that the bundle format can never carry a secret — the pre-existing portable-security debt rows in `BACKLOG.md` remain.

---

## Related state

- **Slice chain (all published):** 1 `353269b` (+ `710fb93`) · 2 `2d30d97` (+ `e2d3fff`) · 3 `0042b74` (+ `ee16f50`) · interop evidence `bc73c74` · 4 `f759332` (+ `9a019a8`) · **5 implementation `7125cc3`**. Full provenance: [`ROADMAP.md`](../../ROADMAP.md) P1 → Native HTTPS / TLS.
- **Open alongside this record:** OWNER-EXPORT-01 (C above), Manual Owner Acceptance (OPEN / BLOCKED), ADR 015 (FROZEN), `BACKLOG.md` items `PROXY-01`, `APP-SHUTDOWN-01`, `CFG-WARN-01`, ADR 018 implementation (NOT STARTED).
- **Design decisions:** unchanged by this slice — D1–D27, P1–P8, §D18, §D20, §D26 and the 18 test obligations of [ADR 019](../adr/019-native-https-secure-origin.md) are exactly as accepted.
