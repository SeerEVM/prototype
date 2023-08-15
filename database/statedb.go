package database

import (
	"github.com/ethereum/go-ethereum/ethdb"
	"icse/core/state"
	"icse/trie"
)

func NewStateCache(db ethdb.Database) state.Database {
	config := defaultStateDBConfig()
	return state.NewDatabaseWithConfig(db, &trie.Config{
		Cache:     config.Cache,
		Journal:   config.Journal,
		Preimages: config.Preimages,
	})
}
