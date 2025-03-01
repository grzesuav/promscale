// This file and its contents are licensed under the Apache License 2.0.
// Please see the included NOTICE for copyright information and
// LICENSE for a copy of the license.

package ingestor

import (
	"sync"

	"github.com/timescale/promscale/pkg/pgmodel/common/schema"
	"github.com/timescale/promscale/pkg/pgmodel/model"
)

const (
	// maximum number of insertDataRequests that should be buffered before the
	// insertHandler flushes to the next layer. We don't want too many as this
	// increases the number of lost writes if the connector dies. This number
	// was chosen arbitrarily.
	flushSize                = 2000
	getCreateMetricsTableSQL = "SELECT table_name FROM " + schema.Catalog + ".get_or_create_metric_table_name($1)"
	finalizeMetricCreation   = "CALL " + schema.Catalog + ".finalize_metric_creation()"
	getEpochSQL              = "SELECT current_epoch FROM " + schema.Catalog + ".ids_epoch LIMIT 1"
	maxCopyRequestsPerTxn    = 100
)

type pendingBuffer struct {
	needsResponse []insertDataTask
	batch         model.SamplesBatch
}

var pendingBuffers = sync.Pool{
	New: func() interface{} {
		pb := new(pendingBuffer)
		pb.needsResponse = make([]insertDataTask, 0)
		pb.batch = model.NewSamplesBatch()
		return pb
	},
}

func NewPendingBuffer() *pendingBuffer {
	return pendingBuffers.Get().(*pendingBuffer)
}

func (p *pendingBuffer) IsFull() bool {
	return p.batch.CountSeries() > flushSize
}

func (p *pendingBuffer) IsEmpty() bool {
	return p.batch.CountSeries() == 0
}

// Report completion of an insert batch to all goroutines that may be waiting
// on it, along with any error that may have occurred.
// This function also resets the pending in preperation for the next batch.
func (p *pendingBuffer) reportResults(err error) {
	for i := 0; i < len(p.needsResponse); i++ {
		p.needsResponse[i].reportResult(err)
	}
}

func (p *pendingBuffer) release() {
	for i := 0; i < len(p.needsResponse); i++ {
		p.needsResponse[i] = insertDataTask{}
	}
	p.needsResponse = p.needsResponse[:0]
	p.batch.Reset()
	pendingBuffers.Put(p)
}

func (p *pendingBuffer) addReq(req *insertDataRequest) {
	p.needsResponse = append(p.needsResponse, insertDataTask{finished: req.finished, errChan: req.errChan})
	p.batch.AppendSlice(req.data)
}

func (p *pendingBuffer) absorb(other *pendingBuffer) {
	p.needsResponse = append(p.needsResponse, other.needsResponse...)
	p.batch.Absorb(other.batch)
}
