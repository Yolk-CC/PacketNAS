//go:build windows

package files

// diskStat is a no-op on Windows (M1): returns 0, 0.
func diskStat(path string) (uint64, uint64) {
	return 0, 0
}
