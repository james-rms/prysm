package kv

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	log "github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"go.opencensus.io/trace"
)

const (
	// The size of each data entry in bytes for the source epoch (8 bytes) and signing root (32 bytes).
	uint64Size             = 8
	latestEpochWrittenSize = uint64Size
	targetSize             = uint64Size
	sourceSize             = uint64Size
	signingRootSize        = 32
	historySize            = targetSize + sourceSize + signingRootSize
	minimalSize            = latestEpochWrittenSize
)

// HistoryData stores the needed data to confirm if an attestation is slashable
// or repeated.
type HistoryData struct {
	Source      uint64
	SigningRoot []byte
}

// EncHistoryData encapsulated history data.
type EncHistoryData []byte

func (hd EncHistoryData) assertSize() error {
	if hd == nil || len(hd) < minimalSize {
		return fmt.Errorf("encapsulated data size: %d is smaller then minimal size: %d", len(hd), minimalSize)
	}
	if (len(hd)-minimalSize)%historySize != 0 {
		return fmt.Errorf("encapsulated data size: %d is not a multiple of entry size: %d", len(hd), historySize)
	}
	return nil
}

func (h *HistoryData) IsEmpty() bool {
	if h == (*HistoryData)(nil) {
		return true
	}
	if h.Source == params.BeaconConfig().FarFutureEpoch {
		return true
	}
	return false
}

func emptyHistoryData() *HistoryData {
	h := &HistoryData{Source: params.BeaconConfig().FarFutureEpoch, SigningRoot: bytesutil.PadTo([]byte{}, 32)}
	return h
}

// NewAttestationHistoryArray creates a new encapsulated attestation history byte array
// sized by the latest epoch written.
func NewAttestationHistoryArray(target uint64) EncHistoryData {
	relativeTarget := target % params.BeaconConfig().WeakSubjectivityPeriod
	historyDataSize := (relativeTarget + 1) * historySize
	arraySize := latestEpochWrittenSize + historyDataSize
	en := make(EncHistoryData, arraySize)
	enc := en
	ctx := context.Background()
	var err error
	for i := uint64(0); i <= target%params.BeaconConfig().WeakSubjectivityPeriod; i++ {
		enc, err = enc.SetTargetData(ctx, i, emptyHistoryData())
		if err != nil {
			log.WithError(err).Error("Failed to set empty target data")
		}
	}
	return enc
}

func (hd EncHistoryData) GetLatestEpochWritten(ctx context.Context) (uint64, error) {
	if err := hd.assertSize(); err != nil {
		return 0, err
	}
	return bytesutil.FromBytes8(hd[:latestEpochWrittenSize]), nil
}

func (hd EncHistoryData) SetLatestEpochWritten(ctx context.Context, latestEpochWritten uint64) (EncHistoryData, error) {
	if err := hd.assertSize(); err != nil {
		return nil, err
	}
	copy(hd[:latestEpochWrittenSize], bytesutil.Uint64ToBytesLittleEndian(latestEpochWritten))
	return hd, nil
}

func (hd EncHistoryData) GetTargetData(ctx context.Context, target uint64) (*HistoryData, error) {
	if err := hd.assertSize(); err != nil {
		return nil, err
	}
	// Cursor for the location to read target epoch from.
	// Modulus of target epoch  X weak subjectivity period in order to have maximum size to the encapsulated data array.
	cursor := (target%params.BeaconConfig().WeakSubjectivityPeriod)*historySize + latestEpochWrittenSize
	if uint64(len(hd)) < cursor+historySize {
		return nil, nil
	}
	history := &HistoryData{}
	history.Source = bytesutil.FromBytes8(hd[cursor : cursor+sourceSize])
	sr := make([]byte, 32)
	copy(sr, hd[cursor+sourceSize:cursor+historySize])
	history.SigningRoot = sr
	return history, nil
}

func (hd EncHistoryData) SetTargetData(ctx context.Context, target uint64, historyData *HistoryData) (EncHistoryData, error) {
	if err := hd.assertSize(); err != nil {
		return nil, err
	}
	// Cursor for the location to write target epoch to.
	// Modulus of target epoch  X weak subjectivity period in order to have maximum size to the encapsulated data array.
	cursor := latestEpochWrittenSize + (target%params.BeaconConfig().WeakSubjectivityPeriod)*historySize

	if uint64(len(hd)) < cursor+historySize {
		ext := make([]byte, cursor+historySize-uint64(len(hd)))
		hd = append(hd, ext...)
	}
	copy(hd[cursor:cursor+sourceSize], bytesutil.Uint64ToBytesLittleEndian(historyData.Source))
	copy(hd[cursor+sourceSize:cursor+sourceSize+signingRootSize], historyData.SigningRoot)
	return hd, nil
}

// AttestationHistoryForPubKeysV2 accepts an array of validator public keys and returns a mapping of corresponding attestation history.
func (store *Store) AttestationHistoryForPubKeysV2(ctx context.Context, publicKeys [][48]byte) (map[[48]byte]EncHistoryData, error) {
	ctx, span := trace.StartSpan(ctx, "Validator.AttestationHistoryForPubKeysV2")
	defer span.End()

	if len(publicKeys) == 0 {
		return make(map[[48]byte]EncHistoryData), nil
	}

	var err error
	attestationHistoryForVals := make(map[[48]byte]EncHistoryData)
	err = store.view(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(newHistoricAttestationsBucket)
		for _, key := range publicKeys {
			enc := bucket.Get(key[:])
			var attestationHistory EncHistoryData
			if len(enc) == 0 {
				attestationHistory = NewAttestationHistoryArray(0)
			} else {
				attestationHistory = enc
			}
			attestationHistoryForVals[key] = attestationHistory
		}
		return nil
	})
	for pk, ah := range attestationHistoryForVals {
		ehd := make(EncHistoryData, len(ah))
		copy(ehd, ah)
		attestationHistoryForVals[pk] = ehd
	}
	return attestationHistoryForVals, err
}

// SaveAttestationHistoryForPubKeysV2 saves the attestation histories for the requested validator public keys.
func (store *Store) SaveAttestationHistoryForPubKeysV2(ctx context.Context, historyByPubKeys map[[48]byte]EncHistoryData) error {
	ctx, span := trace.StartSpan(ctx, "Validator.SaveAttestationHistoryForPubKeysV2")
	defer span.End()

	err := store.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(newHistoricAttestationsBucket)
		for pubKey, encodedHistory := range historyByPubKeys {
			if err := bucket.Put(pubKey[:], encodedHistory); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// SaveAttestationHistoryForPubKeyV2 saves the attestation history for the requested validator public key.
func (store *Store) SaveAttestationHistoryForPubKeyV2(ctx context.Context, pubKey [48]byte, history EncHistoryData) error {
	ctx, span := trace.StartSpan(ctx, "Validator.SaveAttestationHistoryForPubKeyV2")
	defer span.End()
	err := store.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(newHistoricAttestationsBucket)
		return bucket.Put(pubKey[:], history)
	})
	return err
}

// MigrateV2AttestationProtection import old attestation format data into the new attestation format
func (store *Store) MigrateV2AttestationProtection(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "Validator.MigrateV2AttestationProtection")
	defer span.End()
	var allKeys [][48]byte

	if err := store.db.View(func(tx *bolt.Tx) error {
		attestationsBucket := tx.Bucket(historicAttestationsBucket)
		if err := attestationsBucket.ForEach(func(pubKey, _ []byte) error {
			var pubKeyCopy [48]byte
			copy(pubKeyCopy[:], pubKey)
			allKeys = append(allKeys, pubKeyCopy)
			return nil
		}); err != nil {
			return errors.Wrapf(err, "could not retrieve attestations for source in %s", store.databasePath)
		}

		return nil
	}); err != nil {
		return err
	}
	allKeys = removeDuplicateKeys(allKeys)
	attMap, err := store.AttestationHistoryForPubKeys(ctx, allKeys)
	if err != nil {
		return errors.Wrapf(err, "could not retrieve data for public keys %v", allKeys)
	}
	dataMap := make(map[[48]byte]EncHistoryData)
	for key, atts := range attMap {
		dataMap[key] = NewAttestationHistoryArray(atts.LatestEpochWritten)
		dataMap[key], err = dataMap[key].SetLatestEpochWritten(ctx, atts.LatestEpochWritten)
		if err != nil {
			return errors.Wrapf(err, "failed to set latest epoch while migrating attestations to v2")
		}
		for target, source := range atts.TargetToSource {
			dataMap[key], err = dataMap[key].SetTargetData(ctx, target, &HistoryData{
				Source:      source,
				SigningRoot: []byte{1},
			})
			if err != nil {
				return errors.Wrapf(err, "failed to set target data while migrating attestations to v2")
			}
		}
	}
	err = store.SaveAttestationHistoryForPubKeysV2(ctx, dataMap)
	return err
}

// MigrateV2AttestationProtectionDb exports old attestation protection data
// format to the new format and save the exported flag to database.
func (store *Store) MigrateV2AttestationProtectionDb(ctx context.Context) error {
	ctx, span := trace.StartSpan(ctx, "Validator.MigrateV2AttestationProtectionDb")
	defer span.End()
	importAttestations, err := store.shouldMigrateAttestations()
	if err != nil {
		return errors.Wrap(err, "failed to analyze whether attestations should be imported")
	}
	if !importAttestations {
		return nil
	}
	log.Info("Starting proposals protection db migration to v2...")
	err = store.MigrateV2AttestationProtection(ctx)
	if err != nil {
		return errors.Wrap(err, "filed to import attestations")
	}
	err = store.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(historicAttestationsBucket)
		if bucket != nil {
			if err := bucket.Put([]byte(attestationExported), []byte{1}); err != nil {
				return errors.Wrap(err, "failed to set migrated attestations flag in db")
			}
		}
		return nil
	})
	log.Info("Finished proposals protection db migration to v2")
	return err
}

func (store *Store) shouldMigrateAttestations() (bool, error) {
	var importAttestations bool
	err := store.db.View(func(tx *bolt.Tx) error {
		attestationBucket := tx.Bucket(historicAttestationsBucket)
		if attestationBucket != nil && attestationBucket.Stats().KeyN != 0 {
			if exported := attestationBucket.Get([]byte(attestationExported)); exported == nil {
				importAttestations = true
			}
		}
		return nil
	})
	return importAttestations, err
}
