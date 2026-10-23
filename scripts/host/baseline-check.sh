#!/usr/bin/env bash
# Stage 18 Linux host hardening baseline — READ-ONLY check (docs/stage-18/infrastructure-hardening.md). It inspects the
# host it runs on and prints PASS / WARN / FAIL / UNKNOWN per control as JSON lines; it never changes anything (no
# package installs, no config edits, no service restarts). Run it on a candidate host before FundZim is deployed there;
# remediation is a deliberate, reviewed change by the host owner.
#
#   scripts/host/baseline-check.sh > host-baseline.jsonl
set -uo pipefail

emit() { printf '{"control":"%s","result":"%s","detail":"%s"}\n' "$1" "$2" "$(printf '%s' "$3" | tr '"\n' "' ")"; }
have() { command -v "$1" >/dev/null 2>&1; }
sshd_opt() { # effective sshd option (needs read access to sshd -T; falls back to the config file)
  if have sshd && sshd -T >/dev/null 2>&1; then sshd -T 2>/dev/null | awk -v k="$1" 'tolower($1)==k{print tolower($2); exit}'
  elif [ -r /etc/ssh/sshd_config ]; then grep -Ei "^\s*$1\s" /etc/ssh/sshd_config | tail -1 | awk '{print tolower($2)}'; fi
}

# H-01 supported OS release
if [ -r /etc/os-release ]; then . /etc/os-release; emit H-01-os-release INFO "${PRETTY_NAME:-unknown} (confirm it is inside its vendor support window)"
else emit H-01-os-release UNKNOWN "no /etc/os-release"; fi

# H-02 security updates applied automatically or by a recorded schedule
if [ -f /etc/apt/apt.conf.d/20auto-upgrades ] && grep -q 'Unattended-Upgrade "1"' /etc/apt/apt.conf.d/20auto-upgrades; then emit H-02-auto-security-updates PASS "unattended-upgrades enabled"
elif have dnf && systemctl is-enabled dnf-automatic.timer >/dev/null 2>&1; then emit H-02-auto-security-updates PASS "dnf-automatic enabled"
else emit H-02-auto-security-updates WARN "no automatic security updates found: document the patch schedule"; fi
if [ -f /var/run/reboot-required ]; then emit H-03-reboot-pending WARN "a reboot is pending to apply updates"; else emit H-03-reboot-pending PASS "no pending reboot flag"; fi

# H-04..H-07 SSH
v=$(sshd_opt permitrootlogin); case "$v" in no|prohibit-password) emit H-04-ssh-root-login PASS "PermitRootLogin $v";; "") emit H-04-ssh-root-login UNKNOWN "sshd not readable";; *) emit H-04-ssh-root-login FAIL "PermitRootLogin $v";; esac
v=$(sshd_opt passwordauthentication); case "$v" in no) emit H-05-ssh-password-auth PASS "PasswordAuthentication no";; "") emit H-05-ssh-password-auth UNKNOWN "sshd not readable";; *) emit H-05-ssh-password-auth FAIL "PasswordAuthentication $v";; esac
v=$(sshd_opt kbdinteractiveauthentication); case "$v" in no|"") emit H-06-ssh-kbd-interactive "$([ -z "$v" ] && echo UNKNOWN || echo PASS)" "KbdInteractiveAuthentication ${v:-unknown}";; *) emit H-06-ssh-kbd-interactive WARN "KbdInteractiveAuthentication $v";; esac
v=$(sshd_opt x11forwarding); [ "$v" = "yes" ] && emit H-07-ssh-x11 WARN "X11Forwarding yes" || emit H-07-ssh-x11 PASS "X11Forwarding ${v:-default}"

# H-08 host firewall active
if have ufw && ufw status 2>/dev/null | grep -q "Status: active"; then emit H-08-firewall PASS "ufw active"
elif have firewall-cmd && firewall-cmd --state 2>/dev/null | grep -q running; then emit H-08-firewall PASS "firewalld running"
elif have nft && nft list ruleset 2>/dev/null | grep -q "hook input"; then emit H-08-firewall PASS "nftables input hook present"
else emit H-08-firewall WARN "no active host firewall detected (or not readable without privileges)"; fi

# H-09 listening services: only expected ports should listen on non-loopback addresses
if have ss; then pub=$(ss -Hltn 2>/dev/null | awk '{print $4}' | grep -Ev '^(127\.|\[::1\]|::1)' | sort -u | tr '\n' ' '); emit H-09-public-listeners INFO "non-loopback TCP listeners: ${pub:-none}"
else emit H-09-public-listeners UNKNOWN "ss not available"; fi

# H-10 time synchronisation
if have timedatectl && timedatectl show -p NTPSynchronized --value 2>/dev/null | grep -q yes; then emit H-10-time-sync PASS "NTP synchronised"
else emit H-10-time-sync WARN "time not reported as synchronised"; fi

# H-11 audit logging
if have systemctl && systemctl is-active auditd >/dev/null 2>&1; then emit H-11-auditd PASS "auditd active"; else emit H-11-auditd WARN "auditd not active"; fi

# H-12..H-15 kernel settings
k() { sysctl -n "$1" 2>/dev/null; }
[ "$(k kernel.kptr_restrict)" -ge 1 ] 2>/dev/null && emit H-12-kptr-restrict PASS "kernel.kptr_restrict=$(k kernel.kptr_restrict)" || emit H-12-kptr-restrict WARN "kernel.kptr_restrict=$(k kernel.kptr_restrict)"
[ "$(k kernel.dmesg_restrict)" = 1 ] && emit H-13-dmesg-restrict PASS "kernel.dmesg_restrict=1" || emit H-13-dmesg-restrict WARN "kernel.dmesg_restrict=$(k kernel.dmesg_restrict)"
[ "$(k net.ipv4.conf.all.accept_redirects)" = 0 ] && emit H-14-icmp-redirects PASS "accept_redirects=0" || emit H-14-icmp-redirects WARN "net.ipv4.conf.all.accept_redirects=$(k net.ipv4.conf.all.accept_redirects)"
[ "$(k kernel.unprivileged_bpf_disabled)" -ge 1 ] 2>/dev/null && emit H-15-unprivileged-bpf PASS "unprivileged_bpf_disabled=$(k kernel.unprivileged_bpf_disabled)" || emit H-15-unprivileged-bpf WARN "kernel.unprivileged_bpf_disabled=$(k kernel.unprivileged_bpf_disabled)"

# H-16 Docker: socket access is root-equivalent
if [ -S /var/run/docker.sock ]; then members=$(getent group docker | cut -d: -f4); emit H-16-docker-group WARN "docker group members (root-equivalent): ${members:-none}"; fi
if have docker && docker info --format '{{.SecurityOptions}}' 2>/dev/null | grep -q "name=userns"; then emit H-17-docker-userns PASS "user namespace remapping on"
elif have docker; then emit H-17-docker-userns WARN "Docker user namespace remapping off (FundZim containers run as non-root anyway)"; fi

# H-18 world-writable files in sensitive directories
ww=$(find /etc -xdev -type f -perm -0002 2>/dev/null | head -5 | tr '\n' ' ')
[ -z "$ww" ] && emit H-18-world-writable-etc PASS "none in /etc" || emit H-18-world-writable-etc FAIL "$ww"

# H-19 sudo: no NOPASSWD rules for everyone
if [ -r /etc/sudoers ]; then n=$(grep -rhs 'NOPASSWD' /etc/sudoers /etc/sudoers.d 2>/dev/null | grep -vc '^\s*#'); [ "$n" = 0 ] && emit H-19-sudo-nopasswd PASS "no NOPASSWD rules" || emit H-19-sudo-nopasswd WARN "$n NOPASSWD rule(s): review"
else emit H-19-sudo-nopasswd UNKNOWN "sudoers not readable without privileges"; fi

# H-20 host intrusion detection / file integrity monitoring
if have aide || have wazuh-agentd || systemctl is-active wazuh-agent >/dev/null 2>&1; then emit H-20-hids PASS "file integrity / HIDS agent present"; else emit H-20-hids WARN "no AIDE or Wazuh agent found"; fi
