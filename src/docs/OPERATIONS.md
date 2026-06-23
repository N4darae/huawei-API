# OPERATIONS.md — running the farm

Written for the person who has been woken up. Sections 1 to 3 are the ones you want first.

Conventions used below: `NN` is a two-digit slot number, so slot 7 is `dg07`, `px07`, uid `6107`,
subnet `192.168.107.0/24`, route table `1007`, SOCKS port `21007`, HTTP port `22007`.

---

## 1. Triage — a customer says their proxy is down

Work top to bottom. Stop at the first thing that is wrong.

```
dongled preflight                                    # is the host itself still sane
systemctl status dongled                             # is the controller running
systemctl status dongled-proxy@px07                  # is the instance running
ss -Hltn | grep -E '2100[0-9]|2200[0-9]'             # is the listener actually bound
journalctl -u dongled-proxy@px07 -n 50 --no-pager
```

### 1.1 The three symptoms and what they mean

| What you see | What it almost always is |
|---|---|
| `systemctl` says active, **no listener** in `ss` | 3proxy parsed a bad config, printed `Unknown operation type`, and exited 0 — or `setuid`/`setgid` failed. Never trust exit codes or "active" for this service. |
| Customer connection **hangs**, no RST, nothing in the 3proxy log | Routing or nft. The priority-900 rule is gone, or the egress chain is dropping the customer leg. A health check run **on this host will pass** while this is happening. |
| Proxy dies after ~5 minutes idle, works again on reconnect | `MaxIdelTime` went back to 300 on that dongle. See §5. |

### 1.2 The hang case, in detail

A routing bug never produces a refusal. It produces a timeout, `ss` holds no `SYN-RECV`, and the
3proxy log is completely silent. Check the two independent halves, because either one alone produces
identical symptoms:

```
# Half 1 — the customer-facing leg. This must say dev <public_if>.
ip route get 203.0.113.99 from 139.99.68.39 uid 6107

# Half 2 — the dongle egress leg. This must say dev dg07 table 1007.
ip route get 1.1.1.1 from 192.168.107.100 uid 6107

# The global rule that makes half 1 work. Priority 900, one line per public IP.
ip rule show | head -20

# The nft counters. rule (0) must be increasing while a customer is connected.
nft list chain inet dongled proxy_egress
```

If the priority-900 rule is missing, something deleted it. The usual culprit is
`systemd-networkd` with `ManageForeignRoutingPolicyRules=yes`; confirm
`/etc/systemd/networkd.conf.d/dongled.conf` is still in place and still says `no`.

Restore it without waiting for a restart:

```
ip rule add from 139.99.68.39 iif lo lookup main priority 900
```

To reset the nft counters while investigating, use `nft reset rules inet dongled`.
`nft reset counters` does **not** reset rule counters — it only touches named counter objects.

### 1.3 Always test from outside

A health check that runs on the farm host passes in exactly the failure mode that matters most,
because loopback traffic never traverses the paths that are broken. Keep a one-liner on a different
machine:

```
curl -x socks5h://USER:PASS@139.99.68.39:21007 -s --max-time 20 https://ifconfig.me
```

---

## 2. Power-cycling a dongle with uhubctl

This is the recovery of last resort for a stick that has stopped answering its HTTP API entirely.

Find the hub and port first:

```
uhubctl
```

Then cut and restore power to **one** port:

```
sudo uhubctl -l 1-13 -p 3 -a off
sleep 5
sudo uhubctl -l 1-13 -p 3 -a on
```

`-l` is the hub location, `-p` the port on that hub, and both come straight from the `uhubctl`
listing. The slot's `usb_path` in the database (`1-13.3`) tells you the same thing: hub `1-13`,
port `3`.

Map a slot back to a physical port when you only know the interface name:

```
basename "$(readlink -f /sys/class/net/dg07/device)"     # e.g. 1-13.3:1.0
```

Strip the `:1.0`; what is left is `hub-port`.

**Never** run `uhubctl -a cycle` without `-l` and `-p`. Without them it addresses every hub it can
find and takes down the entire farm.

If the hub does not support per-port switching, `uhubctl` will say so and you have to unplug the
stick by hand. That is why `HARDWARE.md` insists on PPPS.

---

## 3. Resetting a dongle over USB

A softer step than cutting power: reset the device on the bus.

```
lsusb -t                       # find the bus and device numbers
sudo usbreset 001/013          # bus 001, device 013
```

**Never do this:**

```
sudo usbreset 12d1:14dc        # WRONG
```

Every E3372 in the farm has vendor:product `12d1:14dc`. Addressing by vendor:product resets **every
dongle on the machine at once**, dropping every customer connection. Always use `bus/device`, which
identifies exactly one physical stick.

`dongled` itself only ever resets by bus/device number; `enroll.USBController.ResetDevice` takes two
integers precisely so that the string form cannot be typed by accident.

Get the numbers from sysfs when `lsusb -t` is hard to read:

```
dev=$(basename "$(readlink -f /sys/class/net/dg07/device)" | cut -d: -f1)
printf '%03d/%03d\n' "$(cat /sys/bus/usb/devices/$dev/busnum)" "$(cat /sys/bus/usb/devices/$dev/devnum)"
```

---

## 4. A slot whose dongle cannot change its LAN IP

Some E3372 firmware answers `100002 ERROR_SYSTEM_NO_SUPPORT` to `POST /api/dhcp/settings`. Enrollment
records `dongles.lan_ip_change_supported = false` and completes, but that slot **cannot** carry
traffic under the normal flat policy-routing design: its LAN stays `192.168.8.0/24`, which collides
with every other unconverted dongle.

You have three options, in order of preference.

### 4.1 Replace the dongle

Cheapest by a wide margin. Firmware that refuses this call tends to refuse other things too.

### 4.2 Keep it as the only 192.168.8.x device

One such dongle can live on the farm as long as no other dongle is ever at the factory default at the
same time. The duplicate-address watchdog will complain the moment a second one appears, which is
what you want. This does not scale past one.

### 4.3 The manual network namespace

Give the stubborn dongle its own namespace so its `192.168.8.0/24` cannot collide with anything.
Substitute `NN` (slot), `IFACE` (its current interface name), and `PORT`/`HTTPPORT`.

```
NN=07
IFACE=dg07
PORT=21007
HTTPPORT=22007
NS=dongled$NN

# 1. Create the namespace and move the dongle interface into it.
ip netns add "$NS"
ip link set "$IFACE" netns "$NS"

# 2. Configure the dongle side inside the namespace. The dongle still thinks it is 192.168.8.1.
ip -n "$NS" addr add 192.168.8.100/24 dev "$IFACE"
ip -n "$NS" link set "$IFACE" up
ip -n "$NS" link set lo up
ip -n "$NS" route add default via 192.168.8.1 dev "$IFACE"

# 3. veth pair between the root namespace and the dongle namespace, on a subnet
#    that no dongle uses. 10.90.NN.0/30.
ip link add "veth$NN" type veth peer name "veth${NN}b"
ip link set "veth${NN}b" netns "$NS"
ip addr add "10.90.$NN.1/30" dev "veth$NN"
ip link set "veth$NN" up
ip -n "$NS" addr add "10.90.$NN.2/30" dev "veth${NN}b"
ip -n "$NS" link set "veth${NN}b" up

# 4. NAT customer traffic from the root namespace into the namespace.
#    This is the part that makes it different from every other slot: it is DNAT,
#    not a bound public address, so 3proxy inside the namespace binds 10.90.NN.2.
ip netns exec "$NS" sysctl -w net.ipv4.conf.all.rp_filter=2
nft add rule inet dongled prerouting tcp dport { $PORT, $HTTPPORT } dnat to 10.90.$NN.2

# 5. Run the instance inside the namespace.
systemctl edit dongled-proxy@px$NN
#   [Service]
#   NetworkNamespacePath=/var/run/netns/dongled07
systemctl restart "dongled-proxy@px$NN"
```

Verify from **another machine**, not from this host:

```
curl -x socks5h://USER:PASS@139.99.68.39:21007 -s --max-time 20 https://ifconfig.me
```

Caveats you are accepting by doing this:

- `dongled` does not manage the namespace. A reboot loses it unless you write the units to recreate it.
- The per-slot `ip rule` and `uidrange` machinery does not apply inside the namespace; egress
  containment there comes from the namespace itself, which is stricter, not weaker.
- The rendered 3proxy config for this slot has the wrong `internal`/`external` addresses and must be
  hand-edited. Set `dongled` to leave the slot alone (disable the proxy in the panel) before editing,
  or the reconcile loop will overwrite your file on the next sweep.

This is genuinely a lot of moving parts for one stick. Option 4.1 is usually correct.

---

## 5. `MaxIdelTime` — the five-minute death

The field is misspelled in Huawei's firmware. It is `MaxIdelTime`, not `MaxIdleTime`, and it defaults
to **300 seconds**. When it expires the dongle tears down the data session, and every idle proxy on
that stick stops working until something re-dials. It looks exactly like a rotation bug.

Enrollment sets it to 0 and reads it back. If a dongle loses the setting — after a factory reset, a
firmware update, or a power cut — put it back:

```
curl -s http://192.168.107.1/api/dialup/connection            # read the current object
# then POST the FULL object back with MaxIdelTime=0
```

Check the whole farm at once:

```
for n in $(seq -w 1 48); do
  v=$(curl -s --max-time 3 "http://192.168.1$n.1/api/dialup/connection" \
      | sed -n 's:.*<MaxIdelTime>\([0-9]*\)</MaxIdelTime>.*:\1:p')
  [ -n "$v" ] && [ "$v" != "0" ] && echo "slot $n has MaxIdelTime=$v"
done
```

---

## 6. Duplicate addresses and the factory-reset detector

Two interfaces holding an address in `192.168.8.0/24` means a dongle has gone back to its factory
default — usually because someone pressed reset, or the firmware rolled back after a power cut. Once
two are there, HTTP requests intended for one dongle can reach the other, and the results are
arbitrary.

The reconcile sweep runs `enroll.AddressConflicts` on every pass. It reports three kinds:

| kind | meaning |
|---|---|
| `duplicate_address` | two interfaces hold the same address; something is very wrong |
| `factory_default_multi` | more than one interface is on `192.168.8.0/24` |
| `factory_default_present` | a **provisioned** `dgNN` is back on `192.168.8.0/24`, i.e. it was reset |

Check by hand:

```
ip -4 -o addr show | awk '{print $4}' | sort | uniq -d      # duplicates
ip -4 -o addr show | grep '192\.168\.8\.'                   # factory default
```

Recovery for a reset dongle: re-run enrollment for that slot after unplugging every other
un-provisioned stick.

---

## 7. `tcp_retries2` — an opt-in procedure, deliberately not in the installer

### What it does for us

`net.ipv4.tcp_retries2` controls how many times the kernel retransmits on an established connection
before giving up. The default of 15 works out to roughly **13 to 30 minutes**. When a dongle's data
session dies, every customer connection through it sits in that retransmit window instead of failing
fast, so the customer sees a hang rather than an error, and our own health probes take minutes to
notice.

Lowering it to 8 brings that down to roughly 30 seconds.

### Why it is not in `sysctl.d/60-dongled.conf`

It is **global**. It applies to every TCP connection on the machine, including ones that have nothing
to do with us. On a host shared with other services, this shortens the failure timeout of:

- database client connections (MySQL, PostgreSQL, Redis) — a brief network blip that used to recover
  now surfaces as a connection error to the application,
- long-lived HTTP keep-alive connections between application tiers,
- SSH sessions, including the one you are using right now,
- any message queue consumer holding a long-lived socket,
- replication links, which will drop and resync more often.

On a **dedicated** farm host none of that exists and the change is a clear win. On a shared host it is
a change to someone else's production behaviour, and it must be their decision.

### The procedure

1. Confirm the host is dedicated:
   ```
   systemctl list-units --type=service --state=running
   ss -Hltnp | awk '{print $4, $6}' | sort -u
   ```
   If anything other than sshd, dongled and its proxies is listening, stop here.

2. Write the drop-in as its own file, so it is obvious and removable:
   ```
   printf 'net.ipv4.tcp_retries2 = 8\n' | sudo tee /etc/sysctl.d/61-dongled-optional.conf
   sudo sysctl --system
   ```

3. Record the change where the next operator will find it — append a line to §10 below.

4. To revert: `sudo rm /etc/sysctl.d/61-dongled-optional.conf && sudo sysctl -w net.ipv4.tcp_retries2=15`

Never put this in `deploy/sysctl.d/60-dongled.conf`. The Makefile lint fails the build if it appears
there, on purpose.

---

## 8. Routine maintenance

### 8.1 Backups

```
dongled backup                       # snapshot + integrity check, records last_backup_at
dongled backup -- --list             # newest snapshot and its age
dongled backup -- --verify /var/backups/dongled/dongled-20260808T101500Z.db
```

`preflight` turns `recent_backup` red after 7 days.

A backup is useless without `/etc/dongled/kek.cred`. That file is the only thing that can decrypt the
stored proxy passwords, and there is no recovery path if it is lost. Copy it off the machine together
with the first backup, and confirm the copy is readable.

### 8.2 Restarting things safely

```
systemctl reload dongled-proxy@px07      # SIGUSR1, applies config without dropping sessions
systemctl restart dongled                # controller only; 3proxy instances keep running
systemctl reload nginx                   # never restart
```

`dongled.service` and the `dongled-proxy@` instances are deliberately separate units. Restarting the
controller does **not** touch customer traffic. This is the main reason the supervisor is systemd and
not a Go process spawning children.

After a reload there is a ~1 second window in which two sets of listeners are alive on the same port
(`SO_REUSEPORT`). An authentication probe run inside that window can be answered by the **old** config
and pass while the new one is broken. Wait at least 1.2 s before believing a post-reload probe.

### 8.3 Log locations

```
journalctl -u dongled -f
journalctl -u 'dongled-proxy@*' -f
/var/log/dongled/pxNN.log                # 3proxy per-instance log
dmesg | grep dongled-                    # nft ssrf and leak drops, rate limited
```

---

## 9. Things that will bite you

- **`nft flush ruleset` is forbidden everywhere.** It wipes the host's other tables — ufw, fail2ban,
  docker — silently and instantly. To remove our rules use `nft delete table inet dongled`.
- **`ip daddr @blackhole4` blocks real customers.** `100.64.0.0/10` is CGNAT and Tailscale,
  `10.0.0.0/8` and `172.16.0.0/12` are customers inside the same datacentre, `127.0.0.0/8` is our own
  self-check. The customer-leg accept rule must stay **before** `blackhole4` in the chain.
- **A DNS containment test with an on-subnet `nserver` passes for the wrong reason.** `192.168.1NN.1`
  has a connected route in `main`, so the query leaves via `dgNN` whether or not the `uidrange` rule
  exists. Test with an off-subnet resolver, and use a fresh hostname every time because `nscache`
  hides the second query.
- **`curl --interface dg07` returns `ENETUNREACH`.** `main` has no default route via `dgNN`. Bind by
  address instead: `curl --interface 192.168.107.100`.
- **The panel never binds a public interface.** It listens on `127.0.0.1:8788` and nginx fronts it.
  Metrics on `127.0.0.1:9788` are never proxied outward.

---

## 10. Measurements

This section is the log of what the hardware actually did. `dongled probe` appends to it:

```
dongled probe -- --experiment a3 --rounds 20 --out docs/OPERATIONS.md
dongled probe -- --experiment a2 --slot 1 --out docs/OPERATIONS.md
dongled probe -- --experiment a4 --iface dg01 --out docs/OPERATIONS.md
dongled probe -- --experiment a6 --out docs/OPERATIONS.md
dongled probe -- --experiment login --out docs/OPERATIONS.md
```

Add `--json-out results/a3.json` when you want the numbers in a form something else can read.

What each one decides:

| experiment | decides |
|---|---|
| **a3** | whether the product exists. Hold ladder 2/6/15/40 s × 20 rotations; under ~70% address changes at every hold means this carrier cannot be resold |
| **a2** | flat policy routing versus a namespace per dongle. A `100002` here means P2's design has to change |
| **a4** | whether `carrier`/`operstate` can be trusted, which sets `ConfigureWithoutCarrier` and the meaning of "present" |
| **a6** | whether this host can be the farm host |
| **login** | the exact SKU and whether the no-password assumption holds |

If a result contradicts an assumption in `docs/DECISIONS.md`, open an issue against the package that
owns the decision. Do not edit the contract to match the measurement without doing that.

<!-- dongled probe appends dated sections below this line -->

### Baseline, taken before any hardware was attached

Measured on the development host, not a farm host, and recorded here so the first real run has
something to compare against.

| measurement | value | note |
|---|---|---|
| `kernel_version` | PASS | 7.0.0-28-generic, well past the 6.2 floor |
| `rp_filter` | PASS | `net.ipv4.conf.all.rp_filter = 2` already set by the distribution |
| `ip_forward` | PASS | 0 |
| `public_addr_static` | **FAIL** | `139.99.68.39` is a DHCP lease, `valid_lft 75445sec`. A farm host must not look like this |
| `rt_tables_dir` | **FAIL** | `/etc/iproute2/rt_tables.d` absent until bootstrap runs |
| `hub_ppps` | UNKNOWN | `uhubctl` not installed |
| `no_foreign_rule_below_900` | PASS | Tailscale holds 5210-5270, all above the ceiling |
