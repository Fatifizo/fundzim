# Stage 18 — Host operating system hardening baseline

No production host exists. This baseline is what a host must meet before FundZim is deployed on it. It is checked by
`scripts/host/baseline-check.sh`, which is **read-only** (it never installs, edits or restarts anything — the brief
forbids destructive changes to an unknown host) and prints one JSON line per control.

| Control | Requirement | Check |
|---|---|---|
| H-01 OS | a release inside its vendor support window (LTS) | `/etc/os-release` (reported for a person to confirm) |
| H-02 Patching | automatic security updates or a documented, recorded schedule | unattended-upgrades / dnf-automatic |
| H-03 Reboots | no pending reboot after updates | `/var/run/reboot-required` |
| H-04–H-07 SSH | no root login, keys only (no passwords, no keyboard-interactive), no X11 forwarding; access through a bastion or VPN | `sshd -T` |
| H-08 Firewall | default-deny inbound; only the proxy's 443 public; administration from the management network | ufw / firewalld / nftables |
| H-09 Listeners | nothing else listening on public addresses | `ss -ltn` |
| H-10 Time | NTP synchronised (audit timestamps, TOTP, token expiry) | `timedatectl` |
| H-11 Audit logging | auditd (or equivalent) shipping to a separate log store | `auditd` |
| H-12–H-15 Kernel | `kptr_restrict ≥ 1`, `dmesg_restrict = 1`, ICMP redirects off, unprivileged BPF disabled | `sysctl` |
| H-16–H-17 Container runtime | the docker group is root-equivalent: only operators; rootless or user-namespace remapping preferred | group, `docker info` |
| H-18 Files | no world-writable files under `/etc` | `find` |
| H-19 Sudo | named administrators, no blanket `NOPASSWD` | sudoers |
| H-20 HIDS | file-integrity monitoring (AIDE, Wazuh or equivalent) | agent present |

## Run on the development host (2026-10-23)

9 PASS, 6 WARN, 3 UNKNOWN (no privileges to read sshd/sudoers), 2 INFO: Ubuntu 25.10; automatic updates on; no pending
reboot; NTP synchronised; kernel settings PASS; **WARN:** X11 forwarding on, no active host firewall detected, auditd
inactive, docker group membership, no user-namespace remapping, no HIDS; public listeners: SSH (22) and RDP
(3389, 3390). This is a developer workstation, not a FundZim host (F-S18-22, owner decision); it shows the check works
and why a production host needs this baseline.

## Administrative access

Named administrator accounts only, MFA on the bastion/VPN, no shared credentials, sudo logged, break-glass accounts
sealed and audited; production database and secret-manager administration only from the management network.
