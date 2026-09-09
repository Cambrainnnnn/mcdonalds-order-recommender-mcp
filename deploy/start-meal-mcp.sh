#!/bin/sh
set -eu

environment_file="${HERMES_HOME:-/opt/data}/meal-mcp.env"
if [ -f "$environment_file" ]; then
    set -a
    # This file is generated from the allowlisted APP_* variables by the
    # container init hook and is readable only by the Hermes runtime user.
    . "$environment_file"
    set +a
fi

exec /usr/local/bin/meal-recommendation-mcp
