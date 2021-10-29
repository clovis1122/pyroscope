package storage

import (
	"os"
	"path/filepath"

	"github.com/dgraph-io/badger/v2"
	"github.com/dgraph-io/badger/v2/options"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/pyroscope-io/pyroscope/pkg/storage/cache"
	"github.com/pyroscope-io/pyroscope/pkg/util/bytesize"
)

type db struct {
	name   string
	logger logrus.FieldLogger

	*badger.DB
	*cache.Cache

	lastGC  bytesize.ByteSize
	gcCount prometheus.Counter
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

func (s *Storage) newBadger(name string, p prefix, codec cache.Codec) (*db, error) {
	badgerPath := filepath.Join(s.config.StoragePath, name)
	if err := os.MkdirAll(badgerPath, 0o755); err != nil {
		return nil, err
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	if level, err := logrus.ParseLevel(s.config.BadgerLogLevel); err == nil {
		logger.SetLevel(level)
	}

	badgerDB, err := badger.Open(badger.DefaultOptions(badgerPath).
		WithTruncate(!s.config.BadgerNoTruncate).
		WithSyncWrites(false).
		WithCompactL0OnClose(false).
		WithCompression(options.ZSTD).
		WithLogger(logger.WithField("badger", name)))

	if err != nil {
		return nil, err
	}

	d := db{
		name:    name,
		DB:      badgerDB,
		logger:  s.logger.WithField("db", name),
		gcCount: s.metrics.gcCount.WithLabelValues(name),
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

	s.maintenanceTask(s.badgerGCTaskInterval, func() {
		diff := calculateDBSize(badgerPath) - d.lastGC
		if d.lastGC == 0 || s.gcSizeDiff == 0 || diff > s.gcSizeDiff {
			d.runGC(0.7)
			d.gcCount.Inc()
			d.lastGC = calculateDBSize(badgerPath)
		}
	})

	return &d, nil
}

func (d *db) close() {
	if d.Cache != nil {
		d.Cache.Flush()
	}
	if err := d.DB.Close(); err != nil {
		d.logger.WithError(err).Error("closing database")
	}
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
			return reclaimed
		case nil:
			reclaimed = true
			continue
		}
	}
}

// TODO(kolesnikovae): filepath.Walk is notoriously slow.
//  Consider use of https://github.com/karrick/godirwalk.
//  Although, every badger.DB calculates its size (reported
//  via Size) in the same way every minute.
func calculateDBSize(path string) bytesize.ByteSize {
	var size int64
	_ = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		switch filepath.Ext(path) {
		case ".sst", ".vlog":
			size += info.Size()
		}
		return nil
	})
	return bytesize.ByteSize(size)
}
