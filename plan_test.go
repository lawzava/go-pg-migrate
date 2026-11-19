package migrate

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func noop() Func { return SQL("SELECT 1") }

func migs(versions ...int64) []Migration {
	out := make([]Migration, 0, len(versions))
	for _, v := range versions {
		out = append(out, Migration{Version: v, Name: "m", Up: noop(), Down: noop()})
	}

	return out
}

func applied(versions ...int64) map[int64]record {
	out := make(map[int64]record, len(versions))
	for _, v := range versions {
		out[v] = record{version: v}
	}

	return out
}

func versionsOf(ms []Migration) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Version)
	}

	return out
}

func TestPlanUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		known      []Migration
		applied    map[int64]record
		target     int64
		outOfOrder bool
		unknown    bool
		want       []int64
		wantPassed []int64
		wantErr    error
	}{
		{name: "fresh database applies all", known: migs(1, 2, 3), target: 3, want: []int64{1, 2, 3}},
		{name: "partial history applies rest", known: migs(1, 2, 3), applied: applied(1), target: 3, want: []int64{2, 3}},
		{name: "stops at target", known: migs(1, 2, 3), target: 2, want: []int64{1, 2}},
		{name: "target below current is a no-op", known: migs(1, 2, 3), applied: applied(1, 2, 3), target: 1, want: []int64{}},
		{name: "no migrations", known: nil, target: 0, want: []int64{}},
		{name: "timestamp versions", known: migs(20261003120000, 20261004120000), target: 20261004120000, want: []int64{20261003120000, 20261004120000}},
		{name: "unknown applied version", known: migs(1, 2), applied: applied(1, 2, 5), target: 2, wantErr: ErrUnknownVersion},
		{name: "gap below newest applied", known: migs(1, 2, 3), applied: applied(1, 3), target: 3, wantErr: ErrOutOfOrder},
		{name: "gap allowed", known: migs(1, 2, 3), applied: applied(1, 3), target: 3, outOfOrder: true, want: []int64{2}},
		{name: "dirty blocks", known: migs(1, 2), applied: map[int64]record{1: {version: 1, dirty: true}}, target: 2, wantErr: ErrDirty},
		{name: "unknown allowed applies pending", known: migs(1, 3, 4), applied: applied(1, 2), target: 4, unknown: true, want: []int64{3, 4}, wantPassed: []int64{2}},
		{name: "unknown allowed with nothing pending", known: migs(1, 2), applied: applied(1, 2, 7, 5), target: 2, unknown: true, want: []int64{}, wantPassed: []int64{5, 7}},
		{name: "unknown allowed without unknown versions", known: migs(1, 2), applied: applied(1), target: 2, unknown: true, want: []int64{2}},
		{name: "unknown allowed still counts for order", known: migs(1, 3), applied: applied(1, 5), target: 3, unknown: true, wantErr: ErrOutOfOrder},
		{name: "out of order allowed still rejects unknown", known: migs(1, 3), applied: applied(1, 5), target: 3, outOfOrder: true, wantErr: ErrUnknownVersion},
		{name: "unknown and out of order allowed", known: migs(1, 3), applied: applied(1, 5), target: 3, outOfOrder: true, unknown: true, want: []int64{3}, wantPassed: []int64{5}},
		{name: "dirty unknown blocks although allowed", known: migs(1, 2), applied: map[int64]record{1: {version: 1}, 9: {version: 9, dirty: true}}, target: 2, unknown: true, wantErr: ErrDirty},
		{name: "dirty wins over unknown", known: migs(1, 2), applied: map[int64]record{1: {version: 1, dirty: true}, 9: {version: 9}}, target: 2, wantErr: ErrDirty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := planOptions{allowOutOfOrder: tt.outOfOrder, allowUnknownVersions: tt.unknown}

			got, passed, err := planUp(tt.known, tt.applied, tt.target, opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				return
			}

			if !slices.Equal(versionsOf(got), tt.want) {
				t.Fatalf("plan = %v, want %v", versionsOf(got), tt.want)
			}

			if !slices.Equal(passed, tt.wantPassed) {
				t.Fatalf("unknown versions passed = %v, want %v", passed, tt.wantPassed)
			}
		})
	}
}

func TestPlanDown(t *testing.T) {
	t.Parallel()

	irreversible := migs(1, 2, 3)
	irreversible[1].Down = nil

	tests := []struct {
		name    string
		known   []Migration
		applied map[int64]record
		target  int64
		unknown bool
		want    []int64
		// wantPassed lists the unknown versions the plan left alone.
		wantPassed []int64
		wantErr    error
		// wantText must be part of the error.
		wantText string
	}{
		{name: "to zero reverts all newest first", known: migs(1, 2, 3), applied: applied(1, 2, 3), target: 0, want: []int64{3, 2, 1}},
		{name: "to known version", known: migs(1, 2, 3), applied: applied(1, 2, 3), target: 1, want: []int64{3, 2}},
		{name: "reverts only applied versions", known: migs(1, 2, 3), applied: applied(1, 3), target: 1, want: []int64{3}},
		{name: "target at current is a no-op", known: migs(1, 2), applied: applied(1, 2), target: 2, want: []int64{}},
		{name: "unknown target", known: migs(10, 20), applied: applied(10, 20), target: 15, wantErr: ErrUnknownTarget},
		{name: "unknown applied version", known: migs(1, 2, 3), applied: applied(1, 2, 3, 4), target: 2, wantErr: ErrUnknownVersion},
		{name: "irreversible on path", known: irreversible, applied: applied(1, 2, 3), target: 0, wantErr: ErrIrreversible},
		{name: "irreversible below target is fine", known: irreversible, applied: applied(1, 2, 3), target: 2, want: []int64{3}},
		{name: "dirty blocks", known: migs(1, 2), applied: map[int64]record{2: {version: 2, dirty: true}}, target: 0, wantErr: ErrDirty},
		{name: "unknown below target fails by default", known: migs(1, 3, 4), applied: applied(1, 2, 3, 4), target: 3, wantErr: ErrUnknownVersion, wantText: "versions [2]"},
		{name: "unknown allowed below target", known: migs(1, 3, 4), applied: applied(1, 2, 3, 4), target: 3, unknown: true, want: []int64{4}, wantPassed: []int64{2}},
		{name: "unknown allowed below target with nothing to revert", known: migs(1, 3), applied: applied(1, 2, 3), target: 3, unknown: true, want: []int64{}, wantPassed: []int64{2}},
		{name: "unknown allowed above target", known: migs(1, 2, 3), applied: applied(1, 2, 3, 5), target: 2, unknown: true, wantErr: ErrUnknownVersion, wantText: "versions [5] are above target 2 and cannot be reverted"},
		{name: "unknown allowed on both sides of target", known: migs(1, 3, 5), applied: applied(1, 2, 3, 4, 5, 6), target: 3, unknown: true, wantErr: ErrUnknownVersion, wantText: "versions [4 6] are above target 3 and cannot be reverted"},
		{name: "unknown allowed above target zero", known: migs(1, 3), applied: applied(1, 2, 3), target: 0, unknown: true, wantErr: ErrUnknownVersion, wantText: "versions [2] are above target 0 and cannot be reverted"},
		{name: "dirty unknown blocks although allowed", known: migs(1, 2), applied: map[int64]record{1: {version: 1, dirty: true}, 2: {version: 2}}, target: 2, unknown: true, wantErr: ErrDirty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, passed, err := planDown(tt.known, tt.applied, tt.target, planOptions{allowUnknownVersions: tt.unknown})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				if !strings.Contains(err.Error(), tt.wantText) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantText)
				}

				return
			}

			if !slices.Equal(versionsOf(got), tt.want) {
				t.Fatalf("plan = %v, want %v", versionsOf(got), tt.want)
			}

			if !slices.Equal(passed, tt.wantPassed) {
				t.Fatalf("unknown versions passed = %v, want %v", passed, tt.wantPassed)
			}
		})
	}
}

func TestBuildStatus(t *testing.T) {
	t.Parallel()

	known := migs(1, 2, 3)
	known[2].Down = nil
	history := map[int64]record{
		1: {version: 1, name: "m"},
		2: {version: 2, name: "m", dirty: true},
		9: {version: 9, name: "gone"},
	}

	got := buildStatus(known, history)
	want := []Entry{
		{Version: 1, Name: "m", State: StateApplied, Reversible: true},
		{Version: 2, Name: "m", State: StateDirty, Reversible: true},
		{Version: 3, Name: "m", State: StatePending},
		{Version: 9, Name: "gone", State: StateUnknown},
	}

	if !slices.Equal(got, want) {
		t.Fatalf("status =\n%+v\nwant\n%+v", got, want)
	}
}
