package client

import (
	"errors"
	"fmt"

	"github.com/coreos/bbolt"
)

var (
	// metaBucket stores all the meta information concerning the state of
	// the database.
	metaBucket = []byte("metadata")

	// dbVersionKey is a boltdb key and it's used for storing/retrieving
	// current database version.
	dbVersionKey = []byte("dbp")

	// ErrDBReversion is returned when detecting an attempt to revert to a
	// prior database version.
	ErrDBReversion = fmt.Errorf("channel db cannot revert to prior version")
)

// migration is a function which takes a prior outdated version of the database
// instances and mutates the key/bucket structure to arrive at a more
// up-to-date version of the database.
type migration func(tx *bbolt.Tx) error

var (
	// dbVersions is storing all versions of database. If current version
	// of database don't match with latest version this list will be used
	// for retrieving all migration function that are need to apply to the
	// current db.
	migrations = []migration{}

	latestDBVersion = uint32(len(migrations))
)

// getDBVersion retrieves the current db version.
func getDBVersion(db *bbolt.DB) (uint32, error) {
	var version uint32

	err := db.View(func(tx *bbolt.Tx) error {
		metaBucket := tx.Bucket(metaBucket)
		if metaBucket == nil {
			return errors.New("bucket does not exist")
		}

		data := metaBucket.Get(dbVersionKey)
		// If no version key found, assume version is 0.
		if data != nil {
			version = byteOrder.Uint32(data)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return version, nil
}

// getDBVersion updates the current db version.
func setDBVersion(tx *bbolt.Tx, version uint32) error {
	metaBucket := tx.Bucket(metaBucket)
	if metaBucket == nil {
		return errors.New("bucket does not exist")
	}

	scratch := make([]byte, 4)
	byteOrder.PutUint32(scratch, version)
	return metaBucket.Put(dbVersionKey, scratch)
}

// syncVersions function is used for safe db version synchronization. It
// applies migration functions to the current database and recovers the
// previous state of db if at least one error/panic appeared during migration.
func syncVersions(db *bbolt.DB) error {
	currentVersion, err := getDBVersion(db)
	if err != nil {
		return err
	}

	logger.Infof("Checking for schema update: latest_version=%v, "+
		"db_version=%v", latestDBVersion, currentVersion)

	switch {

	// If the database reports a higher version that we are aware of, the
	// user is probably trying to revert to a prior version of lnd. We fail
	// here to prevent reversions and unintended corruption.
	case currentVersion > latestDBVersion:
		logger.Errorf("Refusing to revert from db_version=%d to "+
			"lower version=%d", currentVersion,
			latestDBVersion)

		return ErrDBReversion

	// If the current database version matches the latest version number,
	// then we don't need to perform any migrations.
	case currentVersion == latestDBVersion:
		return nil
	}

	logger.Infof("Performing database schema migration")

	// Otherwise we execute the migrations serially within a single database
	// transaction to ensure the migration is atomic.
	return db.Update(func(tx *bbolt.Tx) error {
		for v := currentVersion; v < latestDBVersion; v++ {
			logger.Infof("Applying migration #%v", v+1)
			migration := migrations[v]
			if err := migration(tx); err != nil {
				logger.Infof("Unable to apply migration #%v",
					v+1)
				return err
			}
		}

		return setDBVersion(tx, latestDBVersion)
	})
}