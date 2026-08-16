//go:build !windows

package files

import "syscall"

// diskStat returns (free, total) bytes of the filesystem holding path.
// Returns (0, 0) when the call fails.
func diskStat(path string) (uint64, uint64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize)
}
