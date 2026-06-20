# INSTALL.md — putting dongled on a machine

Read `HARDWARE.md` first and buy the right hub. Everything below assumes you already have hardware
that passes `dongled probe -- --experiment a6`.

Nothing in this document is automated behind your back. `dongled bootstrap` writes files and prints a
list of commands; **it never creates an account, changes a sysctl, or restarts a service.** Those
steps are yours, and each one is listed with what it does and how to undo it.

---

## 1. One host or two

There are two roles:

- **panel host** — runs `dongled serve`, the SQLite database, and nginx in front of the web UI.
- **farm host** — has the USB hubs, the dongles, the `dgNN` interfaces, the `ip rule`s, the nft table
  and the 48 `dongled-proxy@` instances.

**They may be the same machine, and for a first deployment they should be — but only if that machine
has USB ports a human being can physically reach.**

That is not a figure of speech. Recovering a wedged dongle sometimes means unplugging it. If your
"server" is a VPS in a datacentre you have never visited, it cannot be the farm host, no matter how
good the rest of it is. A VPS can be the panel host, with a machine in an office or a colo cage you
can walk to as the farm host.

This release ships the local case: one binary, one database, panel and farm together. There is no
remote agent and no `agent_url`. If you split the roles later, the split is at the HTTP API, and the
panel host holds the database.

The farm host is identified by the file `/etc/dongled/FARM`. `dongled enroll` refuses to run without
it, because enrollment rewrites `ip rule`s, `systemd-networkd` files and nft sets in the root network
namespace and that is not something to do on the wrong machine by accident.

---

## 2. Prepare the operating system

Debian 13, Ubuntu 24.04, or anything else with **kernel ≥ 6.2**. See `HARDWARE.md` §4.2.

```
sudo apt update
sudo apt install nftables iproute2 conntrack usbutils uhubctl nginx
uname -r                                     # must be 6.2 or newer
```

Give the machine a **static** IPv4 address. `ip -4 -o addr show` must not print `dynamic` or a finite
`valid_lft` for the address you intend to sell through. This is checked, and it is the failure that
takes the entire farm dark with no useful log line.

Disable ModemManager if nothing else on the box needs it:

```
sudo systemctl disable --now ModemManager
```

If you cannot, the udev rule installed in step 4 keeps it away from Huawei devices instead.

Run the read-only check before installing anything:

```
./deploy/preflight.sh --public-ip 203.0.113.7
```

It writes nothing. Fix whatever it reports before continuing.

---

## 3. Build

```
cd src
make build                     # -> bin/dongled
make web-install && make web   # builds the SPA into internal/webui/dist
make build                     # rebuild so the SPA is embedded
```

3proxy is pinned to commit `122ca26249aaaac9156e0805891555c70e19f2b3`. Build exactly that commit —
not the `0.9.8` tag, which was re-submitted and may point somewhere else:

```
git clone https://github.com/z3APA3A/3proxy /tmp/3proxy
cd /tmp/3proxy
git checkout 122ca26249aaaac9156e0805891555c70e19f2b3
make -f Makefile.Linux WOLFSSL_CHECK=false OPENSSL_CHECK=false PCRE_CHECK=false PAM_CHECK=false
```

Install both binaries:

```
sudo install -D -m 0755 bin/dongled            /usr/local/bin/dongled
sudo install -D -m 0755 /tmp/3proxy/bin/3proxy /usr/local/lib/dongled/3proxy
sudo mkdir -p /usr/local/share/dongled
sudo cp -r deploy /usr/local/share/dongled/deploy
sudo cp -r docs  /usr/local/share/dongled/docs
```

`dongled bootstrap` records the SHA-256 of the 3proxy binary next to it, and `preflight` compares
that digest on every start. A rebuilt or replaced binary turns the check red until you re-run
bootstrap, which is the intent.

---

## 4. Lay down the files

Rehearse into a scratch directory first. This writes nothing to the real filesystem:

```
dongled bootstrap -- --root /tmp/rehearse --apply --source /usr/local/share/dongled/deploy
find /tmp/rehearse -type f
```

Read what it produced. When you are happy:

```
sudo dongled bootstrap -- --apply --force
```

`--force` is required whenever `--root` is empty; it is there so that a mistyped command cannot write
into `/etc` on a machine you did not mean to touch.

That places:

| file | purpose |
|---|---|
| `/etc/systemd/system/dongled.service` | the controller |
| `/etc/systemd/system/dongled-proxy@.service` | the 3proxy instance template, rendered by the code that also renders the configs |
| `/etc/sysusers.d/dongled.conf` | group `dongled` gid 6100, users `px01`..`px48` uid 6101..6148 |
| `/etc/sysctl.d/60-dongled.conf` | `conf.default.rp_filter=2`, `ip_forward=0`, conntrack sizing |
| `/etc/udev/rules.d/99-dongled-mm-ignore.rules` | keeps ModemManager and NetworkManager off the dongles |
| `/etc/systemd/networkd.conf.d/dongled.conf` | `ManageForeignRoutingPolicyRules=no` |
| `/etc/nginx/conf.d/dongled-limits.conf` | rate limit zones and the SSE server-block template |
| `/etc/iproute2/rt_tables.d/dongled.conf` | names for route tables 1001-1048 |
| `/etc/dongled/FARM` | marks this host as the farm |

Re-running bootstrap is safe. It compares content and prints `keep` for anything already correct.

Nothing under `/etc/nginx/nginx.conf` is touched, ever.

---

## 5. The steps bootstrap will not take for you

Run these in order. Each says what it changes.

```
# 5.1 Create the accounts. Reversible with userdel/groupdel.
sudo systemd-sysusers
getent group dongled                       # must print dongled:x:6100:
getent passwd px01                         # must print uid 6101

# 5.2 Apply the sysctls. Reversible by deleting the drop-in and re-running.
sudo sysctl --system
sysctl net.ipv4.conf.all.rp_filter         # must be 2 (the distro already sets this)
sysctl net.ipv4.ip_forward                 # must be 0

# 5.3 Load the udev rules. Affects Huawei devices only.
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=usb --subsystem-match=net

# 5.4 Pick up ManageForeignRoutingPolicyRules=no.
#     THIS RESTARTS NETWORKING. Do it from the console or inside tmux, never
#     from the ssh session you need to keep.
sudo systemctl restart systemd-networkd
```

### 5.5 The key ceremony

```
sudo dongled bootstrap-kek
```

This writes `/etc/dongled/kek.cred`, mode 0600. It is the only thing that can decrypt the proxy
passwords stored in the database.

**Copy it off this machine now, before enrolling anything, and verify the copy is readable.** A
database backup without this file is worthless and there is no recovery path. Do not re-run
`bootstrap-kek` afterwards; overwriting the key makes every stored password permanently unreadable,
which is why the command refuses without `--force`.

### 5.6 Ingress

If a firewall is in the way, open the proxy ports:

```
sudo ufw allow 21001:21048/tcp comment 'dongled socks'
sudo ufw allow 22001:22048/tcp comment 'dongled http'
```

Only on the farm host, and only if you actually run ufw. The panel port `8788` and the metrics port
`9788` bind `127.0.0.1` and must **not** be opened.

---

## 6. Start the controller

```
sudo tee /etc/dongled/dongled.env >/dev/null <<'EOF'
DONGLED_PUBLIC_HOST=203.0.113.7
DONGLED_NODE_ID=farm1
DONGLED_NETCFG=linux
DONGLED_DEVICE=hilink
DONGLED_PROXY=systemd
DONGLED_FW=nft
EOF

sudo systemctl daemon-reload
sudo dongled preflight -public-host 203.0.113.7
```

Everything except `recent_backup` should be green. `nft_table` stays red until the controller has
built the table once; start it and check again:

```
sudo systemctl enable --now dongled
sudo systemctl status dongled
sudo dongled preflight -public-host 203.0.113.7
```

`dongled.service` runs its own fatal preflight as `ExecStartPre`, so a red fatal check prevents
startup rather than producing a half-working farm.

### A note on the command line

Global flags come **before** `--`, subcommand flags **after** it:

```
dongled preflight -public-host 203.0.113.7 -- --fatal-only --json
dongled enroll    -public-host 203.0.113.7 -- --slot 3
```

The bare `--` is not decoration. The process-wide flag set is parsed first, and it does not know about
`--slot`; the separator hands the rest to the subcommand. `key=value` also works without the
separator, for example `dongled enroll slot=3`.

---

## 7. nginx

Copy the `server` block out of `/etc/nginx/conf.d/dongled-limits.conf` — it is in a comment there —
into your own site configuration, add your certificates, then:

```
sudo nginx -t && sudo systemctl reload nginx
```

Always `reload`, never `restart`.

The `location = /api/v1/events` block is not optional. Without `proxy_buffering off` the SSE stream
that drives the live panel is buffered forever, the page loads and then never updates, and **nothing
is logged on either side**. It gets found days later by a customer.

---

## 8. Enrol the first dongle

One dongle at a time. This is enforced, not advisory.

```
sudo dongled enroll -public-host 203.0.113.7 -- --slot 1 --carrier viettel
```

What it does, in order — the order is load-bearing:

1. Refuses if more than one interface holds a `192.168.8.0/24` address, or if any address is
   duplicated. Two factory-default dongles on one host is the most common way this goes wrong.
2. Disables the USB ports of the other un-provisioned slots for the duration of the session.
3. Waits for the new link and reads the **actual** `ID_PATH` with `udevadm`. On this class of host it
   looks like `pci-0000:00:14.0-usb-0:13.1:1.0`; xHCI is a PCI device, never `platform-*`. A `.link`
   file whose `Path=` does not match falls through to the MAC-matching rule, and E3372s share a MAC,
   so the second dongle fails to rename with `EEXIST` and nothing logs it. A path that is not observed
   is a hard error.
4. Stops if the dongle wants a login, and tells you to turn "Require login" off in its web UI.
5. Stops unless the SIM reports 257 (ready) or 258 (PIN disabled).
6. Reads IMEI/ICCID/IMSI/firmware and takes the slot.
7. Writes `.link` and `.network` and reloads — **before** touching DHCP, so a re-enumeration cannot
   rename the interface halfway through.
8. Moves the LAN to `192.168.10N.1`, sending the full object including the DHCP pool inside the new
   subnet. A timeout here usually means it **worked**; the device stops answering at the old address.
9. Sets `MaxIdelTime=0` and reads it back. Skipping this makes every idle proxy die after five
   minutes, which then looks like a rotation bug.
10. Adds the firewall entry, allocates ports, creates the proxy, starts the instance.
11. Re-enables the other USB ports.

Any failure rolls back, so a slot is never left half-provisioned.

At the end it prints the credentials and the sysfs path of the port it used. **Write the slot number
on that physical port now** — see `HARDWARE.md` §3.2.

Then repeat for the next stick. Take the first backup as soon as one proxy works:

```
sudo dongled backup
```

### 8.1 Verify from somewhere else

```
curl -x socks5h://USER:PASS@203.0.113.7:21001 -s --max-time 20 https://ifconfig.me
```

It must return the **dongle's** public address, not the host's. If it returns the host address the
policy routing is not in effect and every proxy you sell is the same IP. Run it from another machine;
a check from the farm host itself passes in exactly the failure mode you are looking for.

---

## 9. Uninstalling

```
sudo systemctl disable --now dongled 'dongled-proxy@*'
sudo nft delete table inet dongled            # NEVER nft flush ruleset
sudo rm -f /etc/systemd/system/dongled.service /etc/systemd/system/dongled-proxy@.service
sudo rm -f /etc/sysctl.d/60-dongled.conf /etc/udev/rules.d/99-dongled-mm-ignore.rules
sudo rm -f /etc/systemd/networkd.conf.d/dongled.conf /etc/nginx/conf.d/dongled-limits.conf
sudo rm -f /etc/iproute2/rt_tables.d/dongled.conf
sudo rm -f /etc/systemd/network/10-dongled-*.link /etc/systemd/network/70-dongled-*.network
sudo systemctl daemon-reload && sudo sysctl --system && sudo systemctl reload nginx
```

Remove the `ip rule`s the controller created, by priority — priority 900, and `1000+N` / `1500+N` per
slot. Do not flush the rule table; other software on the box owns rules too.

Keep `/var/lib/dongled` and `/etc/dongled/kek.cred` until you are certain you will not need the data.
Deleting the key makes every backup unreadable.
