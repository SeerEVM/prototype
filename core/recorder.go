package core

import (
	"github.com/ethereum/go-ethereum/common"
	"math"
	"seerEVM/core/types"
	"seerEVM/core/vm"
)

// Recorder defines a speedup recorder and calculator (for evaluation)
type Recorder struct {
	serialLatency map[common.Hash]int64
	seerLatency   map[common.Hash]int64
	speedup       map[int]int
	validTxNum    int
}

func NewRecorder() *Recorder {
	return &Recorder{
		serialLatency: make(map[common.Hash]int64),
		seerLatency:   make(map[common.Hash]int64),
		speedup:       make(map[int]int),
		validTxNum:    0,
	}
}

func (r *Recorder) SerialRecord(tx *types.Transaction, stateDB vm.StateDB, latency int64) {
	if tx.To() != nil && IsContract(stateDB, tx.To()) {
		r.serialLatency[tx.Hash()] = latency
	}
}

func (r *Recorder) SeerRecord(tx *types.Transaction, latency int64) {
	r.seerLatency[tx.Hash()] = latency
}

func (r *Recorder) SpeedupCalculation() map[int]int {
	for tx, lat := range r.seerLatency {
		serialLat := r.serialLatency[tx]
		// 排除因执行失败导致时延为0的交易
		if lat != 0 && serialLat != 0 {
			r.validTxNum++
			speedup := float64(serialLat) / float64(lat)
			if speedup < 1 {
				r.speedup[0]++
			} else {
				speedupInt := int(math.Floor(speedup + 0.5))
				r.speedup[speedupInt]++
			}
		}
	}
	return r.speedup
}

func (r *Recorder) GetValidTxNum() int { return r.validTxNum }
