#!/bin/sh
# M0 field test: run on the production VPS, then send back the .tar.gz.
#
#   scp dist/observer-linux-amd64 scripts/m0/run.sh scripts/m0/urls.txt vps:~/m0/
#   ssh vps 'cd ~/m0 && sh run.sh'
#
# Takes about 10 minutes (SKIP_RATE=1 skips the 5-minute rate test). Set RATE_FAST=1 to add a 1-request-per-second burst
# (may get the IP flagged for a while; run it last, and only if you accept that).
# Set S3_ENDPOINT, S3_REGION, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY to also
# measure Wasabi throughput (uploads, downloads and deletes one 8 MB object).
set -eu

cd "$(dirname "$0")"
BIN=${BIN:-./observer-linux-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')}
URLS=${URLS:-urls.txt}
ALIVE=${ALIVE:-https://mp.weixin.qq.com/s/YPak1xLfYK-BOjk-OYbAKg}
OUT=m0-$(hostname)-$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$OUT"
chmod +x "$BIN"

step() { echo "==> $*" >&2; }

step "system info"
{
	"$BIN" version
	uname -a
	free -m || true
	nproc || true
} >"$OUT/system.txt" 2>&1

step "network paths (WeChat, all Wasabi regions, SMTP ports)"
"$BIN" netcheck -wasabi-regions all -samples 5 >"$OUT/netcheck.txt" 2>&1 || true
if [ -n "${S3_ENDPOINT:-}" ]; then
	step "Wasabi throughput"
	"$BIN" netcheck -s3 -smtp-host "" -wasabi-regions "" -samples 1 >"$OUT/s3.txt" 2>&1 || true
fi

step "samples: full fetch, images, referer test, fixtures"
"$BIN" probe -file "$URLS" -full -images 3 -referer-test -save-fixture "$OUT/fixtures" >"$OUT/samples.txt" 2>&1 || true

step "head-only check on all samples"
"$BIN" probe -file "$URLS" -head >"$OUT/head.txt" 2>&1 || true

step "user agents"
for ua in ios android desktop; do
	"$BIN" probe -ua "$ua" "$ALIVE" >"$OUT/ua-$ua.txt" 2>&1 || true
done

if [ "${SKIP_RATE:-0}" != 1 ]; then
	step "sustained rate: 100 checks, one every 3s (production peak rate, ~5 min)"
	"$BIN" probe -repeat 100 -interval 3s "$ALIVE" >"$OUT/rate-3s.txt" 2>&1 || true
fi

if [ "${RATE_FAST:-0}" = 1 ]; then
	step "burst: 60 checks, one per second"
	"$BIN" probe -repeat 60 -interval 1s "$ALIVE" >"$OUT/rate-1s.txt" 2>&1 || true
	step "recovery check after the burst"
	sleep 60
	"$BIN" probe "$ALIVE" >"$OUT/rate-1s-recovery.txt" 2>&1 || true
fi

tar czf "$OUT.tar.gz" "$OUT"
step "done: $(pwd)/$OUT.tar.gz"
