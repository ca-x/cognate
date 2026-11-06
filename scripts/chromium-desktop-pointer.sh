#!/bin/sh
set -eu
exec "${COGNATE_BROWSER_EXECUTABLE:?Set COGNATE_BROWSER_EXECUTABLE to your Chromium executable}" --blink-settings=primaryHoverType=2,primaryPointerType=4,availableHoverTypes=2,availablePointerTypes=4 "$@"
