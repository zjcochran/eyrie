#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# eyrie-nas-health: SnapRAID and SMART health for an Eyrie SNMP storage
# monitor, read through net-snmp's extend lines. Both need root and take
# a while, so a timer collects them and snmpd only reads the results.
#
#   eyrie-nas-health collect          as root (eyrie-nas-health.timer):
#                                     checks, and keeps the results
#   eyrie-nas-health report snapraid  from snmpd: prints a result and
#   eyrie-nas-health report smart     exits with 0 when it is healthy
#
# Settings, from the environment or /etc/default/eyrie-nas-health:
#   SNAPRAID_CONF   SnapRAID's configuration (/etc/snapraid.conf)
#   SYNC_MAX_HOURS  down when the last sync or scrub is older (48; 0: no limit)
#   SCRUB_MAX_DAYS  down when the oldest block was scrubbed longer ago (0: no limit)
#   STALE_MINUTES   down when collect has not run for longer (120)
#   STATE_DIR       where the results are kept (/var/lib/eyrie-nas-health)
set -eu

if [ -r /etc/default/eyrie-nas-health ]; then
	# shellcheck source=/dev/null
	. /etc/default/eyrie-nas-health
fi
SNAPRAID_CONF=${SNAPRAID_CONF:-/etc/snapraid.conf}
SYNC_MAX_HOURS=${SYNC_MAX_HOURS:-48}
SCRUB_MAX_DAYS=${SCRUB_MAX_DAYS:-0}
STALE_MINUTES=${STALE_MINUTES:-120}
STATE_DIR=${STATE_DIR:-/var/lib/eyrie-nas-health}

# A result is one line: the exit status for snmpd (0 healthy, 1 not), then
# the message.

# snapraid_result checks the array: errors, an unfinished sync, and how
# long ago it was synced and scrubbed.
snapraid_result() {
	if ! command -v snapraid >/dev/null 2>&1; then
		echo "1 SnapRAID is not installed"
		return
	fi
	if ! out=$(snapraid -c "$SNAPRAID_CONF" status 2>&1); then
		case $out in
		*lock*) echo "0 SnapRAID: a sync or scrub is running" ;;
		*) echo "1 SnapRAID status failed: $(printf '%s\n' "$out" | tail -n 1)" ;;
		esac
		return
	fi
	problems=""
	errors=$(printf '%s\n' "$out" | sed -n 's/^DANGER! In the array there are \([0-9]*\) errors!.*/\1/p')
	if [ -n "$errors" ]; then
		problems="$errors errors (snapraid -e fix repairs them)"
	fi
	if printf '%s\n' "$out" | grep -q '^WARNING! The array is NOT fully synced'; then
		problems="${problems:+$problems, }not fully synced"
	fi
	oldest=$(printf '%s\n' "$out" | sed -n 's/^The oldest block was scrubbed \([0-9]*\) days ago.*/\1/p')
	if [ "$SCRUB_MAX_DAYS" -gt 0 ] && [ -n "$oldest" ] && [ "$oldest" -gt "$SCRUB_MAX_DAYS" ]; then
		problems="${problems:+$problems, }oldest block scrubbed $oldest days ago"
	fi
	# SnapRAID writes its content file at the end of each sync and scrub.
	synced="never synced"
	content=$(sed -n 's/^content[[:space:]][[:space:]]*//p' "$SNAPRAID_CONF" | head -n 1)
	if [ -n "$content" ] && [ -f "$content" ]; then
		hours=$((($(date +%s) - $(stat -c %Y "$content")) / 3600))
		synced="last synced or scrubbed $hours hours ago"
		if [ "$SYNC_MAX_HOURS" -gt 0 ] && [ "$hours" -gt "$SYNC_MAX_HOURS" ]; then
			problems="${problems:+$problems, }$synced"
		fi
	elif [ "$SYNC_MAX_HOURS" -gt 0 ]; then
		problems="${problems:+$problems, }never synced"
	fi
	if [ -n "$problems" ]; then
		echo "1 SnapRAID: $problems"
	else
		echo "0 SnapRAID: no errors, $synced"
	fi
}

# smart_result asks each disk smartctl finds for its SMART health. Disks
# that are asleep are left asleep.
smart_result() {
	if ! command -v smartctl >/dev/null 2>&1; then
		echo "1 smartmontools is not installed"
		return
	fi
	n=0 asleep=0 bad=""
	scan=$(smartctl --scan)
	while read -r dev _ type _; do
		[ -n "$dev" ] || continue
		n=$((n + 1))
		rc=0
		smartctl -H -n standby,255 -d "$type" "$dev" >/dev/null 2>&1 || rc=$?
		# smartctl's exit status is a bit mask (smartctl(8)).
		if [ "$rc" -eq 255 ]; then
			asleep=$((asleep + 1))
		elif [ $((rc & 8)) -ne 0 ]; then
			bad="${bad:+$bad, }$dev says it is failing"
		elif [ $((rc & 16)) -ne 0 ]; then
			bad="${bad:+$bad, }$dev has a pre-failure attribute at its threshold"
		elif [ $((rc & 6)) -ne 0 ]; then
			bad="${bad:+$bad, }$dev cannot be read"
		fi
	done <<EOF
$scan
EOF
	if [ "$n" -eq 0 ]; then
		echo "1 SMART: smartctl finds no disks"
	elif [ -n "$bad" ]; then
		echo "1 SMART: $bad"
	elif [ "$asleep" -gt 0 ]; then
		echo "0 SMART: $((n - asleep)) of $n disks healthy, $asleep asleep"
	else
		echo "0 SMART: $n disks healthy"
	fi
}

# keep writes a result where report reads it, all at once.
keep() {
	printf '%s\n' "$2" >"$STATE_DIR/$1.new"
	chmod 644 "$STATE_DIR/$1.new"
	mv "$STATE_DIR/$1.new" "$STATE_DIR/$1"
}

collect() {
	mkdir -p "$STATE_DIR"
	chmod 755 "$STATE_DIR"
	keep snapraid "$(snapraid_result)"
	keep smart "$(smart_result)"
}

report() {
	f="$STATE_DIR/$1"
	if [ ! -r "$f" ]; then
		echo "No $1 result yet: is eyrie-nas-health.timer running?"
		exit 3
	fi
	minutes=$((($(date +%s) - $(stat -c %Y "$f")) / 60))
	if [ "$minutes" -gt "$STALE_MINUTES" ]; then
		echo "The last $1 check ran $minutes minutes ago: is eyrie-nas-health.timer running?"
		exit 2
	fi
	read -r status message <"$f"
	echo "$message"
	case $status in
	0) exit 0 ;;
	*) exit 1 ;;
	esac
}

case "${1:-} ${2:-}" in
"collect ") collect ;;
"report snapraid" | "report smart") report "$2" ;;
*)
	echo "usage: eyrie-nas-health collect | report snapraid | report smart" >&2
	exit 64
	;;
esac
