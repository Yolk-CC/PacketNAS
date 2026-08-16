package transcode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// metaDirName duplicates media.MetaDir to keep transcode self-contained.
	metaDirName = ".pocketnas"
	// CacheDirName holds transcoded outputs under .pocketnas.
	CacheDirName = "transcode"
	// CacheLimit is the total transcode cache budget; LRU eviction trims to
	// 80% of it.
	CacheLimit = 2 << 30 // 2GB
)

// CacheKey names the output file for (path, mtime, res): sha256 hex + .mp4.
// Including mtime invalidates cached outputs when the source file changes.
func CacheKey(rel string, mtime int64, res string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", rel, mtime, res)))
	return hex.EncodeToString(sum[:]) + ".mp4"
}

// EvictLRU trims dir to 80% of limit, deleting least-recently-modified
// files first. Called once at manager startup.
func EvictLRU(dir string, limit int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name  string
		size  int64
		mtime time.Time
	}
	var items []item
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		items = append(items, item{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= limit {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime.Before(items[j].mtime) })
	target := limit * 80 / 100
	for _, it := range items {
		if total <= target {
			break
		}
		if err := os.Remove(filepath.Join(dir, it.name)); err == nil {
			total -= it.size
		}
	}
}
