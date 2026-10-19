package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func DefaultLockFilePath() string {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(runtime, "easyprofile.lock")
}

// AcquireProcessLock must precede session loading and all hardware initialization.
// Never unlink a flock file: another process could otherwise lock a different inode.
func AcquireProcessLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another EasyProfile daemon owns the session lock: %w", err)
	}
	return file, nil
}
