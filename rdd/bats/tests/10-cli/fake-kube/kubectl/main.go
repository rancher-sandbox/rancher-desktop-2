// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command fake-kubectl stands in for real kubectl in resolver tests: it
// prints its args with a marker prefix and exits 0, proving a caller ran it.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Println("fake-kubectl:", strings.Join(os.Args[1:], " "))
}
