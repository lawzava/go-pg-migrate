package migrate

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// record is one ledger row.
type record struct {
	version   int64
	name      string
	appliedAt time.Time
	dirty     bool
}

// checkHistory rejects ledgers that no plan can safely build on. known must be
// sorted by version.
func checkHistory(known []Migration, history map[int64]record) error {
	var dirty, unknown []int64

	for version, rec := range history {
		if rec.dirty {
			dirty = append(dirty, version)
		}

		if !isKnown(known, version) {
			unknown = append(unknown, version)
		}
	}

	if len(dirty) > 0 {
		slices.Sort(dirty)

		return fmt.Errorf("%w: versions %v", ErrDirty, dirty)
	}

	if len(unknown) > 0 {
		slices.Sort(unknown)

		return fmt.Errorf("%w: versions %v", ErrUnknownVersion, unknown)
	}

	return nil
}

// planUp returns the migrations to apply, oldest first, to reach target.
// It never plans a rollback: versions above target are left alone.
func planUp(known []Migration, history map[int64]record, target int64, allowOutOfOrder bool) ([]Migration, error) {
	if err := checkHistory(known, history); err != nil {
		return nil, err
	}

	var newest int64
	for version := range history {
		newest = max(newest, version)
	}

	var (
		plan       []Migration
		outOfOrder []int64
	)

	for _, mig := range known {
		if mig.Version > target {
			break
		}

		if _, ok := history[mig.Version]; ok {
			continue
		}

		if mig.Version < newest {
			outOfOrder = append(outOfOrder, mig.Version)
		}

		plan = append(plan, mig)
	}

	if len(outOfOrder) > 0 && !allowOutOfOrder {
		return nil, fmt.Errorf("%w: versions %v are pending below applied version %d", ErrOutOfOrder, outOfOrder, newest)
	}

	return plan, nil
}

// planDown returns the migrations to revert, newest first, to reach target.
// Zero reverts everything. It checks the whole path before returning, so a
// rollback never stops halfway at an irreversible migration.
func planDown(known []Migration, history map[int64]record, target int64) ([]Migration, error) {
	if target != 0 && !isKnown(known, target) {
		return nil, fmt.Errorf("%w: %d", ErrUnknownTarget, target)
	}

	if err := checkHistory(known, history); err != nil {
		return nil, err
	}

	var (
		plan         []Migration
		irreversible []int64
	)

	for _, mig := range slices.Backward(known) {
		if mig.Version <= target {
			break
		}

		if _, ok := history[mig.Version]; !ok {
			continue
		}

		if mig.Down == nil {
			irreversible = append(irreversible, mig.Version)
		}

		plan = append(plan, mig)
	}

	if len(irreversible) > 0 {
		return nil, fmt.Errorf("%w: versions %v", ErrIrreversible, irreversible)
	}

	return plan, nil
}

func isKnown(known []Migration, version int64) bool {
	_, ok := slices.BinarySearchFunc(known, version, func(m Migration, v int64) int {
		return cmp.Compare(m.Version, v)
	})

	return ok
}

// State classifies a version in Migrator.Status.
type State string

const (
	// StateApplied marks a version recorded in the ledger.
	StateApplied State = "applied"
	// StatePending marks a version not yet applied.
	StatePending State = "pending"
	// StateDirty marks a NoTx migration that started but did not finish.
	StateDirty State = "dirty"
	// StateUnknown marks an applied version with no migration in code.
	StateUnknown State = "unknown"
)

// Entry describes one version in Migrator.Status.
type Entry struct {
	Version    int64     `json:"version"`
	Name       string    `json:"name"`
	State      State     `json:"state"`
	AppliedAt  time.Time `json:"applied_at,omitzero"`
	Reversible bool      `json:"reversible"`
}

func buildStatus(known []Migration, history map[int64]record) []Entry {
	entries := make([]Entry, 0, len(known)+len(history))

	for _, mig := range known {
		rec, applied := history[mig.Version]

		state := StatePending
		if applied {
			state = StateApplied
		}

		if rec.dirty {
			state = StateDirty
		}

		entries = append(entries, Entry{
			Version:    mig.Version,
			Name:       mig.Name,
			State:      state,
			AppliedAt:  rec.appliedAt,
			Reversible: mig.Down != nil,
		})
	}

	for version, rec := range history {
		if !isKnown(known, version) {
			entries = append(entries, Entry{
				Version:    version,
				Name:       rec.name,
				State:      StateUnknown,
				AppliedAt:  rec.appliedAt,
				Reversible: false,
			})
		}
	}

	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Version, b.Version) })

	return entries
}
