load 'load'

@test 'get_instance_index calculates correct index for numeric instances' {
    RDD_INSTANCE=5 run get_instance_index
    assert_output "5"

    RDD_INSTANCE=99 run get_instance_index
    assert_output "99"

    RDD_INSTANCE=1 run get_instance_index
    assert_output "1"
}

@test 'get_instance_index calculates correct checksum for string instances' {
    RDD_INSTANCE=bats run get_instance_index
    assert_output "126" # 100 + (98+97+116+115) % 100 = 100 + 26 = 126

    RDD_INSTANCE="test" run get_instance_index
    assert_output "148" # 100 + (116+101+115+116) % 100 = 100 + 48 = 148
}

@test 'get_instance_index handles edge cases' {
    RDD_INSTANCE=100 run get_instance_index
    # 100 is not < 100, so checksum: 100 + (49+48+48) % 100 = 100 + 45 = 145
    assert_output "145"

    RDD_INSTANCE=0 run get_instance_index
    # 0 is not > 0, so checksum: 100 + (48) % 100 = 100 + 48 = 148
    assert_output "148"
}

@test 'get_expected_port calculates correct ports' {
    RDD_INSTANCE=bats run get_expected_port 6443
    assert_output "6569" # 6443 + 126 = 6569

    RDD_INSTANCE=5 run get_expected_port 6443
    assert_output "6448" # 6443 + 5 = 6448

    RDD_INSTANCE=5 run get_expected_port 8080
    assert_output "8085" # 8080 + 5 = 8085
}
