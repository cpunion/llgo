#!/usr/bin/env bash
set -euo pipefail

destination="${1:?expected a new report directory}"
expected="${2:?expected the original report count}"
test "$expected" -gt 0
mkdir "$destination"
awk -v destination="$destination" -v expected="$expected" '
function fail(message) {
	print message > "/dev/stderr"
	bad = 1
	exit 1
}
$0 == "<<<<<< network" { ready = 1; next }
ready && /^# path=/ {
	if (active) fail("report header before EOF")
	name = $0
	gsub(/\\/, "/", name)
	sub(/^.*\//, "", name)
	if (name !~ /^coverage-(main|compiler|test-go|dev-globaldce-unit|dev-lto-globaldce-cl|dev-lto-globaldce-symbols|dev-lto-plugin-cl)\.txt$/)
		fail("unexpected coverage filename")
	if (seen[name]++) fail("duplicate coverage filename")
	file = destination "/" name
	active = 1
	lines = 0
	next
}
ready && $0 == "<<<<<< EOF" {
	if (!active || !lines) fail("EOF without a complete coverage header")
	close(file)
	active = 0
	count++
	next
}
active {
	if (!lines && $0 != "mode: atomic") fail("unexpected coverage mode")
	if (lines && $0 != "" && $0 !~ /^[^[:space:]]+:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ [0-9]+ [0-9]+$/)
		fail("malformed Go coverage record")
	print $0 > file
	lines++
}
END {
	if (bad) exit 1
	if (active || count != expected) fail("incomplete coverage archive")
	print "restored " count " original Go coverage reports"
}'
