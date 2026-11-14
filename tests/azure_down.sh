#!/bin/bash
# Delete the server created by azure_up.sh. It can be run at any time, even if
# azure_up.sh failed halfway, and it fails if the server could not be deleted.
#
# The server is only taken for absent when Azure says it does not exist.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

# exists: 0 when the server exists, 1 when Azure says it does not, 2 when it could not
# be known
exists() {
    local output attempt
    for attempt in 1 2 3; do
        if output=$(arm get "" 2>&1); then
            return 0
        fi
        if echo "$output" | grep -qE "ResourceNotFound|Not Found|was not found"; then
            return 1
        fi
        sleep 5
    done
    echo "Could not check the server $AZURE_NAME: $output" >&2
    return 2
}

exists
status=$?
if [ "$status" = 0 ]; then
    echo "Deleting server $AZURE_NAME"
    arm delete "" > /dev/null
    for attempt in $(seq 90); do
        exists
        status=$?
        [ "$status" = 0 ] || break
        sleep 10
    done
fi

rm -f "$AZURE_STATE"

case "$status" in
1)
    echo "Nothing is left of $AZURE_NAME"
    exit 0
    ;;
0)
    echo "Server $AZURE_NAME still exists" >&2
    exit 1
    ;;
*)
    echo "Something could not be checked: $AZURE_NAME may not be deleted" >&2
    exit 1
    ;;
esac
