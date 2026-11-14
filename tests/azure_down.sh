#!/bin/bash
# Delete the server created by azure_up.sh. It can be run at any time, even if
# azure_up.sh failed halfway, and it fails if the server could not be deleted.
#
# The server is only taken for absent when it is not in the list of the servers of the
# resource group anymore: a server which is being dropped is still in it.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/azure_common.sh"

# exists: 0 when the server is in the list of the servers of the resource group, 1 when it
# is not, 2 when the list could not be read
exists() {
    local names
    for _ in 1 2 3; do
        if names=$(az rest --method get --url "${AZURE_SERVERS_URL}?api-version=${AZURE_API_VERSION}" \
            --query 'value[].name' --output tsv 2>&1); then
            if grep -qxF "$AZURE_NAME" <<< "$names"; then
                return 0
            fi
            return 1
        fi
        sleep 5
    done
    echo "Could not list the servers of $AZURE_RESOURCE_GROUP: $names" >&2
    return 2
}

exists
status=$?
if [ "$status" = 0 ]; then
    echo "Deleting server $AZURE_NAME"
    arm delete "" > /dev/null
    for _ in $(seq 90); do
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
