#!/usr/bin/env bash
cd "$(dirname "$0")"

if [ -z "$1" ]; then
    echo "First argument is the migration name!"
    exit 1
fi

migrate create -ext sql -dir . -seq $1

echo "After making adjustments, run \"make database/generate\" to generate models."