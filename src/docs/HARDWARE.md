# HARDWARE.md — what to buy, and why each line is there

This is the bill of materials for a `dongled` farm host. Every item has a reason attached, because
the failure mode of guessing here is not "it does not work" — it is "it works for four dongles and
then behaves strangely forever".

Read this **before** ordering. The single most expensive mistake is a USB hub without per-port power
switching, and you cannot tell from the product page of most of them.

---

## 1. The short version

| Item | Requirement | If you get it wrong |
|---|---|---|
| USB hub | **PPPS** (per-port power switching), USB 3.0, multi-TT | You cannot power-cycle a wedged dongle without walking to the machine |
| Hub power supply | **1A per populated port**, externally powered | Brown-out at the fourth dongle; random disconnects that look like carrier problems |
| Extension cables | 30–50 cm per dongle | Antennas sit on top of each other, signal drops on every stick |
| Host public IP | **Static**, never a DHCP lease | Every proxy stops accepting connections the day the lease changes |
| Host kernel | **≥ 6.2** | Interface rename returns `EBUSY` and enrollment fails at step 7 |
| Port labels | Physical label per port → slot number | You cannot find the dongle that a customer is complaining about |
| Dongles | Huawei E3372**h** in **HiLink** mode | Stick mode speaks AT, not the HiLink HTTP API this software is built on |
| SIMs | Data plan, **PIN disabled** | Enrollment refuses a locked SIM, and there is no way to enter a PIN remotely |

---

## 2. USB hub — the part people get wrong

### 2.1 PPPS is a hard requirement

`uhubctl` can only cut power to an individual port on a hub whose USB descriptor advertises
**per-port power switching (PPPS)**. Hubs come in three flavours:

- **ppps** — per-port switching. This is the one you need.
- **pps / ganged** — one switch for the whole hub. Cutting power to one dongle cuts power to all of
  them, so recovering one stuck stick takes down every customer on that hub.
- **no switching** — power is hard-wired. `uhubctl` can enumerate the hub and do nothing else.

Check before you buy, and check again when it arrives:

```
sudo uhubctl
```

A usable hub prints a line ending in `ppps`:

```
Current status for hub 1-13 [2109:2817 VIA Labs, Inc. USB3.0 Hub, USB 3.00, 4 ports, ppps]
```

`dongled probe -- --experiment a6` does exactly this check for you and turns it into a pass/fail line.

Hubs that are widely reported to do PPPS correctly are built on **VIA Labs VL812 / VL813 / VL817** and
**Genesys Logic GL3520** controllers. Ask the seller for the controller part number; the brand name on
the box tells you nothing.

### 2.2 Power: budget 1A per port, and mean it

An E3372 is a transmitter. It idles around 200–300 mA and spikes close to **1 A** while it is
uploading on LTE or re-registering after a rotation — which is exactly what a rotating proxy farm
does, constantly, on every stick at once.

A "7-port hub, 2A adapter" therefore supports **two** dongles, not seven. When you exceed the budget
the hub does not report an error. Instead:

- dongles disconnect and re-enumerate at random,
- the interface comes back with a different name if `.link` matching ever fails,
- the HiLink API times out intermittently,
- and every one of those looks like a carrier or firmware problem, so you will spend a day debugging
  the wrong layer.

Do the arithmetic before buying:

```
ports_you_will_populate x 1A + 0.5A headroom = minimum adapter current
```

A 10-port hub fully populated needs a **10.5 A** supply, i.e. 5 V / 12 A or better. That is a large
brick. If the hub ships with a 3 A adapter, plan to replace it, and confirm the hub's own barrel jack
and traces are rated for the current you intend to push.

Prefer **several 4-port hubs with their own supplies** over one 10-port hub. Cheaper to power, cheaper
to replace, and one failing hub takes out four customers instead of ten.

### 2.3 USB 3.0 and multi-TT

Each dongle presents a CDC Ethernet interface. On a USB 2.0 single-TT hub every device shares one
transaction translator, and 8 sticks moving traffic will starve each other. Buy USB 3.0 hubs, and if
the datasheet mentions **multi-TT**, that is the one you want.

The dongles themselves are USB 2.0 devices. The hub being 3.0 is about the uplink to the host and the
per-port scheduling, not about the sticks.

---

## 3. Physical layout

### 3.1 Extension cables, 30–50 cm

The E3372's antenna is inside the plastic body. Plugged directly into a hub, sticks sit 15 mm apart
and detune each other; you will see 5–10 dB worse RSRP on the middle sticks of a row. Use a short USB
extension cable per dongle and spread them out. Cheap and it materially improves throughput.

Do not go much past 50 cm on passive USB 2.0 cable, and buy cables with real 28AWG/24AWG conductors —
thin charging cables drop enough voltage under a 1 A load to cause the same brown-out symptoms as an
undersized supply.

### 3.2 Label every port

Put a physical label on each hub port with the slot number. `dongled enroll` prints the sysfs path it
enrolled (for example `1-13.3`) at the end of the run — write the slot number on the port that path
corresponds to.

At 2am, "customer says slot 17 is dead" has to translate into "the fourth port on the second hub"
without you reading sysfs. `OPERATIONS.md` has the command to map a slot back to a port, but the label
is faster and it works when the panel is down.

---

## 4. The host

### 4.1 Static public IP — not negotiable

Every 3proxy instance binds `internal <public_ip>`. Forty-eight configs contain that literal address.
When a DHCP lease changes, all forty-eight fail to bind, every customer connection stops, and the
logs say nothing useful because the process is running.

Check what you have:

```
ip -4 -o addr show
```

This is a lease and is **not acceptable**:

```
2: enp1s0f0  inet 139.99.68.39/24 ... scope global dynamic enp1s0f0\  valid_lft 75445sec ...
```

This is fine:

```
2: enp1s0f0  inet 203.0.113.7/24 ... scope global enp1s0f0\  valid_lft forever preferred_lft forever
```

`dongled preflight` reports `public_addr_static` red for the first form. If your provider only offers
DHCP, ask for a static assignment or a reservation you control; a "sticky" lease is not a guarantee.

The same address is also the source of the priority-900 `ip rule` that keeps customer traffic
flowing, so a change breaks routing as well as binding.

### 4.2 Kernel ≥ 6.2

Enrollment renames the dongle interface to `dgNN` while it is up. Kernels before 6.2 refuse to rename
an interface with `IFF_UP` set and return `EBUSY`. Mainline 6.2 removed that restriction.

| Distribution | Stock kernel | Usable |
|---|---|---|
| Ubuntu 24.04 | 6.8 | yes |
| Debian 13 | 6.12 | yes |
| Ubuntu 22.04 | 5.15 | **no** — install HWE |
| Debian 12 | 6.1 | **no** — install the backports kernel |

Check with `uname -r`. `dongled preflight` gates on it.

### 4.3 Everything else the preflight wants

- `net.ipv4.conf.all.rp_filter = 2`. Debian and Ubuntu already ship this.
- `net.ipv4.ip_forward = 0`. If Docker or libvirt is installed, it will have set this to 1 — a farm
  host should not be running either.
- `nftables` present, `iproute2` present, `conntrack` module loaded.
- **ModemManager disabled**, or the shipped udev ignore rule installed. ModemManager claims every
  HiLink netdev it sees and dials it independently.
- `uhubctl` installed, if you want remote power recovery.
- No other software holding TCP `8788`, `9788`, `20999`, `21001-21048`, `22001-22048`.

### 4.4 One host, or two?

The panel and the farm may be the same machine, but only if that machine has USB ports someone can
physically reach. See `INSTALL.md` §1.

---

## 5. Dongles and SIMs

### 5.1 E3372**h**, HiLink firmware

There are two firmware families with the same plastic:

- **E3372h (HiLink)** — presents a CDC Ethernet interface and a web UI at `192.168.8.1`. This is what
  `dongled` speaks. `-320` and `-325` are both fine.
- **E3372s (Stick)** — presents serial ports and speaks AT commands. **Not supported.**

If `192.168.8.1` does not answer HTTP after plugging one in, you have a stick-mode unit or one that
has been flashed. `dongled probe -- --experiment login` identifies the SKU and firmware exactly.

### 5.2 Every dongle must have "Require login" turned OFF

Plug the dongle into a laptop, open `http://192.168.8.1`, and make sure no password is set. With a
password, every `POST` to the HiLink API returns `100003` and there is no recovery path in software.
`dongled enroll` refuses such a dongle at step 4 with an explicit message; that is intentional, not a
bug to work around.

### 5.3 SIM PIN must be disabled

Put the SIM in a phone first and turn the PIN off. `dongled enroll` reads `/api/pin/status` and
refuses anything except `257` (ready) or `258` (PIN disabled). There is no remote unlock: a farm of
48 sticks in a rack cannot be handed a PIN prompt.

### 5.4 Buy SIMs from more than one carrier

Rotation depends on the carrier handing out a new address after the data session drops. That is a
carrier policy, not a device feature, and it varies. Before committing to a bulk SIM order, run the
gate:

```
dongled probe -- --experiment a3 --addr 192.168.8.1 --rounds 20 --out docs/OPERATIONS.md
```

If no hold length up to 40 s produces a new address at least ~70% of the time, that carrier cannot be
resold as rotating proxies. Test one SIM per carrier before buying fifty.

---

## 6. A concrete starting configuration

For a first 8-dongle build:

- 2 × 4-port USB 3.0 hub with a VL813 or GL3520 controller, PPPS confirmed with `uhubctl`
- 2 × 5 V 5 A external supply (one per hub)
- 8 × 30 cm USB 2.0 extension cable, 24AWG power conductors
- 8 × Huawei E3372h-320 or -325 in HiLink mode
- 8 × data SIM, PIN disabled, from at least two carriers until A3 says which one works
- 1 × host: 2 cores, 4 GB RAM, static IPv4, kernel ≥ 6.2, physically reachable USB ports
- Label maker

Scale by adding hubs, not by buying a bigger one.

---

## 7. Verifying a delivery before you build the farm

```
uname -r                                            # >= 6.2
ip -4 -o addr show | grep -v dynamic                # your public IP must be here
sudo uhubctl                                        # every hub line must end in ppps
dongled probe -- --experiment a6                    # all of the above, as pass/fail
dongled probe -- --experiment login                 # SKU, firmware, no password
dongled probe -- --experiment a4 --iface usb0       # is carrier/operstate trustworthy
dongled probe -- --experiment a3 --rounds 20        # the gate that decides the product
```

Record the output. `--out docs/OPERATIONS.md` appends a dated section to the measurements log so the
numbers survive the person who ran them.
