// Package collect gathers what the risk register's sources know into a snapshot. Each
// collector reads one source. Collectors run one after another and add to the same snapshot.
package collect

import (
	"context"
	"time"

	"github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/riskregister"
	"github.com/obot-platform/obot/pkg/safehttp"
)

const (
	// httpTimeout bounds one request to an outside source.
	httpTimeout = 30 * time.Second
	// maxPages bounds how far a collector pages through an outside source in one run.
	maxPages = 50
)

var (
	// outsidePolicy is the egress policy for sources an administrator configures. They are
	// public services, so private, loopback and link-local addresses are refused.
	outsidePolicy = safehttp.Options{BlockLoopback: true, BlockLinkLocal: true, BlockPrivateIP: true, Timeout: httpTimeout}
)

// Collector reads one source and adds what it finds to the snapshot. It returns counts of
// what it read, which are shown beside the source so an empty result is distinguishable
// from a source that found nothing worth an entry.
type Collector interface {
	Kind() types.RiskSourceKind
	Collect(ctx context.Context, snapshot *riskregister.Snapshot) (map[string]int, error)
}

// windowStart is the beginning of a snapshot's activity window.
func windowStart(snapshot *riskregister.Snapshot) time.Time {
	return snapshot.CollectedAt.AddDate(0, 0, -snapshot.WindowDays)
}
