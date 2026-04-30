package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type QueueDepthFunc func() (int64, error)

type Collector struct {
	total      atomic.Int64
	success    atomic.Int64
	failure    atomic.Int64
	retry      atomic.Int64
	queueDepth QueueDepthFunc
}

func NewCollector(queueDepth QueueDepthFunc) *Collector {
	return &Collector{queueDepth: queueDepth}
}

func (c *Collector) IncTotalNotifications() { c.total.Add(1) }
func (c *Collector) IncSuccess()            { c.success.Add(1) }
func (c *Collector) IncFailure()            { c.failure.Add(1) }
func (c *Collector) IncRetry()              { c.retry.Add(1) }

func (c *Collector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		depth := int64(0)
		if c.queueDepth != nil {
			if value, err := c.queueDepth(); err == nil {
				depth = value
			}
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w,
			"# TYPE total_notifications counter\ntotal_notifications %d\n# TYPE success_count counter\nsuccess_count %d\n# TYPE failure_count counter\nfailure_count %d\n# TYPE retry_count counter\nretry_count %d\n# TYPE queue_depth gauge\nqueue_depth %d\n",
			c.total.Load(),
			c.success.Load(),
			c.failure.Load(),
			c.retry.Load(),
			depth,
		)
	}
}
