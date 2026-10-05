#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { echo 'Run WireGuard setup as root.' >&2; exit 2; }
config=/etc/wireguard/inspection.conf
[[ -s "$config" ]] || { echo "Provision $config securely before enabling the Actions tunnel." >&2; exit 2; }
[[ "$(stat -c '%a' "$config")" == 600 && "$(stat -c '%U:%G' "$config")" == root:root ]] || { echo 'WireGuard host configuration must be root-owned mode 0600.' >&2; exit 1; }
python3 - "$config" <<'PY'
import ipaddress, re, sys
text=open(sys.argv[1], encoding="ascii").read()
for section in ("Interface", "Peer"):
    if len(re.findall(rf"(?m)^\[{section}\]$", text)) != 1: raise SystemExit(f"expected exactly one [{section}] section")
address=re.search(r"(?m)^Address\s*=\s*(\S+)$", text)
port=re.search(r"(?m)^ListenPort\s*=\s*(\d+)$", text)
private=re.search(r"(?m)^PrivateKey\s*=\s*(\S+)$", text)
allowed=re.search(r"(?m)^AllowedIPs\s*=\s*(\S+)$", text)
if not address or ipaddress.ip_interface(address.group(1)) != ipaddress.ip_interface("10.77.0.1/32"): raise SystemExit("server tunnel address must be 10.77.0.1/32")
if not port or int(port.group(1)) != 51820: raise SystemExit("server WireGuard port must be 51820")
if not private or not re.fullmatch(r"[A-Za-z0-9+/]{43}=", private.group(1)): raise SystemExit("invalid WireGuard private key")
if not allowed or ipaddress.ip_network(allowed.group(1), strict=False) != ipaddress.ip_network("10.77.0.2/32"): raise SystemExit("peer route must be limited to the Actions runner address")
PY
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools
systemctl enable --now wg-quick@inspection.service
wg show inspection
