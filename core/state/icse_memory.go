package state

import (
	"math/big"
	"sync"
)

const (
	NOT_FOUND = iota
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
