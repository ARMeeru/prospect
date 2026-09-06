#!/bin/bash
set -u
mkdir -p /logs/verifier
cd /app
if [ -d /tests/overlay ]; then cp -r /tests/overlay/. /app/; fi
if go test ././  -run '^(TestCountClamp)$' -count=1 -timeout=480s >/tmp/verify.log 2>&1; then
  echo 1 > /logs/verifier/reward.txt
else
  echo 0 > /logs/verifier/reward.txt
  tail -50 /tmp/verify.log
fi
cat /logs/verifier/reward.txt
