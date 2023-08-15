package state

import (
	"math/big"
	"sort"
	"strings"
	"sync"
)

// MVMemory implements the Block-STM multi-version memory storing state updates
type MVMemory struct {
	storedVers *storedVers
	lastWrites *lastWrites
	lastReads  *lastReads
}

const (
	READ_ERROR = iota
	NOT_FOUND
	READ_OK
)

type storedVers struct {
	dataMap map[string]map[int]*operation
	mapLock sync.RWMutex
}

type lastWrites struct {
	dataMap map[int][]string
	mapLock sync.RWMutex
}

type lastReads struct {
	dataMap map[int][]*readPair
	mapLock sync.RWMutex
}

type operation struct {
	incarnation int
	value       *big.Int
	estimate    bool
}

type readPair struct {
	location string
	ver      *Version
}

type Version struct {
	TxIndex     int
	Incarnation int
}

func (ver *Version) Compare(comparedVer *Version) bool {
	if ver.TxIndex == comparedVer.TxIndex && ver.Incarnation == comparedVer.Incarnation {
		return true
	}
	return false
}

// NewMVMemory initializes a new multi-version memory instance
func NewMVMemory() *MVMemory {
	vers := &storedVers{dataMap: make(map[string]map[int]*operation)}
	writes := &lastWrites{dataMap: make(map[int][]string)}
	reads := &lastReads{dataMap: make(map[int][]*readPair)}
	return &MVMemory{
		storedVers: vers,
		lastWrites: writes,
		lastReads:  reads,
	}
}

// Reset resets the multi-version memory at each block height
func (memory *MVMemory) Reset() {
	memory.storedVers.dataMap = make(map[string]map[int]*operation)
	memory.lastWrites.dataMap = make(map[int][]string)
	memory.lastReads.dataMap = make(map[int][]*readPair)
}

// Record updates the read and write sets of a new incarnation of tx
func (memory *MVMemory) Record(ver *Version, reads []string, writes map[string]*big.Int) bool {
	var newLocations []string
	var readPairs []*readPair
	memory.applyWrites(ver.TxIndex, ver.Incarnation, writes)

	for _, rloc := range reads {
		newReadPair := &readPair{
			location: rloc,
			ver:      ver,
		}
		readPairs = append(readPairs, newReadPair)
	}
	memory.lastReads.mapLock.Lock()
	memory.lastReads.dataMap[ver.TxIndex] = readPairs
	memory.lastReads.mapLock.Unlock()

	for wloc := range writes {
		newLocations = append(newLocations, wloc)
	}
	isWroteNew := memory.updateWrittenLocations(ver.TxIndex, newLocations)

	return isWroteNew
}

// MarkEstimates marks the last written locations 'estimate' after being aborted
func (memory *MVMemory) MarkEstimates(index int) {
	memory.lastWrites.mapLock.RLock()
	prevLocations := memory.lastWrites.dataMap[index]
	memory.lastWrites.mapLock.RUnlock()
	memory.storedVers.mapLock.Lock()
	for _, loc := range prevLocations {
		memory.storedVers.dataMap[loc][index].estimate = true
	}
	memory.storedVers.mapLock.Unlock()
}

// Read performs a read operation at a specific location in memory
func (memory *MVMemory) Read(loc string, index int) (int, *Version, *big.Int) {
	memory.storedVers.mapLock.RLock()
	defer memory.storedVers.mapLock.RUnlock()
	var txIndexes []int
	txsMap, ok := memory.storedVers.dataMap[loc]
	if !ok {
		return NOT_FOUND, nil, nil
	}

	for id := range txsMap {
		if id < index {
			txIndexes = append(txIndexes, id)
		}
	}

	if len(txIndexes) == 0 {
		return NOT_FOUND, nil, nil
	}

	sort.Ints(txIndexes)
	maxIndex := txIndexes[len(txIndexes)-1]
	op := memory.storedVers.dataMap[loc][maxIndex]
	ver := &Version{
		TxIndex:     maxIndex,
		Incarnation: op.incarnation,
	}

	if op.estimate {
		return READ_ERROR, ver, nil
	}

	return READ_OK, ver, op.value
}

// Validate validates the pre-fetched read-set
func (memory *MVMemory) Validate(index int) bool {
	memory.lastReads.mapLock.RLock()
	prevReads := memory.lastReads.dataMap[index]
	memory.lastReads.mapLock.RUnlock()
	for _, read := range prevReads {
		status, ver, _ := memory.Read(read.location, index)
		if status == READ_ERROR || status == NOT_FOUND {
			return false
		} else if status == READ_OK && !ver.Compare(read.ver) {
			return false
		}
	}
	return true
}

// applyWrites updates state version stored in storedVers according to tx write sets
func (memory *MVMemory) applyWrites(index, incarnation int, writes map[string]*big.Int) {
	memory.storedVers.mapLock.Lock()
	defer memory.storedVers.mapLock.Unlock()
	for addr, val := range writes {
		memory.storedVers.dataMap[addr] = make(map[int]*operation)
		op := &operation{
			incarnation: incarnation,
			value:       val,
			estimate:    false,
		}
		memory.storedVers.dataMap[addr][index] = op
	}
}

// updateWrittenLocations updates the last written memory locations if a new incarnation of tx writes to new memory locations
func (memory *MVMemory) updateWrittenLocations(index int, locations []string) bool {
	memory.lastWrites.mapLock.Lock()
	defer memory.lastWrites.mapLock.Unlock()
	prevLocations := memory.lastWrites.dataMap[index]
	isWroteNew := compareLocations(prevLocations, locations)
	if isWroteNew {
		memory.lastWrites.dataMap[index] = locations
	}
	return isWroteNew
}

// compareLocations detects whether a new incarnation of tx writes to new memory locations
func compareLocations(locs1, locs2 []string) bool {
	if len(locs1) != len(locs2) {
		return true
	}

	for _, loc2 := range locs2 {
		doesExist := false
		for _, loc1 := range locs1 {
			if strings.Compare(loc1, loc2) == 0 {
				doesExist = true
				break
			}
		}
		if !doesExist {
			return true
		}
	}

	return false
}
