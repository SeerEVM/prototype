package vm

import (
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
)

const historyLen = 50
const round = 20
const threshold = 0.2

// VarTable records the information of branch relevant to state variables
type VarTable struct {
	Space map[common.Address]*SubTable
	Epoch int
}

func CreateNewTable() *VarTable {
	return &VarTable{
		Space: make(map[common.Address]*SubTable),
		Epoch: 0,
	}
}

func (vt *VarTable) GetSubTable(address common.Address) (*SubTable, error) {
	subTable, ok := vt.Space[address]
	if !ok {
		return nil, errors.New("cannot find contract name")
	}
	return subTable, nil
}

func (vt *VarTable) InsertSubTable(address common.Address) *SubTable {
	subTable := createNewSubTable()
	vt.Space[address] = subTable
	return subTable
}

// VarExist checks whether a state variable is relevant to branches
func (vt *VarTable) VarExist(address common.Address, slot common.Hash, offset uint256.Int) bool {
	subTable, err := vt.GetSubTable(address)
	if err != nil {
		return false
	}
	entry, err2 := subTable.GetEntry(slot)
	if err2 != nil {
		return false
	}
	_, ok := entry.space[offset.String()]
	if !ok {
		return false
	}
	return true
}

// AddHistory adds the latest actual branch direction into the branch history list
func (vt *VarTable) AddHistory(branch *BranchContext) error {
	contractAddr := branch.GetAddr()
	sub, err := vt.GetSubTable(contractAddr)
	if err != nil {
		return err
	}
	slot := branch.GetStateUnit().GetSlot()
	entry, err2 := sub.GetEntry(slot.Bytes32())
	if err2 != nil {
		return err2
	}
	offset := branch.GetStateUnit().GetOffset()
	branchID := branch.GetBranchID()
	direction := branch.GetBranchDirection()
	entry.addHistory(offset, branchID, direction)
	return nil
}

func (vt *VarTable) GetEpoch() int { return vt.Epoch }

// Sweep sweeps the storage space every $round$ epoch
func (vt *VarTable) Sweep() {
	vt.Epoch++
	if vt.Epoch%round == 0 {
		go func() {
			for cAddr, subTable := range vt.Space {
				if len(subTable.Space) == 0 {
					delete(vt.Space, cAddr)
				}
				for slot, entry := range subTable.Space {
					if len(entry.space) == 0 {
						delete(subTable.Space, slot)
					}
					go entry.sweeper.sweep(vt.Epoch, entry.space)
				}
			}
		}()
	}
}

// SubTable defines a sub-table to manage the storage of all slots relevant to branches under a contract address
type SubTable struct {
	Space map[common.Hash]*Entry
}

func (st *SubTable) GetEntry(slot common.Hash) (*Entry, error) {
	entry, ok := st.Space[slot]
	if !ok {
		return nil, errors.New("cannot find slot variable")
	}
	return entry, nil
}

func (st *SubTable) InsertEntry(slot common.Hash, isCompact bool) *Entry {
	entry := createNewEntry(isCompact)
	st.Space[slot] = entry
	return entry
}

func createNewSubTable() *SubTable {
	return &SubTable{
		Space: make(map[common.Hash]*Entry),
	}
}

// Entry defines an entry for storing all branch information relevant to a storage slot
type Entry struct {
	// branch的命名方式: 函数签名 + 值 (常数) / 函数签名 + slot + offset (状态变量)
	space     map[string]map[string]*branchInfo
	isCompact bool
	sweeper   *Sweeper
}

func (en *Entry) BranchExist(offset uint256.Int, branchID string) bool {
	branchMap, ok := en.space[offset.String()]
	if !ok {
		return false
	}
	_, ok2 := branchMap[branchID]
	if !ok2 {
		return false
	}
	return true
}

func (en *Entry) GenerateBranchInfo(funcSig string, offset, value uint256.Int, isVar bool, varInfo *VarInfo, epoch int) {
	branch := newBranchInfo(value, isVar, varInfo, epoch)
	branchID := GenerateBranchID(funcSig, isVar, varInfo, value)
	branch.setID(branchID)
	branchMap, ok := en.space[offset.String()]
	if ok {
		if _, ok2 := branchMap[branchID]; ok2 {
			branchMap[branchID] = branch
		}
	} else {
		branchMap = make(map[string]*branchInfo)
		branchMap[branchID] = branch
		en.space[offset.String()] = branchMap
	}
}

func (en *Entry) addHistory(offset uint256.Int, branchID string, taken bool) {
	branchMap := en.space[offset.String()]
	branch := branchMap[branchID]
	branch.history = append(branch.history, taken)
	// 如果分支历史满了，删除第一个元素，实现实时更新
	if len(branch.history) > historyLen {
		branch.history = branch.history[1:]
	}
}

func (en *Entry) GetHistory(offset uint256.Int, branchID string) ([]bool, error) {
	if en.BranchExist(offset, branchID) {
		branchMap := en.space[offset.String()]
		branch := branchMap[branchID]
		return branch.history, nil
	}
	return nil, errors.New("cannot find branch history")
}

func (en *Entry) GetJudgementVal(offset uint256.Int, branchID string) (*VarInfo, uint256.Int) {
	branchMap := en.space[offset.String()]
	branch := branchMap[branchID]
	if branch.isVar {
		return branch.varInfo, branch.value
	}
	return nil, branch.value
}

func (en *Entry) UpdateJudgementVal(judgementVal, updatedVal, offset uint256.Int, branchID string) {
	if updatedVal != judgementVal {
		branchMap := en.space[offset.String()]
		branch := branchMap[branchID]
		branch.value = updatedVal
	}
}

func createNewEntry(isCompact bool) *Entry {
	return &Entry{
		space:     make(map[string]map[string]*branchInfo),
		isCompact: isCompact,
		sweeper:   newSweeper(round, threshold),
	}
}

// Sweeper sweeps inactive branch information in a round
type Sweeper struct {
	round     int
	threshold float32
}

func newSweeper(round int, threshold float32) *Sweeper {
	return &Sweeper{
		round:     round,
		threshold: threshold,
	}
}

// Sweep defines the sweep function of a sweeper
func (s *Sweeper) sweep(epoch int, entry map[string]map[string]*branchInfo) {
	for offset, branchMap := range entry {
		if len(branchMap) == 0 {
			delete(entry, offset)
			continue
		}
		for id, branch := range branchMap {
			length := len(branch.history)
			lifeCycle := epoch - branch.getCreationEpoch()
			frequency := float32(length) / float32(lifeCycle)
			if frequency < s.threshold {
				delete(branchMap, id)
			}
		}
	}
}

func (s *Sweeper) getRound() int         { return s.round }
func (s *Sweeper) getThreshold() float32 { return s.threshold }

// branchInfo defines a struct for storing relevant branch information
type branchInfo struct {
	id       string
	value    uint256.Int
	isVar    bool
	varInfo  *VarInfo
	history  []bool
	creation int // record the epoch of creation
}

type VarInfo struct {
	slot   uint256.Int
	offset uint256.Int
	bits   int
}

func NewVarInfo(slot, offset uint256.Int, bits int) *VarInfo {
	return &VarInfo{
		slot:   slot,
		offset: offset,
		bits:   bits,
	}
}

func newBranchInfo(value uint256.Int, isVar bool, varInfo *VarInfo, creation int) *branchInfo {
	return &branchInfo{
		value:    value,
		isVar:    isVar,
		varInfo:  varInfo,
		history:  make([]bool, 0, historyLen),
		creation: creation,
	}
}

func (br *branchInfo) getCreationEpoch() int { return br.creation }
func (br *branchInfo) setID(id string)       { br.id = id }

// GenerateBranchID generates a unique ID for a branch
func GenerateBranchID(funcSig string, isVar bool, varInfo *VarInfo, value uint256.Int) string {
	var branchID string
	if isVar {
		slot := varInfo.slot
		offset := varInfo.offset
		branchID = funcSig + slot.String() + offset.String()
	} else {
		branchID = funcSig + value.String()
	}
	return branchID
}
