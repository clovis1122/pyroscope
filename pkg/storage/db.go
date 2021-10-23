package storage

import (
	"os"
	"path/filepath"

	"github.com/dgraph-io/badger/v2"
	"github.com/dgraph-io/badger/v2/options"
	"github.com/sirupsen/logrus"

	"github.com/pyroscope-io/pyroscope/pkg/storage/cache"
	"github.com/pyroscope-io/pyroscope/pkg/util/bytesize"
)

type db struct {
	name   string
	logger logrus.FieldLogger

	*badger.DB
	*cache.Cache
}

type prefix string

const (
	segmentPrefix    prefix = "s:"
	treePrefix       prefix = "t:"
	dictionaryPrefix prefix = "d:"
	dimensionPrefix  prefix = "i:"
)

func (p prefix) String() string      { return string(p) }
func (p prefix) bytes() []byte       { return []byte(p) }
func (p prefix) key(k string) []byte { return []byte(string(p) + k) }

func (p prefix) trim(k []byte) ([]byte, bool) {
	if len(k) > len(p) {
		return k[len(p):], true
	}
	return nil, false
}

func (s *Storage) openBadgerDB(name string) (*badger.DB, error) {
	badgerPath := filepath.Join(s.config.StoragePath, name)
	if err := os.MkdirAll(badgerPath, 0o755); err != nil {
		return nil, err
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	if level, err := logrus.ParseLevel(s.config.BadgerLogLevel); err == nil {
		logger.SetLevel(level)
	}

	return badger.Open(badger.DefaultOptions(badgerPath).
		WithTruncate(!s.config.BadgerNoTruncate).
		WithSyncWrites(false).
		WithCompactL0OnClose(false).
		WithCompression(options.ZSTD))
}

func (s *Storage) newDB(badgerDB *badger.DB, name string, p prefix, codec cache.Codec) *db {
	d := db{
		name:   name,
		DB:     badgerDB,
		logger: s.logger.WithField("db", name),
	}

	if codec != nil {
		d.Cache = cache.New(cache.Config{
			DB:      badgerDB,
			Metrics: s.metrics.createCacheMetrics(name),
			TTL:     s.cacheTTL,
			Prefix:  p.String(),
			Codec:   codec,
		})
	}

	return &d
}

func (d *db) size() bytesize.ByteSize {
	// The value is updated once per minute.
	lsm, vlog := d.DB.Size()
	return bytesize.ByteSize(lsm + vlog)
}

func (d *db) runGC(discardRatio float64) (reclaimed bool) {
	d.logger.Debug("starting badger garbage collection")
	// BadgerDB uses 2 compactors by default.
	if err := d.Flatten(2); err != nil {
		d.logger.WithError(err).Error("failed to flatten database")
	}
	for {
		switch err := d.RunValueLogGC(discardRatio); err {
		default:
			d.logger.WithError(err).Warn("failed to run GC")
			return false
		case badger.ErrNoRewrite:
			return false
		case nil:
			reclaimed = true
			continue
		}
	}
}
