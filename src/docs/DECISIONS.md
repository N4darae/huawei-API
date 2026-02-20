# DECISIONS

Frozen architecture decisions for `dongled`. Every entry is a decision plus the reason it exists.
Agents implement these; they do not re-litigate them. Changing one requires a contract-change ticket
against the P0 owner.

Marks: **[U]** user decision, frozen. **[M]** measured on this host with a packet trace or a source
read. **[R]** resolution of a conflict between research documents.

---

## Product and layout

### D-01 Product name is `dongled` [R]
Three names appeared in research (`dongled`, `hzproxy`, `proxyfarm`). One is chosen. Every path, unit,
group, nft table, cookie and metric prefix derives from `config.Product`.

### D-02 All new code lives under `src/` [U]
Go module root is `src/go.mod`, module path `github.com/n4darae/huawei-API/src`. The repository root
keeps the user's existing `README.md`, `e3372.py`, `requirements*` and `.idea/` untouched. The root
`.gitignore` is append-only.

### D-03 Two hosts, two roles [M]
This machine is a DEV/SIM host: `lsusb` shows only a root hub and an ATEN IPMI KVM, so no dongle can
be attached, and it runs commercial nginx/mysql/java/node with ufw, fail2ban and tailscale. The FARM
host does not exist yet. Every root, `/etc`, `nft`, `sysctl` and `ufw` step belongs to FARM.
Consequence: `docs/HARDWARE.md` is a required deliverable, and the DEV host runs
`dongled --netcfg=fake --device=sim --proxy=fake --fw=fake`.

---

## Routing and firewall

### D-04 Flat policy routing, not a netns per dongle [U][M]
The flat design was verified end to end with a netns experiment and a real 3proxy binary. A namespace
per dongle stays as plan B, activated only if the A2 measurement (changing the dongle LAN IP through
`POST /api/dhcp/settings`) fails. Six routing constants in `internal/domain/slot.go` carry
`// PROVISIONAL: pending A2/A3 hardware measurement`: `MaxSlots`, `IfacePrefix`, `SubnetOctetBase`,
`RouteTableBase`, `RulePrioSrc`, `RulePrioUID`. They may change after measurement, by the P0 owner
only.

### D-05 Two `ip rule` per slot, plus one global rule at priority 900 [M]
```
ip rule add from <node.PublicHost> iif lo lookup main priority 900     # global, once, at boot
ip rule add from 192.168.10N.100/32 lookup 100N priority 100N          # per slot
ip rule add uidrange 61NN-61NN      lookup 100N priority 15NN          # per slot
```
Evaluation order is load-bearing: `900` < `100N` < `15NN` < `5210` (tailscale).

Why: a `uidrange` rule alone **breaks the customer-facing leg**. The kernel passes `sk_uid` into the
flow lookup in `inet_csk_route_req()`, so the SYN-ACK to the customer leaves through `dg01` instead of
the public interface and the handshake never completes. Measured:
`dg01 Out 10.90.0.1.21001 > 10.90.0.2.35342: Flags [S.]` and
`ip route get 10.90.0.2 from 10.90.0.1 uid 6101 -> via 192.168.101.1 dev dg01 table 1001`.
The `from <public_ip> iif lo lookup main priority 900` rule fixes both halves: the handshake completes
**and** `ip route get 8.8.8.8 from 192.168.101.100 uid 6101` still resolves to `dev dg01 table 1001`.

`iif lo` limits the rule to locally generated traffic, so the blast radius is zero: other uids already
fall through to `32766: main`. The rule must be installed **before** any `uidrange` rule, otherwise
every customer connection open in that window is cut.

### D-06 The `oif dgNN` rule is removed as measured dead code [M]
It matched **0 packets**. `strace` of 3proxy shows `SO_BINDTODEVICE occurrences = 0` — it is only set
when `-D<dev>` is passed, which we do not pass. Left alone, the rule sends traffic out of the wrong
interface with a spoofed source. Keeping it only creates the illusion of defence in depth. It comes
back only if `-Ddg0N` is added to the 3proxy service line. `RulePrioOif` does not exist;
`RulePrioPublic = 900` replaces it, and a slot exposes exactly two rule priorities.

### D-07 DNS containment depends on `uidrange`, and the test must use an off-subnet nserver [M]
The 3proxy resolver binds `0.0.0.0` unconditionally (`0.9.8:src/auth.c:1226-1240` memsets and binds,
ignoring `external`), so `saddr = 0`, the `from` rule cannot match, and the query falls through to the
`uidrange` rule and leaves via `dgNN` with source `192.168.10N.100`. Removing `uidrange` leaks DNS out
of the public uplink. Because `192.168.10N.1` has a connected route in `main`, a test using the
dongle's own gateway as nserver passes even when containment is broken — the containment test must use
an off-subnet nserver and a fresh hostname per measurement, since `nscache 65536` suppresses the
second query.

### D-08 nft ships only an `output` hook with `policy accept` [M]
No `input` chain, no `policy drop`. Ingress is ufw's job on the FARM host. This host has ufw active,
fail2ban with three jails, a java service listening on `*:8483-8604` and nginx on `[::]:443`; a single
`input policy drop` chain kills all of it, and in netfilter a drop in **any** chain at a hook is a
drop.

### D-09 `nft flush ruleset` is forbidden; only `delete table inet dongled` [M]
`flush ruleset` erases the ufw and fail2ban chains and opens the host silently. The backend may only
add and delete **elements**; rule text belongs to one owner.

### D-10 The customer-leg accept rule is rule (0) and sits before `blackhole4` [M]
```
oifname @public_ifaces ip saddr @public_ips tcp sport @proxy_ports \
    ct state established,related accept comment "customer-facing leg"
```
Correct routing alone is not enough: with routing fixed, `oifname != @dongle_ifaces ... drop` still
counted **8/8 SYN-ACKs** dropped and zero packets on the wire, because `meta skgid` matches the
SYN-ACK generated by the listening socket in the `output` hook. Two independent bugs with identical
symptoms — fix one and it still hangs. The rule was verified tight: public source with a source port
outside the proxy range is blocked, and public source inside the range with `ct state new` is blocked.

### D-11 nft `log` statements are wrapped in `limit rate` [M]
nft `log` writes to the **global** kernel ring buffer and is not namespaced. An unrated SSRF scan by a
paying customer floods the host's `dmesg`. `limit rate 10/second burst 20 packets` precedes every
`log`.

### D-12 `meta skgid 6100` uses the numeric gid [M]
nft resolves group **names** at load time. If the group is missing the whole ruleset fails to load,
the unit still reports "started successfully", and there is no firewall.

### D-13 Interface names are frozen as `dgNN`, matched through an nft set [R]
Research used three names (`hzp0`, `dg01`, `dgl01`). If `.link` produces `dg01` while nft ships a
`hzp*` glob, the drop rule matches 100% of proxy egress and the only symptom is one log line. A set
(`@dongle_ifaces`) can be asserted; a glob cannot.

### D-14 Declarative networkd wins over imperative netlink [R]
`netcfg.Manager` writes `.link`/`.network` files and then calls the correct reload verb. Netlink is
read-only plus subscribe. `networkctl reconfigure` deletes routes and rules networkd does not know
about, which makes an imperative reconciler re-add them forever.

### D-15 No DHCP on the dongle side; static `Address=192.168.10N.100/24`, `DHCP=no` [R]
`DhcpStart == DhcpEnd == .100` already made the address deterministic. Removing DHCP deletes a whole
class of failure — address disappearing mid-rotate, 3proxy `external` bind failing with `00012`, the
lease-renewal race when the LAN IP changes, and the `IFA_F_NOPREFIXROUTE` trap.

### D-16 `ConfigureWithoutCarrier=yes`, `IgnoreCarrierLoss=yes`, and "present" means the HiLink API answers [M]
`cdc_ether` HiLink devices commonly report `carrier=0` and `operstate=unknown` permanently. Three code
paths die silently otherwise: networkd never configures the link, `IfaceUp` is always false, and the
rotate `PreCheck` always aborts. Everywhere, `operstate ∈ {up, unknown}` counts as up.

### D-17 `.link` `Path=` is generated from the observed `ID_PATH` [M]
Measured on this host: `ID_PATH=pci-0000:00:14.0-usb-0:13.1:1.0`. xHCI is PCI, never `platform-*`. A
`platform-*-usb-...` template matches nothing, udev falls back to `73-usb-net-by-mac.link`, and since
E3372 units share a MAC the second dongle fails to rename with `EEXIST`, silently.

### D-18 `ip link set <dev> down` before rename is kept as cheap hygiene [M]
Mainline kernels ≥ 6.2 removed the restriction (`netif_change_name()` no longer checks `IFF_UP`), and
this host runs 7.0.0-28, so it is unnecessary here. A farm host on Debian 12 (6.1) or Ubuntu 22.04
(5.15) **will** return `EBUSY`. `docs/HARDWARE.md` records **kernel ≥ 6.2** as the requirement.

### D-19 `net.ipv4.tcp_retries2` stays out of the installer [M]
It is currently `15`, it is per-netns, and the root netns is shared with mysql, nginx, java, tailscale
and sshd — changing it is a production change to somebody else's product. The correct answer to a
15-minute blackhole is the nft fence plus `SOCK_DESTROY`. On the farm host it becomes a signed-off
opt-in documented in `docs/OPERATIONS.md`, with the list of affected services.

---

## 3proxy

### D-20 systemd template unit, not Go fork/exec [R]
`dongled-proxy@px01.service`. The Go supervisor only renders config, calls
`systemctl start/stop/reload`, and reads state.

Why: with fork/exec, children land in the cgroup of `dongled.service`, whose default `KillMode` is
`control-group`, so every panel restart or upgrade sends `SIGTERM` to all 48 3proxy processes — a
full-farm outage on every deploy — and `Adopt()` becomes dead code that never runs in production. With
systemd, `Restart=on-failure` would also fight a Go-side restart loop. Consequence: `Adopt()`, the
`proxy_runtime` table and all `boot_id` logic are deleted. An agent that finds a hard technical reason
to fork/exec must add `Delegate=yes` plus `KillMode=mixed` and record it here.

### D-21 3proxy is pinned by commit sha `122ca26249aaaac9156e0805891555c70e19f2b3` [M]
Tag `0.9.8` was published as a *re-submit* ("Fix windows build, re-submit as 0.9.8"), so the tag may
have moved. Downgrading to 0.9.7 is also wrong: 0.9.8 fixes a use-after-free on the HTTP `ftp://` path
that requires authenticated access, which every paying customer has, and every source line number
frozen in the plan refers to 0.9.8. This sha built successfully on this machine with
`WOLFSSL_CHECK=false OPENSSL_CHECK=false PCRE_CHECK=false PAM_CHECK=false`.

### D-22 `HTTP_DELETE` is not a valid 3proxy keyword [M]
3proxy prints `Unknown operation type: HTTP_DELETE` and then **exits with code 0 without opening a
single listener**. `Restart=on-failure` never fires and a supervisor checking `$?` believes it
succeeded. Valid keywords are `CONNECT BIND UDPASSOC ICMPASSOC HTTP_GET HTTP_PUT HTTP_POST HTTP_HEAD
HTTP_CONNECT HTTP_OTHER HTTP HTTPS FTP_GET ...`; DELETE is covered by `HTTP_OTHER`.
Consequence, non-negotiable: health is **"is the listener bound"**, never an exit code, never "the
process is alive".

### D-23 `Validate()` runs the real binary, and it must run in a throwaway netns [M]
There is no `--test` flag; running the binary is the only way to validate a candidate config. But
3proxy sets `SO_REUSEPORT` unconditionally (`0.9.8:src/proxymain.c:823-825`) and the validating
instance also does `setuid 6101`, so it **joins the real REUSEPORT group** and absorbs part of the new
connections during the validation window before being killed — random "connection reset" for
customers, on every single Apply. Fix: validate inside a disposable network namespace. If that is
impossible, rewrite `internal 127.0.0.1` with a scratch port and document loudly that it no longer
validates the exact bytes that will be installed.

### D-24 `SIGUSR1` also applies bind parameters, so there is no restart branch [M]
D-06 of the plan claimed a restart is needed for `internal`/`external`/port changes. It is not, and
the restart needlessly kills sessions. But for roughly one second after a reload two sets of listeners
live on the same port under `SO_REUSEPORT` and the kernel load-balances between them, so a synthetic
auth probe can be answered — and pass — by the **old** config while the new one is broken. The probe
must wait more than 1.2s, or verify something only the new config can produce.

### D-25 A bad config on reload leaves a live process with zero listeners [M]
`freeconf()` runs before the file is read. `Restart=on-failure` never fires and every naive health
check stays green while the whole farm is dark. Apply order is therefore mandatory: render →
`Validate` → atomic `rename(2)` → `SIGUSR1` → verify the listener on **every** expected port and a
successful synthetic auth probe → on failure restore the previous config, `SIGUSR1`, then `SIGTERM`
and cold start.

### D-26 `-a`, never `-a2` [M]
Verified in 0.9.8 source: `src/proxymain.c:594 srv.anonymous = 1 + atoi(argv[i]+2)` makes `-a2` mean
`anonymous=3`; `src/proxy.c:913 else if(anonymous>1)` then emits `Via`/`X-Forwarded-For` and
`:918 if(anonymous != 2)` **inserts the client's real IP**. `-a2` destroys the product.

### D-27 Password type `CL`, `[A-Za-z0-9]{16}`, `users` line always quoted [R]
`$3$` is a 3proxy-private construction absent from `x/crypto`, and `3proxy_crypt` does not generate a
salt. The panel must display the password, so hashing buys nothing. An unescaped `$` is parsed as an
include-file directive: the user then does not exist and every customer receives `%E 00005`.

### D-28 `timeouts` emits exactly 10 values: `1 5 30 60 180 1800 15 60 10 5` [M]
`src/common.c:120 int timeouts[12]` with ten non-zero entries ending in `CONNECT_TO, CONNBACK_TO`.
Emitting eight values leaves `CONNECT_TO` at its 15s default; 10s is the LTE-appropriate value.

### D-29 `-4` is valid [M]
`src/proxymain.c:519 case '4': srv.family = atoi(argv[i]+1)`. A research document doubted it; the
source settles it.

### D-30 Bind `internal <public_ip>`, no `-i127.0.0.1` plus DNAT [R]
Keeps the customer's real IP in `%C`, which abuse handling needs, and avoids 50 nft elements that must
stay in sync. `auth strong` plus a trailing `deny *` prevents an open relay.

### D-31 `internal <public_ip>` requires a static address [M]
Measured: `139.99.68.39/24 ... dynamic enp1s0f0 valid_lft 75445sec`. A lease change invalidates 48
`internal` lines, every bind fails, and the whole farm goes dark. `docs/HARDWARE.md` requires a static
IP and preflight checks it. The same address feeds the priority-900 rule, so it must be re-rendered
when it changes.

### D-32 `setuid`/`setgid` and `User=`/`Group=` come from the same `domain.Slot` [M]
`h_setgid`/`h_setuid` return 1 on failure (`0.9.8:conf.c:1542/1564`). A `User=px01` in the unit that
disagrees with `setuid 6101` in the config produces exactly the "process alive, zero listeners" state.
One source, one test.

### D-33 `%e` is mandatory in `logformat`
It is the cheapest signal for a stale `external` / `00012` failure.

---

## Data and API

### D-34 `Proxy` has a foreign key to `Slot`, never to `Dongle` [R]
Port, user `px01`, interface, subnet and uid all belong to the **slot**. Replacing a dead stick updates
`slots.dongle_id`; if a proxy pointed at the dongle, pulling the stick sets it to NULL, the join
`Proxy → Dongle → Slot` goes empty, and **a running proxy can no longer resolve its own slot**.

### D-35 Every long task is a row in `operations` [R]
`Operation{kind, subject, state, step, pct, deadline_at}` plus a `stalled` state when
`now > deadline_at` and nothing finished. `rotations` is a detail table with a foreign key to it. The
frontend builds its whole long-running-operation UX on this resource, and reboot, LAN-IP change and
LTE lock all have an intermediate "cannot be reached" state that needs it. A partial unique index
enforces at most one unfinished operation per subject, which is what makes the 409 semantics real.

### D-36 A concurrent rotate returns HTTP 409 `op_in_progress` with the operation id [R]
Chosen over join-and-share-result because the frontend already has an `onError` branch for it and the
semantics are unambiguous for a customer.

### D-37 The customer rotate API is asynchronous [U]
`POST /api/v1/rotate/{proxy_id}` returns `202` with `operation_id` and `poll_url`. `?wait=true` blocks
for at most 90s for customers who want it synchronous. A per-proxy `min_interval` (default 60s) is a
**product** policy, separate from API-key rate limiting; violating it returns `429` with `Retry-After`.
Farm-wide concurrency is capped at 4 simultaneous rotations with stagger jitter, because 48 SIMs on
one carrier toggling data at the same moment is a pattern carriers detect and throttle.

### D-38 "Rotation succeeded but the IP did not change" is a semantic failure [U]
The API reports `result: "unchanged"` with HTTP 200 and `ip_changed: false`, and the row is written to
`rotations`. That table is the cheapest dispute-settling tool in the design.

### D-39 `ErrProbeEgressLeak` is a hard error, never a warning [M]
If the probe's observed IP equals `node.PublicHost`, the rotation fails. Without this, a developer with
no dongle sees every rotation come back plausibly `unchanged` and ships believing rotation works.

### D-40 `GET /r/{link_token}` renders a confirmation page; the side effect needs `POST` [R]
Blocking `Sec-Purpose`/`Purpose: prefetch` is not enough — Outlook SafeLinks, Proofpoint, antivirus
link scanners and crawlers send no identifying header, and a GET with a side effect is wrong HTTP
anyway. A per-token cooldown is the alternative.

### D-41 Proxies support both auth modes: username/password and IP whitelist [U]
`domain.AuthMode ∈ {userpass, iplist, both}` plus the `proxy_auth_ips` table. Rendering emits
`users` + `allow <user>` for the credential branch and `allow * <cidr>` for the whitelist branch.
`Render` must refuse when the mode needs an IP list and the list is empty (the config would collapse to
`deny *`), and refuse a leftover `users` line when the mode is `iplist`.

### D-42 Default port policy is open, with SMTP dropped at the nft layer [U]
`ProxyPolicy.AllowAllPorts = true` is the default and the `allow` line carries no port list.
`tcp dport { 25, 465, 587 } drop` lives in nft, not in the 3proxy ACL. The RFC1918 blackhole stays as
SSRF protection. `AllowedPorts []PortRange` remains so an operator can tighten a single proxy.

### D-43 Proxy export exists [U]
`GET /api/v1/proxies/export?format=txt|csv&scheme=socks5|http` and a button in the UI. Handing
`host:port:user:pass` to a customer is the single most frequent operator task and it was missing from
the plan.

### D-44 DNS uses the dongle's own gateway first, then `1.1.1.1` [R]
`nserver 192.168.10N.1` keeps geolocation consistent with the exit; nft opens exactly
`udp dport 53 ip daddr @dongle_gws` **before** the RFC1918 blackhole, so `tcp/80` to the dongle admin
UI stays blocked and a customer cannot SSRF into resetting the dongle.

### D-45 Escalation ladder: hold 6s → 15s → 40s → reboot. There is no net-mode rung [R]
`net/register Mode=1` costs the same as the `plmn-list` scan the design already forbids on a proxy that
is being sold (blocking scan, data drops) and it fights the LTE-only lock feature through error
`112001`. It becomes a manual maintenance-mode operation instead. Reboot budget is 4/day per dongle
with a 30-minute cooldown.

### D-46 `ActRecoverRotate` is kept, under `trigger=auto_recovery` [R]
A dead proxy that never heals itself is worse than an unrequested rotation. Conditions: it has its own
action name, it is written to `operations` so a disputing customer can see the panel initiated it, and
the operator can disable it per dongle (`dongles.auto_recover_enabled`).

### D-47 Startup grace is `max(host boot, process start) + 180s` **and** requires a warm cache [R]
`World.BootedAt` alone only protects against a host reboot. A panel restart on day three (deploy, OOM,
upgrade) leaves the observation cache empty, the first sweep sees 48 unreachable dongles, and `Plan()`
emits 48 `ActRecoverRotate` — rotating the IP of 48 active customers that nobody asked for. The gate is
therefore both the time window and "at least one complete sweep has finished".

### D-48 Scheduled auto-rotation is out of scope and `'schedule'` is deleted from the trigger enum [R]
Removing the enum value is what stops an agent from building it.

### D-49 `MaxIdelTime=0` is a mandatory enrollment step with a GET verify [M]
The E3372 disconnects after **5 minutes** idle by default (`MaxIdelTime=300`, seconds despite the UI
showing minutes; the firmware misspells the field). Skip this step and every idle proxy dies within
five minutes and recovery rotates it, which looks exactly like a rotation bug.

### D-50 Enrollment refuses a dongle that has a password set [R]
`GET /api/user/hilink_login` must return `0`. The "no RSA needed" conclusion only holds while that is
true; if an operator ever enables "Require login", every POST fails and there is no recovery path. The
check is repeated periodically, and the result is stored in `dongles.hilink_login_required`.

### D-51 LAN-IP-change capability is a probe with a real writer [R]
`dongles.lan_ip_change_supported`, written by enrollment. The original `netns_fallback` flag had no
writer at all. A slot where it is false shows "this slot needs a manual namespace — see
OPERATIONS.md §7" together with copy-pasteable commands generated from live sysfs, because "manual"
without a concrete command means "impossible".

### D-52 SMS is not reassembled, but fragments are flagged [R]
`SmsType == 2` sets `is_fragment` and the UI shows `[fragment]`. An inbox that mangles long messages
without saying so is worse than no inbox, and this costs nothing.

### D-53 SSE carries no replay: no `id`, no `seq`, no `Last-Event-ID` [R]
The stream opens with a `hello` event and the client refetches its queries on every reconnect. A
lagging subscriber has its channel closed, which forces exactly that refetch. nginx needs
`proxy_buffering off` and `X-Accel-Buffering: no`, otherwise SSE simply dies on day one with nothing in
the logs.

### D-54 Only `/api/v1/**` for everything, admin included; `/r/{link_token}` is the sole exception [R]
Customer-facing responses never contain `dongle_id`.

### D-55 API keys and link tokens are revoked independently [R]
A URL pasted into Slack must not expose the key.

### D-56 Rate limiting uses a token bucket, not GCRA [R]
GCRA was cut with the rest of the over-scoped surface; a token bucket with an accurate `Retry-After`
is sufficient at this size.

### D-57 A thin `customers` table, no `proxy_assignments` [R]
`customers` plus `proxies.customer_id` and `proxies.expires_at` answers "who owns this proxy" for abuse
handling and drives expiry. The full assignment/billing model, `usage_proxy_daily` and log-derived
accounting are out of scope for v1; SIM quota comes from the dongle's own counters.

### D-58 Expiry evicts, it does not rewrite the config [M]
`noforce` means revoking a user does **not** kill live sessions, so expiry emits `ActMarkExpired` plus
`ActEvictProxy` — restart without `noforce` to drop live sessions, then stop.

### D-59 Metrics live on a separate `http.Server` on `127.0.0.1:9788` [R]
Its own mux for `/metrics` and `/debug/pprof`, never mounted on the chi router.

### D-60 `RestrictAddressFamilies` differs per unit [R]
`dongled.service` needs `AF_INET AF_UNIX AF_NETLINK`; `dongled-proxy@.service` needs
`AF_INET AF_UNIX`. Applying the proxy value to the backend removes `AF_NETLINK` and kills netlink
config, `SOCK_DESTROY` and rtnetlink subscription — silently, and only at runtime.

### D-61 Port map is frozen and verified at install [M]
`127.0.0.1:5173` is already occupied on this host (pid 1145222), so `strictPort: true` on the default
Vite port dies on day one; the dev server uses `5273`. Occupied here:
3000, 3100, 3306, 3777, 3779, 5173, 5199, 7681, 7682, 8483-8604, 33052, 33060, 60525. The ephemeral
range is `32768-60999`, so every fixed port must stay below 32768.

### D-62 Single UI language (English), dark and light through CSS custom properties [R]
TypeScript is pinned `~6.0.2`: a newer major buys no product value and adds day-one toolchain risk.

### D-63 KEK sealing is `systemd-creds --with-key=host`; there is no TPM here [M]
`/dev/tpm*` does not exist, so the key material lives in
`/var/lib/systemd/credential.secret`. Losing that file makes every customer password unrecoverable.
The `secrets.Sealer` interface plus at-rest XChaCha20-Poly1305 encryption is in scope; the escrow
ceremony, `restore-kek` and the 7-day backup banner were trimmed to a documented procedure.

### D-64 Bulk actions and TOTP are out of scope [R]
No multi-select, no sticky action bar, no `/api/bulk/*`, no checkbox column. `totp_secret_enc` does not
exist in the schema, because a column with no flow is a support question plus a migration to regret.

### D-65 No remote agent, no gRPC [R]
The `node.Runtime` abstraction, a `nodes` table and `node_id` already satisfy the multi-node
requirement. `cmd/dongle-agent`, `internal/node/remote`, `cmd/simctl`, `agent_url` and
`agent_token_hash` are deleted: a proto toolchain and CI job for zero v1 value, validating nothing.

---

## Measurements that still gate the product

Run with `dongled probe --experiment <id>`; write results into `docs/OPERATIONS.md`. **A result that
contradicts an assumption opens an issue against the owning package — no agent changes a contract on
its own.**

| # | Question | What it gates |
|---|---|---|
| A3 | How many seconds of hold make the carrier issue a new IP? 20 runs × hold 2/6/15/40s, record the `ip_changed` ratio | **The entire product.** Below roughly 70% at every hold ≤ 40s, the project stops |
| A2 | Does `POST /api/dhcp/settings` (with every field, including `DhcpStart/EndIPAddress` inside the new subnet) actually change the LAN IP? How long? Does USB re-enumerate? | flat policy routing vs a namespace per dongle. Failure means switching before P2 is written |
| A4 | `cat /sys/class/net/dgNN/{carrier,operstate}` immediately after enumeration and after 60s | `ConfigureWithoutCarrier`, the definition of "present", the rotate `PreCheck` |
| A-login | Is the real SKU a -320 or a -325? What does `GET /api/user/hilink_login` return? | P1 error handling, and whether enrollment may refuse |
| A6 | Farm host: static IP, kernel ≥ 6.2, USB hub with PPPS, 1A per port | `internal <ip>`, D-18, `docs/HARDWARE.md` |

### D-66 Enrollment is a CLI, not a server-side wizard [U]
`dongled enroll --slot N` performs the frozen ten-step sequence, including the "exactly one
unprovisioned dongle" lock, disabling the USB port of every other unprovisioned slot for the duration,
the duplicate-address watchdog, and `MaxIdelTime=0`. `POST /api/v1/enroll/{begin,confirm,abort}`, the
`EnrollSession` resource and the wizard UI are **not** in the API surface — they were cut with the
wizard. The enrollment run still writes an `operations` row with `kind = "enroll"`, so it appears in
activity history and on the event stream.
