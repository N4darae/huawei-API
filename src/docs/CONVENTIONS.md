# CONVENTIONS.md — rules every agent working in `src/` must follow

`dongled` operates a farm of Huawei E3372 HiLink dongles as sellable SOCKS5/HTTP proxies.
This file is the operating manual for coding agents. Read it fully before writing a line.

---

## 1. HOST SAFETY — this machine is NOT the farm

**This dev machine (`139.99.68.39`) is a DEV/SIM host, not a FARM host.** It runs *other people's
production software*: nginx, mysql, a java service on `*:8483-8604`, node services, with **ufw**
active (default deny incoming), **fail2ban** active (3 jails), and **tailscale** holding `ip rule`
priorities 5210–5270.

### Forbidden on this machine, without exception

| Forbidden | Why it is catastrophic here |
|---|---|
| `nft -f` or `nft flush ruleset` in the **root netns** | wipes the ufw and fail2ban chains; the host opens wide, silently |
| **Any** `ip rule` or `ip route` change in the root netns | tailscale owns 5210–5270; a stray rule steals or kills production egress |
| `ufw` (any subcommand) | ingress policy belongs to the FARM host installer only |
| `sysctl -w` | sysctls are per-netns and the root netns is shared with mysql/nginx/java/sshd |
| `systemctl restart` / `systemctl stop` of anything | takes down running commercial services |
| Writing anywhere under `/etc` | production configuration is not ours |
| `useradd` / `groupadd` | account namespace belongs to the host owner |
| Binding a listener on a public interface | the panel binds `127.0.0.1` only; nginx fronts it |
| `nft flush ruleset` **anywhere, ever** | not even on the farm; only `delete table inet dongled` |

### Tests that need root

Root-requiring tests run **only inside a network namespace the test creates and cleans up itself**,
with its own name prefix, veth pairs created directly into the namespace, and `defer`-based teardown
plus a `make clean-netns` escape hatch. They must **never touch the root netns**. If root is not
available the suite **fails closed** — it must not silently fall back to the fake backends, because
that is exactly how a broken routing setup ships looking green.

After any netns test the following must still hold: `fail2ban-client status` lists every jail,
`ufw status` is active, and `nft list ruleset` still contains every foreign table.

### Symptoms to expect when you get routing or nft wrong

A routing/nft bug shows up as **TIMEOUT, never as refused**. `ss` holds no SYN-RECV, the 3proxy log
is completely silent, and a health check run **from the farm host itself passes** while no real
customer can connect. Every health check needs a leg from outside.

---

## 2. NO COMMENTS IN CODE

The code speaks for itself. Do not add explanatory comments, doc comments, section banners, or TODOs.

The only permitted comments in the entire tree:

- the six `// PROVISIONAL: pending A2/A3 hardware measurement` markers in `internal/domain/slot.go`
- syntactically required directives: `//go:embed`, `//go:build`

Markdown, SQL, YAML, ini and nft files are documentation/config, not code — normal prose applies
there.

---

## 3. COMMITS

- Commit message: **English**, short, **one subject line**, no body.
  Good: `add huawei client`, `fix rotate timeout`, `wire socks5 port`, `add sqlite schema`.
- **One completed feature = one commit.** Never one giant commit per package.
- **No** `Co-Authored-By`, no tool trailers, no AI/assistant references anywhere.
- Never create any file or folder related to an AI assistant, and never add such a line to
  `.gitignore`.
- Never modify, move, or delete the repository-root files `README.md`, `e3372.py`,
  `requirements.in`, `requirements.txt`, `.idea/`. `.gitignore` at the root is **append-only**.

---

## 4. FILE OWNERSHIP — do not write another package's files

`P0` owns the contract surface. Everything below is **read-only** for `P1`–`P8`. Changing it requires
a contract-change ticket handled by the P0 owner, not an edit in your branch.

```
go.mod  go.sum  Makefile  CONVENTIONS.md
internal/config/**                      paths.go ports.go config.go
internal/domain/**                      slot.go model.go errors.go enums.go
internal/store/migrations/0001_init.sql
internal/store/schema.go
internal/eventbus/types.go  internal/eventbus/bus.go
internal/httpapi/router.go
internal/webui/embed.go  internal/webui/dist/**
cmd/dongled/main.go  cmd/dongled/wire.go
api/openapi.yaml
web/package.json  web/tsconfig.json  web/vite.config.ts  web/index.html
web/src/api/schema.d.ts  web/src/api/events.ts  web/src/api/keys.ts
docs/DECISIONS.md  docs/EVENTS.md
tools/gen/events/main.go  tools/gen/openapi/main.go
*/contract.go        <- every package's contract.go belongs to P0
```

`contract.go` in `internal/device`, `internal/netcfg`, `internal/fw`, `internal/proxysup`,
`internal/reconcile`, `internal/store`, `internal/secrets` holds the interfaces and value types that
package publishes. **Implement them in your own files; do not edit `contract.go`.** If a signature is
wrong, open a contract-change ticket.

`web/src/main.tsx` is a P0 placeholder. P7 replaces it.

### Migration numbers are reserved

`0001` = P0 (this package, already written). `0002` = P4 store, `0003` = P6 auth, `0004` = P5 devops,
`0005` = P5 rotate, `0006`–`0009` spare. Never renumber, never edit `0001_init.sql`.

### Generated and committed — never hand-edit

`web/src/api/schema.d.ts` is generated from `api/openapi.yaml`.
`web/src/api/events.ts` is generated from `internal/eventbus/types.go`.
Regenerate with `make gen`. `make check` fails if the committed output is stale.

---

## 5. FROZEN CONTRACT FACTS YOU MUST NOT RE-DERIVE

- **Product name** `dongled`. Every path, unit, group, table and cookie uses that prefix.
- **Ports**: panel `127.0.0.1:8788`, metrics `127.0.0.1:9788`, vite dev `5273` (5173 is occupied on
  this host), SOCKS `21000+slot`, HTTP `22000+slot`. Every fixed port stays below `32768` because the
  ephemeral range here is `32768-60999`.
- **Slot allocation** is `domain.Slot` and nothing else. Interface `dg01`, user `px01`, uid
  `6100+slot`, subnet `192.168.(100+slot).0/24`, host `.100`, gateway `.1`, route table `1000+slot`.
- **Routing rules per slot are two, not three**: `from <hostIP>` at `1000+slot` and `uidrange` at
  `1500+slot`. The `oif` rule was measured to match zero packets and is **removed**. One global rule
  `from <public_ip> iif lo lookup main priority 900` is built once at boot, **before** any slot rule.
- **Every rule of ours stays below `5210`** (`domain.ForeignRuleCeil`).
- **Huawei error codes**: `100002` = no support (`ErrUnsupported`), `100003` = no rights / login
  required (`ErrNeedLogin`), `100005` = format error. **`100006` does not exist.**
  `108001-108007` = login errors (`ErrLoginRequired`), `125001/125002/125003` = token errors,
  `112001` = busy while dialup. Compare with the sentinels in `internal/domain/errors.go`; never with
  a raw integer in your own package.
- **`Proxy.SlotID`**, never `Proxy.DongleID`. The dongle relationship goes through the slot, because
  a dead stick sets `slots.dongle_id = NULL` and a proxy must still resolve its own slot.
- **Supervisor is systemd** (`dongled-proxy@px01.service`). No fork/exec, no `Adopt()`, no
  `proxy_runtime`, no `boot_id` logic.
- **`HTTP_DELETE` is not a 3proxy keyword.** DELETE lives inside `HTTP_OTHER`. 3proxy prints
  `Unknown operation type` and **exits 0 with no listener**, so health is *"listener bound"*, never
  an exit code and never "process alive".
- **`-a`, never `-a1`/`-a2`.** `-a2` makes 3proxy insert the real client IP.
- **Password type `CL`**, `[A-Za-z0-9]{16}`, the `users` line is always fully double quoted.
- **`timeouts` emits exactly 10 values**: `1 5 30 60 180 1800 15 60 10 5`.
- **Default port policy is open** with `25/465/587` dropped at the nft layer, not in the 3proxy ACL.
- **Trigger enum has three members**: `admin_ui`, `customer_api`, `auto_recovery`. There is no
  `'schedule'`; scheduled auto-rotation is out of scope and must not be built.
- **Enrollment is a CLI**, `dongled enroll --slot N`. There is no `POST /api/v1/enroll/*`, no
  `EnrollSession` resource and no wizard UI; the ten-step sequence itself is unchanged and still writes
  an `operations` row with `kind = "enroll"`.
- **URL tree**: everything lives under `/api/v1/**`, admin included. The only exception is
  `/r/{link_token}`. A customer-facing response never contains `dongle_id`.
- **Cut from the model and the schema**: `FwMark`, `drain_timeout_ms`, `netns_fallback`,
  `totp_secret_enc`, `agent_url`, `agent_token_hash`, `proxy_assignments`, `usage_proxy_daily`,
  `carrier_profiles`, SSE `id`/`seq` replay fields.
- **Times are `INTEGER` unix-millis** everywhere. Every table is `STRICT`. No `TIMESTAMPTZ`, `INET`
  or `SMALLINT` — they abort at startup.
- **`ip daddr @blackhole4` will block real customers**: `100.64.0.0/10` is tailscale and CGNAT,
  `10/8` and `172.16/12` are customers inside the same datacenter, `127/8` blocks our own self-check.
  The customer-leg accept rule must sit **before** `blackhole4`.

---

## 6. SILENT-PASS TRAPS — each needs an assertion, not hope

| Thing | How it passes silently | Required assertion |
|---|---|---|
| public IP probe | no `ip rule` means egress leaves via the host uplink, so every proxy reports the same datacenter IP and every rotate looks legitimately `unchanged` | `ErrProbeEgressLeak` is a **hard** error; fake backends return `ErrNotImplemented` from probe paths |
| `SOCK_DESTROY` | matches zero sockets and returns success | return and expose `conns_killed`; netns test asserts `> 0` |
| `nft add element` | succeeds for an interface that does not exist | after add, `nft list set` and verify membership |
| `.link` `Path=` | no USB device means no match and no error | assert `udevadm info` reports the expected `ID_PATH` **before** writing the file |
| `curl --interface dg01` | `ENETUNREACH`, because `main` has no default via `dgNN` | use address-form bind |
| `netcfg/fake`, `fw/fake` | everything passes | mutators return nil, probes return `ErrNotImplemented` |
| hardware tests | `t.Skip()` forever | CI reports the **number** of skipped hardware tests |
| DNS containment test | `nserver` on the dongle subnet has a connected route in `main`, so the query leaves via `dgNN` even with no `uidrange` rule | use an **off-subnet** nserver, and a fresh hostname per measurement because `nscache 65536` hides the second query |
| nft counters | `nft reset counters <table>` does not reset rule counters | use `nft reset rules <table>` |

---

## 7. WORKFLOW

```
make build          go build into bin/dongled
make test           go test ./...
make lint           gofmt, go vet, and the forbidden-pattern greps
make gen            regenerate schema.d.ts and events.ts
make check          lint + gen + test + fail on stale generated files
make web-install    npm install in web/
make web            build the SPA into internal/webui/dist
make web-reset      drop built assets and restore the committed dist placeholder
```

`make web` overwrites `internal/webui/dist/index.html`, so the tree shows it modified after a build.
That is expected. Do not commit build output; run `make web-reset` before committing.

A clean checkout must satisfy `git clean -xdf && go build ./... && go vet ./...`. That is why
`internal/webui/dist/index.html` is committed: `//go:embed` is compile-time and a missing directory
breaks the whole module.

---

## 8. BUILD TAGS — production is Linux, development is not

`dongled` only ever runs as a farm or panel host on Linux. But the whole tree must also `go build`,
`go vet` and `go test` clean for `GOOS=windows` and `GOOS=darwin`, because Windows and macOS are
supported **development** hosts. `make check` cross-builds and cross-vets all three; a file that
breaks the contract fails the build, not just a code review.

There are two independent axes. Pick the narrower one — do not reach for `unix` when `linux` is
correct, and do not reach for `linux` when `unix` would do.

### 8.1 `linux` / `!linux` — things only a Linux kernel has

Netlink and rtnetlink sockets, network namespaces, sysfs, USBDEVFS ioctls, `uname`: anything that
needs the Linux kernel specifically, not just a POSIX environment. See `internal/fw/netlink_linux.go`
and `netlink_other.go`.

The `_linux.go` file needs no `//go:build` line — `linux` is a real `GOOS` value, so the filename
suffix alone is the constraint, and a redundant tag would just be a comment. The negative file cannot
rely on a suffix (`other` is not a `GOOS`), so it carries `//go:build !linux` and returns
`domain.UnsupportedOn("whatever the facility is")` — never a silent fake, never a value that only
looks reasonable.

### 8.2 `unix` / `!unix` — things every unix has but Windows does not

POSIX file mode bits, `syscall.Stat_t`, `os.Getgroups`, `SIGUSR1`: real everywhere except Windows. See
`internal/secrets/kek_permissions_unix.go` and `kek_permissions_other.go`.

Unlike `linux`, `unix` is not a `GOOS` value, so **both** files need an explicit tag: `//go:build unix`
and `//go:build !unix`. Forgetting the first one is caught by nothing but a Windows build; forgetting
the second one is caught by nothing but a Linux build. Cross-building both is why `make check` runs
`cross-build`/`cross-vet` on every call.

### 8.3 ABI constants are written out as plain numbers, never `syscall.X`

`AF_INET6` is `10` on Linux and `23` on Windows. `syscall.AF_INET6` resolves to whichever one matches
the host the code is compiled on, which is the wrong value for a wire format or a netlink request that
is only ever sent on Linux. Write the Linux ABI number literally in the `linux`-tagged file, the same
way `internal/fw/sockdestroy.go` does, instead of borrowing a host constant that happens to compile.
