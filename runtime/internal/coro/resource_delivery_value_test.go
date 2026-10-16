//go:build darwin || linux

package coro

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// This tests the proposed recipe's value predicate, not compiler integration.
// Run the FD 0 and descriptor-reuse cases in a private child so the host test
// runner's own stdin and descriptors are not changed.
func TestResourceDeliveryFDValues(t *testing.T) {
	if os.Getenv("LLGO_DELIVERY_FD_VALUES_CHILD") == "1" {
		testResourceDeliveryFDValuesChild(t)
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestResourceDeliveryFDValues$", "-test.count=1", "-test.v")
	command.Env = append(os.Environ(), "LLGO_DELIVERY_FD_VALUES_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "fd-zero error-zero reuse release-once ok") {
		t.Fatalf("private fd oracle: %v\n%s", err, output)
	}
}

func testResourceDeliveryFDValuesChild(t *testing.T) {
	fd, err := syscall.Open("/dev/null", syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if fd != 0 {
		if err := syscall.Close(0); err != nil && err != syscall.EBADF {
			t.Fatal(err)
		}
		zero, err := syscall.Dup(fd)
		if err != nil || zero != 0 {
			t.Fatalf("duplicate into private stdin: (%d, %v)", zero, err)
		}
		if err := syscall.Close(fd); err != nil {
			t.Fatal(err)
		}
	}
	defer syscall.Close(0)
	var stat syscall.Stat_t
	if err := syscall.Fstat(0, &stat); err != nil {
		t.Fatal("could not establish owned fd zero: ", err)
	}

	// The host Go syscall wrapper really returns zero here: no acquisition
	// occurred, and treating fd >= 0 as sufficient would close valid stdin.
	badFD, badErr := syscall.Open("invalid\x00path", syscall.O_RDONLY, 0)
	if badFD != 0 || badErr != syscall.EINVAL {
		t.Fatalf("path rejection: (%d, %v)", badFD, badErr)
	}
	closeCount := 0
	drop := func(result *int, resultErr error) {
		if resultErr != nil || *result < 0 {
			return
		}
		owned := *result
		*result = -1 // claim before the release, including any future suspend
		closeCount++
		if err := syscall.Close(owned); err != nil {
			t.Fatal(err)
		}
	}
	drop(&badFD, badErr)
	if closeCount != 0 || syscall.Fstat(0, &stat) != nil {
		t.Fatal("error result stole fd zero")
	}

	result := 0
	drop(&result, nil)
	if result != -1 || closeCount != 1 || syscall.Fstat(0, &stat) != syscall.EBADF {
		t.Fatal("successful fd zero did not release once")
	}
	fd, err = syscall.Open("/dev/null", syscall.O_RDONLY, 0)
	if err != nil || fd != 0 {
		t.Fatalf("fd reuse: (%d, %v)", fd, err)
	}
	drop(&result, nil) // repeated cancellation must not close the reused fd
	if closeCount != 1 || syscall.Fstat(0, &stat) != nil {
		t.Fatal("repeated release closed the new resource")
	}
	fmt.Println("fd-zero error-zero reuse release-once ok")
}
