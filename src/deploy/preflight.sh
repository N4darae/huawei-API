#!/usr/bin/env bash
# deploy/preflight.sh — read-only host readiness check.
#
# This is the bootstrap twin of `dongled preflight`. It exists because the very
# first question ("can this machine run dongled at all?") has to be answerable
# before the Go binary, the group, the database or 3proxy exist. Once dongled is
# installed, prefer `dongled preflight`: it checks strictly more, including the
# nft table contents, the 3proxy digest and the backup age.
#
# It READS ONLY. It never runs sysctl -w, ufw, systemctl start/stop/restart,
# useradd, ip rule, ip route or nft -f. Running it on a busy production host is
# safe; that is the whole point.
#
# Exit status: 0 when every check passed, 1 when any check failed.
#
# Usage:
#   ./preflight.sh [--public-ip A.B.C.D] [--fatal-only] [--quiet]

set -uo pipefail

PUBLIC_IP="${DONGLED_PUBLIC_HOST:-}"
FATAL_ONLY=0
QUIET=0

while [ $# -gt 0 ]; do
    case "$1" in
        --public-ip) PUBLIC_IP="${2:-}"; shift 2 ;;
        --fatal-only) FATAL_ONLY=1; shift ;;
        --quiet) QUIET=1; shift ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "preflight: unknown argument $1" >&2; exit 2 ;;
    esac
done

MIN_KERNEL_MAJOR=6
MIN_KERNEL_MINOR=2
GROUP_NAME=dongled
GROUP_GID=6100
RT_TABLES_DIR=/etc/iproute2/rt_tables.d
BIN_3PROXY=/usr/local/lib/dongled/3proxy
PIN_FILE=/usr/local/lib/dongled/3proxy.pin
PIN_COMMIT=122ca26249aaaac9156e0805891555c70e19f2b3
UDEV_RULE=99-dongled-mm-ignore.rules
RULE_PRIO_PUBLIC=900
SOCKS_LO=21001
SOCKS_HI=21048
HTTP_LO=22001
HTTP_HI=22048
PANEL_PORT=8788
METRICS_PORT=9788
VALIDATE_PORT=20999

failed=0
fatal_failed=0

say() { [ "$QUIET" -eq 1 ] || printf '%s\n' "$1"; }

ok()   { say "[ ok ] $1  $2"; }
warn() { say "[FAIL] $1  $2"; failed=$((failed + 1)); }
bad()  { say "[FAIL] $1  $2 [fatal]"; failed=$((failed + 1)); fatal_failed=$((fatal_failed + 1)); }

# --- kernel -----------------------------------------------------------------
rel="$(uname -r)"
maj="${rel%%.*}"
rest="${rel#*.}"
min="${rest%%.*}"
min="${min%%[!0-9]*}"
if [ "$maj" -gt "$MIN_KERNEL_MAJOR" ] 2>/dev/null ||
   { [ "$maj" -eq "$MIN_KERNEL_MAJOR" ] && [ "$min" -ge "$MIN_KERNEL_MINOR" ]; } 2>/dev/null; then
    ok kernel_version "$rel"
else
    bad kernel_version "kernel $rel is older than ${MIN_KERNEL_MAJOR}.${MIN_KERNEL_MINOR}; renaming a live interface returns EBUSY there"
fi

# --- rp_filter --------------------------------------------------------------
rp="$(cat /proc/sys/net/ipv4/conf/all/rp_filter 2>/dev/null || echo -1)"
if [ "$rp" = "2" ]; then
    ok rp_filter "net.ipv4.conf.all.rp_filter = 2"
else
    bad rp_filter "net.ipv4.conf.all.rp_filter is $rp, must be 2; asymmetric dongle return paths are dropped otherwise"
fi

# --- ip_forward -------------------------------------------------------------
fwd="$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo -1)"
if [ "$fwd" = "0" ]; then
    ok ip_forward "net.ipv4.ip_forward = 0"
else
    bad ip_forward "net.ipv4.ip_forward is $fwd, must be 0; this host proxies, it does not route"
fi

# --- rt_tables.d ------------------------------------------------------------
if [ -d "$RT_TABLES_DIR" ]; then
    ok rt_tables_dir "$RT_TABLES_DIR exists"
else
    bad rt_tables_dir "$RT_TABLES_DIR does not exist; iproute2 cannot resolve the per-slot table names"
fi

# --- foreign ip rules below priority 900 ------------------------------------
foreign="$(ip rule show 2>/dev/null | awk -F: '$1 ~ /^[0-9]+$/ && $1+0 > 0 && $1+0 < '"$RULE_PRIO_PUBLIC"' { print }')"
if [ -z "$foreign" ]; then
    ok no_foreign_rule_below_900 "nothing evaluates before priority $RULE_PRIO_PUBLIC"
else
    bad no_foreign_rule_below_900 "foreign rules evaluate first: $(echo "$foreign" | tr '\n' ';')"
fi

# --- the priority 900 rule --------------------------------------------------
if [ -z "$PUBLIC_IP" ]; then
    warn public_src_rule_present "no public ip given; pass --public-ip or set DONGLED_PUBLIC_HOST"
elif ip rule show 2>/dev/null | grep -q "^${RULE_PRIO_PUBLIC}:.*from ${PUBLIC_IP} .*iif lo"; then
    ok public_src_rule_present "priority $RULE_PRIO_PUBLIC rule present for $PUBLIC_IP"
else
    bad public_src_rule_present "missing 'ip rule add from $PUBLIC_IP iif lo lookup main priority $RULE_PRIO_PUBLIC'; every customer handshake dies as a timeout without it"
fi

# --- the public address must not be a lease ---------------------------------
if [ -z "$PUBLIC_IP" ]; then
    warn public_addr_static "no public ip given"
else
    line="$(ip -4 -o addr show 2>/dev/null | grep " ${PUBLIC_IP}/" || true)"
    if [ -z "$line" ]; then
        warn public_addr_static "$PUBLIC_IP is not configured on this host"
    elif printf '%s' "$line" | grep -q " dynamic "; then
        warn public_addr_static "$PUBLIC_IP is a DHCP lease ($(printf '%s' "$line" | grep -o 'valid_lft [^ ]*')); a lease change silently breaks all 96 proxy binds"
    else
        ok public_addr_static "$PUBLIC_IP is static"
    fi
fi

# --- group ------------------------------------------------------------------
gid="$(getent group "$GROUP_NAME" 2>/dev/null | cut -d: -f3)"
if [ "$gid" = "$GROUP_GID" ]; then
    ok group_dongled "group $GROUP_NAME has gid $GROUP_GID"
elif [ -n "$gid" ]; then
    bad group_dongled "group $GROUP_NAME has gid $gid, the nft egress chain matches gid $GROUP_GID"
else
    bad group_dongled "group $GROUP_NAME does not exist; install sysusers.d/dongled.conf and run systemd-sysusers"
fi

# --- 3proxy binary ----------------------------------------------------------
if [ ! -x "$BIN_3PROXY" ]; then
    bad 3proxy_binary "$BIN_3PROXY is missing; build the pinned commit $PIN_COMMIT"
elif [ ! -r "$PIN_FILE" ]; then
    bad 3proxy_binary "$PIN_FILE holds no pin record; run 'dongled bootstrap -- --apply' to record it"
else
    have="$(sha256sum "$BIN_3PROXY" | cut -d' ' -f1)"
    want="$(tr ' ' '\n' < "$PIN_FILE" | sed -n 's/^sha256://p')"
    commit="$(tr ' ' '\n' < "$PIN_FILE" | sed -n 's/^commit://p')"
    if [ "$commit" != "$PIN_COMMIT" ]; then
        bad 3proxy_binary "pin record names commit $commit, this release requires $PIN_COMMIT"
    elif [ "$have" != "$want" ]; then
        bad 3proxy_binary "$BIN_3PROXY hashes to $have, pin record says $want"
    else
        ok 3proxy_binary "$BIN_3PROXY matches the pinned commit"
    fi
fi

# --- ModemManager -----------------------------------------------------------
mm_rule=""
for d in /etc/udev/rules.d /usr/lib/udev/rules.d /lib/udev/rules.d; do
    [ -f "$d/$UDEV_RULE" ] && mm_rule="$d/$UDEV_RULE" && break
done
if [ -n "$mm_rule" ]; then
    ok modemmanager_ignored "udev ignore rule installed at $mm_rule"
else
    state="$(systemctl is-enabled ModemManager.service 2>/dev/null || true)"
    case "$state" in
        ""|disabled|masked|not-found) ok modemmanager_ignored "ModemManager.service is ${state:-not installed}" ;;
        *) bad modemmanager_ignored "ModemManager.service is $state and $UDEV_RULE is not installed; it will claim every dongle netdev" ;;
    esac
fi

# --- conntrack over netlink -------------------------------------------------
if [ -r /proc/net/nf_conntrack ] || [ -r /proc/sys/net/netfilter/nf_conntrack_count ]; then
    ok conntrack_netlink "nf_conntrack is loaded ($(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null || echo '?') entries)"
else
    bad conntrack_netlink "nf_conntrack is not loaded; rotation cannot flush stale flows"
fi

# --- nft table --------------------------------------------------------------
if ! command -v nft >/dev/null 2>&1; then
    bad nft_table "nft is not installed"
elif nft list table inet dongled >/dev/null 2>&1; then
    ok nft_table "table inet dongled is present"
else
    bad nft_table "table inet dongled is absent (or this shell cannot read it; try as root)"
fi

# --- reserved ports ---------------------------------------------------------
busy=""
listening="$(ss -Hltn 2>/dev/null | awk '{print $4}' | sed 's/.*://')"
for p in $PANEL_PORT $METRICS_PORT $VALIDATE_PORT $(seq $SOCKS_LO $SOCKS_HI) $(seq $HTTP_LO $HTTP_HI); do
    printf '%s\n' "$listening" | grep -qx "$p" && busy="$busy $p"
done
if [ -z "$busy" ]; then
    ok ports_free "all reserved ports are free"
else
    warn ports_free "already bound:$busy"
fi

# --- summary ----------------------------------------------------------------
if [ "$FATAL_ONLY" -eq 1 ]; then
    [ "$fatal_failed" -eq 0 ] && exit 0
    say "preflight: $fatal_failed fatal check(s) failed"
    exit 1
fi
[ "$failed" -eq 0 ] && exit 0
say "preflight: $failed check(s) failed"
exit 1
