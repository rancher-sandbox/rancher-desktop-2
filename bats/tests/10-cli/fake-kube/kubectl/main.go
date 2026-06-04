// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command fake-kubectl stands in for the real kubectl binary in BATS
// tests of the rdd kubectl version resolver. It prints its arguments
// with a fixed marker prefix and exits 0, so a passing test proves the
// resolver downloaded, sha-verified, and exec'd this file.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("fake-kubectl:", strings.Join(os.Args[1:], " "))
}
