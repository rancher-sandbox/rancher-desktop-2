# Calculate the RDD_INSTANCE index for the current RDD_INSTANCE
# This mirrors the Index() function in pkg/RDD_INSTANCE/RDD_INSTANCE.go
get_instance_index() {
    if [[ ${RDD_INSTANCE} =~ ^[0-9]+$ ]] && ((RDD_INSTANCE > 0)) && ((RDD_INSTANCE < 100)); then
        echo "${RDD_INSTANCE}"
        return
    fi

    local i sum
    for ((i = 0, sum = 0; i < ${#RDD_INSTANCE}; i++)); do
        sum=$((sum + $(printf "%d" "'${RDD_INSTANCE:${i}:1}")))
    done
    echo $((100 + (sum % 100)))
}

# Calculate the expected port for the current RDD_INSTANCE
get_expected_port() {
    local base_port="${1:-6443}"
    local instance_index
    instance_index=$(get_instance_index)
    echo $((base_port + instance_index))
}
