#!/bin/bash
# Delete the instance created by gcp_up.sh. It can be run at any time, even if
# gcp_up.sh failed halfway, and it fails if the instance could not be deleted.
#
# The instance is only taken for absent when Google Cloud says it does not exist.
set -uo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$HERE/gcp_common.sh"

# exists: 0 when the instance exists, 1 when Google Cloud says it does not, 2 when it
# could not be known
exists() {
    local output
    for _ in 1 2 3; do
        if output=$(gcp sql instances describe "$GCP_NAME" --format 'value(state)' 2>&1); then
            return 0
        fi
        if echo "$output" | grep -qiE "does not exist|HTTPError 404|notFound"; then
            return 1
        fi
        sleep 5
    done
    echo "Could not check the instance $GCP_NAME: $output" >&2
    return 2
}

exists
status=$?
if [ "$status" = 0 ]; then
    echo "Deleting instance $GCP_NAME"
    gcp sql instances delete "$GCP_NAME" --async > /dev/null
    for _ in $(seq 90); do
        exists
        status=$?
        [ "$status" = 0 ] || break
        sleep 10
    done
fi

rm -f "$GCP_STATE"

case "$status" in
1)
    echo "Nothing is left of $GCP_NAME"
    exit 0
    ;;
0)
    echo "Instance $GCP_NAME still exists" >&2
    exit 1
    ;;
*)
    echo "Something could not be checked: $GCP_NAME may not be deleted" >&2
    exit 1
    ;;
esac
