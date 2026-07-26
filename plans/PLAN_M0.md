# PLAN_M0 — Walking skeleton

Implements MILESTONES.md **M0** exactly, under the binding conventions in
[PLAN_CONVENTIONS.md](PLAN_CONVENTIONS.md). SPEC.md is normative; sections are cited by
number throughout.

---

## 1. Goal & capability

**You can now:** run `gibson serve` from a checkout and load the app shell in a browser.

Concretely: a single committed repo builds (Vite → `go:embed` → one Go binary) into a
server that, launched from inside a git checkout, reads and validates `gibson.toml`
(SPEC §3), derives the workspace root (SPEC §2.1), performs the startup checks
(`.gibson/` gitignore warning per §4.2.2; pi presence/version error per §5.4), and serves
the embedded React app shell plus `GET /api/health` — with a `--dev` mode that
reverse-proxies the SPA to the Vite dev server for hot reload (§8.1.3).

## 2. Preconditions

None — M0 is the first milestone; the repo is greenfield (docs only). Everything below is
created from scratch. Toolchain assumptions:

- Go 1.26 on PATH (PLAN_CONVENTIONS §1).
- Node + npm (any current LTS) for the Vite build.
- `git` on PATH (workspace derivation and tests shell out to it).
- For the real-environment proof only: pi 0.82.1 installed (`pi --version` prints
  `0.82.1` to stdout — verified against the installed binary). No pi RPC work happens in
  M0; the only pi interaction is spawning `pi --version` once at startup.

## 3. Deliverables

New files (repo-relative):

| Path | Contents |
|---|---|
| `go.mod` | module `github.com/jmcampanini/gibson`, `go 1.26` |
| `main.go` | grove idiom: `cmd.Execute()`, on error print `gibson: error: %v` to stderr, exit 1 |
| `cmd/root.go` + `cmd/root_test.go` | cobra root: `Use: "gibson"`, `SilenceErrors/SilenceUsage: true`, `var Version = "n/a"` set via ldflags, `Execute()` |
| `cmd/serve.go` + `cmd/serve_test.go` | `gibson serve [--port N] [--dev]` — the startup sequence (§4 below) |
| `internal/config/config.go` + `config_test.go` | `Config`/`Server`/`SessionType` structs, `Load()`, `Validate()` (SPEC §3) |
| `internal/workspace/workspace.go` + `workspace_test.go` | `Locate()`: checkout root + workspace root derivation (SPEC §2.1.2–2.1.3) |
| `internal/pisession/version.go` + `version_test.go` | pi binary resolution + version pin check (SPEC §5.4; PLAN_CONVENTIONS §6) |
| `internal/store/gitignore.go` + `gitignore_test.go` | `.gibson/` gitignore check (SPEC §4.2.2) |
| `internal/httpapi/server.go` + `server_test.go` | `New(Options) http.Handler`: mux, `GET /api/health`, `/api/` 404 JSON envelope, wiring of SPA vs dev proxy |
| `internal/httpapi/spa.go` | embedded-FS static serving with history-API fallback to `index.html` |
| `internal/httpapi/proxy.go` | `--dev` reverse proxy of non-`/api` paths to `http://localhost:5173` |
| `internal/testws/testws.go` | `testws.New(t)`: scratch grove-style workspace for tests (PLAN_CONVENTIONS §9) |
| `web/embed.go` | `package web`; `//go:embed all:dist` → `var Dist embed.FS` |
| `web/` scaffold | `package.json`, `vite.config.ts`, `tsconfig.json`, `index.html`, `src/main.tsx`, `src/App.tsx` — Vite + React 18 + TS app shell |
| `web/dist/.gitkeep` | force-committed placeholder so `go build` compiles before the first `npm run build` (see §4) |
| `.gitignore` | `web/dist/` (placeholder re-added with `git add -f`), `node_modules/`, `.gibson/`, `.sandbox/`, `bin/` |
| `gibson.toml` | dogfood config for this repo itself (SPEC §3.1.1): `server.port = 7311`, one session type `quick` |
| `Makefile` | `make web` (`npm run build --prefix web && touch web/dist/.gitkeep` — re-creates the placeholder Vite's `emptyOutDir` deletes, §4.3), `make build` (web + `go build` with ldflags version), `make test` (`go test ./...`) |

Routes delivered: `GET /api/health` → `{"ok":true,"version":"..."}` (PLAN_CONVENTIONS §3),
plus the non-`/api` SPA fallback. No other routes in M0.

## 4. Design & rationale

Decisions local to M0. Cross-milestone seams (module path, package layout, route table,
error envelope, version-pin constant location) come verbatim from PLAN_CONVENTIONS §1–§3
and §6 and are not restated.

### 4.1 `gibson serve` startup sequence (order is the UX)

Each numbered failure below is a **distinct, clear error** (SPEC §9.1.b) printed as
`gibson: error: ...` with exit code 1; warnings go to stderr and do not stop startup.

1. **Locate.** `workspace.Locate(cwd)`: checkout root = `git rev-parse --show-toplevel`
   (correct for linked worktrees too — it returns the worktree's own root); workspace
   root = `filepath.Dir(checkoutRoot)` (SPEC §2.1.2–2.1.3). Not inside a git repo →
   error: `not inside a git checkout (gibson must run from inside a checkout, e.g.
   <workspace>/main)`.
2. **Config.** `config.Load(checkoutRoot)` reads `<checkoutRoot>/gibson.toml`. Missing
   file and invalid file are distinct errors (SPEC §3.1.3, §9.1.b); validation errors
   name the field (see §4.2).
3. **Flag merge.** `--port` (if set) overrides `server.port` (SPEC §3.2.1). The merge
   happens **after** step 2's validation, and the merged value is **not** re-validated:
   the 1-65535 rule (§4.2) applies to the config-file value only. Consequence (relied on
   by `cmd/serve_test.go`, §7): `--port 0` requests an OS-assigned ephemeral port,
   discoverable from the extracted listener.
4. **Warnings.**
   - Zero `[sessions.*]` tables → warning, not error (SPEC §3.2.5).
   - `store.CheckIgnored(checkoutRoot)` false → prominent warning naming the fix
     (SPEC §4.2.2): suggest adding the line `.gibson/` to `<checkoutRoot>/.gitignore`.
     Gibson never edits `.gitignore` itself.
5. **pi check** (SPEC §5.4.1; PLAN_CONVENTIONS §6). `pisession.ResolvePiBin(cfg.Server.PiBin)`
   — configured path or `exec.LookPath("pi")` — then `pisession.CheckPiVersion(bin)`.
   Three distinct failures: (a) binary not found (`pi not found on $PATH; install pi
   0.82.x or set pi_bin in gibson.toml`), (b) `pi --version` failed to run, (c) version
   mismatch naming **both** versions: `unsupported pi version 0.79.0 at <path>; gibson
   requires 0.82.x`. Even though M0 never speaks RPC, the check runs at every `serve`
   startup — that is where §9.1.b demands it, and it is the milestone's only pi surface.
6. **Static check** (non-`--dev` only). If the embedded `dist/index.html` is absent
   (fresh clone, `npm run build` never ran — only `.gitkeep` embedded), fail with:
   `embedded web assets missing; run 'make web' (or use --dev with the Vite dev server)`.
   Failing at startup beats serving a broken blank page.
7. **Listen.** `net.Listen("tcp", bind:port)`. In use → error naming the port and both
   remedies (`server.port` in gibson.toml, `--port`); **no auto-increment** (SPEC §3.2.1).
8. **Serve.** Log one `slog` line: workspace root, checkout, `http://<bind>:<port>`,
   dev mode on/off. Graceful shutdown on SIGINT/SIGTERM via `signal.NotifyContext` +
   `http.Server.Shutdown` (grove idiom; also the hook M2 needs for killing pi children).

### 4.2 Config schema and validation (SPEC §3.2)

Struct-tagged TOML decoded with `BurntSushi/toml`, `Validate()` naming the offending
field in TOML path syntax (grove style, PLAN_CONVENTIONS §1):

```go
type Config struct {
    Server   Server                 `toml:"server"`
    Sessions map[string]SessionType `toml:"sessions"`
}
type Server struct {
    Port  int    `toml:"port"`   // required (3.2.1)
    Bind  string `toml:"bind"`   // default "127.0.0.1" applied in Load (3.2.2)
    PiBin string `toml:"pi_bin"` // "" → resolve from $PATH (3.2.3)
}
type SessionType struct {
    Description string   `toml:"description"` // required (3.2.4)
    Model       string   `toml:"model"`       // "" = unset
    Thinking    string   `toml:"thinking"`    // "" = unset
    ExtraArgs   []string `toml:"extra_args"`  // opaque; never parsed/validated (3.2.4)
}
```

- Error texts (formats are binding; exact wording may be polished):
  - missing file: `gibson.toml not found at <path>`
  - TOML syntax error: `gibson.toml: <decoder error with line info>`
  - `gibson.toml: server.port is required`
  - `gibson.toml: server.port must be 1-65535, got <n>`
  - `gibson.toml: sessions.<name>.description is required`
- `model`/`thinking` values are **not** validated — the hybrid-schema decision
  (BACKGROUND #11) exists precisely so gibson never chases pi's option surface;
  `extra_args` is stored verbatim and untouched (SPEC §3.2.4).
- Undecoded keys (via `toml.MetaData.Undecoded()`) produce a startup **warning** listing
  them (typo guard), not an error — SPEC §3 defines "invalid" as schema violations only.
- `pi_bin` is used literally (no `~` expansion); a bad path surfaces as the distinct
  pi-not-found error in step 5 which prints the literal path.
- No layering, no fallback locations (SPEC §3.1.2): `Load` reads exactly one path.

### 4.3 Embed pipeline and the bootstrap problem

`web/embed.go` is the entire Go side of §8.1.2:

```go
// Package web embeds the production SPA build.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

`go:embed` refuses to compile if `dist/` doesn't exist, and `dist/` is git-ignored
(PLAN_CONVENTIONS §8: built, not committed). Resolution: force-commit `web/dist/.gitkeep`
(`git add -f web/dist/.gitkeep`); the `all:` prefix embeds dot-files so the directory is
never empty and the module always compiles. Whether a real build is present is then a
*runtime* startup check (step 6 in §4.1), not a compile error. One wrinkle: Vite's
default `build.emptyOutDir` (true when `outDir` is inside the project root) empties
`web/dist/` on every build, deleting the tracked placeholder — which would leave a
tracked-file deletion in `git status` and, if ever committed, break fresh-clone builds
again. So the placeholder is self-healing: `make web` is
`npm run build --prefix web && touch web/dist/.gitkeep`, and `make build` runs `make web`
before `go build` so the shipped binary always carries a real `dist/` and the placeholder
never shows as deleted.

### 4.4 Static serving with history-API fallback

Non-`/api` requests serve the embedded SPA with fallback to `index.html`
(PLAN_CONVENTIONS §3, last paragraph), so client-side routes deep-link correctly from M3
onward. The handler takes an `fs.FS` (not `web.Dist` directly) so tests inject synthetic
trees:

```go
sub, _ := fs.Sub(web.Dist, "dist")          // done in cmd/serve.go
// spa.go: try the exact file; anything not found (and "/") serves index.html
func spaHandler(static fs.FS) http.Handler {
    files := http.FileServerFS(static)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if _, err := fs.Stat(static, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
            r.URL.Path = "/" // rewrite → index.html
        }
        files.ServeHTTP(w, r)
    })
}
```

Mux layout (Go 1.22 patterns, PLAN_CONVENTIONS §1): `GET /api/health` explicit;
`/api/` catch-all returning the 404 error envelope
`{"error":{"code":"not_found","message":...}}` (PLAN_CONVENTIONS §2) so unimplemented API
paths never fall through to HTML; `/` → SPA handler or dev proxy. This mux shape is what
M2 extends with the session routes.

### 4.5 Dev mode (SPEC §8.1.3)

`gibson serve --dev` swaps the `/` handler for
`httputil.NewSingleHostReverseProxy(http://localhost:5173)` (PLAN_CONVENTIONS §1). The
browser always talks to gibson's origin — single origin, no CORS, `/api/*` still served
by gibson. `httputil.ReverseProxy` passes `Connection: Upgrade` through, so Vite's HMR
WebSocket works unmodified. `vite.config.ts` configures **no** proxy of its own
(PLAN_CONVENTIONS §8). In `--dev`, the embedded-assets startup check (§4.1 step 6) is
skipped; if Vite isn't running the proxy returns 502, which is the expected dev-loop
signal, not a gibson error.

### 4.6 App shell scope

Deliberately minimal (MILESTONES: "load the app shell"): `src/App.tsx` renders a "gibson"
heading, fetches `/api/health`, and shows the server version or a connection error. That
one fetch proves the full vertical in both modes: embedded static + same-origin API, and
dev proxy + same-origin API. Dependencies: `react`/`react-dom` pinned `^18`
(PLAN_CONVENTIONS §8), `vite`, `@vitejs/plugin-react`, `typescript` only.
`react-router-dom`/`react-markdown` are pinned choices for later but not installed until
M3 uses them (MILESTONES principle: nothing speculative). No `src/api/` module yet — the
conventions' client/stream/store structure lands with M3's real consumers.

### 4.7 CLI shape

Mirrors grove-cli exactly (PLAN_CONVENTIONS §1): root command with
`SilenceErrors/SilenceUsage`, `cobra.Command` per file, `main.go` printing
`gibson: error: %v`. Flags are exactly `--port N` and `--dev` on `serve` — the pinned CLI
tree admits nothing else (no `--debug`, no `--bind` flag; bind is config-only per SPEC
§3.2.2). `cmd/run.go` does **not** exist yet (M1). Version via
`-ldflags "-X github.com/jmcampanini/gibson/cmd.Version=<v>"`, surfaced in both
`gibson --version` and `/api/health`.

### 4.8 `internal/testws` now, not later

M0's own tests need scratch grove-style workspaces (config loading, gitignore check,
serve integration), and PLAN_CONVENTIONS §9 pins `testws.New(t)` as the one helper for
that — so M0 builds it. M0 scope: temp workspace root, one `main/` checkout created with
real `git init` + an initial commit, committed `.gitignore` containing `.gibson/`, and a
committed default `gibson.toml` (port `7311`, one session type `test`). Shape:

```go
type WS struct {
    Root     string // workspace root (parent of checkouts)
    Checkout string // absolute path of the "main" checkout
}
func New(t *testing.T) *WS
func (ws *WS) WriteConfig(t *testing.T, tomlSrc string) // overwrite gibson.toml
```

Later milestones extend it (extra worktrees, fakepi-pointing configs) without renaming.

## 5. Implementation steps

Ordered; each step leaves `go build ./...` green.

1. **Module + CLI skeleton.** `go.mod`; `main.go`; `cmd/root.go` (root command, `Version`
   var, `Execute()`); `cmd/serve.go` stub registering `--port`/`--dev`. `Makefile` with
   `build`/`web`/`test` targets. Root `.gitignore` per §3 deliverables table.
2. **`internal/config`.** Structs, `Load(checkoutRoot string) (*Config, toml.MetaData, error)`
   (MetaData kept for the undecoded-keys warning), `Validate()`, defaults. Table tests.
3. **`internal/workspace`.** `Locate(dir string) (*Workspace, error)` shelling to
   `git -C <dir> rev-parse --show-toplevel`. Tests with real git repos in `t.TempDir()`
   (repo root, nested subdir, non-repo error, linked-worktree checkout).
4. **`internal/testws`.** As §4.8. Immediately reused by steps 5–8's tests.
5. **`internal/store`.** `CheckIgnored(checkoutRoot string) (bool, error)` via
   `git -C <checkoutRoot> check-ignore -q .gibson/` (exit 0 = ignored, 1 = not, other =
   error). The trailing slash is load-bearing: `check-ignore .gibson` exits 1 against a
   `.gitignore` entry `.gibson/` when the directory does not exist, while `.gibson/`
   matches the pattern either way (verified empirically). Works whether or not `.gibson/`
   exists; M0 never creates it.
6. **`internal/pisession/version.go`.** `SupportedVersionPrefix = "0.82."`;
   `ResolvePiBin(configured string) (string, error)`;
   `CheckPiVersion(bin string) (found string, err error)` — run `<bin> --version` with a
   5s `exec.CommandContext` timeout, trim whitespace from stdout, require
   `strings.HasPrefix(found, SupportedVersionPrefix)`. (Verified: pi 0.82.1 prints
   `0.82.1\n` to stdout, nothing to stderr.)
7. **`web/` scaffold.** Vite react-ts template trimmed to §4.6 scope; `web/embed.go`;
   force-add `web/dist/.gitkeep`. Verify `make web` emits
   `web/dist/index.html` and hashed assets.
8. **`internal/httpapi`.** `Options{Config, Workspace *workspace.Workspace, Version string,
   StaticFS fs.FS, DevProxy *url.URL}`; `New(o Options) http.Handler` building the mux of
   §4.4/§4.5; health handler; JSON error-envelope helper (`writeError(w, status, code, msg)`
   — the same helper M2's handlers will use).
9. **`cmd/serve.go` full startup sequence** per §4.1, wiring steps 2–8 together;
   `http.Server` with graceful shutdown.
10. **Dogfood config.** Commit `gibson.toml` (port 7311, `[sessions.quick]` with a
    description) and confirm `gibson serve` runs in this very checkout.
11. **Proof workflow** (§8 below) run end-to-end; fix what it flushes out.

## 6. Interfaces exposed to later milestones

Exact exported names later plans may consume (all new in M0; none conflict with
PLAN_CONVENTIONS §7, which pins M1/M2 interfaces):

- `internal/config`: `config.Load(checkoutRoot string) (*Config, toml.MetaData, error)`;
  `type Config struct { Server Server; Sessions map[string]SessionType }`;
  `type SessionType struct { Description, Model, Thinking string; ExtraArgs []string }`
  (M1 assembles pi argv from this; M2 serves it on `/api/config/session-types`, mapping
  `""` → JSON `null` for model/thinking).
- `internal/workspace`: `workspace.Locate(dir string) (*Workspace, error)`;
  `type Workspace struct { Root, LaunchCheckout string }`. M2 adds checkout enumeration
  to this package (`git worktree list --porcelain`, PLAN_CONVENTIONS §1); M0 does not.
- `internal/pisession`: `pisession.SupportedVersionPrefix` (in `version.go`, the pinned
  location), `ResolvePiBin`, `CheckPiVersion`. M1's spawn code reuses `ResolvePiBin`.
- `internal/store`: `store.CheckIgnored(checkoutRoot string) (bool, error)`.
- `internal/httpapi`: `httpapi.New(Options) http.Handler` + `Options` as in step 8. M2
  extends `Options` (adds the `session.Manager`) and registers the session routes on the
  same mux; the `/api/` 404-envelope catch-all and `writeError` helper are already in
  place.
- `web`: `web.Dist embed.FS` (contents under `dist/`).
- `internal/testws`: `testws.New(t) *WS`, `WS.WriteConfig` (§4.8).
- Routes: `GET /api/health` and the non-`/api` SPA fallback, exactly per
  PLAN_CONVENTIONS §3.
- **Note handed to M1:** `serve` runs the version check against whatever `pi_bin`
  resolves to, so **fakepi must answer `--version` by printing `0.82.1`** — otherwise no
  fakepi-driven `serve` test can pass startup. M1's fakepi plan must include this.

## 7. Testing

All tests run with `go test ./...`, no network, no LLM, no real pi
(PLAN_CONVENTIONS §9). fakepi does not exist yet; where a pi binary is needed, tests
write a two-line `sh` stub into `t.TempDir()` (`#!/bin/sh\necho 0.82.1`) — local to
version tests, not a shared fixture.

- `internal/config/config_test.go` — table tests: valid full config; minimal config;
  defaults applied (`bind`, empty sessions map); missing file vs syntax error are
  distinct; `server.port` missing/out-of-range names the field; missing
  `sessions.<name>.description` names table and field; `extra_args` round-trips verbatim
  (including `-e`-style flags and `~` paths, untouched); undecoded keys reported.
- `internal/workspace/workspace_test.go` — repo root, nested subdir, non-repo error;
  linked worktree (`git worktree add`) resolves to the worktree's own root; workspace
  root is the parent directory (SPEC §2.1.3).
- `internal/pisession/version_test.go` — stub prints `0.82.1` → ok and returns found
  version; prints `0.79.0` → error containing both `0.79.0` and `0.82`; nonexistent path
  → not-found error; `ResolvePiBin("")` honors a PATH override (`t.Setenv("PATH", dir)`).
- `internal/store/gitignore_test.go` — via `testws.New` (has the entry → true); strip the
  `.gitignore` line → false; works with `.gibson/` absent; non-repo dir → error.
- `internal/httpapi/server_test.go` — `httptest` against `New`: health returns 200
  `{"ok":true,"version":...}`; unknown `/api/x` → 404 with error envelope
  `code:"not_found"`; SPA fallback with a synthetic `fstest.MapFS` (`/` and
  `/sessions/abc` both serve `index.html` content; `/assets/app.js` serves the asset);
  dev mode with an `httptest` fake-Vite backend (`/` proxied, `/api/health` not).
- `cmd/serve_test.go` — startup-sequence integration using `testws.New` + the sh-stub
  pi as `pi_bin`: `--port 0`-style free-port config, assert server answers health;
  assert `--port` overrides config; assert distinct error strings for missing config /
  bad config / pi missing / pi wrong version / occupied port (pre-bind a listener);
  assert warnings for missing gitignore entry and zero session types. (Extract serve's
  body into a testable function returning the chosen listener/handler so tests avoid
  process-level races.)
- No real-pi-gated tests in M0 (`pitest` doesn't exist yet); the real binary is exercised
  by the proof workflow below.

## 8. Agent-verified proof workflow

Run by an agent from the repo root
(`/Users/jmcampanini/Code/github.com/jmcampanini/gibson/main`). Uses `.sandbox/` for
scratch (house rule; git-ignored). `PORT=7841` avoids the dogfood port. Every step states
its assertion; any failed assertion fails the milestone.

1. **Build.**
   ```sh
   make test && make build
   GIBSON=$PWD/bin/gibson   # absolute path used by every later step
   ```
   Assert: exit 0; `bin/gibson` exists; `bin/gibson --version` prints a version;
   `git status --porcelain` shows no `web/dist/.gitkeep` deletion (the build re-touches
   the placeholder, §4.3).
2. **Scratch workspace.**
   ```sh
   rm -rf .sandbox/m0 && mkdir -p .sandbox/m0/scratch-repo/main
   cd .sandbox/m0/scratch-repo/main && git init -q
   printf '.gibson/\n' > .gitignore
   printf '[server]\nport = 7841\n\n[sessions.test]\ndescription = "M0 test type"\n' > gibson.toml
   git add -A && git commit -qm init
   ```
3. **Serve + fetch (embedded mode).** From `.sandbox/m0/scratch-repo/main`, start
   `$GIBSON serve` in the background (absolute path from step 1 — a relative path would
   mis-resolve from the scratch checkout); then:
   - `curl -s http://127.0.0.1:7841/api/health` → assert JSON with `"ok":true` and the
     build version (§9.1.a; PLAN_CONVENTIONS §3).
   - `curl -s http://127.0.0.1:7841/` → assert HTML containing `<div id="root">` and a
     hashed `/assets/*.js` script tag (embedded build, SPEC §8.1.2).
   - `curl -s http://127.0.0.1:7841/some/client/route` → assert same `index.html`
     (history fallback).
   - `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:7841/api/nope` → assert
     `404`, and body has `"code":"not_found"` envelope.
   - Assert startup log named the workspace root as the **parent** of the checkout
     (`.../scratch-repo`), per SPEC §2.1.3.
   - Also start once from a subdirectory (`mkdir -p sub && cd sub`) → assert identical
     behavior (checkout-root discovery, SPEC §2.1.2). Kill the server between runs.
4. **Config validation errors** (SPEC §3.1.3, §9.1.b) — each run asserts exit ≠ 0 and the
   quoted fragment in stderr:
   - `mv gibson.toml g.bak` → run serve → `gibson.toml not found`; restore.
   - Config without `port` → `server.port is required`.
   - `port = 99999` → `server.port`.
   - Session type without description → `sessions.test.description`.
   - Syntactically broken TOML (`port = `) → decoder error mentioning `gibson.toml`.
5. **Occupied port** (SPEC §3.2.1): pre-bind with `python3 -c 'import socket,time;
   s=socket.socket(); s.bind(("127.0.0.1",7841)); s.listen(); time.sleep(60)' &` → run
   serve → assert exit ≠ 0, error names port `7841`, and no serve on 7842 (no
   auto-increment). Kill the placeholder.
6. **`--port` override** (SPEC §3.2.1): `serve --port 7842` → health answers on 7842;
   nothing on 7841. Kill.
7. **pi checks** (SPEC §5.4.1, §9.1.b). `pi_bin` MUST land inside the `[server]` table —
   appending it to the end of `gibson.toml` would put it under `[sessions.test]`, where
   it is only an undecoded-key warning and serve starts cleanly on the real PATH pi.
   Rewrite the whole file each time, e.g.
   `printf '[server]\nport = 7841\npi_bin = "%s"\n\n[sessions.test]\ndescription = "M0 test type"\n' <path> > gibson.toml`:
   - `pi_bin = "/nonexistent/pi"` under `[server]` → assert exit ≠ 0, message contains
     `/nonexistent/pi` and reads as *not found* (distinct from version error).
   - Stub `printf '#!/bin/sh\necho 0.50.0\n' > badpi && chmod +x badpi`, set
     `pi_bin = "<abs>/badpi"` under `[server]` → assert error contains **both** `0.50.0`
     and `0.82`.
   - Restore the original config without `pi_bin` (real pi 0.82.1 on PATH) → assert
     serve starts cleanly.
8. **Warnings** (SPEC §4.2.2, §3.2.5):
   - Delete the `.gitignore` line → start serve → assert stderr warning contains
     `.gibson/` and suggests the `.gitignore` line, **and** the server still serves
     health (warning, not error). Restore.
   - Remove the `[sessions.test]` table → assert a zero-session-types warning and the
     server still starts. Restore.
9. **Dev mode** (SPEC §8.1.3): start `npm run dev --prefix web` (Vite on 5173) and
   `serve --dev` in the scratch checkout:
   - `curl -s http://127.0.0.1:7841/` → assert HTML contains `/@vite/client` (proxied
     dev shell, not the embedded build).
   - `curl -s http://127.0.0.1:7841/api/health` → assert still answered by gibson
     (`"ok":true`).
   Kill both.
10. **Browser check.** No server is running at this point (step 3's embedded server and
    step 9's dev-mode pair were all killed), so first restart embedded mode: from the
    scratch checkout, start `$GIBSON serve` in the background on port 7841. Via browser
    automation, open `http://127.0.0.1:7841/`: assert the page shows the "gibson" shell
    and the health-derived version text (the shell's fetch to `/api/health` succeeded —
    full vertical proven). Close the browser and kill the server before step 11.
11. **Hygiene.** `git -C .sandbox/m0/scratch-repo/main status --porcelain` → assert no
    `.gibson/` noise (M0 never creates it); `git -C . status` in the gibson repo →
    assert scratch artifacts are ignored. Kill any remaining background processes;
    `rm -rf .sandbox/m0`.

## 9. Success criteria checklist

- [ ] `make test` green; `make build` produces one self-contained binary embedding the
      SPA (SPEC §8.1.1, §8.1.2; MILESTONES M0 "build pipeline").
- [ ] `gibson serve` from inside a checkout (root or subdirectory) reads that checkout's
      `gibson.toml` and derives workspace root = parent of checkout (SPEC §2.1.1–2.1.3).
- [ ] Valid config serves the SPA shell on the configured bind/port (SPEC §9.1.a);
      history-API fallback and `/api/` 404 envelope behave per PLAN_CONVENTIONS §3.
- [ ] `GET /api/health` returns `{"ok":true,"version":...}` (PLAN_CONVENTIONS §3).
- [ ] Missing config, invalid config (field named: `server.port`,
      `sessions.<name>.description`), occupied port (no auto-increment), missing pi
      binary, and unsupported pi version (both versions named) each produce a distinct
      clear error (SPEC §3.1.3, §3.2.1, §5.4.1, §9.1.b).
- [ ] `--port` overrides `server.port`; `bind` defaults to `127.0.0.1` (SPEC §3.2.1–3.2.2).
- [ ] `extra_args` accepted verbatim, never parsed or validated (SPEC §3.2.4).
- [ ] Missing `.gibson/` gitignore entry → prominent warning naming the fix; gibson never
      edits `.gitignore` (SPEC §4.2.2, §9.1.c). Zero session types → warning, not error
      (SPEC §3.2.5).
- [ ] `serve --dev` proxies non-`/api` to Vite :5173, single origin, HMR functional;
      `/api/*` always gibson's (SPEC §8.1.3; PLAN_CONVENTIONS §1).
- [ ] CLI mirrors grove-cli: root `main.go` → `cmd.Execute()`, cobra with
      `SilenceErrors/SilenceUsage`, ldflags version, per-command files with test siblings
      (PLAN_CONVENTIONS §1).
- [ ] Agent proof workflow (§8) passes end-to-end (MILESTONES M0 proof).

## 10. Explicitly out of scope (owned by later milestones)

- All pi RPC: spawning `pi --mode rpc`, JSONL framing, commands, events, `pisession.Session`
  — **M1**. M0's `internal/pisession` contains only `version.go`.
- `gibson run`, `.gibson/` directory creation, `sessions/`, `state.json` registry, session
  ids, stderr logs — **M1** (`store` in M0 is only the gitignore check).
- fakepi, `internal/pitest`, real-pi-gated tests, `test/fixtures/extensions/confirm-gate.ts`
  — **M1/M5** (M0 hands fakepi the `--version` requirement, §6).
- Checkout enumeration (`git worktree list --porcelain`) and `GET /api/checkouts` — **M2**.
- All session routes, `session.Manager`/`Broker`, SSE (contract in PLAN_CONVENTIONS §4),
  wire status machine — **M2**.
- SPA beyond the health-checking shell: router, `src/api/{types,client,stream}.ts`,
  `sessionStore`, all chat components — **M3/M4**; dialogs — **M5**; session
  list/resume/orphan cleanup — **M6**; hardening + full §9.5 acceptance — **M7**.
