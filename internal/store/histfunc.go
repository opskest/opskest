package store

import (
	"database/sql/driver"

	"modernc.org/sqlite"

	"opskest/internal/latency"
)

// opskest_hist_merge(a, b) merges two encoded latency histograms, so the
// rollup upsert can accumulate lat_hist in SQL exactly like it does
// lat_sum — the single writer stays the only write path. Registered in
// init so it exists on every connection the driver opens, before Open
// runs any statement.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("opskest_hist_merge", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			merged := latency.MergeBlobs(blobArg(args[0]), blobArg(args[1]))
			if merged == nil {
				return nil, nil
			}
			return merged, nil
		})
}

func blobArg(v driver.Value) []byte {
	b, _ := v.([]byte)
	return b
}
