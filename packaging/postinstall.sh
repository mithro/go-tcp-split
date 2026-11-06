#!/bin/sh
# Pick up the new unit files. Never enable or start anything.
set -e
if command -v systemctl > /dev/null; then
	systemctl daemon-reload || true
fi
